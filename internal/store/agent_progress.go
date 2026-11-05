package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const AgentRunActivityLimit = 12

type AgentRunProgress struct {
	Phase       string             `json:"phase,omitempty"`
	Turn        int                `json:"turn,omitempty"`
	UpdatedAt   time.Time          `json:"updated_at"`
	Activities  []AgentRunActivity `json:"activities,omitempty"`
	Processed   int                `json:"processed,omitempty"`
	Total       int                `json:"total,omitempty"`
	Failed      int                `json:"failed,omitempty"`
	NeedsReview int                `json:"needs_review,omitempty"`
}

type AgentRunActivity struct {
	ID          int64      `json:"id"`
	ToolName    string     `json:"tool_name"`
	Title       string     `json:"title"`
	Detail      string     `json:"detail,omitempty"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Output      string     `json:"output,omitempty"`
}

// These snapshots are presentation data only. They are never model messages.
func (s *Store) UpdateAgentRunProgress(ctx context.Context, id int64, progress *AgentRunProgress) (AgentRun, error) {
	value := boundedProgress(progress)
	if value != nil {
		value.UpdatedAt = time.Now().UTC()
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return AgentRun{}, fmt.Errorf("encode agent run progress: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET progress_json = ?, updated_at = ? WHERE id = ? AND status = 'running'`, string(encoded), formatTime(time.Now().UTC()), id)
	if err != nil {
		return AgentRun{}, fmt.Errorf("save agent run progress: %w", err)
	}
	if err := requireAgentTransition(ctx, s.db, "agent_runs", id, result); err != nil {
		return AgentRun{}, err
	}
	return s.GetAgentRun(ctx, id)
}

func boundedProgress(progress *AgentRunProgress) *AgentRunProgress {
	if progress == nil {
		return nil
	}
	value := *progress
	items := progress.Activities
	if len(items) > AgentRunActivityLimit {
		items = items[len(items)-AgentRunActivityLimit:]
	}
	value.Activities = append([]AgentRunActivity(nil), items...)
	value.Phase = progressText(value.Phase, 40)
	for i := range value.Activities {
		activity := &value.Activities[i]
		activity.ToolName = progressText(activity.ToolName, 80)
		activity.Title = progressText(activity.Title, 180)
		activity.Detail = progressText(activity.Detail, 500)
		activity.Output = progressText(activity.Output, 2000)
	}
	return &value
}

func progressText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value
	}
	cut := limit - len("…")
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + "…"
}

func finishProgress(progress *AgentRunProgress, status string, now time.Time) *AgentRunProgress {
	if progress == nil {
		return nil
	}
	value := boundedProgress(progress)
	value.Phase = status
	value.UpdatedAt = now
	for i := range value.Activities {
		activity := &value.Activities[i]
		if activity.Status == AgentStatusRunning {
			activity.Status = status
			activity.CompletedAt = &now
		}
	}
	return value
}

func finishAgentRunProgress(ctx context.Context, tx *sql.Tx, id int64, status string, now time.Time) error {
	var encoded string
	if err := tx.QueryRowContext(ctx, `SELECT progress_json FROM agent_runs WHERE id = ?`, id).Scan(&encoded); err != nil {
		return err
	}
	if encoded == "{}" || encoded == "null" || encoded == "" {
		return nil
	}
	var progress *AgentRunProgress
	if err := json.Unmarshal([]byte(encoded), &progress); err != nil {
		return fmt.Errorf("decode agent run progress: %w", err)
	}
	data, err := json.Marshal(finishProgress(progress, status, now))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE agent_runs SET progress_json = ? WHERE id = ?`, string(data), id)
	return err
}

func finishRecoveredRunProgress(ctx context.Context, tx *sql.Tx, completedAt string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM agent_runs WHERE status = 'failed' AND error = 'interrupted before completion' AND completed_at = ?`, completedAt)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	iterationErr := rows.Err()
	if err := rows.Close(); err != nil {
		return err
	}
	if iterationErr != nil {
		return iterationErr
	}
	now, err := parseTime(completedAt)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := finishAgentRunProgress(ctx, tx, id, AgentStatusFailed, now); err != nil {
			return err
		}
	}
	return nil
}
