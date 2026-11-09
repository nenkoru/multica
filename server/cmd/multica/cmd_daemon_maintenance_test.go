package main

import (
	"context"
	"encoding/json"
	"github.com/multica-ai/multica/server/internal/daemon"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMaintenanceEnterFailureResumesAdmission(t *testing.T) {
	for _, reason := range []string{"timeout", "failed reports", "unavailable counters", "lost acquire response"} {
		t.Run(reason, func(t *testing.T) {
			var mu sync.Mutex
			paused := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					json.NewEncoder(w).Encode(map[string]any{"status": "running", "daemon_id": "d", "profile": "", "pid": 123})
					return
				}
				var req map[string]any
				json.NewDecoder(r.Body).Decode(&req)
				mu.Lock()
				defer mu.Unlock()
				switch req["action"] {
				case "acquire":
					paused = true
					if reason == "lost acquire response" {
						http.Error(w, "response failed", 500)
						return
					}
				case "resume":
					paused = false
					json.NewEncoder(w).Encode(map[string]bool{"resumed": true})
					return
				}
				if reason == "unavailable counters" {
					http.Error(w, "unavailable", 500)
					return
				}
				json.NewEncoder(w).Encode(daemon.MaintenanceStatus{PID: 123, DaemonID: "d", FailedCount: map[bool]int{true: 1, false: 0}[reason == "failed reports"]})
			}))
			// No profile/port resolution or real daemon process involved.
			defer srv.Close()
			_, rawPort, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(rawPort)
			if err != nil {
				t.Fatal(err)
			}
			_, err = enterMaintenance(context.Background(), port, maintenanceSession{Token: strings.Repeat("a", 64), DaemonID: "d"}, 50*time.Millisecond)
			if err == nil {
				t.Fatal("drain unexpectedly succeeded")
			}
			mu.Lock()
			defer mu.Unlock()
			if paused {
				t.Fatalf("%s left admission paused: %v", reason, err)
			}
		})
	}
}
