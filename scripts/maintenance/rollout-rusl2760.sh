#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
export TZ=Europe/Moscow
rusl_release_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
readonly rusl_release_dir
readonly rusl_bin=/home/mika/.local/share/multica-selfhost/bin/multica
readonly rusl_old_bin=/home/mika/.local/share/multica-selfhost/bin/multica.rusl2779-maintenance
readonly rusl_candidate=/home/mika/.local/share/multica-selfhost/bin/multica.rusl2760-candidate
readonly rusl_unit=multica-daemon-selfhost.service
exec >> "$rusl_release_dir/rollout.log" 2>&1
exec 9>"$rusl_release_dir/rollout.lock"
flock -n 9 || exit 0
rusl_check_only=${1:-}
rusl_mutated=0
rusl_standby_promoted=0
rusl_watch_paused=0
rusl_maintenance=0
readonly rusl_receipt="$rusl_release_dir/maintenance-receipt.json"
# Bootstrap may already own this gate before rollout preflight starts.
# --check must never resume an existing gate, even when its checks fail.
if [[ "$rusl_check_only" != --check && -f "$rusl_receipt" ]]; then rusl_maintenance=1; fi
RUSL_WATCH_WAS_ACTIVE=inactive

kube() {
  /home/mika/.local/bin/kubectl --kubeconfig /home/mika/.kube/nenkoru-prod.yaml -n multica "$@"
}
standby() {
  /usr/bin/ssh -o IdentityAgent=none -o BatchMode=yes -o ConnectTimeout=10 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 multica-ru.ziti "$@"
}
phase() {
  printf '%s\n' "$1" > "$rusl_release_dir/phase.new"
  mv "$rusl_release_dir/phase.new" "$rusl_release_dir/phase"
  printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S МСК')" "$1"
}
restore_watch() {
  if [[ "$rusl_watch_paused" == 1 && "$RUSL_WATCH_WAS_ACTIVE" == active ]]; then
    standby 'sudo -n systemctl start multica-standby-watch.timer'
  fi
}
rollback() {
  local rusl_rc=$1
  trap - ERR INT TERM
  set +e
  phase rollback_started
  local rusl_failed=0
  if [[ "$rusl_mutated" == 1 ]]; then
    if [[ "$rusl_maintenance" == 0 ]]; then
      rusl_maintenance=1
      if ! "$rusl_bin" --profile selfhost daemon maintenance enter --token-file "$rusl_receipt" --timeout 5m; then
        restore_watch
        phase rollback_failed_could_not_drain
        exit "$rusl_rc"
      fi
      if ! "$rusl_bin" --profile selfhost daemon maintenance stop --token-file "$rusl_receipt"; then
        restore_watch
        phase rollback_failed_could_not_stop
        exit "$rusl_rc"
      fi
    fi
    sudo -n systemctl stop "$rusl_unit" || { phase rollback_failed; exit "$rusl_rc"; }
    local rusl_current_backend rusl_current_frontend rusl_current_cli
    rusl_current_backend=$(kube get deployment multica-backend -o jsonpath='{.spec.template.spec.containers[0].image}')
    rusl_current_frontend=$(kube get deployment multica-frontend -o jsonpath='{.spec.template.spec.containers[0].image}')
    rusl_current_cli=$(sha256sum "$rusl_bin" | cut -d' ' -f1)
    if [[ ( -n "$rusl_current_backend" && "$rusl_current_backend" != "$RUSL_NEW_BACKEND" && "$rusl_current_backend" != "$RUSL_OLD_BACKEND" ) || ( -n "$rusl_current_frontend" && "$rusl_current_frontend" != "$RUSL_NEW_FRONTEND" && "$rusl_current_frontend" != "$RUSL_OLD_FRONTEND" ) || ( -n "$rusl_current_cli" && "$rusl_current_cli" != "$RUSL_NEW_CLI_SHA256" && "$rusl_current_cli" != "$RUSL_MAINT_CLI_SHA256" ) ]]; then
      restore_watch
      phase external_deployment_detected
      exit "$rusl_rc"
    fi
    if [[ -n "$rusl_current_backend" ]]; then kube set image deployment/multica-backend "backend=$RUSL_OLD_BACKEND" || rusl_failed=1; else rusl_failed=1; fi
    if [[ -n "$rusl_current_frontend" ]]; then kube set image deployment/multica-frontend "frontend=$RUSL_OLD_FRONTEND" || rusl_failed=1; else rusl_failed=1; fi
    install -m 755 "$rusl_old_bin" "$rusl_bin.rusl2760-restore" && mv "$rusl_bin.rusl2760-restore" "$rusl_bin" || rusl_failed=1
    kube rollout status deployment/multica-backend --timeout=180s || rusl_failed=1
    kube rollout status deployment/multica-frontend --timeout=180s || rusl_failed=1
    if [[ "$rusl_failed" == 0 ]]; then
      sudo -n systemctl restart "$rusl_unit" || rusl_failed=1
      local rusl_old_ready=0
      for ((rusl_attempt=0; rusl_attempt<30; rusl_attempt++)); do
        if "$rusl_bin" --profile selfhost daemon status --output json | python3 -c 'import json,sys;d=json.load(sys.stdin);assert d.get("status")=="running" and d.get("cli_version")=="v0.7.2-rusl2779-maintenance" and all(d.get(k)==0 for k in ["pending_terminal_report_count","pending_terminal_report_bytes","failed_terminal_report_count","failed_terminal_report_bytes"])' && "$rusl_bin" --profile selfhost daemon maintenance status --token-file "$rusl_receipt" | python3 -c 'import json,sys;assert json.load(sys.stdin).get("ready") is True'; then
          rusl_old_ready=1
          break
        fi
        sleep 2
      done
      [[ "$rusl_old_ready" == 1 ]] || rusl_failed=1
    fi
  fi
  if [[ "$rusl_standby_promoted" == 1 ]]; then
    standby 'install -m 755 /home/mika/multica-standby/bin/multica.rusl2760-bak /home/mika/multica-standby/bin/multica.rusl2760-restore && mv /home/mika/multica-standby/bin/multica.rusl2760-restore /home/mika/multica-standby/bin/multica' || rusl_failed=1
  fi
  if [[ "$rusl_failed" == 0 && "$rusl_maintenance" == 1 ]]; then
    "$rusl_bin" --profile selfhost daemon maintenance resume --token-file "$rusl_receipt" || rusl_failed=1
  fi
  restore_watch || rusl_failed=1
  if [[ "$rusl_failed" != 0 ]]; then phase rollback_failed;
  elif [[ "$rusl_mutated" == 0 ]]; then phase aborted_before_deployment;
  else phase rolled_back; fi
  exit "$rusl_rc"
}
trap 'rollback "$?"' ERR
trap 'rollback 130' INT
trap 'rollback 143' TERM

phase preflight
[[ "$rusl_check_only" == '' || "$rusl_check_only" == --check ]]
source "$rusl_release_dir/release.env"
python3 - "$rusl_release_dir/qa.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))
for key in ['go_verified','db_rehearsal_verified','review_verified','views_failures_resolved','frontend_image_verified','backend_image_verified','maintenance_review_verified']:
    assert d.get(key) is True, 'Missing release gate: '+key
PY
[[ "$(sha256sum "$rusl_candidate" | cut -d' ' -f1)" == "$RUSL_NEW_CLI_SHA256" ]]
[[ "$(sha256sum "$rusl_bin" | cut -d' ' -f1)" == "$RUSL_MAINT_CLI_SHA256" ]]
[[ "$(sha256sum "$rusl_old_bin" | cut -d' ' -f1)" == "$RUSL_MAINT_CLI_SHA256" ]]
[[ "$(kube get deployment multica-backend -o jsonpath='{.spec.template.spec.containers[0].image}')" == "$RUSL_OLD_BACKEND" ]]
[[ "$(kube get deployment multica-frontend -o jsonpath='{.spec.template.spec.containers[0].image}')" == "$RUSL_OLD_FRONTEND" ]]
kube wait --for=condition=Ready pod/rusl2760-rollback-images --timeout=20s
[[ "$(standby 'systemctl is-active multica-daemon-standby.service || true')" == inactive ]]
standby "printf '%s  %s\\n' '$RUSL_OLD_CLI_SHA256' /home/mika/multica-standby/bin/multica '$RUSL_OLD_CLI_SHA256' /home/mika/multica-standby/bin/multica.rusl2760-bak | sha256sum -c -"
RUSL_WATCH_WAS_ACTIVE=$(standby 'systemctl is-active multica-standby-watch.timer || true')
[[ "$RUSL_WATCH_WAS_ACTIVE" == active || "$RUSL_WATCH_WAS_ACTIVE" == inactive ]]
if [[ "$rusl_check_only" == --check ]]; then phase preflight_verified; exit 0; fi

phase acquiring_maintenance
rusl_maintenance=1
"$rusl_bin" --profile selfhost daemon maintenance enter --token-file "$rusl_receipt" --timeout 5m
rusl_watch_paused=1
standby 'sudo -n systemctl stop multica-standby-watch.timer && sudo -n systemctl stop multica-standby-watch.service'
"$rusl_bin" --profile selfhost daemon maintenance stop --token-file "$rusl_receipt"
# A Restart=always successor restores the marker before it can claim anything.
rusl_mutated=1
sudo -n systemctl stop "$rusl_unit"
phase fresh_backup
python3 "$rusl_release_dir/verify-rollout-backup.py"

phase deploying_server
kube set image deployment/multica-backend "backend=$RUSL_NEW_BACKEND"
kube rollout status deployment/multica-backend --timeout=180s
kube set image deployment/multica-frontend "frontend=$RUSL_NEW_FRONTEND"
kube rollout status deployment/multica-frontend --timeout=180s
phase deploying_primary
install -m 755 "$rusl_candidate" "$rusl_bin.rusl2760-new"
mv "$rusl_bin.rusl2760-new" "$rusl_bin"
sudo -n systemctl start "$rusl_unit"
rusl_ready=0
for ((rusl_attempt=0; rusl_attempt<30; rusl_attempt++)); do
  if "$rusl_bin" --profile selfhost daemon status --output json | python3 -c 'import json,sys;d=json.load(sys.stdin);sys.exit(0 if d.get("status")=="running" and d.get("cli_version")=="v0.8.1-rusl2760" else 1)'; then
    rusl_ready=1
    break
  fi
  sleep 2
done
[[ "$rusl_ready" == 1 ]]
"$rusl_bin" --profile selfhost daemon maintenance status --token-file "$rusl_receipt" | python3 -c 'import json,sys;assert json.load(sys.stdin).get("ready") is True'

phase deploying_standby
[[ "$(standby 'systemctl is-active multica-daemon-standby.service || true')" == inactive ]]
rusl_standby_promoted=1
standby "printf '%s  %s\\n' '$RUSL_NEW_CLI_SHA256' /home/mika/multica-standby/bin/multica.rusl2760-candidate | sha256sum -c - && install -m 755 /home/mika/multica-standby/bin/multica.rusl2760-candidate /home/mika/multica-standby/bin/multica.rusl2760-new && mv /home/mika/multica-standby/bin/multica.rusl2760-new /home/mika/multica-standby/bin/multica"
standby '/home/mika/multica-standby/bin/multica version'
restore_watch
rusl_watch_paused=0

healthy() {
  "$rusl_bin" --profile selfhost daemon status --output json | python3 -c 'import json,sys;d=json.load(sys.stdin);assert d.get("status")=="running" and d.get("cli_version")=="v0.8.1-rusl2760" and d.get("failed_terminal_report_count")==0' || return 1
  kube get deployment multica-backend multica-frontend -o json | python3 -c 'import json,sys;d=json.load(sys.stdin);expected=dict(zip(["multica-backend","multica-frontend"],sys.argv[1:]));assert all(x.get("status",{}).get("readyReplicas",0)==x["spec"].get("replicas",1) and x.get("status",{}).get("observedGeneration",0)>=x["metadata"]["generation"] and x["spec"]["template"]["spec"]["containers"][0]["image"]==expected[x["metadata"]["name"]] for x in d["items"])' "$RUSL_NEW_BACKEND" "$RUSL_NEW_FRONTEND" || return 1
}
# A lost resume response may already have opened claims. Any subsequent error
# must reacquire/drain, even if the CLI fails before acknowledging success.
rusl_maintenance=0
"$rusl_bin" --profile selfhost daemon maintenance resume --token-file "$rusl_receipt"
phase observing
for ((rusl_minute=0; rusl_minute<30; rusl_minute++)); do
  sleep 60
  if ! healthy; then sleep 10; healthy; fi
done
phase deployed_and_observed
