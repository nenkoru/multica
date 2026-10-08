# Custom planning build

This port starts at upstream `5063fc90794f94a2b117cdf6db29ca2b96d9b5f9`
and adapts [PR #4045](https://github.com/multica-ai/multica/pull/4045), head
`47ebf353bfeca69030012ab9599752bc24a05e65`. The PR was open, unmerged and
conflicted when inspected on 2026-10-08; its review/comment lists were empty.
Its old issue update and UI query paths were adapted to the current transactional
issue service and shared server-backed issue surface. CLI and Desktop wiring
are additional custom changes. Mobile UI is outside this port.

## Contract

An issue has independently nullable `epic_id` and `sprint_id`. They never replace,
inherit from, or modify `parent_issue_id`, `stage` or `project_id`. Imported
issues default to null and retain their hierarchy and metadata. Explicit JSON
null clears a relation; an omitted field preserves it. Deleting an epic/sprint
unlinks issues transactionally and advances their revisions without deleting
them. Workspace teardown also removes planning records. Attachment validation,
planning edits and deletion share a workspace advisory transaction lock.

Epics have title, optional description, color, and `open`/`closed` status.
Sprints have name, optional goal, required `YYYY-MM-DD` dates and explicit
`planned`/`active`/`completed`/`cancelled` status. End must be strictly after start.
Multiple active or overlapping sprints are allowed. There is no lifecycle
scheduler or automatic movement of unfinished issues. Counts include linked
issues; done counts include built-in terminal states and custom done/closed
categories, scoped to the entity's workspace.

CRUD is under `/api/epics` and `/api/sprints`, with UUID detail routes, existing
authentication and workspace membership middleware. Lists accept `status`.
Issue lists accept `epic_id` and `sprint_id`; the table surface additionally
supports multi-value `epic_ids`/`sprint_ids` and `include_no_epic`/`include_no_sprint`.
Values and null are OR within a dimension, dimensions are AND. Existing batch
issue updates reject planning fields explicitly: use individual atomic updates.
No planning swimlane/grouping mode is introduced. Saved view JSON stores and
restores planning selections; its existing chip editor has no planning-specific
fixed-value chips yet. English, Japanese, Korean and Simplified Chinese have
planning namespaces; French uses English namespace fallback with French nav and
issue-property labels.

The backend flag `MULTICA_PLANNING_ENABLED=true` enables planning CRUD and new
issue attachments. It defaults to false and is advertised by `/api/config`.
Custom clients hide navigation, pickers and filters while disabled; disabled
planning selections do not narrow issue surfaces. Ordinary issue operations
and explicit clearing still work. Older clients ignore added nullable response
fields. The custom CLI checks the capability before sending planning issue
flags, so an older server cannot silently drop them.

## CLI

```bash
multica epic list --output json
multica epic create --title 'Release' --description 'Deliverable'
multica epic get EPIC_UUID
multica epic update EPIC_UUID --status closed --description ''
multica epic delete EPIC_UUID
multica sprint create --name 'Week 1' --goal 'Ship planning' --start-date 2026-10-09 --end-date 2026-10-16
multica sprint list --status active --output json
multica sprint get SPRINT_UUID
multica sprint update SPRINT_UUID --status active
multica sprint update SPRINT_UUID --start-date 2026-10-10 --end-date 2026-10-17 --goal ''
multica sprint delete SPRINT_UUID
multica issue create --title 'Deliver planning' --epic EPIC_UUID --sprint SPRINT_UUID
multica issue update ISSUE_UUID --epic '' --sprint ''
multica issue list --epic EPIC_UUID --sprint SPRINT_UUID --output json
```

Replace uppercase UUID placeholders. Existing CLI auth and task scope apply;
these commands do not grant access to another workspace or server.

## Build inputs and Desktop

Build only from a clean checkout of the reviewed source commit. Commit identity,
`server/go.mod`/`go.sum`, `pnpm-lock.yaml`, `pnpm@10.28.2`, Go 1.26 and the existing
Dockerfiles define the candidate inputs. Container base tags remain those of
upstream; reproducible source provenance does not guarantee identical bytes if
those tags change. Candidate images carry the full source SHA in their tag and
OCI revision label. No image was published by this port's local delivery.

On an authorized build runner with Docker:

```bash
export PLANNING_SOURCE_SHA=$(git rev-parse HEAD)
export PLANNING_BUILD_DATE=$(git show -s --format=%cI)
docker compose -f docker-compose.selfhost.yml -f docker-compose.planning.yml build
```

This builds candidates without deploying them. Run migrations and deployment
only in the separate authorized deployment/migration work, with a backup and
independent review. The override forwards the flag, which still defaults off.

Stock Desktop bundles its renderer; `appUrl` selects browser/share-link URLs,
not a remotely loaded custom renderer. A custom Desktop build is required for
the new pages. Its router uses the same shared views as web. `pnpm --filter
@multica/desktop build` compiles main/preload/renderer and, when Go is installed,
bundles the custom CLI from this source. Release packaging/signing is separate;
frontend-only builds without Go fall back to an upstream CLI and cannot deliver
these commands. Use a build runner with Go for a deployable desktop artifact.

Production Desktop reads `~/.multica/desktop.json` on startup:

```json
{
  "schemaVersion": 1,
  "apiUrl": "https://api.example.test",
  "wsUrl": "wss://api.example.test/ws",
  "appUrl": "https://app.example.test"
}
```

These are example addresses, not a tested deployment. Restart Desktop after
changing the file. Development uses `VITE_API_URL`, `VITE_WS_URL`, `VITE_APP_URL`.
Absent production configuration falls back to Multica Cloud. Existing runtime
configuration tests verify normalization and rejection of invalid URLs; the
installed Mac application and its live connection were not changed.

## Migrations and rollback

Migration 565 creates two tables and nullable issue columns. Migrations 566–570
each build one concurrent index; the runner's interrupted-index recovery hooks
include them. No foreign keys or cascade actions are introduced. Forward
migrations are tracked and repeat safely. The integration test runs up twice,
checks every index valid, runs these six down files in reverse order, verifies
the original issue hierarchy, and runs up again.

The safe operational rollback is one configuration step: set
`MULTICA_PLANNING_ENABLED=false` in the backend deployment and restart/recreate
that service using its existing deployment procedure. Keep the additive schema
and planning data. For the supplied Compose candidate, after its independent
deployment approval, the equivalent command is:

```bash
MULTICA_PLANNING_ENABLED=false docker compose -f docker-compose.selfhost.yml -f docker-compose.planning.yml up -d --no-deps backend
```

Schema rollback requires a backup and stopping the custom backend first. Only
roll back 570 through 565, in descending order, using their down files and the
migration runner's ledger handling. Those down files remove planning tables and
relations, so their planning data is lost; original issues remain. Do not use
the unrestricted `migrate down`: it rolls back older upstream migrations too.
Do not run the custom server after removing its columns. A full application
rollback uses the previous reviewed backend/web/desktop artifacts while leaving
the additive schema in place.

## Upstream candidates

Initialize a `custom/planning` working branch in the authorized fork. Enable its
repository variable `PLANNING_CANDIDATES_ENABLED=true` only after the initial
review. `.github/workflows/planning-candidate.yml` runs manually or weekly. It
fetches upstream main, creates a unique candidate branch and performs a normal
merge, preserving custom history. The preflight detects migration numbering
collisions. Conflicts fail without pushing an unresolved merge; resolve them
in a separate reviewed port. Nothing force-pushes or rebases the working branch.

The heavy verify/image jobs require an existing authorized Proxmox GitHub
runner labeled `planning-build`, with Docker, Go and Node toolchain support.
This change provisions no runner or credentials. The lightweight sync/PR jobs
use hosted runners. PR events in this same fork rerun checks against the PR
head so branch protection receives candidate checks rather than only a scheduled
run on the default branch. External-repository PR heads do not run on that
self-hosted runner.

Clean candidates run sqlc drift checks, migration/Go race tests, frontend checks,
web/Desktop builds and backend/web Docker builds. Image archives are artifacts,
not registry publications. Only then does the workflow open a PR inside its own
fork. Required independent review and acceptance precede merging into the
working branch. There is no production job or automatic deployment. Configure
the fork's existing branch protection to require these checks and review.
Python git fixtures exercise a clean merge, a migration collision and a content
conflict: `python3 scripts/planning-upstream-candidate.test.py`.

Heavy verification runs on approved cluster/Proxmox runners. The standalone
cluster test script creates a disposable PostgreSQL database inside its job;
never give that script production credentials. Test results and the exact custom
commit are delivered separately with the task handoff.
