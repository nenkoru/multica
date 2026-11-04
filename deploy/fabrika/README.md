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
| POST | `/asks/push` | shown questions without live buttons → Мультика's Telegram chat with inline buttons; `{"sent": n}` |

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
  - the focus belongs to the one chat (`GET /multika`), whichever session the turn came from;
  - «давай про Терн», «переключись на buvg01» → focus on that stream for 30 min;
    «всё», «назад» or 30 min of silence → focus off, the manager folds the
    decisions into the summary. The silence exit is applied on the next turn
    (or `GET /multika`, which already hides an expired focus), so the manager
    hears about it only when Руслан writes again;
  - while focused, remarks go to the stream's manager in the reserved slot; remarks
    sent before that turn starts join its brief;
  - a line like «1 а, 2 да» with shown questions is applied as answers;
  - Мультика gets a short context block (streams, focus, charter, summary, what code
    did) in front of the message; it is not stored in the chat.
- **Never silence.** A failed run or an offline runtime posts «Мультика недоступна,
  потоки идут» in Telegram instead of the generic English notice.
- **Question buttons (§7).** After the digest text `fabrika-digest.sh` calls
  `/asks/push`: each shown question goes to Мультика's Telegram direct chat as its
  own message, numbered as in the digest, with one button per option (`да`/`нет`
  for a batch). A button carries a short token from `ui_action`; the press comes
  back through the same getUpdates loop (`allowed_updates` gains `callback_query`
  only with the flag), is accepted only from that direct chat, answers the
  question once and closes its other buttons. Text answers («1 а») keep working;
  a button of an already answered question says «Вопрос уже закрыт».

- **New stream from the interview (§9.3).** Мультика asks all the questions at once,
  shows the charter and, only after Руслан's explicit yes, ends her reply with a
  ```` ```fabrika-charter ```` JSON block (`slug`, `title`, `charter` with goal,
  metric, done, mandate; optional `pause`). On `chat:done` code validates it and
  creates the stream (migration 9003, `multika_charter` keeps it once per turn):
  executor chains, manager, weight and carrier project come from the stream named
  by `FABRIKA_STREAM_TEMPLATE`; the block may not set chains. With five streams
  active the block must name one to `pause` (with room to spare `pause` is ignored). The new stream gets the focus and its
  first manager turn at once; the outcome, or why nothing was created, is in
  Мультика's next context. Without `FABRIKA_STREAM_TEMPLATE` nothing is created.
  Undo: `deactivate` the stream.

## Verify and milestones (§8)

- A `verify` step never runs on the author: its chain drops the author's provider
  key and agent. Other verifiers out of budget → the step waits; a chain with only
  the author → the stream is `broken` («нет проверяющего на другой модели»).
- A milestone counts only when a `verify` accepted work after the previous
  milestone; that check is appended to its evidence. Otherwise the journal gets
  `milestone_rejected` and the rest of the manager turn applies.

## Budgets (§6.7)

All off until set on the server:

- `FABRIKA_EXECUTOR_SLOTS=claude-sub=3,qwen-gpu=2` caps concurrent steps per
  executor over all streams. Each stream is guaranteed `ceil(min_share × N)` of
  an executor's slots; above that it uses only what other waiting streams'
  guarantees leave free and, while other streams wait for the same executor, no
  more than its `weight`-proportional part of the slots (so weight divides empty
  capacity in the same tick). A waiting stream holds its guarantee only on the
  executors its queued steps can use. The focused stream's reserved slot obeys the same rule. A step
  whose links are full waits with «шаг ждёт свободный слот исполнителя», not a
  budget state. Unlisted executors are unlimited.
- `FABRIKA_GPU_EXECUTORS=qwen-gpu` names the executors a charter window
  `{"kind": "gpu", "start": "02:00", "end": "07:00"}` (Moscow) takes over: while it
  is open, other streams skip them (class S falls through to the subscription);
  a chain with nothing else goes to `budget` until the window ends («бюджет до
  T»), not to a slot wait. Overlapping windows share the GPU among their owners;
  others wait for the latest end.
- `FABRIKA_API_MONTHLY_USD=…` adds a pulse line per stream with a `budget_share`:
  provider-reported API cost this month vs the pace (share ÷ days of month).
  Shown, never enforced.

## Voice (§13 day 9)

Off until `FABRIKA_WHISPER_URL` points at an OpenAI-compatible transcription
service (its root; `/v1/audio/transcriptions` is appended).
`FABRIKA_WHISPER_MODEL` defaults to `whisper-1`. With it set, `GET
/api/streams/multika` reports `"voice": true` and the web chat shows a
microphone next to Send in Мультика's chat only: a note of up to a minute
(5 MB) goes to `POST /api/streams/multika/voice` (multipart `file`), the text
lands in the draft and is sent only by Руслан. Without the URL the endpoint
answers 404 and the button is not shown. Rollback: unset the variable.

## Moving a stream off the board (§12, day 10)

The old board keeps working; streams move one at a time.

1. Charter from the interview: `PUT /api/streams/{slug}` as usual, plus
   `"migrate": ["RUSL-1768", …]` — the stream's in_progress and blocked
   issues (≤ 40, backlog does not move). In the same transaction the stream
   gets one `summarize` turn with origin `migration` instead of the plain
   first turn.
2. That turn reads only the listed issues and returns the summary, a step
   queue in `next` (or a `wait` of kind `issue` for work still running), and
   in `result` the lists `перенесены: …` and `migrated: …`.
3. The mover then labels the listed issues `migrated` and closes those that
   did not become steps; the turn itself never touches them.
4. The stream's autopilots are turned off (`multica autopilot update <id>
   --status paused`).

Rollback: `deactivate` the stream and resume the autopilots; the old issues
are only labelled, never deleted.

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
