# TV screen: game layer and cockpit alarms

A custom addition to the TV screen: a map strip where epics are crystal fields
and agents are workers, and an alarm layer that sounds only when something
changes. Code lives in `packages/views/tv-game/`; the health aggregate in
`server/internal/handler/tv_health.go`.

## Wiring it into a screen

```tsx
import { parseTVGame, parseTVHealth } from "@multica/core/api/tv-game-schemas";
import { FailedRunTracker, TvGameDemo, TvGameLayer, toTvGameSnapshot, useTvGameController } from "@multica/views/tv-game";

const game = useTvGameController({ labels: { llm: "27B" } });
const failed = useRef(new FailedRunTracker());
// On every refresh (the change signal on /api/tv/ws, or the 60 s fallback):
const g = parseTVGame(await getJSON("/api/tv/game"));
const h = parseTVHealth(await getJSON("/api/tv/health"));
game.update(toTvGameSnapshot(g, h, needsYouIds, Date.now()));
game.pushEvents(failed.current.next(g));

return demo ? <TvGameDemo /> : <TvGameLayer controller={game} />;
```

`needsYouIds` are the issue ids the base screen shows under "needs you"; a
blocked worker gets a "!" either way, and a caution only when it is one of them.

The layer fills its container; give it 35–40 % of the screen height. Numbers
and lists of the base screen stay where they are.

`TvGameSnapshot` (`model.ts`) is plain data: epics with open and done-in-sprint
counts and last activity, agents with slots, current work items (running, in
review, blocked, with a `needsYou` flag), done-in-sprint, running runs, the real
host ceiling, queued count, recently done issues and closed epics, and the
health signals. Subissues are mapped to their parent by the caller.

| Work | On the map |
|---|---|
| Epic | Crystal field; crystals from open issues, fading as the sprint finishes them |
| Agent | Worker labelled with the agent's name, up to its slot count |
| Running | Worker digging at its epic's field |
| In review | Worker carrying a load to the base |
| Done | "+1" over the base, the Done counter grows, soft ding |
| Idle agent | Worker standing at the base |
| Blocked | Worker at the field with a "!" |
| Run failed | Worker down, flash, wreck labelled with the issue for 60 s |
| No activity > 24 h | Field overgrown and dimmed |

Supply is running runs over the host ceiling passed in the snapshot, not the
sum of agents' `max_concurrent_tasks`.

## Alarms

Only transitions sound; a state that was already true stays silent. The first
snapshot after load is a baseline and raises no info or caution events.

| Tier | Sound | Trigger |
|---|---|---|
| Info | one soft ding | issue done, epic closed, warning recovered |
| Caution | double gong + "Внимание: …" | run failed, new "needs you" issue, issue crossed 24 h without activity, no runs while the queue is not empty |
| Warning | master warning repeating every 8 s, voice once a minute, until acknowledged | a `warning` signal from `/api/tv/health` |

- At most one caution sound per 2 minutes; cautions inside the interval are
  grouped by kind into one line ("3 запуска упали") when it ends.
- 23:00–08:00 MSK only warnings sound. Plates still appear.
- Any key or a click on a flashing plate acknowledges: sound stops, flashing
  stops. A warning plate stays until its signal is `ok` again; `unknown` does
  not clear it. A caution plate leaves after 10 minutes without updates.
- "Quiet for 1 h" mutes every tier for an hour; plates keep working.
- Voice lines are Russian regardless of the UI locale and use the browser's
  speech synthesis. Without a Russian voice the chimes still play.
- TV browsers block audio until the first gesture: the strip shows
  "Enable sound", and the first key press also unlocks audio. A Chromium kiosk
  can start with `--autoplay-policy=no-user-gesture-required`.
- `prefers-reduced-motion: reduce` draws a still picture, redrawn on changes,
  and plates do not flash. The animation is capped at 30 fps.

## Endpoints

Both are read-only and take the TV kiosk credential (bearer token or the
kiosk cookie), like `/api/tv/overview`; the token's workspace scopes them.

`GET /api/tv/game`: agents with a run in the last week (name, slots), work
(a running task shown on its top-level issue, or a top-level issue in review
or blocked assigned to an agent), open epics with open issues and done in the
active sprint, running and queued task counts, the host ceiling
(`MULTICA_TV_HOST_CEILING`, default 12), issues done and epics closed in the
last 24 h, and runs failed in the last 10 minutes. Titles and names only, no
descriptions or comments.

`GET /api/tv/health` returns counts and reason codes only, never URLs,
hostnames, runtime names or error text.

| Signal | Warning when |
|---|---|
| `daemon` | a local daemon that heartbeated in the last 24 h has had every runtime offline for more than 2 minutes |
| `llm` | two consecutive probes of `MULTICA_TV_HEALTH_LLM_URL` failed (connection error, timeout, non-2xx) |
| `oom` | a run in this workspace failed with an out-of-memory kill in the last 15 minutes |
| `site` | two consecutive probes of `MULTICA_TV_HEALTH_SITE_URL` failed |

Probe results are cached for 30 s and shared by all clients, so the upstream
sees at most two requests a minute. An unset URL reports `unknown` with reason
`not_configured`, which never sounds. `MULTICA_TV_HEALTH_LLM_TOKEN`, when set,
is sent as a bearer token to the LLM probe; point the URL at a route that
fails when the model backend is down, not only when the proxy is.

## Demo

`?demo=1` on the TV screen renders `TvGameDemo`, a looping 60-second script
through every event above, on a virtual clock that jumps over the 2-minute
limit and into night mode. Without a backend: `pnpm dev:ui-lab` and open
`/?tv-game`. `demo.test.ts` runs the same script on a fake clock and checks
the sounds it produces.

Assets: see `packages/views/tv-game/ASSETS.md`.
