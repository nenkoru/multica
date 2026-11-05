package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var ErrPlanningRelationNotFound = errors.New("epic or sprint not found in this workspace")

// ValidatePlanningRelations runs inside the issue write transaction. Deletion
// holds the same workspace lock before unlinking issues, so an attach cannot
// commit a dangling pointer. Existing issue writers omit set_* and preserve
// both relations in SQL; parent/stage changes do not inherit planning links.
func ValidatePlanningRelations(ctx context.Context, q *db.Queries, workspace, epic, sprint pgtype.UUID) error {
	if !epic.Valid && !sprint.Valid {
		return nil
	}
	if err := q.LockPlanningWorkspace(ctx, workspace); err != nil {
		return err
	}
	if epic.Valid {
		if _, err := q.GetEpicInWorkspace(ctx, db.GetEpicInWorkspaceParams{ID: epic, WorkspaceID: workspace}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPlanningRelationNotFound
			}
			return fmt.Errorf("validate epic: %w", err)
		}
	}
	if sprint.Valid {
		if _, err := q.GetSprintInWorkspace(ctx, db.GetSprintInWorkspaceParams{ID: sprint, WorkspaceID: workspace}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPlanningRelationNotFound
			}
			return fmt.Errorf("validate sprint: %w", err)
		}
	}
	return nil
}
