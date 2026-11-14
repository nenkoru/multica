package engine

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/journeyaudit"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestJourneyNativeChannelAppendKeepsProvenance(t *testing.T) {
	pool := sessionPersistenceTestDB(t)
	f := seedSessionPersistenceFixture(t, pool)
	t.Setenv("FABRIKA_JOURNEY_AUDIT", "true")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE channel_installation SET channel_type='telegram' WHERE id=$1`, f.installationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE channel_chat_session_binding SET channel_type='telegram' WHERE installation_id=$1`, f.installationID); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM activity_log WHERE workspace_id=$1 AND action=$2`, f.workspaceID, journeyaudit.Action)
	})
	// Only synthetic fixtures. No live installation, credentials or user message.
	s := NewChatSession(db.New(pool), pool, channel.Type("telegram"), SessionTitles{})
	result, err := s.AppendUserMessage(ctx, AppendInput{SessionID: f.sessionID, Sender: f.userID, InstallationID: f.installationID, Body: "Synthetic RUSL-3231 metadata test"})
	if err != nil {
		t.Fatal(err)
	}
	var source, route, user string
	err = pool.QueryRow(ctx, `SELECT details->>'source',details->>'route',details->>'user_id' FROM activity_log WHERE workspace_id=$1 AND action=$2 AND details->>'message_id'=$3`, f.workspaceID, journeyaudit.Action, utilUUID(result.MessageID)).Scan(&source, &route, &user)
	if err != nil || source != "human" || route != "telegram" || user != utilUUID(f.userID) {
		t.Fatalf("native stamp %s %s %s %v", source, route, user, err)
	}
	// A failed append leaves neither a second message nor a second audit row.
	_, err = s.AppendUserMessage(ctx, AppendInput{SessionID: uid(252), Sender: f.userID, InstallationID: f.installationID, Body: "Synthetic rejected message"})
	if err == nil {
		t.Fatal("missing session admitted")
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM activity_log WHERE workspace_id=$1 AND action=$2`, f.workspaceID, journeyaudit.Action).Scan(&n); err != nil || n != 1 {
		t.Fatalf("failed append was recorded %d %v", n, err)
	}
}
