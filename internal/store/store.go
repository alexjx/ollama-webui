package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrInvalidStorageKey = errors.New("invalid storage key")
)

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

type ConversationContext struct {
	ConversationID int64     `json:"conversation_id"`
	StorageKey     string    `json:"storage_key"`
	CreatedAt      time.Time `json:"created_at"`
}

type ContextCheckpoint struct {
	ConversationID   int64          `json:"conversation_id"`
	ThroughMessageID int64          `json:"through_message_id"`
	Summary          string         `json:"summary"`
	State            map[string]any `json:"state,omitempty"`
	EstimatedTokens  int            `json:"estimated_tokens"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type UpsertCheckpointParams struct {
	ConversationID   int64
	ThroughMessageID int64
	Summary          string
	State            map[string]any
	EstimatedTokens  int
}

type ContextArtifact struct {
	ID              int64     `json:"id"`
	ConversationID  int64     `json:"conversation_id"`
	StorageKey      string    `json:"storage_key"`
	SourceMessageID *int64    `json:"source_message_id,omitempty"`
	SourceStepID    *int64    `json:"source_step_id,omitempty"`
	Kind            string    `json:"kind"`
	DisplayName     string    `json:"display_name"`
	MediaType       string    `json:"media_type"`
	SizeBytes       int64     `json:"size_bytes"`
	SHA256          string    `json:"sha256"`
	Summary         string    `json:"summary"`
	CreatedAt       time.Time `json:"created_at"`
}

type CreateContextArtifactParams struct {
	ConversationID  int64
	StorageKey      string
	SourceMessageID *int64
	SourceStepID    *int64
	Kind            string
	DisplayName     string
	MediaType       string
	SizeBytes       int64
	SHA256          string
	Summary         string
}

type Store struct {
	db   *sql.DB
	path string
}

type Stats struct {
	Conversations        int64 `json:"conversations"`
	Messages             int64 `json:"messages"`
	Attachments          int64 `json:"attachments"`
	AgentSteps           int64 `json:"agent_steps"`
	ContextCheckpoints   int64 `json:"context_checkpoints"`
	ContextArtifacts     int64 `json:"context_artifacts"`
	AttachmentBytes      int64 `json:"attachment_bytes"`
	ContextArtifactBytes int64 `json:"context_artifact_bytes"`
	DatabaseBytes        int64 `json:"database_bytes"`
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
CREATE TABLE IF NOT EXISTS conversation_contexts (
  conversation_id INTEGER PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
  storage_key TEXT NOT NULL UNIQUE
    CHECK (length(storage_key) = 32 AND storage_key NOT GLOB '*[^0-9a-f]*'),
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS context_checkpoints (
  conversation_id INTEGER PRIMARY KEY REFERENCES conversation_contexts(conversation_id) ON DELETE CASCADE,
  through_message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  summary TEXT NOT NULL,
  state_json TEXT NOT NULL DEFAULT '{}',
  estimated_tokens INTEGER NOT NULL DEFAULT 0 CHECK (estimated_tokens >= 0),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS context_artifacts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  conversation_id INTEGER NOT NULL REFERENCES conversation_contexts(conversation_id) ON DELETE CASCADE,
  storage_key TEXT NOT NULL UNIQUE
    CHECK (length(storage_key) = 32 AND storage_key NOT GLOB '*[^0-9a-f]*'),
  source_message_id INTEGER REFERENCES messages(id) ON DELETE SET NULL,
  source_step_id INTEGER REFERENCES agent_steps(id) ON DELETE SET NULL,
  kind TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  media_type TEXT NOT NULL DEFAULT 'text/plain',
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  sha256 TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS context_artifacts_conversation_created_idx
  ON context_artifacts(conversation_id, created_at, id);
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

func (s *Store) EnsureConversationContext(ctx context.Context, conversationID int64) (ConversationContext, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConversationContext{}, fmt.Errorf("begin conversation context transaction: %w", err)
	}
	defer tx.Rollback()

	stored, err := getConversationContext(ctx, tx, conversationID)
	if err == nil {
		return stored, tx.Commit()
	}
	if !errors.Is(err, ErrNotFound) {
		return ConversationContext{}, err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conversations WHERE id = ?)`, conversationID).Scan(&exists); err != nil {
		return ConversationContext{}, fmt.Errorf("check conversation for context: %w", err)
	}
	if !exists {
		return ConversationContext{}, ErrNotFound
	}
	key, err := newStorageKey()
	if err != nil {
		return ConversationContext{}, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO conversation_contexts (conversation_id, storage_key, created_at)
VALUES (?, ?, ?)`, conversationID, key, formatTime(now)); err != nil {
		return ConversationContext{}, fmt.Errorf("create conversation context: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ConversationContext{}, fmt.Errorf("commit conversation context: %w", err)
	}
	return ConversationContext{ConversationID: conversationID, StorageKey: key, CreatedAt: now}, nil
}

func (s *Store) GetConversationContext(ctx context.Context, conversationID int64) (ConversationContext, error) {
	return getConversationContext(ctx, s.db, conversationID)
}

func (s *Store) ListConversationContexts(ctx context.Context) ([]ConversationContext, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT conversation_id, storage_key, created_at
FROM conversation_contexts ORDER BY conversation_id`)
	if err != nil {
		return nil, fmt.Errorf("list conversation contexts: %w", err)
	}
	defer rows.Close()
	items := make([]ConversationContext, 0)
	for rows.Next() {
		item, err := scanConversationContext(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpsertCheckpoint(ctx context.Context, params UpsertCheckpointParams) (ContextCheckpoint, error) {
	if _, err := s.EnsureConversationContext(ctx, params.ConversationID); err != nil {
		return ContextCheckpoint{}, err
	}
	state := params.State
	if state == nil {
		state = map[string]any{}
	}
	encodedState, err := json.Marshal(state)
	if err != nil {
		return ContextCheckpoint{}, fmt.Errorf("encode checkpoint state: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ContextCheckpoint{}, fmt.Errorf("begin checkpoint transaction: %w", err)
	}
	defer tx.Rollback()
	belongs, err := messageBelongsToConversation(ctx, tx, params.ThroughMessageID, params.ConversationID)
	if err != nil {
		return ContextCheckpoint{}, err
	}
	if !belongs {
		return ContextCheckpoint{}, ErrNotFound
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO context_checkpoints
  (conversation_id, through_message_id, summary, state_json, estimated_tokens, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(conversation_id) DO UPDATE SET
  through_message_id = excluded.through_message_id,
  summary = excluded.summary,
  state_json = excluded.state_json,
  estimated_tokens = excluded.estimated_tokens,
  updated_at = excluded.updated_at`, params.ConversationID, params.ThroughMessageID, params.Summary,
		string(encodedState), params.EstimatedTokens, formatTime(now), formatTime(now)); err != nil {
		return ContextCheckpoint{}, fmt.Errorf("upsert checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ContextCheckpoint{}, fmt.Errorf("commit checkpoint: %w", err)
	}
	return s.GetCheckpoint(ctx, params.ConversationID)
}

func (s *Store) GetCheckpoint(ctx context.Context, conversationID int64) (ContextCheckpoint, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT c.conversation_id, c.through_message_id, c.summary, c.state_json,
       c.estimated_tokens, c.created_at, c.updated_at
FROM context_checkpoints c
JOIN messages m ON m.id = c.through_message_id
WHERE c.conversation_id = ? AND m.conversation_id = c.conversation_id`, conversationID)
	var checkpoint ContextCheckpoint
	var stateJSON, createdAt, updatedAt string
	if err := row.Scan(&checkpoint.ConversationID, &checkpoint.ThroughMessageID, &checkpoint.Summary,
		&stateJSON, &checkpoint.EstimatedTokens, &createdAt, &updatedAt); errors.Is(err, sql.ErrNoRows) {
		return ContextCheckpoint{}, ErrNotFound
	} else if err != nil {
		return ContextCheckpoint{}, fmt.Errorf("get checkpoint: %w", err)
	}
	if err := json.Unmarshal([]byte(stateJSON), &checkpoint.State); err != nil {
		return ContextCheckpoint{}, fmt.Errorf("decode checkpoint state: %w", err)
	}
	var err error
	if checkpoint.CreatedAt, err = parseTime(createdAt); err != nil {
		return ContextCheckpoint{}, err
	}
	if checkpoint.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return ContextCheckpoint{}, err
	}
	return checkpoint, nil
}

func (s *Store) CreateContextArtifact(ctx context.Context, params CreateContextArtifactParams) (ContextArtifact, error) {
	key := params.StorageKey
	if key != "" && !validStorageKey(key) {
		return ContextArtifact{}, ErrInvalidStorageKey
	}
	if key == "" {
		var err error
		key, err = newStorageKey()
		if err != nil {
			return ContextArtifact{}, err
		}
	}
	if params.MediaType == "" {
		params.MediaType = "text/plain"
	}
	if _, err := s.EnsureConversationContext(ctx, params.ConversationID); err != nil {
		return ContextArtifact{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("begin context artifact transaction: %w", err)
	}
	defer tx.Rollback()
	if params.SourceMessageID != nil {
		belongs, err := messageBelongsToConversation(ctx, tx, *params.SourceMessageID, params.ConversationID)
		if err != nil {
			return ContextArtifact{}, err
		}
		if !belongs {
			return ContextArtifact{}, ErrNotFound
		}
	}
	if params.SourceStepID != nil {
		var belongs bool
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM agent_steps s JOIN messages m ON m.id = s.message_id
  WHERE s.id = ? AND m.conversation_id = ?
)`, *params.SourceStepID, params.ConversationID).Scan(&belongs); err != nil {
			return ContextArtifact{}, fmt.Errorf("check artifact source step: %w", err)
		}
		if !belongs {
			return ContextArtifact{}, ErrNotFound
		}
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
INSERT INTO context_artifacts
  (conversation_id, storage_key, source_message_id, source_step_id, kind, display_name,
   media_type, size_bytes, sha256, summary, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, params.ConversationID, key, params.SourceMessageID,
		params.SourceStepID, params.Kind, params.DisplayName, params.MediaType, params.SizeBytes,
		params.SHA256, params.Summary, formatTime(now))
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("create context artifact: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("read context artifact id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ContextArtifact{}, fmt.Errorf("commit context artifact: %w", err)
	}
	return ContextArtifact{
		ID: id, ConversationID: params.ConversationID, StorageKey: key,
		SourceMessageID: params.SourceMessageID, SourceStepID: params.SourceStepID,
		Kind: params.Kind, DisplayName: params.DisplayName, MediaType: params.MediaType,
		SizeBytes: params.SizeBytes, SHA256: params.SHA256, Summary: params.Summary, CreatedAt: now,
	}, nil
}

func (s *Store) GetContextArtifact(ctx context.Context, conversationID, artifactID int64) (ContextArtifact, error) {
	return scanContextArtifact(s.db.QueryRowContext(ctx, contextArtifactSelect+`
WHERE conversation_id = ? AND id = ?`, conversationID, artifactID))
}

func (s *Store) GetContextArtifactByStorageKey(ctx context.Context, conversationID int64, storageKey string) (ContextArtifact, error) {
	return scanContextArtifact(s.db.QueryRowContext(ctx, contextArtifactSelect+`
WHERE conversation_id = ? AND storage_key = ?`, conversationID, storageKey))
}

func (s *Store) ListContextArtifacts(ctx context.Context, conversationID int64) ([]ContextArtifact, error) {
	rows, err := s.db.QueryContext(ctx, contextArtifactSelect+`
WHERE conversation_id = ? ORDER BY created_at, id`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list context artifacts: %w", err)
	}
	defer rows.Close()
	items := make([]ContextArtifact, 0)
	for rows.Next() {
		item, err := scanContextArtifact(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteContextArtifact(ctx context.Context, conversationID, artifactID int64) error {
	result, err := s.db.ExecContext(ctx, `
DELETE FROM context_artifacts WHERE conversation_id = ? AND id = ?`, conversationID, artifactID)
	if err != nil {
		return fmt.Errorf("delete context artifact: %w", err)
	}
	return requireAffected(result)
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	err := s.db.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(*) FROM conversations),
  (SELECT COUNT(*) FROM messages),
  (SELECT COUNT(*) FROM message_attachments),
  (SELECT COUNT(*) FROM agent_steps),
  (SELECT COUNT(*) FROM context_checkpoints),
  (SELECT COUNT(*) FROM context_artifacts),
  (SELECT COALESCE(SUM(LENGTH(data)), 0) FROM message_attachments),
  (SELECT COALESCE(SUM(size_bytes), 0) FROM context_artifacts)`).Scan(
		&stats.Conversations, &stats.Messages, &stats.Attachments, &stats.AgentSteps,
		&stats.ContextCheckpoints, &stats.ContextArtifacts, &stats.AttachmentBytes, &stats.ContextArtifactBytes)
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

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const contextArtifactSelect = `
SELECT id, conversation_id, storage_key, source_message_id, source_step_id, kind,
       display_name, media_type, size_bytes, sha256, summary, created_at
FROM context_artifacts `

func getConversationContext(ctx context.Context, query queryRower, conversationID int64) (ConversationContext, error) {
	return scanConversationContext(query.QueryRowContext(ctx, `
SELECT conversation_id, storage_key, created_at
FROM conversation_contexts WHERE conversation_id = ?`, conversationID))
}

func scanConversationContext(row rowScanner) (ConversationContext, error) {
	var item ConversationContext
	var createdAt string
	if err := row.Scan(&item.ConversationID, &item.StorageKey, &createdAt); errors.Is(err, sql.ErrNoRows) {
		return ConversationContext{}, ErrNotFound
	} else if err != nil {
		return ConversationContext{}, fmt.Errorf("scan conversation context: %w", err)
	}
	var err error
	if item.CreatedAt, err = parseTime(createdAt); err != nil {
		return ConversationContext{}, err
	}
	return item, nil
}

func scanContextArtifact(row rowScanner) (ContextArtifact, error) {
	var item ContextArtifact
	var sourceMessageID, sourceStepID sql.NullInt64
	var createdAt string
	if err := row.Scan(&item.ID, &item.ConversationID, &item.StorageKey, &sourceMessageID,
		&sourceStepID, &item.Kind, &item.DisplayName, &item.MediaType, &item.SizeBytes,
		&item.SHA256, &item.Summary, &createdAt); errors.Is(err, sql.ErrNoRows) {
		return ContextArtifact{}, ErrNotFound
	} else if err != nil {
		return ContextArtifact{}, fmt.Errorf("scan context artifact: %w", err)
	}
	if sourceMessageID.Valid {
		value := sourceMessageID.Int64
		item.SourceMessageID = &value
	}
	if sourceStepID.Valid {
		value := sourceStepID.Int64
		item.SourceStepID = &value
	}
	var err error
	if item.CreatedAt, err = parseTime(createdAt); err != nil {
		return ContextArtifact{}, err
	}
	return item, nil
}

func messageBelongsToConversation(ctx context.Context, query queryRower, messageID, conversationID int64) (bool, error) {
	var belongs bool
	if err := query.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM messages WHERE id = ? AND conversation_id = ?)`, messageID, conversationID).Scan(&belongs); err != nil {
		return false, fmt.Errorf("check message conversation: %w", err)
	}
	return belongs, nil
}

func newStorageKey() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate storage key: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func validStorageKey(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

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
