#!/usr/bin/env bash
# Fabrika watchdog (§6.6): lives outside the server and outside Multica, so it
# still speaks when both are down. Run every minute (systemd timer). One
# message per incident, one when it recovers.
#
# Env (from FABRIKA_ENV, default ~/.config/fabrika/fabrika.env):
#   FABRIKA_API         base URL of the Multica server, e.g. https://multica.example
#   FABRIKA_NOTIFY      command that sends a message; gets the text on stdin and
#                       the event (urgent|info) as $1
#   FABRIKA_STALE_SEC   silence before alarm, default 300
set -uo pipefail
ENV_FILE=${FABRIKA_ENV:-$HOME/.config/fabrika/fabrika.env}
# shellcheck disable=SC1090
set -a; . "$ENV_FILE"; set +a
: "${FABRIKA_API:?}" "${FABRIKA_NOTIFY:?}"
STALE=${FABRIKA_STALE_SEC:-300}
STATE_DIR=${FABRIKA_STATE_DIR:-$HOME/.local/state/fabrika}
mkdir -p "$STATE_DIR"
SINCE_FILE=$STATE_DIR/watchdog-bad-since
ALERTED=$STATE_DIR/watchdog-alerted

now=$(date +%s)
body=$(curl -sS -m 20 "${FABRIKA_API%/}/api/streams/health" 2>&1)
code=$?
age=$(printf '%s' "$body" | python3 -c 'import sys,json
try: print(int(json.load(sys.stdin).get("tick_age_seconds", -1)))
except Exception: print(-1)')

if [ "$code" -eq 0 ] && [ "$age" -ge 0 ] && [ "$age" -lt "$STALE" ]; then
  if [ -f "$ALERTED" ]; then
    printf 'Фабрика снова работает: супервизор тикает (последний тик %s с назад).' "$age" | $FABRIKA_NOTIFY info
    rm -f "$ALERTED"
  fi
  rm -f "$SINCE_FILE"
  exit 0
fi

# Unhealthy: server unreachable, no tick yet, or the tick is stale.
[ -f "$SINCE_FILE" ] || echo "$now" > "$SINCE_FILE"
since=$(cat "$SINCE_FILE")
down=$(( now - since ))
if [ "$age" -ge 0 ]; then down=$(( age > down ? age : down )); fi
if [ "$down" -ge "$STALE" ] && [ ! -f "$ALERTED" ]; then
  if [ "$code" -ne 0 ]; then why="сервер не отвечает"; elif [ "$age" -lt 0 ]; then why="супервизор не отчитывается"; else why="последний тик $((age / 60)) мин назад"; fi
  printf 'Фабрика молчит %s мин: %s. Потоки не двигаются — смотри runbook «Фабрика», раздел «Сторож сработал».' "$((down / 60))" "$why" | $FABRIKA_NOTIFY urgent
  touch "$ALERTED"
fi
exit 0
