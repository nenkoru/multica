package journeyaudit

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func TestJourneyReportWindowUnknownAndIsolation(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ws, user := dbid.NewV7(), dbid.NewV7()
	_, err = pool.Exec(ctx, `INSERT INTO "user"(id,name,email)VALUES($1,'Synthetic RUSL-3231',$2)`, user, uuidString(user)+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, user) })
	_, err = pool.Exec(ctx, `INSERT INTO workspace(id,name,slug,issue_prefix)VALUES($1,'Synthetic audit',$2,'AUD')`, ws, "audit-"+uuidString(ws))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM activity_log WHERE workspace_id=$1`, ws)
		_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, ws)
	})
	f := testutil.New(pool, uuidString(ws), uuidString(user))
	agent := f.Agent(t, "Synthetic Multika", f.Runtime(t, "Synthetic audit runtime"))
	session := f.ChatSession(t, agent)
	var aid, sid pgtype.UUID
	_ = aid.Scan(agent)
	_ = sid.Scan(session)
	from := time.Now().Add(-10 * time.Minute)
	to := from.Add(5 * time.Minute)
	t.Setenv("FABRIKA_JOURNEY_AUDIT", "true")
	stamp := Stamp{Source: "human", Route: "web", RouteEvidence: "server", UserID: uuidString(user)}
	record := func(e Entry, at time.Time) {
		t.Helper()
		id, err := Record(ctx, db.New(pool), ws, e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE activity_log SET created_at=$2 WHERE id=$1`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	open := Entry{Stamp: stamp, Kind: "board_open", Outcome: "complete", SurfaceID: uuidString(dbid.NewV7())}
	record(open, from.Add(time.Minute))
	record(open, from.Add(2*time.Minute))
	record(Entry{Stamp: stamp, Kind: "issue_read", Outcome: "complete"}, from.Add(time.Minute))
	service := stamp
	service.Source = "service"
	record(Entry{Stamp: service, Kind: "issue_action", Outcome: "complete"}, from.Add(time.Minute))
	agentStamp := stamp
	agentStamp.Source = "agent"
	agentStamp.AgentID = agent
	agentStamp.Via = "multika"
	record(Entry{Stamp: agentStamp, Kind: "issue_action", Outcome: "complete"}, from.Add(time.Minute))
	unknown := stamp
	unknown.Source = "unknown"
	record(Entry{Stamp: unknown, Kind: "issue_action", Outcome: "pending"}, from.Add(time.Minute))
	other := stamp
	other.UserID = uuidString(dbid.NewV7())
	record(Entry{Stamp: other, Kind: "issue_action", Outcome: "complete"}, from.Add(time.Minute))
	record(Entry{Stamp: stamp, Kind: "issue_action", Outcome: "complete"}, to)
	message := f.Insert(t, "chat_message", testutil.Cols{"chat_session_id": session, "role": "user", "content": "Synthetic RUSL-3231 private marker: DO NOT EXPORT", "created_at": from.Add(time.Minute)})
	record(Entry{Stamp: Stamp{Source: "unknown", Route: "unknown", RouteEvidence: "unknown"}, Kind: "chat_message", Outcome: "complete", SessionID: session, MessageID: message}, from.Add(time.Minute))
	for at := from.Add(-time.Second); at.Before(to); at = at.Add(time.Minute) {
		record(Entry{Stamp: Stamp{Source: "service", Route: "unknown", RouteEvidence: "server"}, Kind: "coverage", Outcome: "complete"}, at)
	}
	f.Insert(t, "activity_log", testutil.Cols{"workspace_id": uuidString(ws), "actor_type": "member", "actor_id": uuidString(user), "action": "status_changed", "created_at": from.Add(time.Minute), "details": "{}"})
	out, err := Report(ctx, pool, ws, user, aid, "", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if out.UserMessages != 1 || out.UnknownMessageSources != 1 || out.ActiveSessions != 1 || out.MemberActivitiesUnknown != 1 || !out.CoverageComplete || out.Verdict != "criterion_failed" {
		t.Fatalf("summary %+v", out)
	}
	counts := map[string]int64{}
	for _, c := range out.Counts {
		counts[c.Kind+":"+c.Source] = c.Count
	}
	if counts["board_open:human"] != 1 || counts["issue_read:human"] != 1 || counts["issue_action:service"] != 1 || counts["issue_action:agent"] != 1 || counts["issue_action:unknown"] != 1 || counts["issue_action:human"] != 0 {
		t.Fatalf("counts %+v", counts)
	}
	empty, err := Report(ctx, pool, dbid.NewV7(), user, aid, "", from, to)
	if err != nil || len(empty.Counts) != 0 || empty.UserMessages != 0 {
		t.Fatalf("foreign workspace leaked: %+v %v", empty, err)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "DO NOT EXPORT") || strings.Contains(string(raw), "content") {
		t.Fatal("transcript leaked")
	}
	project := uuidString(dbid.NewV7())
	scoped, err := Report(ctx, pool, ws, user, aid, project, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Counts) != 0 || scoped.UnscopedEvents == 0 {
		t.Fatalf("unscoped metadata silently dropped: %+v", scoped)
	}
	_, err = pool.Exec(ctx, `DELETE FROM activity_log WHERE workspace_id=$1 AND action=$2 AND details->>'kind'='coverage'`, ws, Action)
	if err != nil {
		t.Fatal(err)
	}
	out, err = Report(ctx, pool, ws, user, aid, "", from, to)
	if err != nil || out.CoverageComplete {
		t.Fatalf("missing collector counted complete: %+v %v", out, err)
	}
}
