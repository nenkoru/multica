package journeyaudit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCredentialProvenance(t *testing.T) {
	hash := strings.Repeat("a", 64)
	t.Setenv("FABRIKA_AUDIT_SERVICE_PAT_HASHES", hash)
	for _, tc := range []struct {
		name, kind, hash, platform string
		cookie                     bool
		source, route, evidence    string
	}{
		{"manual web", "session", "", "web", true, "human", "web", "server"},
		{"bearer is ambiguous", "session", "", "web", false, "unknown", "unknown", "unknown"},
		{"member service", "pat", hash, "cli", false, "service", "cli", "client"},
		{"shared PAT", "pat", "other", "web", false, "unknown", "unknown", "unknown"},
		{"CLI claim", "pat", "other", "cli", false, "unknown", "cli", "client"},
		{"Multika action", "task_token", "", "cli", false, "agent", "cli", "client"},
		{"cloud service", "cloud_pat", "", "web", false, "service", "unknown", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := HTTPStamp("user", "agent", "task", tc.kind, tc.hash, tc.platform, tc.cookie)
			if s.Source != tc.source || s.Route != tc.route || s.RouteEvidence != tc.evidence {
				t.Fatalf("stamp %+v", s)
			}
			if tc.kind != "task_token" && (s.AgentID != "" || s.TaskID != "") {
				t.Fatal("forged agent identity retained")
			}
		})
	}
	if s := From(context.Background()); s.Source != "unknown" || s.Route != "unknown" {
		t.Fatal(s)
	}
}

type captureWriter struct{ rows []db.CreateActivityParams }

func (w *captureWriter) CreateActivity(_ context.Context, p db.CreateActivityParams) (db.ActivityLog, error) {
	w.rows = append(w.rows, p)
	return db.ActivityLog{ID: p.ID}, nil
}

func TestAuditMetadataAndFlag(t *testing.T) {
	w := &captureWriter{}
	var ws pgtype.UUID
	_ = ws.Scan("00000000-0000-0000-0000-000000000001")
	stamp := Stamp{Source: "service", Route: "unknown", RouteEvidence: "server", UserID: "00000000-0000-0000-0000-000000000002"}
	t.Setenv("FABRIKA_JOURNEY_AUDIT", "false")
	_, err := Record(context.Background(), w, ws, Entry{Stamp: stamp, Kind: "issue_action", Outcome: "complete"})
	if err != nil || len(w.rows) != 0 {
		t.Fatal("disabled collector wrote")
	}
	t.Setenv("FABRIKA_JOURNEY_AUDIT", "true")
	_, err = Record(context.Background(), w, ws, Entry{Stamp: stamp, Kind: "issue_action", Outcome: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	p := w.rows[0]
	if p.ActorType.String != "member" || uuidString(p.ActorID) != stamp.UserID {
		t.Fatalf("actor identity changed: %+v", p)
	}
	var fields map[string]any
	if json.Unmarshal(p.Details, &fields) != nil {
		t.Fatal("invalid metadata")
	}
	for _, k := range []string{"content", "text", "cookie", "token", "token_hash", "path", "query", "email"} {
		if _, ok := fields[k]; ok {
			t.Fatalf("private field %s", k)
		}
	}
}
