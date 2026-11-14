#!/bin/sh
set -u
corepack enable
pnpm install --frozen-lockfile --ignore-scripts --filter @multica/views... --filter multica || exit
mkdir -p quality-artifacts
code=0
run() {
  name="$1"; shift
  "$@" > "quality-artifacts/$name.log" 2>&1
  rc=$?
  echo "$name exit=$rc" | tee -a quality-artifacts/status.txt
  if [ "$rc" != 0 ]; then tail -60 "quality-artifacts/$name.log"; code=1; fi
}
run core-tests pnpm --filter @multica/core exec vitest run streams/screens.test.ts streams/schemas.test.ts
run view-tests pnpm --filter @multica/views exec vitest run streams/genui/screen-view.test.tsx streams/genui/task-screens.test.tsx chat/chat-page.test.tsx chat/components/chat-message-list.test.tsx
run core-types pnpm --filter @multica/core typecheck
run view-types pnpm --filter @multica/views typecheck
run core-lint pnpm --filter @multica/core exec eslint streams/screens.ts streams/screens.test.ts streams/screen-props.ts streams/index.ts
run view-lint pnpm --filter @multica/views exec eslint streams/genui/screen-view.tsx streams/genui/screen-view.test.tsx streams/genui/task-screens.tsx streams/genui/task-screens.test.tsx chat/chat-page.tsx chat/components/chat-message-list.tsx
exit "$code"
