package daemon

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/cli"
)

// Maintenance is local operator state, independent of agent/runtime pauses.
// Its marker survives supervisor restarts; only an explicit resume removes it.
type maintenanceMarker struct {
	Token         string `json:"token"`
	DaemonID      string `json:"daemon_id"`
	ServerURL     string `json:"server_url"`
	ResumeOnStart bool   `json:"resume_on_start,omitempty"`
}

type MaintenanceStatus struct {
	PID            int    `json:"pid"`
	DaemonID       string `json:"daemon_id"`
	ClaimsInFlight int    `json:"claims_in_flight"`
	ActiveTasks    int64  `json:"active_task_count"`
	PendingCount   int    `json:"pending_terminal_report_count"`
	PendingBytes   int64  `json:"pending_terminal_report_bytes"`
	FailedCount    int    `json:"failed_terminal_report_count"`
	FailedBytes    int64  `json:"failed_terminal_report_bytes"`
	Ready          bool   `json:"ready"`
}

func (d *Daemon) maintenancePath() (string, error) {
	dir, err := cli.ProfileDir(d.cfg.Profile)
	return filepath.Join(dir, "maintenance.json"), err
}

func validMaintenanceToken(token string) bool {
	b, err := hex.DecodeString(token)
	return err == nil && len(b) == 32
}

func (d *Daemon) restoreMaintenance() error {
	path, err := d.maintenancePath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var marker maintenanceMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return err
	}
	if !validMaintenanceToken(marker.Token) || marker.DaemonID != d.cfg.DaemonID || marker.ServerURL != d.cfg.ServerBaseURL {
		return errors.New("maintenance marker identity mismatch")
	}
	if marker.ResumeOnStart {
		// The old daemon may still be finishing its native reload after an
		// operator aborted bootstrap. Its portable child belongs to the old
		// systemd cgroup and must never start work before the supervisor takes over.
		if d.cfg.PortableSystemdHandoff {
			return errors.New("bootstrap aborted; waiting for supervisor restart")
		}
		return os.Remove(path)
	}
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	d.maintenanceToken = marker.Token
	d.pauseClaims = true
	d.updating.Store(true)
	return nil
}

// Called under claimMu after update ownership has been acquired. Atomic rename
// prevents a supervisor from accepting a partial marker after a process crash.
func (d *Daemon) persistMaintenance(token string) error {
	path, err := d.maintenancePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(maintenanceMarker{Token: token, DaemonID: d.cfg.DaemonID, ServerURL: d.cfg.ServerBaseURL})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".maintenance-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Caller holds claimMu, so a zero claim count cannot turn into a new handoff.
// activeTasks decrements only after handleTask's terminal reporting completes.
func (d *Daemon) maintenanceStatus() (MaintenanceStatus, error) {
	s := MaintenanceStatus{PID: os.Getpid(), DaemonID: d.cfg.DaemonID,
		ClaimsInFlight: d.claimsInFlight, ActiveTasks: d.activeTasks.Load()}
	stats, err := d.terminalReports.stats()
	if err != nil {
		return s, fmt.Errorf("scan terminal report queue: %w", err)
	}
	s.PendingCount, s.PendingBytes = stats.PendingCount, stats.PendingBytes
	s.FailedCount, s.FailedBytes = stats.FailedCount, stats.FailedBytes
	s.Ready = d.ready.Load() && s.ClaimsInFlight == 0 && s.ActiveTasks == 0 &&
		s.PendingCount == 0 && s.PendingBytes == 0 && s.FailedCount == 0 && s.FailedBytes == 0
	return s, nil
}

func (d *Daemon) maintenanceHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Action   string `json:"action"`
			Token    string `json:"token"`
			PID      int    `json:"pid"`
			DaemonID string `json:"daemon_id"`
			Profile  string `json:"profile"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || !validMaintenanceToken(req.Token) {
			http.Error(w, "invalid maintenance request", http.StatusBadRequest)
			return
		}
		if req.PID != os.Getpid() || req.DaemonID != d.cfg.DaemonID || req.Profile != d.cfg.Profile {
			http.Error(w, "daemon identity changed", http.StatusConflict)
			return
		}
		if req.Action != "acquire" && req.Action != "status" && req.Action != "resume" && req.Action != "stop" {
			http.Error(w, "unknown maintenance action", http.StatusBadRequest)
			return
		}
		d.claimMu.Lock()
		defer d.claimMu.Unlock()
		if req.Action == "acquire" && d.maintenanceToken == "" {
			if !d.ready.Load() || !d.updating.CompareAndSwap(false, true) {
				http.Error(w, "daemon starting or update in progress", http.StatusConflict)
				return
			}
			if d.pauseClaims || d.RestartBinary() != "" {
				d.updating.Store(false)
				http.Error(w, "claim barrier already held", http.StatusConflict)
				return
			}
			if err := d.persistMaintenance(req.Token); err != nil {
				d.updating.Store(false)
				http.Error(w, "could not persist maintenance barrier", http.StatusInternalServerError)
				return
			}
			d.maintenanceToken = req.Token
			d.pauseClaims = true
		}
		if req.Action == "resume" && d.maintenanceToken == "" && d.maintenanceResumedToken == req.Token {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"resumed": true})
			return
		}
		if d.maintenanceToken != req.Token {
			http.Error(w, "maintenance ownership mismatch", http.StatusConflict)
			return
		}
		if d.maintenanceStopping {
			http.Error(w, "maintenance stop already committed", http.StatusConflict)
			return
		}
		if req.Action == "resume" {
			path, err := d.maintenancePath()
			if err == nil {
				err = os.Remove(path)
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				http.Error(w, "could not remove maintenance marker", http.StatusInternalServerError)
				return
			}
			d.maintenanceToken = ""
			d.maintenanceResumedToken = req.Token
			d.pauseClaims = false
			d.updating.Store(false)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"resumed": true})
			return
		}
		status, err := d.maintenanceStatus()
		if err != nil {
			http.Error(w, "terminal report counters unavailable", http.StatusInternalServerError)
			return
		}
		if req.Action == "stop" {
			if !status.Ready || d.cancelFunc == nil {
				http.Error(w, "maintenance drain incomplete", http.StatusConflict)
				return
			}
			d.maintenanceStopping = true
			defer func() { go d.cancelFunc() }()
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
		if req.Action == "stop" {
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}
