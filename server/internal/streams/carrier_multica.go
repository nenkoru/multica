package streams

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MulticaCarrier is the production Carrier: upstream IssueService creates
// the carrier issue (and through it the agent task), TaskService cancels.
type MulticaCarrier struct {
	Issues  *service.IssueService
	Tasks   *service.TaskService
	Queries *db.Queries
}

func (m *MulticaCarrier) Dispatch(ctx context.Context, req DispatchRequest) (DispatchResult, error) {
	res, err := m.Issues.Create(ctx, service.IssueCreateParams{
		WorkspaceID:    req.WorkspaceID,
		Title:          req.Title,
		Description:    pgtype.Text{String: req.Body, Valid: true},
		Status:         "todo",
		Priority:       "medium",
		AssigneeType:   pgtype.Text{String: "agent", Valid: true},
		AssigneeID:     req.AgentID,
		CreatorType:    "agent",
		CreatorID:      req.CreatorAgentID,
		ProjectID:      req.ProjectID,
		AllowDuplicate: true,
	}, service.IssueCreateOpts{ActorID: uuidString(req.CreatorAgentID), AnalyticsAgentID: uuidString(req.AgentID)})
	if err != nil {
		return DispatchResult{}, fmt.Errorf("create carrier issue: %w", err)
	}
	return DispatchResult{IssueID: res.Issue.ID, TaskID: res.AssignedTaskID}, nil
}

func (m *MulticaCarrier) Cancel(ctx context.Context, taskID pgtype.UUID, reason string) error {
	_, err := m.Tasks.CancelTaskWithReason(ctx, taskID, reason, "fabrika_lease_expired")
	return err
}

func (m *MulticaCarrier) Close(ctx context.Context, issueID pgtype.UUID, done bool) error {
	issue, err := m.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return err
	}
	status := "done"
	if !done {
		status = "cancelled"
		if err := m.Tasks.CancelTasksForIssue(ctx, issueID); err != nil {
			return err
		}
	}
	if issue.Status == status {
		return nil
	}
	_, err = m.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: issueID, Status: status, WorkspaceID: issue.WorkspaceID})
	return err
}

func (m *MulticaCarrier) TaskComments(ctx context.Context, issueID, taskID pgtype.UUID) ([]string, error) {
	issue, err := m.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return nil, err
	}
	rows, err := m.Queries.ListCommentsSinceForIssue(ctx, db.ListCommentsSinceForIssueParams{
		IssueID: issueID, WorkspaceID: issue.WorkspaceID, CreatedAt: pgtype.Timestamptz{Valid: true}, Limit: 200,
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].SourceTaskID == taskID {
			out = append(out, rows[i].Content)
		}
	}
	return out, nil
}
