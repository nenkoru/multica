# TV screen (`/tv`)

A custom, read-only, single-screen view of a workspace for a wall monitor or
TV: the current sprint, three large counters, epics with work in flight, a
"needs you" column and the last day's events. No scrolling, dark theme, Moscow
time everywhere.

## Pairing a phone or screen

The screen does not log in. It uses a kiosk token bound to one workspace.

```bash
# Owners and admins only. The token is printed once; keep it for pairing.
multica kiosk-token create --name "Phone" --owner-label "Нужен Руслан"
multica kiosk-token pair <id>            # one-time link + QR code, valid 15 min
multica kiosk-token list                 # paired devices show their parent_id
multica kiosk-token revoke <id>          # revoking the parent signs out all its devices
```

Scan the QR code with the phone (or open the link on the TV). The link carries a
single-use code in the fragment (`/tv#pair=…`); the page sends it once in a POST
body to `/api/tv/session`, which mints a device token under the parent token
and answers with an HttpOnly cookie (`multica_tv`, SameSite=Strict, one year).
The fragment is removed from the address, fragments never reach the server,
and the code is dead after first use — so no working key lives in a URL. A
kiosk token or a code can also be pasted on `/tv`. `?lang=en` switches the copy
to English; Russian is the default.

## Phone first

- **Portrait** is the main mode: sprint and clock, the three numbers, "needs
  you", epics, feed — one column, scrolling if it does not fit.
- **Landscape** is the 16:9 wall picture: the same layout a monitor gets when
  the phone is connected to it, sized from whichever of width and height binds.
- **Installable** (`/tv/manifest.webmanifest`, `display: fullscreen`): "Add to
  home screen" opens `/tv` without the address bar.
- **Screen stays on** while the page is visible (Screen Wake Lock); the lock is
  taken again after the page returns or on a tap.
- **Battery:** no polling and no socket while the page is hidden; on return it
  reconnects and refetches. The clock ticks every 10 s only while visible.
- **Layer slot:** `TvScreen` takes `layer` (a band of ~38 % of the landscape
  height, the top of the portrait screen) and `onEvents`, one stream of changes
  between snapshots (done issues, failed runs, "needs you" added/cleared,
  counter changes), for the game layer and sounds of RUSL-1786.

## What a kiosk token can do

| Request | Result |
|---|---|
| `GET /api/tv/overview` | 200, aggregate below |
| `GET /api/tv/ws` | WebSocket that only ever receives `{"type":"tv:changed"}` |
| any other `/api/*` (issues, comments, chat, …) with the token as bearer or as the only cookie | 403 |
| after `revoke` | 401 on the aggregate; the socket is closed within a minute |

Only SHA-256 hashes of tokens and pairing codes are stored (`kiosk_token`,
`kiosk_pairing`, migrations 573–576).

The aggregate carries titles, identifiers, assignee names, counts and times. It
never carries descriptions, comments, run output or run error text — a failed
run shows only its coarse `failure_reason`.

## "Waiting for Ruslan" — the source

One mechanism: **blocked issues carrying the screen's owner label**. The label
is set per token (`--owner-label`); for Ruslan's workspace it is the issue label
**«Нужен Руслан»**. An issue counts while its status is `blocked` and the label
is attached; removing the label or unblocking the issue takes it off the
screen. Put the label on a blocked issue when the next action is Ruslan's
(the five-point blocked comment of RUSL-1489 describes that action).

## What is counted

- Top-level issues only for epic rows, "stuck" and "no epic"; sub-issues are
  represented by their parent. The "needs you" column also lists labelled
  sub-issues — the label is an explicit request.
- **Agents working:** distinct agents with a task in `dispatched` or `running`.
- **Stuck > 24 h:** issues in progress, in review or blocked with no activity
  for 24 hours. Such issues are dimmed in lists.
- **Epic rows:** open epics with work in flight or activity in the last 24 h,
  busiest first, at most five; the rest is one number. The bar shows in
  progress / in review / blocked / done since the sprint started (since a week
  ago without a sprint), and up to three issues in progress with assignee and
  time since they entered the status.
- **Current sprint:** the active sprint, else a planned sprint whose dates
  include today.
- **Feed:** issues closed as done and failed agent runs of the last 24 h,
  newest first, at most eight.

## Updates

The page refetches the aggregate 0.75 s after a change signal from the socket
(issue, task, epic, sprint, label and agent events) and every 60 s regardless.
The last good data stays on screen while offline.

## Rollback

Revoke the tokens. To remove the feature, deploy the previous images; the
`kiosk_token` table can stay or be dropped with the down migrations.
