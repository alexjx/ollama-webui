package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ForkConversation snapshots a conversation and its dependent records. Artifact
// bytes live outside SQLite: the optional copier must copy the supplied keys to
// the new directory without calling Store (the transaction owns its connection).
// The caller must remove that directory if this method returns an error.
func (s *Store) ForkConversation(ctx context.Context, sourceID int64, copyArtifacts ...func(string, string, map[string]string) error) (Conversation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback()
	now := formatTime(time.Now().UTC())
	result, err := tx.ExecContext(ctx, `INSERT INTO conversations
 (title,auto_title_pending,web_search_enabled,model,mode,system_prompt,context_window,thinking_enabled,thinking_mode,temperature,created_at,updated_at)
 SELECT 'Fork of ' || title,0,web_search_enabled,model,mode,system_prompt,context_window,thinking_enabled,thinking_mode,temperature,?,? FROM conversations WHERE id=?`, now, now, sourceID)
	if err != nil {
		return Conversation{}, fmt.Errorf("fork conversation: %w", err)
	}
	if err = requireAffected(result); err != nil {
		return Conversation{}, err
	}
	targetID, err := result.LastInsertId()
	if err != nil {
		return Conversation{}, err
	}

	keys := map[string]string{}
	sourceContext, contextErr := getConversationContext(ctx, tx, sourceID)
	targetKey := ""
	if contextErr != nil && contextErr != ErrNotFound {
		return Conversation{}, contextErr
	}
	if contextErr == nil {
		targetKey, err = newStorageKey()
		if err != nil {
			return Conversation{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO conversation_contexts(conversation_id,storage_key,created_at) VALUES(?,?,?)`, targetID, targetKey, now)
		if err != nil {
			return Conversation{}, err
		}
		rows, err := tx.QueryContext(ctx, `SELECT storage_key FROM context_artifacts WHERE conversation_id=?`, sourceID)
		if err != nil {
			return Conversation{}, err
		}
		for rows.Next() {
			var key string
			if err = rows.Scan(&key); err != nil {
				rows.Close()
				return Conversation{}, err
			}
			replacement, e := newStorageKey()
			if e != nil {
				rows.Close()
				return Conversation{}, e
			}
			keys[key] = replacement
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Conversation{}, err
		}
	}
	ids := map[string]map[int64]int64{"conversations": {sourceID: targetID}}
	type copiedTable struct {
		name    string
		columns []string
		rows    [][]any
	}
	copied := []copiedTable{}
	specs := []struct {
		name, where string
		foreign     map[string]string
	}{
		{"messages", "conversation_id=?", map[string]string{"conversation_id": "conversations"}},
		{"message_attachments", "message_id IN (SELECT id FROM messages WHERE conversation_id=?)", map[string]string{"message_id": "messages"}},
		{"agent_steps", "message_id IN (SELECT id FROM messages WHERE conversation_id=?)", map[string]string{"message_id": "messages"}},
		{"agent_jobs", "conversation_id=?", map[string]string{"conversation_id": "conversations", "requesting_message_id": "messages"}},
		{"agent_runs", "job_id IN (SELECT id FROM agent_jobs WHERE conversation_id=?)", map[string]string{"job_id": "agent_jobs", "parent_run_id": "agent_runs"}},
		{"context_artifacts", "conversation_id=?", map[string]string{"conversation_id": "conversations", "source_message_id": "messages", "source_step_id": "agent_steps"}},
		{"context_checkpoints", "conversation_id=?", map[string]string{"conversation_id": "conversations", "through_message_id": "messages"}},
	}
	for _, spec := range specs {
		// Fixed internal table/column names only; values are always bound parameters.
		order := "id"
		if spec.name == "context_checkpoints" {
			order = "conversation_id"
		}
		columns, rows, err := forkRows(ctx, tx, "SELECT * FROM "+spec.name+" WHERE "+spec.where+" ORDER BY "+order, sourceID)
		if err != nil {
			return Conversation{}, err
		}
		ids[spec.name] = map[int64]int64{}
		for _, row := range rows {
			var oldID int64
			for i, col := range columns {
				if col == "id" {
					oldID = row[i].(int64)
					row[i] = nil
				}
				if table, ok := spec.foreign[col]; ok && row[i] != nil {
					replacement, ok := ids[table][row[i].(int64)]
					if !ok {
						return Conversation{}, fmt.Errorf("fork %s: missing %s reference", spec.name, col)
					}
					row[i] = replacement
				}
				if col == "storage_key" {
					row[i] = keys[row[i].(string)]
				}
				if col == "status" && (row[i] == "streaming" || row[i] == "running" || row[i] == "queued") {
					row[i] = "cancelled"
				}
			}
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
			result, err := tx.ExecContext(ctx, "INSERT INTO "+spec.name+" ("+strings.Join(columns, ",")+") VALUES ("+placeholders+")", row...)
			if err != nil {
				return Conversation{}, fmt.Errorf("fork %s: %w", spec.name, err)
			}
			if oldID != 0 {
				newID, err := result.LastInsertId()
				if err != nil {
					return Conversation{}, err
				}
				ids[spec.name][oldID] = newID
				for i, col := range columns {
					if col == "id" {
						row[i] = newID
					}
				}
			}
		}
		copied = append(copied, copiedTable{spec.name, columns, rows})
	}
	// References embedded in persisted tool transcripts and checkpoint state must
	// refer to the fork too; metric values and unrelated numeric data stay intact.
	pairs := make([]string, 0, len(keys)*2)
	for old, newKey := range keys {
		pairs = append(pairs, old, newKey)
	}
	replacer := strings.NewReplacer(pairs...)
	for _, table := range copied {
		for _, row := range table.rows {
			var primary any
			primaryColumn := "id"
			if table.name == "context_checkpoints" {
				primaryColumn = "conversation_id"
			}
			updates := []string{}
			values := []any{}
			for i, col := range table.columns {
				if col == primaryColumn {
					primary = row[i]
				}
				value, ok := row[i].(string)
				if !ok || col == "storage_key" {
					continue
				}
				changed := replacer.Replace(value)
				changed = forkNumericReference.ReplaceAllStringFunc(changed, func(reference string) string {
					parts := forkNumericReference.FindStringSubmatch(reference)
					oldID, _ := strconv.ParseInt(parts[3], 10, 64)
					table := "messages"
					if strings.Contains(parts[1], "attachment_id") {
						table = "message_attachments"
					}
					if newID, ok := ids[table][oldID]; ok {
						return parts[1] + parts[2] + strconv.FormatInt(newID, 10)
					}
					return reference
				})
				changed = forkAttachmentURL.ReplaceAllStringFunc(changed, func(url string) string {
					oldID, _ := strconv.ParseInt(strings.TrimPrefix(url, "/api/attachments/"), 10, 64)
					if newID, ok := ids["message_attachments"][oldID]; ok {
						return "/api/attachments/" + strconv.FormatInt(newID, 10)
					}
					return url
				})
				if col == "metadata_json" || col == "state_json" {
					changed = remapForkMessageReferences(changed, ids["messages"])
				}
				if changed != value {
					updates = append(updates, col+"=?")
					values = append(values, changed)
				}
			}
			if len(updates) > 0 {
				values = append(values, primary)
				if _, err = tx.ExecContext(ctx, "UPDATE "+table.name+" SET "+strings.Join(updates, ",")+" WHERE "+primaryColumn+"=?", values...); err != nil {
					return Conversation{}, err
				}
			}
		}
	}
	if len(keys) > 0 {
		if len(copyArtifacts) == 0 || copyArtifacts[0] == nil {
			return Conversation{}, fmt.Errorf("fork conversation requires an artifact copier")
		}
		if err = copyArtifacts[0](sourceContext.StorageKey, targetKey, keys); err != nil {
			return Conversation{}, fmt.Errorf("fork artifacts: %w", err)
		}
	}
	fork, err := scanConversation(tx.QueryRowContext(ctx, `SELECT c.id,c.title,c.model,c.mode,c.system_prompt,c.context_window,c.thinking_enabled,c.thinking_mode,c.temperature,
 c.created_at,c.updated_at,MAX(m.created_at),COUNT(m.id),c.auto_title_pending,c.web_search_enabled
 FROM conversations c LEFT JOIN messages m ON m.conversation_id=c.id WHERE c.id=? GROUP BY c.id`, targetID))
	if err != nil {
		return Conversation{}, err
	}
	if err = tx.Commit(); err != nil {
		return Conversation{}, fmt.Errorf("commit fork: %w", err)
	}
	return fork, nil
}

var forkAttachmentURL = regexp.MustCompile(`/api/attachments/[0-9]+`)
var forkNumericReference = regexp.MustCompile(`("(?:attachment_id|message_id)"|\battachment_id)(\s*:\s*|=)([0-9]+)`)

func forkRows(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, [][]any, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	result := [][]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			return nil, nil, err
		}
		result = append(result, values)
	}
	return columns, result, rows.Err()
}

func remapForkMessageReferences(value string, ids map[int64]int64) string {
	var data map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &data) != nil {
		return value
	}
	changed := false
	for _, key := range []string{"context_through_message_id", "compacted_after_message_id"} {
		var old int64
		if json.Unmarshal(data[key], &old) == nil {
			if replacement, ok := ids[old]; ok {
				data[key], _ = json.Marshal(replacement)
				changed = true
			}
		}
	}
	if !changed {
		return value
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return value
	}
	return string(encoded)
}
