package telegram

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestPickNoticePerAgent(t *testing.T) {
	multika := pgtype.UUID{Bytes: [16]byte{7}, Valid: true}
	other := pgtype.UUID{Bytes: [16]byte{8}, Valid: true}
	f := NoticesFunc(func(id pgtype.UUID) (Notices, bool) {
		if id != multika {
			return Notices{}, false
		}
		return Notices{TaskFailed: "Мультика недоступна"}, true
	})
	failed := func(n Notices) string { return n.TaskFailed }
	offline := func(n Notices) string { return n.AgentOffline }
	if got := pickNotice(f, multika, failed, taskFailedText); got != "Мультика недоступна" {
		t.Fatalf("multika: %q", got)
	}
	if got := pickNotice(f, other, failed, taskFailedText); got != taskFailedText {
		t.Fatalf("other agent must keep upstream text: %q", got)
	}
	if got := pickNotice(f, multika, offline, msgAgentOffline); got != msgAgentOffline {
		t.Fatalf("empty override must keep upstream text: %q", got)
	}
	if got := (&Outbound{}).failedText(multika); got != taskFailedText {
		t.Fatalf("no notices installed: %q", got)
	}
}
