package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
)

func TestMaintenanceAbortedPortableChildAfterCLISetup(t *testing.T) {
	t.Setenv("MULTICA_TASK_CONFIG_ROOT", t.TempDir())
	t.Setenv("MULTICA_DAEMON_HANDOFF", "1")
	t.Setenv("INVOCATION_ID", "test-supervisor")
	cfg := daemon.Config{DaemonID: "d", ServerBaseURL: "https://example.test", WorkspacesRoot: t.TempDir()}
	dir, err := cli.ProfileDir("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "maintenance.json")
	data, _ := json.Marshal(map[string]any{"token": strings.Repeat("a", 64), "daemon_id": "d", "server_url": cfg.ServerBaseURL, "resume_on_start": true})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	// Same lock + environment consumption boundary as runDaemonForeground.
	lock, err := acquireDaemonStartupLock("", &cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if os.Getenv("MULTICA_DAEMON_HANDOFF") != "" {
		t.Fatal("handoff env was not consumed")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	d := daemon.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.RunWithHealthListener(ctx, ln); err == nil || !strings.Contains(err.Error(), "waiting for supervisor restart") {
		t.Fatalf("portable child passed startup after CLI setup: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("portable child consumed recovery marker")
	}
}

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
