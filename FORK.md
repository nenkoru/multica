# Self-host fork

## RUSL-2760: v0.8.1 candidate

Upstream base: `v0.8.1` (`b1ea927def6490c43bedff537b6f4ed9a47fa828`).
Production source inputs: `fabrika` at `44882282`, and CLI/daemon
`custom/RUSL-2508-v072-cli` at `d61b8d97c`.

The integration branch starts at the upstream tag. The GitHub publication
branch snapshots its application tree onto the existing `fabrika` head and
retains the fork's existing `.github/workflows`: the available token has no
workflow scope. Verify tree equality outside that directory. Preserve the
integration history in the release bundle; no additional GitHub permission is
needed to publish application changes.

| Patch | Main files | Purpose | Still needed |
| --- | --- | --- | --- |
| Planning | `server/internal/handler/{epic,sprint,planning}.go`, `server/pkg/db/queries/{epic,sprint,issue}.sql`, `packages/core/{epics,sprints}`, `packages/views/{epics,sprints}` | Epics, sprints and issue membership | Yes; upstream has no equivalent |
| Planning CLI | `server/cmd/multica/cmd_planning.go`, `cmd_issue.go` | Preserve epic/sprint commands and filters | Yes |
| Agent chat authorship and posting | `server/internal/service/agent_chat_post.go`, chat handlers, `server/cmd/multica/cmd_chat*.go` | Agent attribution, bounded posting and chat reads | Yes |
| Claude idle resume bound (RUSL-1741) | `server/internal/daemon/{config,daemon}.go`, `server/pkg/agent/claude.go` | Avoid resuming an expired prompt cache | Yes; separate issue/chat settings |
| Secret value masking (RUSL-2281) | `server/pkg/redact/{redact,values}.go`, `server/internal/daemon/{client,secretvalues}.go` | Redact known secret values before reports leave the daemon | Yes |
| Graceful shutdown recovery (RUSL-2266) | `server/internal/daemon/daemon.go` | Recover interrupted runs | Partly superseded: use upstream `runtime_recovery` for clean cancellation and directory-lock waits; retain process-error recovery and its safety exclusions |
| Interruption notice | `server/internal/daemon/{prompt,types}.go`, `server/internal/handler/daemon.go` | Tell retries to inspect prior side effects | Yes |
| TV / kiosk / Orbit | TV handlers, kiosk migrations, `packages/views/tv`, kiosk CLI | Read-only wall display | Yes |
| Fabrika and Multika | `server/internal/streams`, `packages/core/streams`, `packages/views/streams`, migrations `9000+` | Streams, summaries, spend, mandate confirmations, voice and screens | Yes; retain latest RUSL-2729 behavior |
| Spanish custom features | `packages/views/locales/es`, locale registration | Translate retained planning, streams, chat and recovery copy for upstream's new locale | Yes |
| Inactive table virtualizer | `packages/ui/components/ui/data-table.tsx` | Avoid scroll callbacks for ordinary, non-virtualized tables | Yes on v0.8.1; remove when upstream gates the unused virtualizer |

The v0.8.0 layout replaced the old sidebar and save-view dialog. Planning and
Fabrika entry points move into the Work panel; the obsolete components stay
deleted. Saved view definitions retain epic/sprint filters.

## Upstream changes and migration review

Read changelog entries in `apps/web/features/landing/i18n/en.ts` and highlights
in `packages/views/whats-new/releases.ts`. The update introduces the new panel
layout, advanced views, unread issues, skill history, runtime concurrency caps,
and CLI workflow/view commands. It also tightens issue deletion permissions and
provider process cleanup checks; these upstream safety checks must remain.

There are 21 new upward migrations, numbered 595–616 (609 is absent): saved
sidebar preferences, unread state, WeCom installation sessions and indexes,
runtime task limits, Inbox sections, provider credits, ZCode, release tracking,
skill versions, project creators, attachment dimensions and pinned workspaces.
Existing custom planning/chat/kiosk and Fabrika migrations remain. The runner
tracks full migration names, not just numeric prefixes.

No production migration is authorized by a successful compilation alone.
Before rollout, restore a fresh production dump into an empty database, run the
candidate migrations against the restored copy, and verify planning and stream
counts. Preserve old backend/frontend images and both daemon binaries.

## Rollback baseline

Observed production: backend `v0.7.2-fabrika16`, frontend
`v0.7.2-fabrika12`, CLI/daemon `v0.7.2-rusl2508-cli2`.
Before changing anything, resolve image digests and retain the old binaries as
`*-bak`. Roll back images and binaries together; if reverting schema is needed,
restore the verified pre-rollout dump during a quiescent window. Do not drop
new tables or columns after writes have resumed.

The release is complete only after review, restore/migration rehearsal,
server → hetzner daemon/CLI → Proxmox standby rollout, functional smoke checks
and 30 minutes of observation. Until then this is a candidate, not a deployed
release.
