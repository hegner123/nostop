package storage

import (
    "context"
    "fmt"
    "time"

    "github.com/google/uuid"
)

// ----------------------------------------------------------------------------
// Work Unit Archive Operations (Phase D: Plan-Driven Archiving)
// ----------------------------------------------------------------------------

// ListActiveMessagesByWorkUnit retrieves non-archived messages for a work unit.
func (s *SQLite) ListActiveMessagesByWorkUnit(ctx context.Context, workUnitID string) ([]Message, error) {
    query := `
        SELECT id, conversation_id, topic_id, role, content, token_count, is_archived, created_at,
               COALESCE(is_summary, FALSE), COALESCE(summary_source, ''), summary_source_id, work_unit_id
        FROM messages
        WHERE work_unit_id = ? AND is_archived = FALSE
        ORDER BY created_at ASC
    `
    rows, err := s.db.QueryContext(ctx, query, workUnitID)
    if err != nil {
        return nil, fmt.Errorf("failed to list active messages by work unit: %w", err)
    }
    defer rows.Close()

    var messages []Message
    for rows.Next() {
        var msg Message
        err := rows.Scan(
            &msg.ID, &msg.ConversationID, &msg.TopicID, &msg.Role, &msg.Content,
            &msg.TokenCount, &msg.IsArchived, &msg.CreatedAt,
            &msg.IsSummary, &msg.SummarySource, &msg.SummarySourceID, &msg.WorkUnitID,
        )
        if err != nil {
            return nil, fmt.Errorf("failed to scan message: %w", err)
        }
        messages = append(messages, msg)
    }
    return messages, rows.Err()
}

// ArchiveWorkUnitWithSummary archives a work unit's messages, optionally creating a summary.
// The work unit must be in Complete status.
//
// This method:
//   1. Archives messages to message_archive table
//   2. Marks messages as archived
//   3. Creates summary message (if provided) with summary_source = 'work_unit'
//   4. Updates work unit status to Archived
func (s *SQLite) ArchiveWorkUnitWithSummary(ctx context.Context, workUnitID string, usageBefore, usageAfter float64, summary *SummaryMessageInput) error {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %w", err)
    }
    defer tx.Rollback()

    now := time.Now()

    // Get work unit info
    var wu WorkUnit
    err = tx.QueryRowContext(ctx,
        "SELECT id, conversation_id, plan_file, name, level, status, parent_id, line_number, created_at, completed_at FROM work_units WHERE id = ?",
        workUnitID,
    ).Scan(&wu.ID, &wu.ConversationID, &wu.PlanFile, &wu.Name, &wu.Level,
        &wu.Status, &wu.ParentID, &wu.LineNumber, &wu.CreatedAt, &wu.CompletedAt)
    if err != nil {
        return fmt.Errorf("failed to get work unit: %w", err)
    }

    // Validate status
    if wu.Status != WorkUnitComplete {
        return fmt.Errorf("work unit must be complete before archiving (current status: %s)", wu.Status.String())
    }

    // Get non-archived messages for this work unit
    rows, err := tx.QueryContext(ctx,
        "SELECT id, content, token_count, topic_id FROM messages WHERE work_unit_id = ? AND is_archived = FALSE",
        workUnitID,
    )
    if err != nil {
        return fmt.Errorf("failed to get messages: %w", err)
    }

    var tokensAffected int
    var messageIDs []string
    var archiveInserts []struct {
        id        string
        messageID string
        topicID   *string
        content   string
    }

    for rows.Next() {
        var msgID, content string
        var topicID *string
        var tokenCount int
        if err := rows.Scan(&msgID, &content, &tokenCount, &topicID); err != nil {
            rows.Close()
            return fmt.Errorf("failed to scan message: %w", err)
        }
        tokensAffected += tokenCount
        messageIDs = append(messageIDs, msgID)
        archiveInserts = append(archiveInserts, struct {
            id        string
            messageID string
            topicID   *string
            content   string
        }{
            id:        uuid.New().String(),
            messageID: msgID,
            topicID:   topicID,
            content:   content,
        })
    }
    rows.Close()

    // Archive messages to message_archive table
    // Note: We use work_unit_id as the grouping mechanism, but message_archive
    // uses topic_id. For work unit archives, we'll store the work_unit_id in
    // a way that can be retrieved (using the topic_id field with a prefix).
    for _, ins := range archiveInserts {
        // Use topic_id if present, otherwise use work unit ID with prefix
        archiveTopicID := workUnitID
        if ins.topicID != nil && *ins.topicID != "" {
            archiveTopicID = *ins.topicID
        }
        _, err = tx.ExecContext(ctx,
            "INSERT INTO message_archive (id, message_id, topic_id, full_content, archived_at) VALUES (?, ?, ?, ?, ?)",
            ins.id, ins.messageID, archiveTopicID, ins.content, now,
        )
        if err != nil {
            return fmt.Errorf("failed to archive message: %w", err)
        }
    }

    // Mark messages as archived
    for _, msgID := range messageIDs {
        _, err = tx.ExecContext(ctx,
            "UPDATE messages SET is_archived = TRUE WHERE id = ?",
            msgID,
        )
        if err != nil {
            return fmt.Errorf("failed to mark message as archived: %w", err)
        }
    }

    // Create summary message if provided (stays in active context)
    if summary != nil {
        summaryID := uuid.New().String()
        _, err = tx.ExecContext(ctx,
            `INSERT INTO messages (id, conversation_id, topic_id, role, content, token_count, is_archived, created_at, is_summary, summary_source, summary_source_id, work_unit_id)
            VALUES (?, ?, NULL, ?, ?, ?, FALSE, ?, TRUE, 'work_unit', ?, ?)`,
            summaryID, wu.ConversationID, RoleSystem, summary.Content,
            summary.TokenCount, now, workUnitID, workUnitID,
        )
        if err != nil {
            return fmt.Errorf("failed to create summary message: %w", err)
        }
    }

    // Update work unit status to Archived
    _, err = tx.ExecContext(ctx,
        "UPDATE work_units SET status = ?, completed_at = ? WHERE id = ?",
        WorkUnitArchived, now, workUnitID,
    )
    if err != nil {
        return fmt.Errorf("failed to update work unit status: %w", err)
    }

    // Record archive event (using work_unit_id as topic_id for tracking)
    _, err = tx.ExecContext(ctx,
        "INSERT INTO archive_events (conversation_id, topic_id, action, tokens_affected, context_usage_before, context_usage_after, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
        wu.ConversationID, workUnitID, ArchiveActionArchive, tokensAffected, usageBefore, usageAfter, now,
    )
    if err != nil {
        return fmt.Errorf("failed to record archive event: %w", err)
    }

    return tx.Commit()
}

// RestoreWorkUnit restores an archived work unit's messages back into active context.
// If keepSummary is false, the summary message is deleted.
func (s *SQLite) RestoreWorkUnit(ctx context.Context, workUnitID string, keepSummary bool, usageBefore, usageAfter float64) error {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %w", err)
    }
    defer tx.Rollback()

    now := time.Now()

    // Get work unit info
    var wu WorkUnit
    err = tx.QueryRowContext(ctx,
        "SELECT id, conversation_id, plan_file, name, level, status, parent_id, line_number, created_at, completed_at FROM work_units WHERE id = ?",
        workUnitID,
    ).Scan(&wu.ID, &wu.ConversationID, &wu.PlanFile, &wu.Name, &wu.Level,
        &wu.Status, &wu.ParentID, &wu.LineNumber, &wu.CreatedAt, &wu.CompletedAt)
    if err != nil {
        return fmt.Errorf("failed to get work unit: %w", err)
    }

    // Validate status
    if wu.Status != WorkUnitArchived {
        return fmt.Errorf("work unit must be archived to restore (current status: %s)", wu.Status.String())
    }

    // Get archived messages for this work unit
    rows, err := tx.QueryContext(ctx,
        `SELECT ma.message_id, ma.full_content
         FROM message_archive ma
         JOIN messages m ON m.id = ma.message_id
         WHERE m.work_unit_id = ?`,
        workUnitID,
    )
    if err != nil {
        return fmt.Errorf("failed to get archived messages: %w", err)
    }

    var tokensAffected int
    type restoreData struct {
        messageID string
        content   string
    }
    var restores []restoreData

    for rows.Next() {
        var r restoreData
        if err := rows.Scan(&r.messageID, &r.content); err != nil {
            rows.Close()
            return fmt.Errorf("failed to scan archived message: %w", err)
        }
        restores = append(restores, r)
    }
    rows.Close()

    // Restore messages
    for _, r := range restores {
        // Get token count
        var tokenCount int
        err = tx.QueryRowContext(ctx, "SELECT token_count FROM messages WHERE id = ?", r.messageID).Scan(&tokenCount)
        if err != nil {
            return fmt.Errorf("failed to get message token count: %w", err)
        }
        tokensAffected += tokenCount

        // Restore message content and unarchive
        _, err = tx.ExecContext(ctx,
            "UPDATE messages SET content = ?, is_archived = FALSE WHERE id = ?",
            r.content, r.messageID,
        )
        if err != nil {
            return fmt.Errorf("failed to restore message: %w", err)
        }
    }

    // Delete archive records for these messages
    for _, r := range restores {
        _, err = tx.ExecContext(ctx, "DELETE FROM message_archive WHERE message_id = ?", r.messageID)
        if err != nil {
            return fmt.Errorf("failed to delete archive record: %w", err)
        }
    }

    // Optionally delete the summary message
    if !keepSummary {
        _, err = tx.ExecContext(ctx,
            "DELETE FROM messages WHERE is_summary = TRUE AND summary_source = 'work_unit' AND summary_source_id = ?",
            workUnitID,
        )
        if err != nil {
            return fmt.Errorf("failed to delete summary message: %w", err)
        }
    }

    // Update work unit status back to Complete
    _, err = tx.ExecContext(ctx,
        "UPDATE work_units SET status = ? WHERE id = ?",
        WorkUnitComplete, workUnitID,
    )
    if err != nil {
        return fmt.Errorf("failed to update work unit status: %w", err)
    }

    // Record restore event
    _, err = tx.ExecContext(ctx,
        "INSERT INTO archive_events (conversation_id, topic_id, action, tokens_affected, context_usage_before, context_usage_after, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
        wu.ConversationID, workUnitID, ArchiveActionRestore, tokensAffected, usageBefore, usageAfter, now,
    )
    if err != nil {
        return fmt.Errorf("failed to record restore event: %w", err)
    }

    return tx.Commit()
}

// GetWorkUnitStats retrieves statistics for a work unit.
func (s *SQLite) GetWorkUnitStats(ctx context.Context, workUnitID string) (*WorkUnitStats, error) {
    stats := &WorkUnitStats{WorkUnitID: workUnitID}

    // Get message counts and tokens
    err := s.db.QueryRowContext(ctx,
        `SELECT COUNT(*), COALESCE(SUM(token_count), 0)
         FROM messages WHERE work_unit_id = ?`,
        workUnitID,
    ).Scan(&stats.MessageCount, &stats.TotalTokens)
    if err != nil {
        return nil, fmt.Errorf("failed to get message stats: %w", err)
    }

    // Get archived tokens
    err = s.db.QueryRowContext(ctx,
        `SELECT COALESCE(SUM(token_count), 0)
         FROM messages WHERE work_unit_id = ? AND is_archived = TRUE`,
        workUnitID,
    ).Scan(&stats.ArchivedTokens)
    if err != nil {
        return nil, fmt.Errorf("failed to get archived token stats: %w", err)
    }

    stats.ActiveTokens = stats.TotalTokens - stats.ArchivedTokens

    // Check for summary message
    var summaryCount int
    err = s.db.QueryRowContext(ctx,
        `SELECT COUNT(*) FROM messages
         WHERE is_summary = TRUE AND summary_source = 'work_unit' AND summary_source_id = ?`,
        workUnitID,
    ).Scan(&summaryCount)
    if err != nil {
        return nil, fmt.Errorf("failed to check for summary: %w", err)
    }
    stats.HasSummary = summaryCount > 0

    return stats, nil
}

// WorkUnitStats contains statistics about a work unit's messages.
type WorkUnitStats struct {
    WorkUnitID     string `json:"work_unit_id"`
    MessageCount   int    `json:"message_count"`
    TotalTokens    int    `json:"total_tokens"`
    ArchivedTokens int    `json:"archived_tokens"`
    ActiveTokens   int    `json:"active_tokens"`
    HasSummary     bool   `json:"has_summary"`
}

// GetWorkUnitSummaryPreview returns the summary content for an archived work unit.
// Returns empty string if no summary exists.
func (s *SQLite) GetWorkUnitSummaryPreview(ctx context.Context, workUnitID string) (string, error) {
    var content string
    err := s.db.QueryRowContext(ctx,
        `SELECT content FROM messages
         WHERE is_summary = TRUE AND summary_source = 'work_unit' AND summary_source_id = ?
         LIMIT 1`,
        workUnitID,
    ).Scan(&content)
    if err != nil {
        // No summary found is not an error
        return "", nil
    }
    return content, nil
}
