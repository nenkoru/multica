#!/usr/bin/env bash
set -u
corepack enable
pnpm install --frozen-lockfile > planning-install.log 2>&1 || exit $?
failed=0
for check in typecheck lint test; do
  if [ "$check" = test ]; then
    pnpm exec turbo test --filter=!@multica/mobile --concurrency=2 -- --maxWorkers=2 > "planning-${check}.log" 2>&1
  else
    pnpm exec turbo "$check" --filter=!@multica/mobile --concurrency=2 > "planning-${check}.log" 2>&1
  fi
  code=$?
  echo "$check: $code"
  tail -n 55 "planning-${check}.log"
  if [ "$code" -ne 0 ]; then failed=1; fi
done
for target in web desktop; do
  pnpm --filter "@multica/$target" build > "planning-${target}-build.log" 2>&1
  code=$?
  echo "$target build: $code"
  tail -n 35 "planning-${target}-build.log"
  if [ "$code" -ne 0 ]; then failed=1; fi
done
exit "$failed"
