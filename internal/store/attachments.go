package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ListTextAttachments returns metadata only, scoped to a conversation.
func (s *Store) ListTextAttachments(ctx context.Context, conversationID int64) ([]Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT a.id, a.message_id, a.file_name, a.media_type, LENGTH(a.data)
FROM message_attachments a JOIN messages m ON m.id = a.message_id
WHERE m.conversation_id = ? AND a.media_type = 'text/plain'
ORDER BY a.id`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list text attachments: %w", err)
	}
	defer rows.Close()
	attachments := make([]Attachment, 0)
	for rows.Next() {
		var attachment Attachment
		if err := rows.Scan(&attachment.ID, &attachment.MessageID, &attachment.FileName, &attachment.MediaType, &attachment.Size); err != nil {
			return nil, fmt.Errorf("scan text attachment: %w", err)
		}
		attachments = append(attachments, attachment)
	}
	return attachments, rows.Err()
}

// ReadTextAttachment reads the requested BLOB slice and never resolves files
// belonging to another conversation. A zero limit reads the remaining file.
// Size describes the whole file.
func (s *Store) ReadTextAttachment(ctx context.Context, conversationID, attachmentID, offset, limit int64) (Attachment, error) {
	if offset < 0 || limit < 0 {
		return Attachment{}, errors.New("attachment offset and limit must be non-negative")
	}
	var attachment Attachment
	err := s.db.QueryRowContext(ctx, `
SELECT a.id, a.message_id, a.file_name, a.media_type, LENGTH(a.data), SUBSTR(a.data, ? + 1, CASE WHEN ? = 0 THEN LENGTH(a.data) ELSE ? END)
FROM message_attachments a JOIN messages m ON m.id = a.message_id
WHERE m.conversation_id = ? AND a.id = ? AND a.media_type = 'text/plain'`, offset, limit, limit, conversationID, attachmentID).Scan(
		&attachment.ID, &attachment.MessageID, &attachment.FileName, &attachment.MediaType, &attachment.Size, &attachment.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("get text attachment: %w", err)
	}
	return attachment, nil
}
