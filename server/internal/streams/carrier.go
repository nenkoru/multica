package streams

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// Carrier runs a step on Multica's execution machine: one short issue per
// step in the hidden streams/<slug> project, assigned to the executor agent
// (§4.3). Claim, retry and the runtime sweeper stay upstream's.
type Carrier interface {
	// Dispatch creates the carrier issue assigned to req.AgentID and returns
	// the task Multica enqueued for it. A zero TaskID is an error: the agent
	// is not ready and the step must go to the next executor.
	Dispatch(ctx context.Context, req DispatchRequest) (DispatchResult, error)
	// Cancel stops a hung task through upstream's cancel path.
	Cancel(ctx context.Context, taskID pgtype.UUID, reason string) error
	// Close moves the carrier issue to done or cancelled with its step.
	Close(ctx context.Context, issueID pgtype.UUID, done bool) error
}

type DispatchRequest struct {
	WorkspaceID pgtype.UUID
	ProjectID   pgtype.UUID
	// CreatorAgentID is the stream's manager; carrier issues are agent-made.
	CreatorAgentID pgtype.UUID
	AgentID        pgtype.UUID
	Title          string
	Body           string
}

type DispatchResult struct {
	IssueID pgtype.UUID
	TaskID  pgtype.UUID
}
