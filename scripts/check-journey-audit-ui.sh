#!/usr/bin/env bash
set -euo pipefail
mkdir -p audit-artifacts
corepack enable
pnpm config set store-dir /cache/pnpm-store
pnpm install --frozen-lockfile --ignore-scripts > audit-artifacts/ui-install.log 2>&1
pnpm --filter @multica/core exec vitest run streams/use-issue-surface-audit.test.tsx > audit-artifacts/ui-tests.log 2>&1
pnpm --filter @multica/core typecheck > audit-artifacts/core-typecheck.log 2>&1
pnpm --filter @multica/views typecheck > audit-artifacts/views-typecheck.log 2>&1
pnpm --filter @multica/core exec eslint streams/use-issue-surface-audit.ts streams/use-issue-surface-audit.test.tsx > audit-artifacts/ui-lint.log 2>&1
echo AUDIT_UI_PASS
