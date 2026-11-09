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
	"sync/atomic"
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

func TestMaintenanceResumeWaitsForSupervisorSuccessor(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			pid := 123
			if attempts.Load() > 0 {
				pid = 124
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "running", "daemon_id": "d", "profile": "", "pid": pid})
			return
		}
		if attempts.Add(1) == 1 {
			http.Error(w, "maintenance stop already committed", 409)
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"resumed": true})
	}))
	defer srv.Close()
	_, rawPort, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumeMaintenance(context.Background(), port, maintenanceSession{Token: strings.Repeat("a", 64), DaemonID: "d"}); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("resume attempts=%d", attempts.Load())
	}
}

func TestMaintenanceCallRejectsMalformedProof(t *testing.T) {
	for _, fault := range []string{"missing counter", "null counter", "negative counter", "bytes remain", "wrong identity"} {
		t.Run(fault, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					json.NewEncoder(w).Encode(map[string]any{"status": "running", "daemon_id": "d", "profile": "", "pid": 123})
					return
				}
				data, _ := json.Marshal(daemon.MaintenanceStatus{PID: 123, DaemonID: "d", Ready: true})
				var fields map[string]any
				json.Unmarshal(data, &fields)
				switch fault {
				case "missing counter":
					delete(fields, "claims_in_flight")
				case "null counter":
					fields["pending_terminal_report_bytes"] = nil
				case "negative counter":
					fields["active_task_count"] = -1
				case "bytes remain":
					fields["failed_terminal_report_bytes"] = 1
				case "wrong identity":
					fields["pid"] = 124
				}
				json.NewEncoder(w).Encode(fields)
			}))
			defer srv.Close()
			_, rawPort, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(rawPort)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := maintenanceCall(context.Background(), port, maintenanceSession{Token: strings.Repeat("a", 64), DaemonID: "d"}, "stop"); err == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
}
