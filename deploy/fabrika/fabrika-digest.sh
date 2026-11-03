#!/usr/bin/env bash
# Fabrika digest sender (§7.1): asks the server to build the morning or
# evening digest and hands the text to the notifier. The server promotes asks
# within the daily cap, so this script stays dumb.
#   fabrika-digest.sh morning|evening|preview
#
# Env (from FABRIKA_ENV, default ~/.config/fabrika/fabrika.env):
#   FABRIKA_API, FABRIKA_NOTIFY   see fabrika-watchdog.sh
#   FABRIKA_TOKEN                 Multica API token of a workspace member
#   FABRIKA_WORKSPACE_ID          workspace UUID
set -euo pipefail
SLOT=${1:-morning}
ENV_FILE=${FABRIKA_ENV:-$HOME/.config/fabrika/fabrika.env}
# shellcheck disable=SC1090
set -a; . "$ENV_FILE"; set +a
: "${FABRIKA_API:?}" "${FABRIKA_NOTIFY:?}" "${FABRIKA_TOKEN:?}" "${FABRIKA_WORKSPACE_ID:?}"

resp=$(curl -sS -m 60 --fail-with-body -X POST \
  -H "Authorization: Bearer $FABRIKA_TOKEN" -H "X-Workspace-ID: $FABRIKA_WORKSPACE_ID" \
  -H 'Content-Type: application/json' --data "{\"slot\":\"$SLOT\"}" \
  "${FABRIKA_API%/}/api/streams/digest") || {
  printf 'Фабрика: сводка «%s» не собралась — сервер ответил ошибкой. Потоки проверь по runbook.' "$SLOT" | $FABRIKA_NOTIFY urgent
  exit 1
}
text=$(printf '%s' "$resp" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("text",""))')
# An evening slot with nothing to answer is empty by design.
[ -n "$text" ] || exit 0
printf '%s\n' "$text" | $FABRIKA_NOTIFY digest
