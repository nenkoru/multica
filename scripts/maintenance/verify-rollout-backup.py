#!/usr/bin/env python3
"""Create and restore one fresh backup using existing cluster credentials."""
import copy
from datetime import datetime
import json
from pathlib import Path
import subprocess
import time
from zoneinfo import ZoneInfo

KUBE = ['/home/mika/.local/bin/kubectl', '--kubeconfig', '/home/mika/.kube/nenkoru-prod.yaml', '-n', 'multica']


def run(args, data=None, timeout=60):
    return subprocess.run(KUBE + args, input=None if data is None else json.dumps(data),
                          text=True, capture_output=True, check=True, timeout=timeout).stdout


def get(kind, name):
    return json.loads(run(['get', kind, name, '-o', 'json']))


def wait(kind, name, timeout):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        status = get(kind, name).get('status', {})
        if kind == 'job':
            if status.get('succeeded', 0):
                return
            if any(c.get('type') == 'Failed' and c.get('status') == 'True' for c in status.get('conditions', [])):
                raise RuntimeError(f'{name}: backup failed; inspect its protected cluster logs')
        else:
            if status.get('phase') == 'Succeeded':
                return
            if status.get('phase') == 'Failed':
                raise RuntimeError(f'{name}: restore failed; inspect its protected cluster logs')
        time.sleep(5)
    raise TimeoutError(f'{name}: verification deadline exceeded')


stamp = datetime.now(ZoneInfo('Europe/Moscow')).strftime('%Y%m%d%H%M%S')
job_name = 'multica-backup-2760-' + stamp
pod_name = 'restore-2760-' + stamp
database = 'restore_2760_' + stamp
dump_name = 'rusl2760-' + stamp + '.dump'
cron = get('cronjob', 'multica-backup')
job_spec = copy.deepcopy(cron['spec']['jobTemplate']['spec'])
job_spec.update(backoffLimit=0, activeDeadlineSeconds=600)
pod_spec = job_spec['template']['spec']
pod_spec['restartPolicy'] = 'Never'
container = pod_spec['containers'][0]
container['args'] = [f'''set -euo pipefail
umask 077
pg_dump --format=custom --file=/backups/{dump_name}.part
pg_restore --list /backups/{dump_name}.part >/dev/null
mv /backups/{dump_name}.part /backups/{dump_name}
cd /backups
sha256sum {dump_name} > {dump_name}.sha256
echo DB-DUMP-READY
''']
container['resources'] = {'requests': {'cpu': '250m', 'memory': '256Mi'}, 'limits': {'cpu': '2', 'memory': '2Gi'}}
container['volumeMounts'] = [m for m in container['volumeMounts'] if m['name'] == 'backups']
pod_spec['volumes'] = [v for v in pod_spec['volumes'] if v['name'] == 'backups']
run(['create', '-f', '-'], {'apiVersion': 'batch/v1', 'kind': 'Job', 'metadata': {'name': job_name}, 'spec': job_spec})
wait('job', job_name, 630)

restore = {k: copy.deepcopy(container[k]) for k in ['image', 'env', 'envFrom'] if k in container}
restore.update(name='restore', command=['bash', '-c'], resources=container['resources'],
               volumeMounts=[{'name': 'backups', 'mountPath': '/backups', 'readOnly': True}])
restore['args'] = [f'''set -euo pipefail
cd /backups
sha256sum -c {dump_name}.sha256 >/dev/null
psql -d postgres -v ON_ERROR_STOP=1 -qc "CREATE DATABASE {database}"
trap 'psql -d postgres -v ON_ERROR_STOP=1 -qc "DROP DATABASE IF EXISTS {database}"' EXIT
if ! pg_restore --exit-on-error --no-owner --no-privileges -d {database} /backups/{dump_name} > /tmp/restore-details.log 2>&1; then
  echo RESTORE-FAILED
  exit 1
fi
psql -d {database} -Atc "select 'epics',count(*) from epic union all select 'sprints',count(*) from sprint union all select 'issues',count(*) from issue union all select 'streams',count(*) from stream union all select 'migrations',count(*) from schema_migrations"
echo RESTORE-VERIFIED
''']
spec = {'restartPolicy': 'Never', 'activeDeadlineSeconds': 600, 'automountServiceAccountToken': False,
        'containers': [restore], 'volumes': [{'name': 'backups', 'persistentVolumeClaim': {'claimName': 'multica-backups'}}]}
run(['create', '-f', '-'], {'apiVersion': 'v1', 'kind': 'Pod', 'metadata': {'name': pod_name}, 'spec': spec})
wait('pod', pod_name, 630)
summary = run(['logs', pod_name, '--tail=10'])
if 'RESTORE-VERIFIED' not in summary:
    raise RuntimeError('Restore did not produce its verification marker')
result = {'verified': True, 'dump': '/backups/' + dump_name, 'job': job_name, 'restore_pod': pod_name,
          'verified_at_msk': datetime.now(ZoneInfo('Europe/Moscow')).strftime('%Y-%m-%d %H:%M:%S МСК'),
          'summary': summary.strip()}
Path(__file__).with_name('verified-backup.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
print(json.dumps(result, ensure_ascii=False))
