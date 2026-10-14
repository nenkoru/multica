package realtime

import "testing"

func TestKioskProjection(t *testing.T) {
	changed := string(kioskChangedFrame)
	cases := []struct {
		in   string
		want string
	}{
		{`{"type":"issue:updated","payload":{"issue":{"title":"x","description":"secret"}}}`, changed},
		{`{"type":"task:failed","payload":{"task_id":"t"}}`, changed},
		{`{"type":"sprint:updated","payload":{}}`, changed},
		{`{"type":"issue_labels:changed","payload":{}}`, changed},
		{`{"type":"pong"}`, `{"type":"pong"}`},
		{`{"type":"task:message","payload":{"content":"agent output"}}`, ""},
		{`{"type":"comment:created","payload":{"comment":{"content":"secret"}}}`, ""},
		{`{"type":"chat:message","payload":{}}`, ""},
		{`{"type":"inbox:new","payload":{}}`, ""},
		{`not json`, ""},
	}
	for _, tc := range cases {
		got := string(kioskProjection([]byte(tc.in)))
		if got != tc.want {
			t.Errorf("kioskProjection(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
