package journeyaudit

import (
	"testing"
	"time"
)

func TestJourneyVerdictRequiresFullEvidence(t *testing.T) {
	base := Summary{From: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), CoverageComplete: true, ActiveSessions: 1, MultikaTurns: 2, Counts: []Count{
		{Kind: "chat_message", Source: "human", Route: "web", RouteEvidence: "server", Outcome: "complete", Count: 1},
		{Kind: "chat_message", Source: "human", Route: "telegram", RouteEvidence: "server", Outcome: "complete", Count: 1},
		{Kind: "ask_answer", Source: "human", Route: "web", RouteEvidence: "server", Outcome: "complete", Count: 1},
	}}
	base.To = base.From.Add(7 * 24 * time.Hour)
	if got := verdict(base); got != "ready_for_acceptance" {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		name   string
		change func(*Summary)
		want   string
	}{
		{"missing Telegram", func(s *Summary) { s.Counts = s.Counts[:1] }, "insufficient_evidence"},
		{"old unknown", func(s *Summary) { s.MemberActivitiesUnknown = 1 }, "insufficient_evidence"},
		{"service template comment", func(s *Summary) { s.MemberCommentsUnknown = 1 }, "insufficient_evidence"},
		{"source unavailable", func(s *Summary) { s.UnknownMessageSources = 1 }, "insufficient_evidence"},
		{"collector gap", func(s *Summary) { s.CoverageComplete = false }, "insufficient_evidence"},
		{"second session", func(s *Summary) { s.ActiveSessions = 2 }, "insufficient_evidence"},
		{"short window", func(s *Summary) { s.To = s.To.Add(-time.Second) }, "insufficient_evidence"},
		{"project is partial", func(s *Summary) { s.ProjectID = "project" }, "insufficient_evidence"},
		{"manual open", func(s *Summary) {
			s.Counts = append(s.Counts, Count{Kind: "board_open", Source: "human", Outcome: "complete", Count: 1})
		}, "criterion_failed"},
		{"unknown open", func(s *Summary) {
			s.Counts = append(s.Counts, Count{Kind: "board_open", Source: "unknown", Outcome: "complete", Count: 1})
		}, "insufficient_evidence"},
		{"incomplete action", func(s *Summary) {
			s.Counts = append(s.Counts, Count{Kind: "issue_action", Source: "human", Outcome: "pending", Count: 1})
		}, "insufficient_evidence"},
		{"service under member", func(s *Summary) {
			s.Counts = append(s.Counts, Count{Kind: "issue_action", Source: "service", Outcome: "complete", Count: 1})
		}, "ready_for_acceptance"},
		{"unmatched web read", func(s *Summary) {
			s.Counts = append(s.Counts, Count{Kind: "issue_read", Source: "human", Outcome: "complete", Count: 1})
		}, "insufficient_evidence"},
		{"Multika read", func(s *Summary) {
			s.Counts = append(s.Counts, Count{Kind: "issue_read", Source: "agent", Via: "multika", Outcome: "complete", Count: 1})
		}, "ready_for_acceptance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			s.Counts = append([]Count{}, base.Counts...)
			tc.change(&s)
			if got := verdict(s); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}
