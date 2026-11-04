# Fabrika: streams that never stall

Server module `server/internal/streams`, tables from migration `9000_fabrika_streams`.
A supervisor tick (scheduler job `fabrika_streams_supervisor`, every 30 s, plus a
kick on task completion) keeps every active stream in exactly one live state:
`running`, `waiting`, `asking`, `budget`, `paused` or `broken`. A stream with no
live reason is an orphan; the tick journals it and queues a manager turn.

## API

All under `/api/streams`, workspace member auth and `X-Workspace-ID`.

| Method | Path | What |
|---|---|---|
| GET | `/health` | public; 503 when the last tick is older than 2 min |
| GET | `/` | streams with state and reason |
| GET | `/{slug}` | stream, open steps, waits, asks, journal |
| PUT | `/{slug}` | create or update a stream from a charter (human only) |
| POST | `/{slug}/pause`, `/{slug}/resume` | pause or resume (human only) |
| POST | `/{slug}/activate`, `/{slug}/deactivate` | include or exclude from ticks (human only) |
| POST | `/{slug}/manage` | manager turn in the reserved slot |
| POST | `/digest` | `{"slot":"morning\|evening\|preview"}` → `{"text": …}`; morning and evening promote asks within the daily cap |
| POST | `/answer` | `{"line":"1 а, 2 да", "channel":"telegram"}` |
| POST | `/waits/{id}/fire` | resolve a wait early (CI finished, process exited) |
| GET | `/multika` | the one chat with Мультика: `chat_session_id`, current focus |
| PUT | `/multika/focus` | `{"slug":"buvg01"}` sets the focus, `{"slug":""}` clears it (human only) |

A charter needs `title`, `charter.goal`, `charter.metric`, `charter.done`.
`charter.chains` maps a cost class (`S`, `M`, `L`, `manage`, `verify`, `*`) to an
ordered list of `{name, agent_id}`; `name` is the quota key, so agents sharing
one provider account share a name.

## Install

1. Roll the server out with the migration (`migrate up`). Down: `9000_fabrika_streams.down.sql` drops only Fabrika tables.
2. Create a hidden project per stream for carrier issues and put its id into `carrier_project_id`.
3. `PUT /api/streams/{slug}` with the charter. The first tick queues a manager turn.
4. On a host outside the server (so it speaks when the server is down):
   - copy `fabrika-watchdog.sh` and `fabrika-digest.sh` to `~/.local/bin/`;
   - write `~/.config/fabrika/fabrika.env` (mode 600) with `FABRIKA_API`, `FABRIKA_TOKEN`, `FABRIKA_WORKSPACE_ID`, `FABRIKA_NOTIFY`;
   - copy `systemd/*` to `~/.config/systemd/user/`, then
     `systemctl --user daemon-reload && systemctl --user enable --now fabrika-watchdog.timer fabrika-digest-morning.timer fabrika-digest-evening.timer`.

`FABRIKA_NOTIFY` is any command that reads the message on stdin and takes the
event (`digest`, `urgent`, `info`) as its first argument.

## Мультика: one chat (week 2)

Set `FABRIKA_MULTIKA_AGENT_ID` on the server to the agent whose Telegram bot and
web chat Руслан uses. Unset keeps everything below off.

- **One chat.** Telegram `/new` is off for that agent: a bare `/new` gets a notice,
  `/new <text>` lands in the current chat. `GET /api/streams/multika` returns the
  chat the Telegram binding points to (else the agent's latest web chat).
- **Focus.** Each claimed chat turn of Мультика passes through `OnMultikaClaim`
  (migration 9002, table `multika_turn` makes it idempotent):
  - «давай про Терн», «переключись на buvg01» → focus on that stream for 30 min;
    «всё», «назад» or 30 min of silence → focus off, the manager folds the
    decisions into the summary;
  - while focused, remarks go to the stream's manager in the reserved slot; remarks
    sent before that turn starts join its brief;
  - a line like «1 а, 2 да» with shown questions is applied as answers;
  - Мультика gets a short context block (streams, focus, charter, summary, what code
    did) in front of the message; it is not stored in the chat.
- **Never silence.** A failed run or an offline runtime posts «Мультика недоступна,
  потоки идут» in Telegram instead of the generic English notice.

## Operations

- **Watchdog fired.** `curl $FABRIKA_API/api/streams/health`. Unreachable → server down, follow the server runbook. Stale tick → check the server log for `fabrika_streams_supervisor` and `sys_cron_executions` for the job; a restart re-registers the job.
- **Stream `broken`.** Three failed steps in a row. The digest carries a red question N: reply `N а` to retry with a manager turn, `N б` to pause. `GET /api/streams/{slug}` shows the journal.
- **Stream `budget`.** Every executor in the chain is exhausted until T; it resumes by itself at T. Extend the chain in the charter to resume earlier.
- **Stop everything.** `deactivate` each stream; carrier issues stay as they are.
- **Rollback.** Revert to the previous server image; the Fabrika tables are not read by upstream code. Remove them with the down migration only after a dump.

## Checks

`kjob run --cpu 6 --mem 8Gi --image pgvector/pgvector:pg17 -- bash deploy/fabrika/kjob-test.sh`
runs gofmt, migrations (with 9000 down/up), vet, build and the streams tests
against a real Postgres. `FABRIKA_FIX=1` regenerates sqlc and gofmts in place first.
