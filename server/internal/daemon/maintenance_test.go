package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const maintenanceTestToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func maintenanceTestDaemon(t *testing.T) (*Daemon, chan struct{}) {
	t.Helper()
	t.Setenv("MULTICA_TASK_CONFIG_ROOT", t.TempDir())
	d := &Daemon{cfg: Config{DaemonID: "maintenance-daemon", ServerBaseURL: "https://example.test", WorkspacesRoot: t.TempDir()}}
	d.terminalReports = newTerminalReportStore(d.cfg)
	d.ready.Store(true)
	stopped := make(chan struct{}, 1)
	d.cancelFunc = func() { stopped <- struct{}{} }
	return d, stopped
}

func maintenanceTestCall(t *testing.T, d *Daemon, action, token string, code int) MaintenanceStatus {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"action": action, "token": token, "pid": os.Getpid(), "daemon_id": d.cfg.DaemonID, "profile": d.cfg.Profile})
	w := httptest.NewRecorder()
	d.maintenanceHandler()(w, httptest.NewRequest(http.MethodPost, "/maintenance", bytes.NewReader(body)))
	if w.Code != code {
		t.Fatalf("%s: HTTP %d, want %d: %s", action, w.Code, code, w.Body.String())
	}
	var status MaintenanceStatus
	if code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
	}
	return status
}

func TestMaintenanceClaimHandoffNeverInterrupted(t *testing.T) {
	d, stopped := maintenanceTestDaemon(t)
	// Hold a real claim admission across acquire. Its response may arrive later.
	if !d.tryEnterClaim() {
		t.Fatal("initial claim denied")
	}
	s := maintenanceTestCall(t, d, "acquire", maintenanceTestToken, 200)
	if s.Ready || s.ClaimsInFlight != 1 {
		t.Fatalf("claim was not drained: %+v", s)
	}
	if d.tryEnterClaim() {
		t.Fatal("new claim admitted during maintenance")
	}
	maintenanceTestCall(t, d, "stop", maintenanceTestToken, 409)
	// Same handoff order as runBatchPoller: active increments before exitClaim.
	d.activeTasks.Add(1)
	d.exitClaim()
	maintenanceTestCall(t, d, "stop", maintenanceTestToken, 409)
	select {
	case <-stopped:
		t.Fatal("running task interrupted")
	default:
	}
	d.activeTasks.Add(-1)
	if !maintenanceTestCall(t, d, "stop", maintenanceTestToken, 200).Ready {
		t.Fatal("idle stop not ready")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("idle stop did not cancel")
	}
	maintenanceTestCall(t, d, "resume", maintenanceTestToken, 409)
	// Restart=always must not open claims between the RPC and systemctl stop.
	next := &Daemon{cfg: d.cfg}
	if err := next.restoreMaintenance(); err != nil {
		t.Fatal(err)
	}
	if next.tryEnterClaim() {
		t.Fatal("supervisor restart lost maintenance barrier")
	}
	maintenanceTestCall(t, next, "resume", maintenanceTestToken, 200)
	if !next.tryEnterClaim() {
		t.Fatal("resume did not restore claiming")
	}
	next.exitClaim()
}

func TestMaintenanceTerminalCountersAndScanFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "failed"}[failed], func(t *testing.T) {
			d, _ := maintenanceTestDaemon(t)
			dir := d.terminalReports.dir
			if failed {
				dir = d.terminalReports.failedDir()
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "report.json")
			if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			s := maintenanceTestCall(t, d, "acquire", maintenanceTestToken, 200)
			if s.Ready {
				t.Fatal("outbox bypassed")
			}
			if failed && (s.FailedCount != 1 || s.FailedBytes != 2) {
				t.Fatalf("failed counters: %+v", s)
			}
			if !failed && (s.PendingCount != 1 || s.PendingBytes != 2) {
				t.Fatalf("pending counters: %+v", s)
			}
			maintenanceTestCall(t, d, "stop", maintenanceTestToken, 409)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if !maintenanceTestCall(t, d, "status", maintenanceTestToken, 200).Ready {
				t.Fatal("drained outbox not ready")
			}
			maintenanceTestCall(t, d, "resume", maintenanceTestToken, 200)
		})
	}
	d, _ := maintenanceTestDaemon(t)
	if err := os.MkdirAll(filepath.Dir(d.terminalReports.dir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.terminalReports.dir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	maintenanceTestCall(t, d, "acquire", maintenanceTestToken, 500)
	maintenanceTestCall(t, d, "stop", maintenanceTestToken, 500)
	maintenanceTestCall(t, d, "resume", maintenanceTestToken, 200)
	if !d.tryEnterClaim() {
		t.Fatal("scan failure recovery did not resume claims")
	}
	d.exitClaim()
}

func TestMaintenanceOwnershipAndPersistenceFailures(t *testing.T) {
	d, _ := maintenanceTestDaemon(t)
	d.updating.Store(true)
	maintenanceTestCall(t, d, "acquire", maintenanceTestToken, 409)
	if !d.tryEnterClaim() {
		t.Fatal("competing update changed admission")
	}
	d.exitClaim()
	d.updating.Store(false)
	maintenanceTestCall(t, d, "acquire", maintenanceTestToken, 200)
	maintenanceTestCall(t, d, "resume", strings.Repeat("f", 64), 409)
	if d.tryEnterClaim() {
		t.Fatal("wrong owner released barrier")
	}
	maintenanceTestCall(t, d, "resume", maintenanceTestToken, 200)
	maintenanceTestCall(t, d, "resume", maintenanceTestToken, 200)
	path, err := d.maintenancePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (&Daemon{cfg: d.cfg}).restoreMaintenance(); err == nil {
		t.Fatal("corrupt state permitted startup")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Dir(path), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	maintenanceTestCall(t, d, "acquire", maintenanceTestToken, 500)
	if d.updating.Load() || !d.tryEnterClaim() {
		t.Fatal("failed persistence parked daemon")
	}
	d.exitClaim()
}

func TestMaintenanceBootstrapAbortResumesOnlySupervisor(t *testing.T) {
	d, _ := maintenanceTestDaemon(t)
	maintenanceTestCall(t, d, "acquire", maintenanceTestToken, 200)
	path, err := d.maintenancePath()
	if err != nil {
		t.Fatal(err)
	}
	marker := maintenanceMarker{Token: maintenanceTestToken, DaemonID: d.cfg.DaemonID, ServerURL: d.cfg.ServerBaseURL, ResumeOnStart: true}
	data, _ := json.Marshal(marker)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INVOCATION_ID", "supervisor")
	t.Setenv("MULTICA_DAEMON_HANDOFF", "1")
	if err := (&Daemon{cfg: d.cfg}).restoreMaintenance(); err == nil {
		t.Fatal("transient handoff child could start work")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("handoff child consumed recovery instruction")
	}
	t.Setenv("MULTICA_DAEMON_HANDOFF", "")
	next := &Daemon{cfg: d.cfg}
	if err := next.restoreMaintenance(); err != nil {
		t.Fatal(err)
	}
	if !next.tryEnterClaim() {
		t.Fatal("aborted bootstrap did not restore claiming")
	}
	next.exitClaim()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("aborted marker not consumed")
	}
}
