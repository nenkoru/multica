#!/usr/bin/env bash
# Fabrika notifier (FABRIKA_NOTIFY): text on stdin, event (digest|urgent|info)
# as $1. Sends through a tg_notify.py-style sender (`send --event --text`), the
# same bot as the Multica Telegram chat; it never calls getUpdates or touches
# webhooks.
#   FABRIKA_TG_NOTIFY  path to the sender script (required)
set -euo pipefail
event=${1:-info}
: "${FABRIKA_TG_NOTIFY:?set FABRIKA_TG_NOTIFY in fabrika.env}"
text=$(cat)
[ -n "$text" ] || exit 0
exec python3 "$FABRIKA_TG_NOTIFY" send \
  --event "$event" --source Фабрика --no-voice --text "$text"
