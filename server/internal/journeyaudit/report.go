package journeyaudit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/util"
)

type Count struct {
	Kind          string `json:"kind"`
	Source        string `json:"source"`
	Route         string `json:"route"`
	RouteEvidence string `json:"route_evidence"`
	Outcome       string `json:"outcome"`
	Via           string `json:"via"`
	Count         int64  `json:"count"`
}

type Summary struct {
	From                          time.Time   `json:"from"`
	To                            time.Time   `json:"to"`
	WorkspaceID                   pgtype.UUID `json:"workspace_id"`
	ProjectID                     string      `json:"project_id,omitempty"`
	Counts                        []Count     `json:"counts"`
	UserMessages                  int64       `json:"user_messages"`
	UnknownMessageSources         int64       `json:"unknown_message_sources"`
	MultikaTurns                  int64       `json:"multika_turns"`
	ActiveSessions                int64       `json:"active_sessions"`
	MemberActivitiesUnknown       int64       `json:"member_activities_unknown"`
	WorkspaceAnswersWithoutSource int64       `json:"workspace_answers_without_source"`
	MemberCommentsUnknown         int64       `json:"member_comments_unknown"`
	UnscopedEvents                int64       `json:"unscoped_events"`
	CoverageComplete              bool        `json:"coverage_complete"`
	Verdict                       string      `json:"verdict"`
}

// Report is metadata-only. Authorization and current complete-project access
// are required by the HTTP caller. Account scope is ALWAYS the authenticated
// user; no user selector or transcript is accepted or returned.
func Report(ctx context.Context, pool *pgxpool.Pool, ws, user, agent pgtype.UUID, project string, from, to time.Time) (Summary, error) {
	out := Summary{From: from, To: to, WorkspaceID: ws, ProjectID: project, Counts: []Count{}, Verdict: "insufficient_evidence"}
	rows, err := pool.Query(ctx, `SELECT details->>'kind',COALESCE(details->>'source','unknown'),COALESCE(details->>'route','unknown'),
 COALESCE(details->>'route_evidence','unknown'),COALESCE(details->>'outcome','pending'),COALESCE(details->>'via',''),
 CASE WHEN details->>'kind'='board_open' THEN count(DISTINCT details->>'surface_id') ELSE count(*) END
 FROM activity_log WHERE workspace_id=$1 AND action=$2 AND details->>'user_id'=$3
 AND created_at >= $4 AND created_at < $5
 AND ($6='' OR details->>'project_id'=$6)
 AND (details->>'kind'<>'chat_message' OR details->>'session_id' IN (SELECT id::text FROM chat_session WHERE workspace_id=$1 AND creator_id=$7 AND agent_id=$8))
 GROUP BY 1,2,3,4,5,6 ORDER BY 1,2,3,4,5,6`, ws, Action, uuidString(user), from, to, project, user, agent)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c Count
		if err = rows.Scan(&c.Kind, &c.Source, &c.Route, &c.RouteEvidence, &c.Outcome, &c.Via, &c.Count); err != nil {
			rows.Close()
			return out, err
		}
		out.Counts = append(out.Counts, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// A project-filtered report must not quietly drop workspace-level opens or
	// requests whose project was not established. They remain explicitly unscoped.
	if project != "" {
		err = pool.QueryRow(ctx, `SELECT count(*) FROM activity_log WHERE workspace_id=$1 AND action=$2 AND details->>'user_id'=$3 AND created_at >= $4 AND created_at < $5 AND COALESCE(details->>'project_id','')=''`, ws, Action, uuidString(user), from, to).Scan(&out.UnscopedEvents)
		if err != nil {
			return out, err
		}
	}
	// Chat/turn counts are account-wide, independent of project filtering:
	// a permanent chat spans projects. Never inspect message content/actions.
	err = pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT m.chat_session_id),count(*) FILTER(WHERE NOT EXISTS (
 SELECT 1 FROM activity_log a WHERE a.workspace_id=$1 AND a.action=$6 AND a.details->>'message_id'=m.id::text AND a.details->>'source'<>'unknown' AND a.details->>'user_id'=$2::uuid::text))
 FROM chat_message m JOIN chat_session c ON c.id=m.chat_session_id
 WHERE c.workspace_id=$1 AND c.creator_id=$2 AND c.agent_id=$3 AND m.role='user'
 AND m.created_at >= $4 AND m.created_at < $5`, ws, user, agent, from, to, Action).Scan(&out.UserMessages, &out.ActiveSessions, &out.UnknownMessageSources)
	if err != nil {
		return out, err
	}
	err = pool.QueryRow(ctx, `SELECT count(*) FROM multika_turn t JOIN chat_session c ON c.id=t.chat_session_id
 WHERE c.workspace_id=$1 AND c.creator_id=$2 AND c.agent_id=$3 AND t.created_at >= $4 AND t.created_at < $5`, ws, user, agent, from, to).Scan(&out.MultikaTurns)
	if err != nil {
		return out, err
	}
	// Any unstamped member activity is unknown, including new writes from an
	// uninstrumented producer. Never backfill from actor identity or a date.
	err = pool.QueryRow(ctx, `SELECT count(*) FROM activity_log a LEFT JOIN issue i ON i.id=a.issue_id AND i.workspace_id=a.workspace_id
 WHERE a.workspace_id=$1 AND a.actor_id=$2 AND a.actor_type='member' AND a.action<>$3
 AND a.created_at >= $4 AND a.created_at < $5 AND ($6='' OR i.project_id::text=$6)
 AND COALESCE(a.details->'journey'->>'source','unknown')='unknown'`, ws, user, Action, from, to, project).Scan(&out.MemberActivitiesUnknown)
	if err != nil {
		return out, err
	}
	err = pool.QueryRow(ctx, `SELECT count(*) FROM ask a JOIN stream s ON s.id=a.stream_id
 WHERE s.workspace_id=$1 AND a.status='answered' AND a.resolved_at >= $2 AND a.resolved_at < $3
 AND ($4='' OR s.carrier_project_id::text=$4) AND NOT EXISTS(SELECT 1 FROM activity_log l WHERE l.workspace_id=$1 AND l.action=$5 AND l.details->>'ask_id'=a.id::text AND l.details->>'source'<>'unknown')`, ws, from, to, project, Action).Scan(&out.WorkspaceAnswersWithoutSource)
	if err != nil {
		return out, err
	}
	err = pool.QueryRow(ctx, `SELECT count(*) FROM comment c JOIN issue i ON i.id=c.issue_id
 WHERE i.workspace_id=$1 AND c.author_type='member' AND c.author_id=$2 AND c.created_at >= $3 AND c.created_at < $4
 AND ($5='' OR i.project_id::text=$5) AND NOT EXISTS(SELECT 1 FROM activity_log a WHERE a.workspace_id=$1 AND a.action=$6 AND a.details->>'comment_id'=c.id::text)`, ws, user, from, to, project, Action).Scan(&out.MemberCommentsUnknown)
	if err != nil {
		return out, err
	}
	// Heartbeats attest only to collector continuity. They do not attest that
	// every browser was upgraded or that an uninstrumented client was unused.
	err = pool.QueryRow(ctx, `WITH beats AS (
 SELECT created_at FROM activity_log WHERE workspace_id=$1 AND action=$2 AND details->>'kind'='coverage'
 AND created_at >= $3::timestamptz-interval '90 seconds' AND created_at < $4
 ), gaps AS (SELECT created_at,created_at-lag(created_at) OVER(ORDER BY created_at) AS gap FROM beats)
 SELECT COALESCE(min(created_at)<=$3 AND max(created_at)>=$4::timestamptz-interval '90 seconds'
 AND COALESCE(max(gap),interval '0 seconds')<=interval '90 seconds',false) FROM gaps`, ws, Action, from, to).Scan(&out.CoverageComplete)
	if err != nil {
		return out, err
	}
	out.Verdict = verdict(out)
	return out, nil
}

// ready_for_acceptance is a candidate for Mika, not an assertion of physical
// human presence or coverage of old/uninstrumented clients. Ops must attach the
// deployed-client/version attestation and native Telegram smoke to that window.
func verdict(s Summary) string {
	unknown := s.UnknownMessageSources + s.MemberActivitiesUnknown + s.MemberCommentsUnknown + s.WorkspaceAnswersWithoutSource + s.UnscopedEvents
	var web, tg, answered int64
	for _, c := range s.Counts {
		if c.Outcome == "rejected" {
			continue
		}
		if c.Source == "unknown" || c.Outcome != "complete" {
			unknown += c.Count
		}
		if c.Source != "human" || c.Outcome != "complete" {
			continue
		}
		if c.Kind == "board_open" || c.Kind == "issue_action" {
			return "criterion_failed"
		}
		// A human web read without a page signal may be a preload or a lost
		// opening event. It cannot certify that the board stayed closed.
		if c.Kind == "issue_read" {
			unknown += c.Count
		}
		if c.Kind == "chat_message" && c.RouteEvidence == "server" {
			if c.Route == "web" {
				web += c.Count
			}
			if c.Route == "telegram" {
				tg += c.Count
			}
		}
		if c.Kind == "ask_answer" && c.RouteEvidence == "server" && (c.Route == "web" || c.Route == "telegram") {
			answered += c.Count
		}
	}
	if s.ProjectID == "" && s.CoverageComplete && s.To.Sub(s.From) >= 7*24*time.Hour && unknown == 0 && s.ActiveSessions == 1 && s.MultikaTurns > 0 && web > 0 && tg > 0 && answered > 0 {
		return "ready_for_acceptance"
	}
	return "insufficient_evidence"
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return util.UUIDToString(id)
}
