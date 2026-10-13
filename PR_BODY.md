## What does this PR do?

The epic and sprint pages showed a title, a description and one counter. This PR turns them into working pages in the shape of Linear's project and cycle views: the work on the left, a summary on the right.

**Epic page**

- header with color, title, status, description edited in place, created date and last activity
- segmented progress bar by status (done / in review / in progress / blocked / not started, plus cancelled) with counts and percent done
- "Needs attention": blocked issues and issues in review, shown only when there are any
- issue list grouped by status, sprint or assignee; each row has identifier, title, status, priority, assignee and sprint; sub-issues are nested under their parent
- status board (the existing issue surface, scoped to the epic)
- "New issue" creates inside the epic, "Link existing" attaches an issue that already exists
- summary column: per-assignee done/total, the sprints the epic's issues sit in, the 10 newest issue events
- an empty epic shows the two ways to add an issue instead of a blank page

**Sprint page**

- header with name, goal, dates, status, "Day N of M · Days left: K", and Start / Complete actions
- the same progress bar, plus a burndown chart built from the actual closing dates; it is not drawn when there is no data
- issue list grouped by status, epic or assignee, and the status board
- summary column: workload per assignee, breakdown by epic, "Needs attention"
- completing a sprint opens a dialog: move unfinished issues to another sprint (the next planned one is preselected), leave them, or remove them from the sprint. Finished issues stay. Nothing is deleted.

**Lists of epics and sprints** are now rows with the progress bar, counts, dates and status.

### Backend

- `GET /api/epics/{id}/overview`, `GET /api/sprints/{id}/overview`: progress per status bucket, per-assignee counts, breakdown over the other planning relation, up to 20 blocked or in-review issues, recent activity (epic), burndown (sprint). Every query filters by `workspace_id`; a foreign id is a 404.
- No N+1: progress, assignees and the cross-relation breakdown come from one grouped scan; attention, activity and burndown are one query each; the list pages get the breakdown for all rows in one query.
- Epic and sprint list/detail responses gain a `progress` object. `issue_count` and `done_count` are unchanged.
- `POST /api/sprints/{id}/complete` with a required `open_issues` of `keep`, `move` or `unlink` (and optional `target_sprint_id`). `PUT status=completed` still changes only the status. The move, the status and the revision bumps commit in one transaction. `move` without a planned sprint to go to is a 409 that changes nothing.
- CLI: `multica sprint complete <uuid> --open-issues keep|move|unlink [--to <uuid>]`.
- No schema change and no migration. Existing epics, sprints and issue links are untouched.

## Related Issue

RUSL-1606 (stage 1 of RUSL-1605). Stacked on #2.

## Type of Change

- [ ] Bug fix (non-breaking change that fixes an issue)
- [x] New feature (non-breaking change that adds functionality)
- [ ] Refactor / code improvement (no behavior change)
- [x] Documentation update
- [x] Tests (adding or improving test coverage)
- [ ] CI / infrastructure

## Changes Made

- `server/internal/handler/planning_overview.go`: overview aggregates, list progress, `CompleteSprint`; routes in `server/cmd/server/router.go`; `progress` on `epic.go` / `sprint.go` responses
- `server/cmd/multica/cmd_planning.go`: `sprint complete`
- `packages/core/planning/`: bucket rule, percent and segments, sprint day count, grouping and parent/child tree (pure, unit tested)
- `packages/core/api/planning-schemas.ts`, `client.ts`, `epics/`, `sprints/`: schemas with fallbacks, client methods, queries, `useCompleteSprint`
- `packages/core/issues/surface/`: `epic` and `sprint` scopes for the issue surface (board)
- `packages/views/planning/`: progress bar, attention block, grouped issue list, work area, summary breakdowns, activity, burndown chart, complete-sprint dialog
- `packages/views/epics/`, `packages/views/sprints/`: detail pages and list rows
- `packages/views/modals/create-issue.tsx`: passes the epic or sprint the dialog was opened from
- `packages/views/locales/*/planning.json`: new namespace in en, fr, ja, ko, zh-Hans
- `docs/custom-planning.md`: endpoints, bucket rule, burndown rule, completion contract

## How to Test

Checks run in the build cluster on commit `c5c31265`. The head, `34ab1ae9`, changes translation strings only; the locale parity test and the planning view tests were re-run on it and pass.

| Check | Result |
| --- | --- |
| Go, planning tests (`internal/handler`, `cmd/multica`, `cmd/migrate`) | 34 top-level tests pass, 0 fail |
| Go, full suite with `-race` (`scripts/test-go.sh --race`) | 68 packages ok, 14 without tests, 0 FAIL, 0 races |
| `go vet ./internal/handler ./cmd/multica`, `gofmt -l` on changed files | clean |
| `turbo typecheck` (all packages except mobile) | 10 of 10 tasks pass |
| `turbo lint` (all packages except mobile) | 0 errors; every warning is in a file this PR does not touch |
| `@multica/views` tests | 478 files, 6028 tests pass |
| `@multica/core` tests | 171 files, 2244 tests pass |
| `@multica/web`, `@multica/desktop`, `@multica/ui-lab`, `@multica/docs` tests | 279, 666, 46 and 16 tests pass |
| `next build` of `@multica/web` | succeeds |
| Live stand: PostgreSQL + this backend + the production web build, driven by Playwright | passes, see below |

The live stand run seeded two epics and three sprints and checked against the API, not only the screen:

1. linking an existing issue from an empty epic page set its `epic_id`
2. creating an issue from the epic page put it in the epic
3. completing an active sprint through the dialog moved its 5 unfinished issues to the next planned sprint, left the 4 finished or cancelled ones, lost none, and set the sprint to `completed`
4. the board opened on the epic's and the sprint's own issues

To try it by hand: enable `MULTICA_PLANNING_ENABLED=true`, create an epic and a sprint, add issues in different statuses, open both pages.

## Checklist

- [x] I have run tests locally and they pass
- [x] I have added or updated tests where applicable
- [x] If this change affects the UI, I have included before/after screenshots
- [x] I have updated relevant documentation to reflect my changes
- [x] If this PR touches Chinese product copy, I checked it against `apps/docs/content/docs/developers/conventions.zh.mdx`
- [x] I have considered and documented any risks above

## Deliberately not done

- **Grouping by sprint or epic on the board, and filters on the grouped list.** The board is the existing issue surface with its status columns, filters and display options. Grouping by sprint or epic exists in the list only. Teaching the server-owned table and board grouping a new group kind touches the group query, cursors and four views; it deserves its own change.
- **The grouped list loads at most 1000 issues** and says so when it stops; the board has no cap.
- **Burndown scope is the sprint's current issue set.** Issues added or removed mid-sprint shift the line instead of showing as a step, because scope changes are not recorded. An issue with no recorded status change (an imported one) closes on its last update time. Days are UTC.
- **Completion writes no per-issue activity rows** for the issues it moves or unlinks. It bumps their revision, as deleting a sprint already does, and clients refresh on the sprint event.
- **The create dialog has no epic or sprint field.** Opened from an epic or sprint page it creates the issue there, without showing that in the dialog.
- **No "remove from epic" on a list row.** Use the epic picker on the issue.
- **Recent activity is on the epic page only**, as designed.
- **French, Japanese, Korean and Chinese strings were written by the author.** Their terms follow the conventions pages (issue is tâche / タスク / 태스크 / 任务; Sprint and Epic stay as written); the wording was not reviewed by a native speaker.
- **Mobile is untouched.** Desktop shares these views; its typecheck and tests pass, a desktop build was not made.
- **Aggregates are hand-written SQL in the handler**, like the table query, rather than sqlc queries: one query serves both epics and sprints by column.
- Out of scope by design: story points, velocity, roadmap or timeline, automatic sprint status by date.

## Rollback

No schema change and no data migration, so rolling back is deploying the previous backend and web images. Sprints completed through the new endpoint keep whatever the user chose for their issues.

## AI Disclosure

**AI tool used:** Claude Code (Multica agent)

**Prompt / approach:** Implemented from a fixed design brief. Backend aggregates and their tests first, then shared pure helpers with unit tests, then the views. Every check ran in the build cluster; the pages were then driven on a disposable stack to take the screenshots and to verify the create, link and complete flows against the API.

## Screenshots

Taken on a disposable seeded stand (the burndown history is backdated seed data).

**Epic, filled**

![Epic page](https://raw.githubusercontent.com/nenkoru/multica/579d966021a3ed766aabbe775534b3ffb8bb270b/03-epic-filled.png)

**Epic, grouped by sprint, with a sub-issue tree**

![Epic grouped by sprint](https://raw.githubusercontent.com/nenkoru/multica/579d966021a3ed766aabbe775534b3ffb8bb270b/04-epic-grouped-by-sprint.png)

**Epic, empty**

![Empty epic](https://raw.githubusercontent.com/nenkoru/multica/579d966021a3ed766aabbe775534b3ffb8bb270b/07-epic-empty.png)

**Sprint, filled**

![Sprint page](https://raw.githubusercontent.com/nenkoru/multica/579d966021a3ed766aabbe775534b3ffb8bb270b/09-sprint-filled.png)

**Sprint, empty**

![Empty sprint](https://raw.githubusercontent.com/nenkoru/multica/579d966021a3ed766aabbe775534b3ffb8bb270b/12-sprint-empty.png)

**Sprint board**

![Sprint board](https://raw.githubusercontent.com/nenkoru/multica/579d966021a3ed766aabbe775534b3ffb8bb270b/11-sprint-board.png)

**Completing a sprint**

![Complete sprint dialog](https://raw.githubusercontent.com/nenkoru/multica/579d966021a3ed766aabbe775534b3ffb8bb270b/13-sprint-complete-dialog.png)

**Lists**

![Epics list](https://raw.githubusercontent.com/nenkoru/multica/579d966021a3ed766aabbe775534b3ffb8bb270b/01-epics-list.png)

![Sprints list](https://raw.githubusercontent.com/nenkoru/multica/579d966021a3ed766aabbe775534b3ffb8bb270b/02-sprints-list.png)

More: [all 15 screenshots](https://github.com/nenkoru/multica/tree/assets/RUSL-1606-screenshots).

🤖 Generated with [Claude Code](https://claude.com/claude-code)
