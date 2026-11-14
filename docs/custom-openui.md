# OpenUI delivery and readable fallbacks

The Fabrika catalog is fork-owned. `Table` headers remain short labels (80
characters), while each cell accepts bounded text (1200 characters), like
`Text`. Row/column counts, URL restrictions, closed component/action catalogs,
and server-bound action IDs still apply. A required container with no surviving
children fails compilation instead of producing a successful empty screen.

The client shows a loading message for an unresolved live root and an explicit
failure for an empty, unknown, malformed or cyclic completed root. The completed
chat message supplies its original fenced source as escaped plain text when
the screen cannot render or its delivery record is missing. The client never
evaluates the source. Stored screens and source fallbacks retain sequence order.
Old pruned records are not rewritten: reloading them displays their original
source, without re-running model repairs or creating new action records.
Content checks cover every catalog leaf's visible fields. Tooltip-only hints,
links without labels and whitespace do not make a screen readable; displayed
zero values, status icons, chart data and content reached through references do.

## Fork inventory

| Change | Files | Why still needed |
| --- | --- | --- |
| Cell text bound and empty-container failure | `server/internal/streams/genui/{catalog,compile}.go` | The custom catalog and compiler are absent upstream |
| Visible loading/failure and original-source fallback | `packages/core/streams/`, `packages/views/streams/genui/`, chat extras wiring | Prevent silent loss of custom screens on live and historical messages |

## Regression harness

`server/internal/streams/genui/testdata/table-message.md` is the exact assistant
message read by the authorized CLI for RUSL-3752, not a reconstruction. Content
SHA256: `d30359300189c90bde2b19cfcdf44c5bbfb0901b8c5d31f8845623aef5072f72`.
`scripts/genui-browser/baseline.json` records the old compiler output for it.

Run expensive checks only in the authorized job environment. First run the real
compiler and regenerate its JSON fixture before running the browser:

```sh
cd server
GENUI_REPRO_OUTPUT="$PWD/../scripts/genui-browser/result.json" go test ./internal/streams/genui -run TestStoredTableMessage -count=1
cd ..
sh scripts/genui-browser/run.sh
```

When using separate Go and Playwright jobs, export `repro.json` from the Go job
and place it at `scripts/genui-browser/result.json` before submitting the browser
job. The browser job
uses the installed Chromium via `CHROME_PATH` or `/ms-playwright`, imports the
production schemas, cache reducers, `TaskScreens` and `ScreenView`, and asserts
4 rows, 3 columns, 12 cells, surrounding text, safe old/unknown/broken/missing
fallbacks, reload and streamed completion. It exports DOM, screenshots and
console evidence into `browser-artifacts/`. Harness CSS is a controlled layout;
this does not attest production CSS, cookies, browser cache or a signed-in chat.

`sh scripts/genui-browser/quality.sh` runs the focused core/view tests (including
chat wiring), both package typechecks, and targeted lint.

## Delivery and rollback

RUSL-3752 is based on the live v0.8.1 line `custom/RUSL-2760-v081`, not the
unreleased v0.8.2 audit stack. This change has no schema migration, new endpoint,
action capability or external dependency. It can ship independently of RUSL-3254
after independent review and the existing backup/restore, build, quiet-window
and smoke gates. Keep its source commit available for a later cherry-pick onto
the accepted v0.8.2 target; do not change that target's pins silently.

A frontend-only deployment is a narrow first step compatible with the existing
backend: it restores readable source for already-pruned records. Future long
tables need the matching compiler fix. Old records remain readable source until
a separately authorized repair; no automatic production rewrite is included.

Immediately before rollout, re-read deployment digests and configuration, save
the prior artifacts and verify a fresh database backup by restoration. Preserve
the schema and all newer records on rollback. The observed baseline on
2026-11-14 was:

- Backend: `git.bayfut.net/hetzner-agents/multica-planning-backend@sha256:8ddef73f4f1b168035e6e7e7af07c4c1a184b573bc1c2e952a934fbea5fe64f9`
- Frontend: `git.bayfut.net/hetzner-agents/multica-planning-web@sha256:fe7dfdf96c83176f756e65d27311ddb47bc7736a955a8ad4b8a163cb835ad55e`
- Web version: `0.8.1`, Next build ID: `n0nH-dbjsu9tWlBqrEwA3`.

If those baselines remain current, the rollback image commands are:

```sh
kubectl -n multica set image deployment/multica-frontend frontend=git.bayfut.net/hetzner-agents/multica-planning-web@sha256:fe7dfdf96c83176f756e65d27311ddb47bc7736a955a8ad4b8a163cb835ad55e
kubectl -n multica set image deployment/multica-backend backend=git.bayfut.net/hetzner-agents/multica-planning-backend@sha256:8ddef73f4f1b168035e6e7e7af07c4c1a184b573bc1c2e952a934fbea5fe64f9
```

Use only the established kube context and maintenance procedure; wait for both
deployments to become ready and verify chat loading and action gates. This
document does not attest that rollout, a fresh restore or production DOM smoke
has happened. CLI message reading succeeded; the CLI currently exposes no
stored-screen reader, and this task has no configured signed-in browser session.
The old persisted `ui_block`, user's cached bundle and production DOM therefore
remain unobserved. Do not substitute another user's session to close that gap.
