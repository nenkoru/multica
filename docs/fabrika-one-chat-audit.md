# Fabrika one-chat audit

The collector observes authenticated issue API requests, mounted issue surfaces,
incoming chat messages, and explicit §7 answers. It stores metadata in the existing
`activity_log` (`action=fabrika_journey_v1`); it creates no table or migration.
Existing issue and wakeup activities receive `details.journey` at creation time.
Historical rows are never updated or classified from their member actor alone.

Source base is the independently accepted custom v0.8.2 candidate from PR #25,
`a4e032acb480953d819e20b32717dd004890d2e2` (DSH round RUSL-2941). The live
installation remains the accepted v0.8.1 release from RUSL-2760. This change is
stacked on the reviewed port, not on historical `fabrika`. Ops must select the
reviewed port's rollout sequence; deploying this tree is not a direct v0.8.1
hotfix. A v0.8.1 backport would need its own diff and checks.

## Source and privacy contract

| Source | Evidence | Limits |
| --- | --- | --- |
| `human`, `web` | Validated interactive cookie; existing CSRF checks apply | Interactive credential use, not proof of physical human presence |
| `human`, `telegram` | Existing native adapter after sender resolution; immutable metadata written with the message | Requires the existing installation/binding; RUSL-2675 owns connection |
| `agent` | Existing validated task token, bound agent/task/workspace | `via=multika` only for the configured Multika agent; owner is not relabelled as the operator |
| `service` | Existing cloud-node credential or explicitly registered dedicated machine PAT | Member identity is retained; shared owner PATs must not be registered |
| `unknown` | Missing/ambiguous provenance, legacy row, unregistered PAT, bearer session | Never infer web from `channel_ingested=false`, user-agent, service text templates or owner identity |

`X-Client-Platform: cli` is only a client claim (`route_evidence=client`). It does
not prove a manual CLI action and cannot promote a service or unknown source to
human. Auth still strips forged agent/task/actor headers. No labels change RBAC,
run attribution, invocation, signatures, quarantine, or account access.

Payload fields are bounded kinds/status/method, existing UUID references, source,
route and route evidence. There are no message bodies, titles, answers, URLs,
query strings, cookies, tokens, token hashes, emails or device fingerprints.
Collectors do not read chat content to classify it. The aggregate report selects
only identifiers, timestamps, state and metadata. It never returns message IDs
or personal transcripts.

## Collection semantics

Set `FABRIKA_JOURNEY_AUDIT=true` only after reviewed backend/frontend deployment.
Default is off. Optional private server configuration
`FABRIKA_AUDIT_SERVICE_PAT_HASHES` contains comma-separated SHA-256 fingerprints
of **already authorized dedicated machine PATs**. Ops inventories producers first;
do not add shared owner credentials, mint new credentials, or copy owner sessions.
Changing this mapping is a provenance configuration change, not a grant.

- An issue surface mount in web/shared desktop views sends one opaque random ID.
  List, table, board, empty results and project surfaces all count; data refreshes
  and StrictMode effect replay reuse the ID. API reads alone are not page opens.
- Issue API requests have a durable `pending` observation before execution and
  `complete`/`rejected` after execution. A crash or failed completion keeps
  `pending`. Pre-record failure returns 503 before the mutation. The counts are
  **requests**, not a one-to-one count of semantic issue edits.
- Direct and native channel message metadata share the message transaction.
  Native retries keep existing dedup semantics. Legacy/mixed input batches remain
  unknown when Multika answers; execution credentials do not replace the sender.
- §7 metadata shares the conditional answer transaction. Replays, rejected
  answers, defaults and red confirmation prompts do not increment explicit answers.
  Telegram callbacks are attributed only when the existing native user binding
  resolves their sender; missing attribution remains unknown.
- Supervisor ticks emit workspace collector heartbeats. Gaps over 90 seconds
  invalidate continuity. These attest to the collector, not to browser coverage.

The page signal is best effort. An old/offline/uninstrumented client or a failed
page signal can leave an unobserved open. Consequently **zero is not universal
proof of no board use**. Ops must attest the deployed client versions, all entry
surfaces used by the participant, and failures in the observation window. The
report never claims final acceptance from aggregates alone. A human issue API
read without a page signal also prevents a candidate: it may be a preload or a
lost opening event. Invisible telemetry
does not change page rendering or gate navigation.

## Report and verdict

Authenticated `GET /api/streams/audit/report?from=<RFC3339>&to=<RFC3339>` uses the
selected workspace, the authenticated account and configured Multika agent.
`from` is inclusive, `to` exclusive; maximum 31 days, 10-second request budget.
There is no account selector. Existing complete-project visibility and human
credential gates protect the endpoint. An inaccessible private project denies the
report rather than leaking a partial private summary.

Optional `project_id=<UUID>` filters project-specific metadata. Unknown/unscoped
requests remain in `unscoped_events`; permanent-chat/turn/session counts are
explicitly **account-wide**, since one chat spans projects. A project report
cannot certify the account-wide criterion. The caller cannot select another
workspace/account through the report query. The selected workspace must pass
normal membership checks; a project must belong to that workspace when recording
an open. No arbitrary event source or user identity is accepted from the client.

Example inside the authenticated app's developer console (replace workspace ID;
do not extract cookies/tokens):

```js
await fetch('/api/streams/audit/report?' + new URLSearchParams({
  from: '2026-11-16T12:00:00+03:00',
  to: '2026-11-23T12:00:00+03:00',
}), { headers: { 'X-Workspace-ID': '<own-workspace-uuid>' }, credentials: 'include' })
  .then(r => { if (!r.ok) throw new Error(`report HTTP ${r.status}`); return r.json(); });
```

Counters distinguish user-role messages from actual human-source messages;
Multika turns use the existing `multika_turn`, and active session count uses
the account's own sessions. Unknown member activities/comments and unanswered
provenance are reported independently, even if they occurred after the first
collector heartbeat. They are not automatically zeroed by a deployment date.
No pre-deployment window can retroactively prove browser coverage.

Verdicts:

- `criterion_failed`: a recorded human issue-surface opening or direct issue API
  mutation. This establishes a violation within instrumented paths.
- `insufficient_evidence`: missing week, route, answers, continuity, source, or
  single-session evidence. Unknowns and pending operations prevent a candidate.
- `ready_for_acceptance`: at least seven exact days; continuous collector; one
  active Multika session; human web and Telegram messages; explicit human §7
  answer; Multika turns; no recorded manual issue mutation/open or unknown.
  Mika must additionally verify client coverage and native Telegram delivery.

## Ops delivery, observation and rollback

No production change is authorized by running the tests. Deploy separately after
independent DSH review. Before deploying, record the exact current backend/frontend
image **digests**, flag values, private config references and backup location in
the Ops issue. Verify the backup by restoration under the existing rollout
procedure. Do not substitute historical `fabrika` images for the current release.

Deploy the reviewed backend/frontend candidate with the flag off. Check health,
existing chat/source gates, migration count (unchanged), and instrumentation in
the deployed clients. Enable the collector, inspect metadata-only synthetic
fixtures in an isolated scope, and check a bounded series of heartbeat intervals.
Do not create a message as Ruslan. A real-path test must use an explicitly marked
test source and authorized participant. Native Telegram smoke and live buttons
wait for RUSL-2675; no second bot/bridge is created by this change.

Rollback is immediately `FABRIKA_JOURNEY_AUDIT=false` using the deployment's
existing configuration mechanism. This stops collection and request pre-writes;
existing records are retained. If required, restore the **captured** previous
backend/frontend digests through the existing rollout procedure. No database
down-migration or deletion is needed. Invalidated/rolled-back windows are
insufficient evidence, not zero-activity weeks.

After RUSL-2675 and successful native smoke, Ops hands Mika one start timestamp
in МСК, exact reviewed/deployed refs, client/collector coverage evidence and the
end timestamp exactly seven days later. Mika owns RUSL-2738/RUSL-2560 and the
final verdict. Use one finite issue continuation; no permanent polling job.

## Reproduce checks

Run heavy checks only in the authorized cluster via kjob:

```sh
kjob run --cpu 4 --mem 8Gi --image go --timeout 20m \
  --artifact audit-artifacts -- bash scripts/check-journey-audit.sh
kjob run --cpu 4 --mem 8Gi --disk 8Gi --image node --timeout 20m \
  --artifact audit-artifacts -- bash scripts/check-journey-audit-ui.sh
```

The Go script provisions a disposable local PostgreSQL inside the job, applies
existing migrations, and runs targeted source/window/isolation/ask/channel tests
and server/CLI builds. Database fixture strings are explicitly synthetic
RUSL-3231; no production data, bot credentials or model invocation is used.
The UI script runs the surface hook regressions and core/views type checks.
