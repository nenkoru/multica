#!/bin/sh
set -eu
corepack enable
pnpm install --frozen-lockfile --ignore-scripts --filter @multica/views... --filter multica
node scripts/genui-browser/serve.mjs
