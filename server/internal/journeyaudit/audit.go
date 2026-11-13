// Package journeyaudit records metadata for the Fabrika one-chat criterion.
// These labels never participate in authorization or accountable-human resolution.
package journeyaudit

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const Action = "fabrika_journey_v1"

type Stamp struct {
	Source        string `json:"source"`         // human, agent, service, unknown
	Route         string `json:"route"`          // web, telegram, cli, unknown
	RouteEvidence string `json:"route_evidence"` // server, client, unknown
	UserID        string `json:"user_id"`
	AgentID       string `json:"agent_id,omitempty"`
	Via           string `json:"via,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
}

type key struct{}

func Enabled() bool { return os.Getenv("FABRIKA_JOURNEY_AUDIT") == "true" }

// With is for authenticated server producers. Never decode a Stamp from a client.
func With(ctx context.Context, s Stamp) context.Context { return context.WithValue(ctx, key{}, s) }

func From(ctx context.Context) Stamp {
	if s, ok := ctx.Value(key{}).(Stamp); ok {
		return s
	}
	return Stamp{Source: "unknown", Route: "unknown", RouteEvidence: "unknown"}
}

// HTTPStamp is called only AFTER Auth has verified the credential and stripped
// agent headers. A PAT's owner is not evidence that a human acted. A CLI header
// is retained as a claim, never promoted into server-verified provenance.
func HTTPStamp(user, agent, task, credential, tokenHash, platform string, cookie bool) Stamp {
	s := Stamp{Source: "unknown", Route: "unknown", RouteEvidence: "unknown", UserID: user}
	if platform == "cli" {
		s.Route, s.RouteEvidence = "cli", "client"
	}
	switch credential {
	case "task_token":
		s.Source, s.AgentID, s.TaskID = "agent", agent, task
		if agent != "" && agent == os.Getenv("FABRIKA_MULTIKA_AGENT_ID") {
			s.Via = "multika"
		}
	case "cloud_pat":
		s.Source = "service"
	case "pat":
		// Register only dedicated existing machine credentials. Shared owner
		// PATs must stay unknown. Hashes are read from private server config;
		// neither hashes nor tokens are persisted in the audit.
		for _, hash := range strings.Split(os.Getenv("FABRIKA_AUDIT_SERVICE_PAT_HASHES"), ",") {
			if len(hash) == 64 && hash == tokenHash {
				s.Source = "service"
			}
		}
	case "session":
		if cookie {
			s.Source, s.Route, s.RouteEvidence = "human", "web", "server"
		}
	}
	return s
}

func Channel(user, route string) Stamp {
	s := Stamp{Source: "human", Route: "unknown", RouteEvidence: "server", UserID: user}
	if route == "telegram" {
		s.Route = route
	}
	if user == "" {
		s.Source = "unknown"
	}
	return s
}

type Entry struct {
	Stamp
	Kind      string `json:"kind"`
	Outcome   string `json:"outcome"`
	ProjectID string `json:"project_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	AskID     string `json:"ask_id,omitempty"`
	CommentID string `json:"comment_id,omitempty"`
	SurfaceID string `json:"surface_id,omitempty"`
	// Method/category are bounded enums, never paths, queries or request bodies.
	Method string `json:"method,omitempty"`
}

type Writer interface {
	CreateActivity(context.Context, db.CreateActivityParams) (db.ActivityLog, error)
}

// ActivityDetails enriches a NEW existing journal row; it never rewrites history.
func ActivityDetails(raw []byte, s Stamp, actorType, actorID string) []byte {
	if !Enabled() {
		return raw
	}
	if s.Source == "" {
		s = From(context.Background())
	}
	if s.UserID == "" && actorType == "member" {
		s.UserID = actorID
	}
	if s.Source == "unknown" && actorType == "agent" {
		s.Source = "agent"
		s.AgentID = actorID
	}
	if s.Source == "unknown" && actorType == "system" {
		s.Source = "service"
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		fields = map[string]json.RawMessage{}
	}
	fields["journey"], _ = json.Marshal(s)
	out, _ := json.Marshal(fields)
	return out
}

func Record(ctx context.Context, q Writer, workspace pgtype.UUID, e Entry) (pgtype.UUID, error) {
	if !Enabled() {
		return pgtype.UUID{}, nil
	}
	if e.Source == "" {
		e.Stamp = From(ctx)
	}
	actorType := "member"
	actorID := e.UserID
	if e.Source == "agent" {
		actorType, actorID = "agent", e.AgentID
	}
	if actorID == "" {
		actorType = "system"
	}
	var aid pgtype.UUID
	if actorID != "" {
		if err := aid.Scan(actorID); err != nil {
			return pgtype.UUID{}, err
		}
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return pgtype.UUID{}, err
	}
	row, err := q.CreateActivity(ctx, db.CreateActivityParams{
		ID: dbid.NewV7(), WorkspaceID: workspace, ActorType: pgtype.Text{String: actorType, Valid: true}, ActorID: aid,
		Action: Action, Details: raw,
	})
	return row.ID, err
}
