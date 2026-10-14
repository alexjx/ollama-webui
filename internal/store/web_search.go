package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Tokens are server-owned application settings, separate from conversation data.
type WebSearchSettings struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"-"`
}

// UpdateConversationWebSearch changes only this session's tool permission.
func (s *Store) UpdateConversationWebSearch(ctx context.Context, id int64, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE conversations SET web_search_enabled = ?, updated_at = ? WHERE id = ?`,
		enabled, formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("update session web search: %w", err)
	}
	return requireAffected(result)
}

func (s *Store) GetWebSearchSettings(ctx context.Context) (WebSearchSettings, error) {
	var settings WebSearchSettings
	err := s.db.QueryRowContext(ctx, "SELECT enabled, api_token FROM web_search_settings WHERE id = 1").Scan(&settings.Enabled, &settings.Token)
	if errors.Is(err, sql.ErrNoRows) {
		return WebSearchSettings{}, nil
	}
	return settings, err
}

func (s *Store) SetWebSearchSettings(ctx context.Context, settings WebSearchSettings) error {
	if len(settings.Token) > 512 || strings.IndexFunc(settings.Token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("Tavily API token must contain at most 512 bytes and no whitespace or control characters")
	}
	if settings.Enabled && settings.Token == "" {
		return errors.New("add a Tavily API token before enabling web search")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO web_search_settings (id, enabled, api_token, updated_at) VALUES (1, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled, api_token = excluded.api_token, updated_at = excluded.updated_at`, settings.Enabled, settings.Token, formatTime(time.Now().UTC()))
	return err
}
