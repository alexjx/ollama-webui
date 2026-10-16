package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// TemplateSettings preserves the distinction between omitted settings (inherit)
// and explicit null values (use the model default).
type TemplateSettings map[string]json.RawMessage

type TemplateInput struct {
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Message      string           `json:"message"`
	SystemPrompt string           `json:"system_prompt"`
	Settings     TemplateSettings `json:"settings"`
}

type Template struct {
	ID int64 `json:"id"`
	TemplateInput
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (input TemplateInput) Validate() error {
	if strings.TrimSpace(input.Name) == "" || utf8.RuneCountInString(input.Name) > 120 {
		return errors.New("name must contain 1 to 120 characters")
	}
	if utf8.RuneCountInString(input.Description) > 2000 {
		return errors.New("description must contain at most 2000 characters")
	}
	if len(input.Message) > 128<<10 || len(input.SystemPrompt) > 128<<10 {
		return errors.New("message and system instructions must each contain at most 128 KiB")
	}
	if strings.TrimSpace(input.Message) == "" && strings.TrimSpace(input.SystemPrompt) == "" {
		return errors.New("add a message or system instructions")
	}
	for key, raw := range input.Settings {
		null := strings.TrimSpace(string(raw)) == "null"
		switch key {
		case "model", "mode", "thinking_mode":
			if null && key == "thinking_mode" {
				continue
			}
			var value string
			if null || json.Unmarshal(raw, &value) != nil {
				return fmt.Errorf("%s must be a string", key)
			}
			if key == "model" && (strings.TrimSpace(value) == "" || len(value) > 512) {
				return errors.New("model must contain 1 to 512 bytes")
			}
			if key == "mode" && value != "chat" && value != "agent" {
				return errors.New("mode must be chat or agent")
			}
			if key == "thinking_mode" {
				switch value {
				case "off", "on", "low", "medium", "high", "max":
				default:
					return errors.New("invalid thinking_mode")
				}
			}
		case "context_window":
			if null {
				continue
			}
			var value int
			if json.Unmarshal(raw, &value) != nil || value <= 0 {
				return errors.New("context_window must be a positive integer or null")
			}
		case "temperature":
			if null {
				continue
			}
			var value float64
			if json.Unmarshal(raw, &value) != nil || value < 0 || value > 2 {
				return errors.New("temperature must be between 0 and 2 or null")
			}
		case "web_search_enabled":
			var value bool
			if null || json.Unmarshal(raw, &value) != nil {
				return errors.New("web_search_enabled must be a boolean")
			}
		default:
			return fmt.Errorf("unknown template setting %q", key)
		}
	}
	return nil
}

const templateColumns = "id, name, description, message, system_prompt, settings, created_at, updated_at"

func (s *Store) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+templateColumns+" FROM conversation_templates ORDER BY updated_at DESC, id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Template{}
	for rows.Next() {
		item, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Store) GetTemplate(ctx context.Context, id int64) (Template, error) {
	return scanTemplate(s.db.QueryRowContext(ctx, "SELECT "+templateColumns+" FROM conversation_templates WHERE id = ?", id))
}
func (s *Store) CreateTemplate(ctx context.Context, input TemplateInput) (Template, error) {
	if err := input.Validate(); err != nil {
		return Template{}, err
	}
	settings, err := json.Marshal(input.Settings)
	if err != nil {
		return Template{}, err
	}
	now := formatTime(time.Now().UTC())
	result, err := s.db.ExecContext(ctx, `INSERT INTO conversation_templates (name, description, message, system_prompt, settings, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, strings.TrimSpace(input.Name), input.Description, input.Message, input.SystemPrompt, string(settings), now, now)
	if err != nil {
		return Template{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Template{}, err
	}
	return s.GetTemplate(ctx, id)
}
func (s *Store) UpdateTemplate(ctx context.Context, id int64, input TemplateInput) (Template, error) {
	if err := input.Validate(); err != nil {
		return Template{}, err
	}
	settings, err := json.Marshal(input.Settings)
	if err != nil {
		return Template{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE conversation_templates SET name = ?, description = ?, message = ?, system_prompt = ?, settings = ?, updated_at = ? WHERE id = ?`, strings.TrimSpace(input.Name), input.Description, input.Message, input.SystemPrompt, string(settings), formatTime(time.Now().UTC()), id)
	if err != nil {
		return Template{}, err
	}
	if err = requireAffected(result); err != nil {
		return Template{}, err
	}
	return s.GetTemplate(ctx, id)
}
func (s *Store) DeleteTemplate(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM conversation_templates WHERE id = ?", id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}
func scanTemplate(row rowScanner) (Template, error) {
	var item Template
	var settings, created, updated string
	err := row.Scan(&item.ID, &item.Name, &item.Description, &item.Message, &item.SystemPrompt, &settings, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	if err = json.Unmarshal([]byte(settings), &item.Settings); err != nil {
		return item, err
	}
	if item.CreatedAt, err = parseTime(created); err != nil {
		return item, err
	}
	item.UpdatedAt, err = parseTime(updated)
	return item, err
}
