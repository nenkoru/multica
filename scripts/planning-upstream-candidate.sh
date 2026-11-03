#!/usr/bin/env bash
# Run only in a disposable, clean checkout of the custom fork.
# A conflict deliberately leaves the candidate unmerged for inspection.
set -euo pipefail
upstream_ref=${1:-refs/remotes/upstream/main}
if [ -n "$(git status --porcelain)" ]; then
  echo 'Refusing to merge into a dirty checkout.' >&2
  exit 2
fi
git rev-parse --verify "$upstream_ref" >/dev/null
python3 - "$upstream_ref" <<'PY'
import subprocess, sys
from pathlib import PurePosixPath
def migrations(ref):
    names = subprocess.check_output(['git', 'ls-tree', '-r', '--name-only', ref, 'server/migrations'], text=True).splitlines()
    return {PurePosixPath(n).name.split('_', 1)[0]: PurePosixPath(n).name for n in names if n.endswith('.up.sql')}
current, upstream = migrations('HEAD'), migrations(sys.argv[1])
collisions = [f'{n}: {current[n]} / {upstream[n]}' for n in current.keys() & upstream.keys() if current[n] != upstream[n]]
if collisions:
    print('Migration numbering collision; a reviewed port is required:\n' + '\n'.join(sorted(collisions)), file=sys.stderr)
    sys.exit(3)
PY
git merge --no-ff --no-edit "$upstream_ref"
git diff --check HEAD~1 HEAD
git rev-parse HEAD
