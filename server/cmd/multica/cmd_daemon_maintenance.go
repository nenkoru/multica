package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/spf13/cobra"
)

type maintenanceSession struct {
	Token    string `json:"token"`
	DaemonID string `json:"daemon_id"`
	Profile  string `json:"profile"`
}

func init() {
	cmd := &cobra.Command{
		Use:   "maintenance enter|status|resume|stop",
		Short: "Drain local claims and terminal reports before maintenance",
		Args:  cobra.ExactArgs(1), RunE: runDaemonMaintenance,
	}
	cmd.Flags().String("token-file", "", "Operator receipt path (required; keep until resume)")
	cmd.Flags().Duration("timeout", 5*time.Minute, "Maximum drain wait; timeout resumes claims")
	cmd.MarkFlagRequired("token-file")
	daemonCmd.AddCommand(cmd)
}

func maintenanceCall(ctx context.Context, port int, session maintenanceSession, action string) (daemon.MaintenanceStatus, error) {
	var result daemon.MaintenanceStatus
	health := checkDaemonHealthOnPort(ctx, port)
	if !daemonAlive(health) || health["daemon_id"] != session.DaemonID {
		return result, errors.New("maintenance daemon identity unavailable or changed")
	}
	if err := daemonIdentityMismatch(health, session.Profile, port); err != nil {
		return result, err
	}
	body, err := json.Marshal(map[string]any{"action": action, "token": session.Token,
		"pid": health["pid"], "daemon_id": session.DaemonID, "profile": session.Profile})
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/maintenance", port), bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return result, fmt.Errorf("maintenance %s: HTTP %d: %s", action, resp.StatusCode, message)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16384))
	if err != nil {
		return result, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return result, err
	}
	if action == "resume" {
		var resumed bool
		if err := json.Unmarshal(fields["resumed"], &resumed); err != nil || !resumed {
			return result, errors.New("maintenance resume was not acknowledged")
		}
		return result, nil
	}
	for _, key := range []string{"pid", "daemon_id", "claims_in_flight", "active_task_count", "pending_terminal_report_count", "pending_terminal_report_bytes", "failed_terminal_report_count", "failed_terminal_report_bytes", "ready"} {
		if raw, ok := fields[key]; !ok || string(raw) == "null" {
			return result, fmt.Errorf("maintenance response missing %s", key)
		}
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	if result.PID <= 0 || float64(result.PID) != health["pid"] || result.DaemonID != session.DaemonID {
		return result, errors.New("maintenance response identity mismatch")
	}
	if result.ClaimsInFlight < 0 || result.ActiveTasks < 0 || result.PendingCount < 0 || result.PendingBytes < 0 || result.FailedCount < 0 || result.FailedBytes < 0 ||
		(result.Ready && (result.ClaimsInFlight != 0 || result.ActiveTasks != 0 || result.PendingCount != 0 || result.PendingBytes != 0 || result.FailedCount != 0 || result.FailedBytes != 0)) {
		return result, errors.New("invalid maintenance drain counters")
	}
	return result, nil
}

func runDaemonMaintenance(cmd *cobra.Command, args []string) error {
	if err := requireHumanLocalCommand("daemon maintenance"); err != nil {
		return err
	}
	action := args[0]
	if action != "enter" && action != "status" && action != "resume" && action != "stop" {
		return errors.New("expected enter, status, resume, or stop")
	}
	path, _ := cmd.Flags().GetString("token-file")
	if path == "" {
		return errors.New("--token-file is required")
	}
	profile := resolveProfile(cmd)
	port := resolveHealthPort(profile)
	var session maintenanceSession
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &session); err != nil {
			return err
		}
		if session.Profile != profile || session.DaemonID == "" {
			return errors.New("receipt profile/identity mismatch")
		}
	} else if errors.Is(err, os.ErrNotExist) && action == "enter" {
		health := checkDaemonHealthOnPort(cmd.Context(), port)
		if !daemonAlive(health) {
			return errors.New("daemon is not running")
		}
		if err := daemonIdentityMismatch(health, profile, port); err != nil {
			return err
		}
		id, _ := health["daemon_id"].(string)
		if id == "" {
			return errors.New("daemon identity missing")
		}
		var nonce [32]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		session = maintenanceSession{hex.EncodeToString(nonce[:]), id, profile}
		data, err := json.Marshal(session)
		if err != nil {
			return err
		}
		// Persist ownership before sending acquire, including a lost-response case.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(data)
		syncErr := f.Sync()
		closeErr := f.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return err
		}
	} else {
		return err
	}

	if action == "enter" {
		timeout, _ := cmd.Flags().GetDuration("timeout")
		status, err := enterMaintenance(cmd.Context(), port, session, timeout)
		if err != nil {
			return err
		}
		return cli.PrintJSON(cmd.OutOrStdout(), status)
	}
	var status daemon.MaintenanceStatus
	if action == "resume" {
		status, err = resumeMaintenance(cmd.Context(), port, session)
	} else {
		status, err = maintenanceCall(cmd.Context(), port, session, action)
	}
	if err != nil {
		return err
	}
	if action == "resume" {
		if err := os.Remove(path); err != nil {
			return err
		}
		return cli.PrintJSON(cmd.OutOrStdout(), map[string]bool{"resumed": true})
	}
	return cli.PrintJSON(cmd.OutOrStdout(), status)
}

func enterMaintenance(ctx context.Context, port int, session maintenanceSession, timeout time.Duration) (status daemon.MaintenanceStatus, err error) {
	// A failed acquire may have committed even if its response was lost. Always
	// try the owned resume before returning; keep the receipt for manual recovery.
	defer func() {
		if err != nil {
			recoveryCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, resumeErr := resumeMaintenance(recoveryCtx, port, session)
			if resumeErr != nil {
				err = errors.Join(err, fmt.Errorf("resume failed; retain --token-file for recovery: %w", resumeErr))
			}
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	status, err = maintenanceCall(ctx, port, session, "acquire")
	if err != nil {
		return status, err
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if status.FailedCount != 0 || status.FailedBytes != 0 {
			return status, errors.New("failed terminal reports require remediation; maintenance aborted")
		}
		if status.Ready {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return status, fmt.Errorf("maintenance drain: %w", ctx.Err())
		case <-ticker.C:
			status, err = maintenanceCall(ctx, port, session, "status")
			if err != nil {
				return status, err
			}
		}
	}
}

// A stop response can be lost while Restart=always replaces the process. The
// durable marker keeps admission fenced; wait for that same daemon's successor
// before retrying the owned, idempotent resume. Other conflicts fail immediately.
func resumeMaintenance(ctx context.Context, port int, session maintenanceSession) (daemon.MaintenanceStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		status, err := maintenanceCall(ctx, port, session, "resume")
		if err == nil {
			return status, nil
		}
		var networkError net.Error
		if !errors.As(err, &networkError) && !strings.Contains(err.Error(), "maintenance stop already committed") && !strings.Contains(err.Error(), "daemon identity unavailable") {
			return status, err
		}
		select {
		case <-ctx.Done():
			return status, errors.Join(err, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}
