package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Conversation struct {
	ID              int64      `json:"id"`
	Title           string     `json:"title"`
	Model           string     `json:"model"`
	Mode            string     `json:"mode"`
	SystemPrompt    string     `json:"system_prompt"`
	ContextWindow   *int       `json:"context_window"`
	ThinkingMode    *string    `json:"thinking_mode"`
	ThinkingEnabled *bool      `json:"thinking_enabled"`
	Temperature     *float64   `json:"temperature"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	LastMessageAt   *time.Time `json:"last_message_at"`
	MessageCount    int        `json:"message_count"`
}

type Message struct {
	ID             int64          `json:"id"`
	ConversationID int64          `json:"conversation_id"`
	Role           string         `json:"role"`
	Content        string         `json:"content"`
	Thinking       string         `json:"thinking,omitempty"`
	Status         string         `json:"status"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Attachments    []Attachment   `json:"attachments,omitempty"`
	AgentSteps     []AgentStep    `json:"agent_steps,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type AgentStep struct {
	ID          int64      `json:"id"`
	MessageID   int64      `json:"message_id"`
	Turn        int        `json:"turn"`
	ToolName    string     `json:"tool_name"`
	Input       string     `json:"input"`
	Output      string     `json:"output"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type Attachment struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	FileName  string `json:"file_name"`
	MediaType string `json:"media_type"`
	Size      int    `json:"size"`
	URL       string `json:"url"`
	Data      []byte `json:"-"`
}

type NewAttachment struct {
	FileName  string
	MediaType string
	Data      []byte
}

type Store struct {
	db   *sql.DB
	path string
}

type Stats struct {
	Conversations   int64 `json:"conversations"`
	Messages        int64 `json:"messages"`
	Attachments     int64 `json:"attachments"`
	AgentSteps      int64 `json:"agent_steps"`
	AttachmentBytes int64 `json:"attachment_bytes"`
	DatabaseBytes   int64 `json:"database_bytes"`
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path != ":memory:" {
		if err := ensureParent(path); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func ensureParent(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
CREATE TABLE IF NOT EXISTS conversations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL DEFAULT 'New chat',
  model TEXT NOT NULL,
  mode TEXT NOT NULL DEFAULT 'agent' CHECK (mode IN ('agent', 'chat')),
  system_prompt TEXT NOT NULL DEFAULT '',
  context_window INTEGER CHECK (context_window > 0),
  thinking_enabled INTEGER CHECK (thinking_enabled IN (0, 1)),
  thinking_mode TEXT CHECK (thinking_mode IN ('off', 'on', 'low', 'medium', 'high', 'max')),
  temperature REAL CHECK (temperature >= 0 AND temperature <= 2),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('system', 'user', 'assistant')),
  content TEXT NOT NULL,
  thinking TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'complete' CHECK (status IN ('streaming', 'complete', 'cancelled', 'error')),
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_conversation_created_idx
  ON messages(conversation_id, created_at, id);
CREATE TABLE IF NOT EXISTS message_attachments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  file_name TEXT NOT NULL,
  media_type TEXT NOT NULL,
  data BLOB NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS message_attachments_message_idx
  ON message_attachments(message_id, id);
CREATE TABLE IF NOT EXISTS agent_steps (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  turn_number INTEGER NOT NULL,
  tool_name TEXT NOT NULL,
  input_json TEXT NOT NULL,
  output TEXT NOT NULL DEFAULT '',
  exit_code INTEGER,
  status TEXT NOT NULL CHECK (status IN ('running', 'complete', 'cancelled', 'error')),
  started_at TEXT NOT NULL,
  completed_at TEXT
);
CREATE INDEX IF NOT EXISTS agent_steps_message_idx ON agent_steps(message_id, id);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if err := s.ensureColumn(ctx, "messages", "thinking", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "conversations", "thinking_enabled", "INTEGER CHECK (thinking_enabled IN (0, 1))"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "conversations", "thinking_mode", "TEXT CHECK (thinking_mode IN ('off', 'on', 'low', 'medium', 'high', 'max'))"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE conversations
SET thinking_mode = CASE WHEN thinking_enabled = 1 THEN 'on' ELSE 'off' END
WHERE thinking_mode IS NULL AND thinking_enabled IS NOT NULL`); err != nil {
		return fmt.Errorf("migrate conversation thinking modes: %w", err)
	}
	if err := s.ensureColumn(ctx, "conversations", "mode", "TEXT NOT NULL DEFAULT 'agent' CHECK (mode IN ('agent', 'chat'))"); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("inspect %s schema: %w", table, err)
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("inspect %s column: %w", table, err)
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column+" "+definition); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

type CreateConversationParams struct {
	Title           string
	Model           string
	Mode            string
	SystemPrompt    string
	ContextWindow   *int
	ThinkingMode    *string
	ThinkingEnabled *bool
	Temperature     *float64
}

func (s *Store) CreateConversation(ctx context.Context, params CreateConversationParams) (Conversation, error) {
	if strings.TrimSpace(params.Title) == "" {
		params.Title = "New chat"
	}
	if params.Mode == "" {
		params.Mode = "agent"
	}
	if params.ThinkingMode == nil && params.ThinkingEnabled != nil {
		mode := "off"
		if *params.ThinkingEnabled {
			mode = "on"
		}
		params.ThinkingMode = &mode
	}
	var legacyThinkingEnabled *bool
	if params.ThinkingMode != nil && (*params.ThinkingMode == "on" || *params.ThinkingMode == "off") {
		enabled := *params.ThinkingMode == "on"
		legacyThinkingEnabled = &enabled
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
INSERT INTO conversations (title, model, mode, system_prompt, context_window, thinking_enabled, thinking_mode, temperature, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, params.Title, params.Model, params.Mode, params.SystemPrompt,
		params.ContextWindow, legacyThinkingEnabled, params.ThinkingMode, params.Temperature, formatTime(now), formatTime(now))
	if err != nil {
		return Conversation{}, fmt.Errorf("create conversation: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Conversation{}, fmt.Errorf("read conversation id: %w", err)
	}
	return s.GetConversation(ctx, id)
}

func (s *Store) GetConversation(ctx context.Context, id int64) (Conversation, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT c.id, c.title, c.model, c.mode, c.system_prompt, c.context_window, c.thinking_enabled, c.thinking_mode, c.temperature,
       c.created_at, c.updated_at, MAX(m.created_at), COUNT(m.id)
FROM conversations c
LEFT JOIN messages m ON m.conversation_id = c.id
WHERE c.id = ?
GROUP BY c.id`, id)
	conversation, err := scanConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	return conversation, err
}

func (s *Store) ListConversations(ctx context.Context, query string) ([]Conversation, error) {
	pattern := "%" + escapeLike(strings.TrimSpace(query)) + "%"
	rows, err := s.db.QueryContext(ctx, `
SELECT c.id, c.title, c.model, c.mode, c.system_prompt, c.context_window, c.thinking_enabled, c.thinking_mode, c.temperature,
       c.created_at, c.updated_at, MAX(m.created_at), COUNT(m.id)
FROM conversations c
LEFT JOIN messages m ON m.conversation_id = c.id
WHERE ? = '%%' OR c.title LIKE ? ESCAPE '\' OR EXISTS (
  SELECT 1 FROM messages searched
  WHERE searched.conversation_id = c.id AND searched.content LIKE ? ESCAPE '\'
)
GROUP BY c.id
ORDER BY c.updated_at DESC, c.id DESC
LIMIT 200`, pattern, pattern, pattern)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	conversations := make([]Conversation, 0)
	for rows.Next() {
		conversation, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		conversations = append(conversations, conversation)
	}
	return conversations, rows.Err()
}

func (s *Store) UpdateTitle(ctx context.Context, id int64, title string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE conversations SET title = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(title), formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("update conversation: %w", err)
	}
	return requireAffected(result)
}

func (s *Store) DeleteConversation(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	return requireAffected(result)
}

func (s *Store) ClearConversations(ctx context.Context) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin clear conversations: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM conversations`)
	if err != nil {
		return 0, fmt.Errorf("clear conversations: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count cleared conversations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit clear conversations: %w", err)
	}
	return deleted, nil
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	err := s.db.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(*) FROM conversations),
  (SELECT COUNT(*) FROM messages),
  (SELECT COUNT(*) FROM message_attachments),
  (SELECT COUNT(*) FROM agent_steps),
  (SELECT COALESCE(SUM(LENGTH(data)), 0) FROM message_attachments)`).Scan(
		&stats.Conversations, &stats.Messages, &stats.Attachments, &stats.AgentSteps, &stats.AttachmentBytes)
	if err != nil {
		return Stats{}, fmt.Errorf("read database stats: %w", err)
	}
	if s.path != ":memory:" {
		for _, path := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
			info, statErr := os.Stat(path)
			if statErr == nil {
				stats.DatabaseBytes += info.Size()
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return Stats{}, fmt.Errorf("read database size: %w", statErr)
			}
		}
	}
	return stats, nil
}

func (s *Store) AddMessage(ctx context.Context, conversationID int64, role, content, status string) (Message, error) {
	return s.AddMessageWithAttachments(ctx, conversationID, role, content, status, nil)
}

func (s *Store) AddMessageWithAttachments(ctx context.Context, conversationID int64, role, content, status string, attachments []NewAttachment) (Message, error) {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, fmt.Errorf("begin message transaction: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
INSERT INTO messages (conversation_id, role, content, status, created_at)
VALUES (?, ?, ?, ?, ?)`, conversationID, role, content, status, formatTime(now))
	if err != nil {
		return Message{}, fmt.Errorf("create message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, formatTime(now), conversationID); err != nil {
		return Message{}, fmt.Errorf("touch conversation: %w", err)
	}
	id, _ := result.LastInsertId()
	storedAttachments := make([]Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		attachmentResult, err := tx.ExecContext(ctx, `
INSERT INTO message_attachments (message_id, file_name, media_type, data, created_at)
VALUES (?, ?, ?, ?, ?)`, id, attachment.FileName, attachment.MediaType, attachment.Data, formatTime(now))
		if err != nil {
			return Message{}, fmt.Errorf("create attachment: %w", err)
		}
		attachmentID, _ := attachmentResult.LastInsertId()
		storedAttachments = append(storedAttachments, Attachment{
			ID: attachmentID, MessageID: id, FileName: attachment.FileName,
			MediaType: attachment.MediaType, Size: len(attachment.Data),
			URL: fmt.Sprintf("/api/attachments/%d", attachmentID),
		})
	}
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("commit message: %w", err)
	}
	return Message{ID: id, ConversationID: conversationID, Role: role, Content: content,
		Status: status, Attachments: storedAttachments, CreatedAt: now}, nil
}

func (s *Store) UpdateMessage(ctx context.Context, id int64, content, thinking, status string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode message metadata: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE messages SET content = ?, thinking = ?, status = ?, metadata_json = ? WHERE id = ?`,
		content, thinking, status, string(encoded), id)
	if err != nil {
		return fmt.Errorf("update message: %w", err)
	}
	return requireAffected(result)
}

func (s *Store) Messages(ctx context.Context, conversationID int64) ([]Message, error) {
	return s.messages(ctx, conversationID, false)
}

func (s *Store) MessagesWithAttachmentData(ctx context.Context, conversationID int64) ([]Message, error) {
	return s.messages(ctx, conversationID, true)
}

func (s *Store) messages(ctx context.Context, conversationID int64, includeAttachmentData bool) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, conversation_id, role, content, thinking, status, metadata_json, created_at
FROM messages WHERE conversation_id = ? ORDER BY created_at, id`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	messages := make([]Message, 0)
	messageIndexes := make(map[int64]int)
	for rows.Next() {
		var message Message
		var metadataJSON, createdAt string
		if err := rows.Scan(&message.ID, &message.ConversationID, &message.Role, &message.Content,
			&message.Thinking, &message.Status, &metadataJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		message.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(metadataJSON), &message.Metadata); err != nil {
			return nil, fmt.Errorf("decode message metadata: %w", err)
		}
		messageIndexes[message.ID] = len(messages)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	attachmentRows, err := s.db.QueryContext(ctx, `
SELECT a.id, a.message_id, a.file_name, a.media_type, length(a.data),
       CASE WHEN ? THEN a.data ELSE NULL END
FROM message_attachments a
JOIN messages m ON m.id = a.message_id
WHERE m.conversation_id = ? ORDER BY a.message_id, a.id`, includeAttachmentData, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	defer attachmentRows.Close()
	for attachmentRows.Next() {
		var attachment Attachment
		if err := attachmentRows.Scan(&attachment.ID, &attachment.MessageID, &attachment.FileName,
			&attachment.MediaType, &attachment.Size, &attachment.Data); err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		attachment.URL = fmt.Sprintf("/api/attachments/%d", attachment.ID)
		if index, ok := messageIndexes[attachment.MessageID]; ok {
			messages[index].Attachments = append(messages[index].Attachments, attachment)
		}
	}
	if err := attachmentRows.Err(); err != nil {
		return nil, err
	}
	if err := attachmentRows.Close(); err != nil {
		return nil, err
	}
	stepRows, err := s.db.QueryContext(ctx, `
SELECT s.id, s.message_id, s.turn_number, s.tool_name, s.input_json, s.output,
       s.exit_code, s.status, s.started_at, s.completed_at
FROM agent_steps s JOIN messages m ON m.id = s.message_id
WHERE m.conversation_id = ? ORDER BY s.id`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list agent steps: %w", err)
	}
	defer stepRows.Close()
	for stepRows.Next() {
		var step AgentStep
		var exitCode sql.NullInt64
		var startedAt string
		var completedAt sql.NullString
		if err := stepRows.Scan(&step.ID, &step.MessageID, &step.Turn, &step.ToolName,
			&step.Input, &step.Output, &exitCode, &step.Status, &startedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scan agent step: %w", err)
		}
		step.StartedAt, err = parseTime(startedAt)
		if err != nil {
			return nil, err
		}
		if exitCode.Valid {
			value := int(exitCode.Int64)
			step.ExitCode = &value
		}
		if completedAt.Valid {
			value, err := parseTime(completedAt.String)
			if err != nil {
				return nil, err
			}
			step.CompletedAt = &value
		}
		if index, ok := messageIndexes[step.MessageID]; ok {
			messages[index].AgentSteps = append(messages[index].AgentSteps, step)
		}
	}
	return messages, stepRows.Err()
}

func (s *Store) BeginAgentStep(ctx context.Context, messageID int64, turn int, toolName, input string) (AgentStep, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
INSERT INTO agent_steps (message_id, turn_number, tool_name, input_json, status, started_at)
VALUES (?, ?, ?, ?, 'running', ?)`, messageID, turn, toolName, input, formatTime(now))
	if err != nil {
		return AgentStep{}, fmt.Errorf("create agent step: %w", err)
	}
	id, _ := result.LastInsertId()
	return AgentStep{ID: id, MessageID: messageID, Turn: turn, ToolName: toolName,
		Input: input, Status: "running", StartedAt: now}, nil
}

func (s *Store) CompleteAgentStep(ctx context.Context, id int64, output string, exitCode int, status string) (AgentStep, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE agent_steps SET output = ?, exit_code = ?, status = ?, completed_at = ? WHERE id = ?`,
		output, exitCode, status, formatTime(now), id)
	if err != nil {
		return AgentStep{}, fmt.Errorf("complete agent step: %w", err)
	}
	if err := requireAffected(result); err != nil {
		return AgentStep{}, err
	}
	return AgentStep{ID: id, Output: output, ExitCode: &exitCode, Status: status, CompletedAt: &now}, nil
}

func (s *Store) GetAttachment(ctx context.Context, id int64) (Attachment, error) {
	var attachment Attachment
	err := s.db.QueryRowContext(ctx, `
SELECT id, message_id, file_name, media_type, data
FROM message_attachments WHERE id = ?`, id).Scan(&attachment.ID, &attachment.MessageID,
		&attachment.FileName, &attachment.MediaType, &attachment.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("get attachment: %w", err)
	}
	attachment.Size = len(attachment.Data)
	attachment.URL = fmt.Sprintf("/api/attachments/%d", attachment.ID)
	return attachment, nil
}

type rowScanner interface{ Scan(...any) error }

func scanConversation(row rowScanner) (Conversation, error) {
	var conversation Conversation
	var contextWindow sql.NullInt64
	var thinkingEnabled sql.NullBool
	var thinkingMode sql.NullString
	var temperature sql.NullFloat64
	var createdAt, updatedAt string
	var lastMessageAt sql.NullString
	if err := row.Scan(&conversation.ID, &conversation.Title, &conversation.Model, &conversation.Mode, &conversation.SystemPrompt,
		&contextWindow, &thinkingEnabled, &thinkingMode, &temperature, &createdAt, &updatedAt, &lastMessageAt, &conversation.MessageCount); err != nil {
		return Conversation{}, err
	}
	var err error
	if conversation.CreatedAt, err = parseTime(createdAt); err != nil {
		return Conversation{}, err
	}
	if conversation.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Conversation{}, err
	}
	if contextWindow.Valid {
		value := int(contextWindow.Int64)
		conversation.ContextWindow = &value
	}
	if thinkingEnabled.Valid {
		value := thinkingEnabled.Bool
		conversation.ThinkingEnabled = &value
	}
	if thinkingMode.Valid {
		value := thinkingMode.String
		conversation.ThinkingMode = &value
	}
	if temperature.Valid {
		value := temperature.Float64
		conversation.Temperature = &value
	}
	if lastMessageAt.Valid {
		value, err := parseTime(lastMessageAt.String)
		if err != nil {
			return Conversation{}, err
		}
		conversation.LastMessageAt = &value
	}
	return conversation, nil
}

func requireAffected(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func formatTime(value time.Time) string { return value.Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored timestamp: %w", err)
	}
	return parsed, nil
}
