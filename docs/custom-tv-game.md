# TV screen: Orbit and cockpit alarms

The main view of `/tv` (RUSL-1786), built for a phone in portrait first; a
monitor connected to the phone shows the same page in landscape. The board
from RUSL-1784 stays at `/tv?view=board`. The page renders `TvOrbitPage`,
which is the base `TvScreen` (pairing, realtime, wake lock) with the Orbit
passed through its `screen` slot in place of the dashboard. Code: `packages/views/tv-game/`,
feeds in `packages/core/tv/game.ts`, server in
`server/internal/handler/tv_{game,health,config,sounds}.go`.

## Screen

- **The orb (Mika)** is the one thing to look at: calm blue breathing — all
  is running; amber, faster — Ruslan is needed; red, fast — a warning; rings
  — a voice conversation. Tapping the orb is the hook for the voice feature.
- **One line** under the orb ("Всё идёт. 6 агентов в работе." / "Mika зовёт:
  27B недоступна 4 мин") and one hint.
- **"Главное сейчас"**: exactly one thing — the top warning, else the oldest
  "needs you" issue as one decision, else the longest-running run.
- **Epics are planets** on three orbits: the busiest closest, size by open
  issues, a day without activity dims the planet onto the far orbit. Agents
  with a run going are satellites of their epic's planet. A finished issue is
  a spark flying into the orb.
- **Today closed** and the streak of days with at least one finished issue.
- **Focus**: one tap hides everything but the orb, the line and "needs you".
- **Landscape** (≥ 768 px wide): a right column with "needs you" and the
  last 24 hours.
- **PWA and wake lock** come from the base screen (`app/tv/tv-manifest.ts`,
  `views/tv/use-device.ts`); the Orbit adds none of its own. Feeds poll
  only in the foreground (`/api/tv/game` 60 s, `/api/tv/health` 30 s), plus
  the kiosk WebSocket change signal.
- **Load**: canvas capped at 24 fps, paused with the tab;
  `prefers-reduced-motion` draws one still frame per change.
- Copy is Russian by default, English with `?lang=en`.

## Alarms

One table, `tv-game/sound-table.ts`; order is priority.

| Signal | Level | File |
|---|---|---|
| Multica daemon offline | warning | `airbus_master_warning_crc` |
| 27B unavailable | warning | `airbus_master_warning_crc` |
| Site not responding | warning | `boeing_fire_bell_737` |
| Work stalled: queue waits, no run going | warning | `airbus_stall_cricket` |
| Budget or subscription limit at reserve | warning | `gpws_pull_up_x3` |
| Host overloaded / OOM-killed runs | caution | `overspeed_clacker` |
| Autopilot run failed | caution | `airbus_ap_disconnect_cavalry` |
| Many failed runs in a window | caution | `boeing_stick_shaker` |
| Two runs on one issue | caution | `tcas_traffic_traffic` |
| 27B slower than normal | caution | `gpws_sink_rate` |
| Run failed / new "needs you" / issue stuck > 24 h | caution | `airbus_master_caution_chime` |
| Sprint ends within a day, too little done | caution | `gpws_minimums` |
| Warning cleared | info | `tcas_clear` |
| Issue done | info | `airbus_cabin_chime` |
| Epic closed | info | `airbus_altitude_c_chord` |

- Only a change sounds. One sound at a time: the highest row wins, and
  nothing below an unacknowledged warning sounds while it is active.
- A warning repeats every 8 s until acknowledged (any tap or key); its plate
  stays until the signal is `ok` again, then "cleared" sounds. `unknown`
  never sounds and never clears.
- A caution sounds once, at most one per 2 minutes; cautions inside the
  interval are grouped into the next sound.
- 23:00–08:00 MSK only warnings sound. "Тихо на 1 ч" mutes everything.
- Mobile browsers allow audio after the first tap: the screen shows
  "Включить звук", and the first tap also unlocks audio. Safari's audio
  session is set to `playback`, so the ringer switch does not mute alarms.
  A Chromium kiosk can start with `--autoplay-policy=no-user-gesture-required`.

### Sound files

The files are GPL-2.0 recordings from FlightGear aircraft
(`docs/tv-sounds-attribution.md`) and are **not in this repository**. The
server serves them to kiosk screens from `MULTICA_TV_SOUNDS_DIR` at
`/api/tv/sounds/<file>.mp3` (only `[a-z0-9_].mp3` names). Without the
directory the alarms still show, silently. mp3 only: iPhone Safari does not
play Ogg.

## Server

All TV endpoints take the kiosk credential (bearer token or the kiosk
cookie), like `/api/tv/overview`.

`GET /api/tv/game`: agents with a run in the last week (name, slots), work (a
running task shown on its top-level issue, or a top-level issue in review or
blocked assigned to an agent, with its start), open epics, running and
queued counts, the host ceiling, done today and the streak, issues done and
epics closed in 24 h, runs failed in 10 minutes. Titles and names only.

`GET /api/tv/health` returns the signals above (except the event cautions)
in table order, each `ok`, `warning` (raised) or `unknown`, with counts and
reason codes only — never URLs, hostnames, runtime names or error text.

| Signal | Raised when |
|---|---|
| `daemon` | a daemon from `watched_daemons` has had every runtime offline for over 2 minutes; without the list the signal is `unknown` |
| `llm`, `site`, `budget`, `llm_slow` | two consecutive probes of the configured URL failed (non-2xx, timeout, no connection); cached 30 s |
| `stall` | no run going while a queued task has waited past `stall_minutes` |
| `oom` | a run failed with an out-of-memory kill (`oom_kill`, "out of memory", exit status 137) within `oom_window_minutes`; runs killed by their own timeout or idle watchdog do not count |
| `autopilot` | an active autopilot's run failed within `autopilot_window_minutes` |
| `failed_burst` | `failed_burst` runs failed within `failed_burst_window_minutes` |
| `duplicate_run` | two runs on the same issue have been active together for over `duplicate_run_minutes` (a hand-off between agents overlaps for seconds and does not count; a parent and its sub-issue are not a duplicate) |
| `minimums` | the active sprint ends within `minimums_hours` with less than `minimums_percent` done |

### Configuration

Every threshold and probe is one JSON in `MULTICA_TV_CONFIG`, read at start
(a restart, no rebuild). Defaults:

```json
{
  "host_ceiling": 12,
  "stall_minutes": 10,
  "failed_burst": 5,
  "failed_burst_window_minutes": 10,
  "duplicate_run_minutes": 2,
  "oom_window_minutes": 15,
  "autopilot_window_minutes": 30,
  "minimums_hours": 24,
  "minimums_percent": 70,
  "watched_daemons": ["<daemon id of each server>"],
  "probes": {
    "llm": { "url": "https://…/health", "bearer_env": "MULTICA_TV_LLM_TOKEN" },
    "site": { "url": "https://…/" },
    "budget": { "url": "https://…/budget-ok" },
    "llm_slow": { "url": "https://…/speed-ok" }
  },
  "llm_cache": { "url": "http://litellm-cache-report.litellm.svc:8080/report.json?hours=24" }
}
```

`watched_daemons` lists daemon ids (as `multica runtime list` shows them) of
the machines that must stay up — servers, not a laptop that is closed for the
night. Tokens are never in the JSON: `bearer_env` names the variable that holds
one. An unset probe reports `unknown` (`not_configured`) and never sounds.
`budget` and `llm_slow` need an endpoint that answers 2xx while all is well
and non-2xx past the limit; until one exists they stay `unknown`. An invalid
JSON is logged and the defaults apply.

`llm_cache` (RUSL-1860) adds the "27B cache" block under "needs you" on the
board (`?view=board`): the share of reusable prompt tokens taken from the
prefix cache against the 99.5 % goal, the share of all prompt tokens beside it,
the last 24 hours as bars and "saved / spent" against the cloud price. The URL
is the JSON of `cache_stats.py serve` (`infra/litellm-rusl1860` in
`vergel-brain`). The server asks it at most once a minute, forwards only these
numbers to the kiosk, keeps the last answer for 30 minutes while the source is
down and then hides the block. Without the key there is no block.

## Demo

`/tv?demo=1` plays `TvOrbitDemo` without a kiosk token: calm, a finished
issue, Mika calling, a conversation, then every row of the sound table — the
cautions one by one past the 2-minute limit, the warnings in priority order
with a lower warning staying silent under a higher one, acknowledgement,
two warnings at once, and night mode at 23:30 MSK — on a virtual clock.
Sounds load from `/api/tv/sounds`, so they play on a paired screen. Without
a backend: `pnpm dev:ui-lab`, put the files in `apps/ui-lab/public/tv-sounds/`
and open `/?tv-game`. `demo.test.ts` runs the same script on a fake clock.
