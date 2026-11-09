"""Isolated operational checks: no systemd, SSH, Kubernetes, or daemon calls."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('bootstrap', ROOT / 'bootstrap.py')
bootstrap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bootstrap)


class MaintenanceOperations(unittest.TestCase):
    def rollback(self, mutated=True, held=True, fail='', expected='rolled_back', preflight=None):
        source = (ROOT / 'rollout-rusl2760.sh').read_text()
        function = source[source.index('rollback() {'):source.index("trap 'rollback")]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            cli = root / 'cli'
            cli.write_text('''#!/usr/bin/env python3
import json,os,sys
with open(os.environ['EVENTS'],'a') as f: f.write('cli '+' '.join(sys.argv[1:])+'\\n')
if 'maintenance' in sys.argv:
    if 'enter' in sys.argv and os.environ.get('FAIL')=='drain': sys.exit(1)
    if 'resume' in sys.argv and os.environ.get('FAIL')=='resume-response':
        from pathlib import Path
        stamp=Path(os.environ['EVENTS']+'.resumed')
        if not stamp.exists(): stamp.write_text('response lost'); sys.exit(1)
    print('{"ready":true}'); sys.exit(0)
print(json.dumps({'status':'stopped' if os.environ.get('FAIL')=='primary-ready' else 'running','cli_version':'v0.7.2-rusl2779-maintenance',
'pending_terminal_report_count':0,'pending_terminal_report_bytes':0,
'failed_terminal_report_count':0,'failed_terminal_report_bytes':0}))
''')
            cli.chmod(0o700)
            (root/'maintenance-receipt.json').write_text('{}')
            env = {**os.environ, 'EVENTS':str(root/'events'), 'PHASE':str(root/'phase'),
                   'MOCK_CLI':str(cli), 'FAIL':fail, 'MUTATED':str(int(mutated)), 'HELD':str(int(held)), 'RELEASE_DIR':str(root)}
            harness = '''set -Eeuo pipefail
rusl_mutated=$MUTATED
rusl_maintenance=$HELD
rusl_standby_promoted=0
rusl_bin=$MOCK_CLI
rusl_old_bin=old-cli
rusl_receipt=receipt
rusl_unit=primary
RUSL_OLD_BACKEND=old-backend
RUSL_NEW_BACKEND=new-backend
RUSL_OLD_FRONTEND=old-frontend
RUSL_NEW_FRONTEND=new-frontend
RUSL_NEW_CLI_SHA256=new-hash
RUSL_MAINT_CLI_SHA256=old-hash
event() { echo "$*" >> "$EVENTS"; }
phase() { event "phase $1"; echo "$1" > "$PHASE"; }
restore_watch() { event restore_watch; }
standby() { event "standby $*"; }
sudo() { event "sudo $*"; }
install() { event "install $*"; }
mv() { event "mv $*"; }
sleep() { :; }
sha256sum() { echo 'new-hash  cli'; }
kube() {
  event "kube $*"
  if [[ "$1" == get ]]; then
    if [[ "$3" == multica-backend ]]; then echo new-backend; else echo new-frontend; fi
  fi
  if [[ "$1 $2 $3" == 'rollout status deployment/multica-backend' && "$FAIL" == old-ready ]]; then return 1; fi
}
'''+function
            if preflight is not None:
                start = source.index('rusl_mutated=0')
                end = source.index('\nkube() {',start)
                init = source[start:end]
                start = source.index('phase preflight\n')
                end = source.index('python3 - ',start)
                mode = preflight
                extra = ''
                if preflight == 'missing-variable':
                    mode = ''
                    (root/'release.env').write_text('')
                    (root/'qa.json').write_text(json.dumps(dict.fromkeys(['go_verified','db_rehearsal_verified','review_verified','views_failures_resolved','frontend_image_verified','backend_image_verified','maintenance_review_verified'],True)))
                    extra = '\nrusl_candidate=candidate\nunset RUSL_NEW_CLI_SHA256\n'
                    end = source.index('[[ "$(sha256sum "$rusl_bin"',start)
                harness += '\nrusl_release_dir=$RELEASE_DIR\nrusl_check_only='+mode+'\n'+init+extra
                harness += '\n'+source[source.index("trap 'rollback"):source.index('phase preflight\n')]+source[start:end]
            elif fail == 'resume-response':
                start = source.rindex('rusl_maintenance=0')
                end = source.index('phase observing',start)
                harness += "\ntrap 'rollback 1' ERR\nrusl_maintenance=1\n"+source[start:end]
            else:
                harness += '\nrollback 1\n'
            result = subprocess.run(['bash'],input=harness,text=True,capture_output=True,env=env,timeout=10)
            self.assertEqual(result.returncode,1,result.stderr)
            self.assertEqual((root/'phase').read_text().strip(),expected,result.stderr)
            return (root/'events').read_text().splitlines()

    def test_old_backend_ready_precedes_primary_restart(self):
        events = self.rollback()
        ready = next(i for i,v in enumerate(events) if 'rollout status deployment/multica-backend' in v)
        restart = next(i for i,v in enumerate(events) if 'systemctl restart' in v)
        old_status = next(i for i,v in enumerate(events) if 'daemon status' in v)
        resume = next(i for i,v in enumerate(events) if 'maintenance resume' in v)
        self.assertLess(ready,restart)
        self.assertLess(restart,old_status)
        self.assertLess(old_status,resume)

    def test_failed_old_readiness_never_restarts_primary(self):
        events = self.rollback(fail='old-ready',expected='rollback_failed')
        self.assertFalse(any('systemctl restart' in v or 'maintenance resume' in v for v in events))

    def test_error_before_deploy_restores_admission(self):
        events = self.rollback(mutated=False,expected='aborted_before_deployment')
        self.assertTrue(any('maintenance resume' in v for v in events))
        self.assertFalse(any('set image' in v or 'systemctl' in v for v in events))

    def test_failed_old_primary_readiness_retains_barrier(self):
        events = self.rollback(fail='primary-ready',expected='rollback_failed')
        self.assertTrue(any('systemctl restart' in v for v in events))
        self.assertFalse(any('maintenance resume' in v for v in events))

    def test_observation_failure_drains_before_rollback(self):
        events = self.rollback(held=False)
        enter = next(i for i,v in enumerate(events) if 'maintenance enter' in v)
        stop = next(i for i,v in enumerate(events) if 'maintenance stop' in v)
        service_stop = next(i for i,v in enumerate(events) if 'systemctl stop' in v)
        image = next(i for i,v in enumerate(events) if 'set image' in v)
        self.assertLess(enter,stop)
        self.assertLess(stop,service_stop)
        self.assertLess(service_stop,image)

    def test_busy_rollback_defers_without_stopping_tasks(self):
        events = self.rollback(held=False,fail='drain',expected='rollback_failed_could_not_drain')
        self.assertFalse(any('systemctl' in v or 'set image' in v for v in events))

    def test_lost_resume_response_requires_another_drain(self):
        events = self.rollback(fail='resume-response')
        released = next(i for i,v in enumerate(events) if 'maintenance resume' in v)
        enter = next(i for i,v in enumerate(events) if 'maintenance enter' in v)
        stop = next(i for i,v in enumerate(events) if 'systemctl stop' in v)
        self.assertLess(released,enter)
        self.assertLess(enter,stop)

    def test_bootstrap_gate_resumes_when_release_config_is_missing(self):
        events = self.rollback(expected='aborted_before_deployment',preflight='')
        self.assertTrue(any('maintenance resume' in v for v in events))
        self.assertFalse(any('systemctl' in v or 'set image' in v for v in events))

    def test_check_failure_never_resumes_existing_bootstrap_gate(self):
        events = self.rollback(expected='aborted_before_deployment',preflight='--check')
        self.assertFalse(any('maintenance resume' in v or 'systemctl' in v or 'set image' in v for v in events))

    def test_unset_release_variable_also_restores_bootstrap_gate(self):
        events = self.rollback(expected='aborted_before_deployment',preflight='missing-variable')
        self.assertTrue(any('maintenance resume' in v for v in events))
        self.assertFalse(any('systemctl' in v or 'set image' in v for v in events))

    def test_bootstrap_abort_is_owned_and_durable(self):
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary)/'maintenance.json'
            receipt = {'token':'a'*64,'daemon_id':'daemon'}
            bootstrap.atomic_json(marker,{**receipt,'server_url':'https://example.test'})
            bootstrap.abort_marker(marker,receipt)
            self.assertTrue(json.loads(marker.read_text())['resume_on_start'])
            with self.assertRaises(RuntimeError):
                bootstrap.abort_marker(marker,{**receipt,'token':'b'*64})
            self.assertEqual(marker.stat().st_mode & 0o777,0o600)


if __name__ == '__main__':
    unittest.main()
