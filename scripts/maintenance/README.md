Maintenance for RUSL-2760
=========================

The external status/stop sequence races claims. `daemon maintenance enter`
owns the existing `updating` CAS and `claimMu` barrier, then waits for admitted
claims, active tasks and all four pending/failed terminal-report counters.
Terminal-report scan errors fail closed. `stop` rechecks under the same mutex;
the gate is never released between drain and cancellation. This local endpoint
has the same loopback operator boundary as `/shutdown`. The CLI remains
unavailable inside agent task identities.

Operator receipts are ownership nonces, not account credentials. They are
0600 files. Keep the receipt until `resume`. The daemon stores its matching
marker in the existing profile directory; a supervisor restart restores that
barrier before any poller starts. Existing explicit pauses, providers, models,
runtime arguments, and standby role settings are independent and unchanged.

Bootstrap from the installed v0.7.2
----------------------------------

The installed old process does not know the new endpoint. Build the six-file
daemon/CLI change on the exact production source `d61b8d97c` as
`v0.7.2-rusl2779-maintenance`, preserving the existing original binary backup.
Build the same change on the accepted v0.8.1 source as `v0.8.1-rusl2760`.
Record both SHA-256 values and stage the files without replacing production
until independent review is accepted.

`bootstrap.py` uses the old daemon's native self-reload: its existing
`trySetClaimBarrier` defers while any claim or task is active. No external stop
is involved. The current primary has native reload enabled and `Restart=always`
(read-only verification on 09.11.2026). The helper rechecks those settings;
explicitly disabled native reload is an error, never an instruction to enable it.

Run from the final durable release unit outside the daemon cgroup, after review:

```bash
python3 bootstrap.py \
  --installed /home/mika/.local/share/multica-selfhost/bin/multica \
  --candidate /home/mika/.local/share/multica-selfhost/bin/multica.rusl2779-maintenance \
  --profile-dir /home/mika/.multica/profiles/selfhost \
  --receipt "$release_dir/maintenance-receipt.json"
```

The marker is prepared before the atomic binary replacement. Thus even a
portable native handoff child cannot claim before systemd takes over. Success
requires the new version, actual service MainPID, daemon identity and completed
drain; it leaves the gate held. A native tick may be up to ten minutes away;
bootstrap has a twenty-minute deadline and never forces an active task to exit.
On failure it requests owned resume, or persists `resume_on_start` if the
successor has not appeared yet. The late portable child refuses to start work;
the supervisor's instance consumes that recovery instruction and resumes.
The maintenance binary remains staged/installed on timeout; it retains the old
application source. No server deployment follows a failed bootstrap.

Application rollout and recovery
--------------------------------

`rollout-rusl2760.sh` is the revised parent script. Copy it and the unchanged
`verify-rollout-backup.py` into the release directory. `release.env` contains
image/version/hash metadata only. Add `RUSL_MAINT_CLI_SHA256` for the bootstrap
binary; retain `RUSL_OLD_CLI_SHA256` for the original standby. The rollback
primary is the old application plus this maintenance capability, version
`v0.7.2-rusl2779-maintenance`; the original pre-bootstrap backup also remains.
Update `RUSL_NEW_CLI_SHA256` after rebuilding the v0.8.1 CLI and restage standby.

The new required QA key `maintenance_review_verified` must be set only after
DSH accepts these changes. The original parent script remains disabled until
that handoff. `--check` is read-only. Do not schedule this copy with an old QA
file or without the accepted bootstrap binaries and recorded hashes.

The script keeps maintenance held across backup, server, primary and standby.
It verifies the new primary's real terminal-report scan before resume. On a
pre-deploy failure it resumes claims. On a later observation failure, rollback
first re-enters maintenance and drains; a busy daemon leaves deployment intact
and reports `rollback_failed_could_not_drain`. Rollback waits for the old
backend/frontend readiness before starting the old primary, verifies its exact
version and maintenance readiness, and only then resumes and records
`rolled_back`. A readiness failure leaves `rollback_failed` and retains the
barrier. Recover through the saved receipt once the underlying fault is fixed.

Parent delivery still owns the fresh verified backup, release gates, Mika chat
and autopilot smoke tests, thirty-minute observation and final acceptance.
Creating or reviewing this remediation does not complete RUSL-2760. This issue
does not deploy production, schedule a rollout, or alter RBAC, PodSecurity,
networking, Ziti, certificates, OMP runtimes or explicit pauses.

Verification
------------

```bash
python3 -m unittest discover -s scripts/maintenance -p 'test_*.py' -v
bash -n scripts/maintenance/rollout-rusl2760.sh
# Run builds/race tests through kjob, on both production and v0.8.1 bases:
go test -race ./internal/daemon ./cmd/multica \
  -run 'TestMaintenance|TestTrySelfReload|TestTrySetClaimBarrier|TestDaemonLocalCommandsFailClosed|TestTerminalReport|TestRunBatch|TestTryAutoUpdate|TestHandleUpdate'
```

The operational tests replace every external command; they never call a real
daemon, service manager, cluster, or SSH endpoint. The deterministic handoff
test holds a claim across acquisition, hands it into an active task, and proves
that neither step permits a stop. It also restores the persisted gate on a
successor and verifies explicit resume.
