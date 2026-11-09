#!/usr/bin/env python3
"""Bootstrap the drain API through the old daemon's native claim barrier.

Run as the service owner, outside any agent task/cgroup, before the application
rollout. No service stop/restart, agent pause, database, or network changes.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import time


def command(args, timeout=15):
    return subprocess.run(args, text=True, capture_output=True, check=True, timeout=timeout).stdout


def atomic_json(path, value):
    temporary = path.with_suffix('.new')
    with temporary.open('x', encoding='utf-8') as stream:
        os.chmod(temporary, 0o600)
        json.dump(value, stream)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


def abort_marker(marker, receipt):
    if not marker.exists():
        return
    value = json.loads(marker.read_text())
    if value['token'] != receipt['token'] or value['daemon_id'] != receipt['daemon_id']:
        raise RuntimeError('maintenance marker ownership changed; retained')
    value['resume_on_start'] = True
    atomic_json(marker, value)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--installed', type=Path, required=True)
    parser.add_argument('--candidate', type=Path, required=True)
    parser.add_argument('--receipt', type=Path, required=True)
    parser.add_argument('--profile-dir', type=Path, required=True)
    parser.add_argument('--profile', default='selfhost')
    parser.add_argument('--unit', default='multica-daemon-selfhost.service')
    parser.add_argument('--timeout', type=float, default=1200)
    args = parser.parse_args()
    if os.environ.get('MULTICA_TASK_ID') or os.environ.get('MULTICA_AGENT_ID'):
        raise RuntimeError('bootstrap must run outside an agent task')
    installed = str(args.installed.resolve())
    candidate = str(args.candidate.resolve())
    cli = [candidate, '--profile', args.profile]
    health_command = [installed, '--profile', args.profile, 'daemon', 'status', '--output', 'json']
    health = json.loads(command(health_command))
    pid = health['pid']
    if health['status'] != 'running' or health['cli_version'] != 'v0.7.2-rusl2508-cli2':
        raise RuntimeError('unexpected bootstrap source version or readiness')
    main_pid = command(['systemctl', 'show', args.unit, '-p', 'MainPID', '--value']).strip()
    if str(pid) != main_pid:
        raise RuntimeError('health PID does not own the service')
    if command(['systemctl', 'show', args.unit, '-p', 'Restart', '--value']).strip() != 'always':
        raise RuntimeError('native bootstrap requires the existing Restart=always supervisor')
    process_args = Path(f'/proc/{pid}/cmdline').read_bytes().decode().split('\0')
    process_env = dict(item.split('=', 1) for item in Path(f'/proc/{pid}/environ').read_bytes().decode().split('\0') if '=' in item)
    config = json.loads((args.profile_dir / 'config.json').read_text())
    signal = process_env.get('MULTICA_DAEMON_AUTO_RELOAD', '').strip().lower()
    disabled = any(item == '--no-auto-reload' or item.startswith('--no-auto-reload=') and item != '--no-auto-reload=false' for item in process_args)
    disabled |= signal in ('false', '0', 'no', 'off') if signal else config.get('disable_auto_reload', False)
    if disabled:
        raise RuntimeError('native auto-reload is explicitly disabled; left unchanged')
    if 'v0.7.2-rusl2779-maintenance' not in command([candidate, '--version']):
        raise RuntimeError('unexpected maintenance candidate version')
    marker = args.profile_dir / 'maintenance.json'
    if marker.exists() or args.receipt.exists():
        raise RuntimeError('existing maintenance state requires explicit recovery')
    receipt = {'token': secrets.token_hex(32), 'daemon_id': health['daemon_id'], 'profile': args.profile}
    atomic_json(args.receipt, receipt)
    # Pre-seeding prevents even a short-lived native handoff child from claiming.
    atomic_json(marker, {**receipt, 'server_url': health['server_url']})
    try:
        temporary = args.installed.with_suffix('.rusl2779-new')
        shutil.copyfile(candidate, temporary)
        temporary.chmod(0o755)
        os.replace(temporary, args.installed)
        deadline = time.monotonic() + args.timeout
        while time.monotonic() < deadline:
            current = json.loads(command(health_command))
            supervisor = command(['systemctl', 'show', args.unit, '-p', 'MainPID', '--value']).strip()
            if (current.get('status') == 'running' and current.get('daemon_id') == receipt['daemon_id']
                    and current.get('cli_version') == 'v0.7.2-rusl2779-maintenance'
                    and str(current.get('pid')) == supervisor and current.get('pid') != pid):
                command(cli + ['daemon', 'maintenance', 'enter', '--token-file', str(args.receipt), '--timeout', '5m'], timeout=320)
                print(json.dumps({'bootstrap_verified': True, 'claims_held': True, 'version': current['cli_version']}))
                return
            time.sleep(2)
        raise TimeoutError('native reload deferred beyond the bootstrap deadline')
    except BaseException:
        # Do not race the old process by replacing its restart target again.
        # A late successor follows the old application code, then resumes claims.
        abort_marker(marker, receipt)
        try:
            current = json.loads(command(health_command))
            supervisor = command(['systemctl', 'show', args.unit, '-p', 'MainPID', '--value']).strip()
            if current.get('cli_version') == 'v0.7.2-rusl2779-maintenance' and str(current.get('pid')) == supervisor:
                command(cli + ['daemon', 'maintenance', 'resume', '--token-file', str(args.receipt)])
        except (subprocess.SubprocessError, ValueError, OSError):
            pass  # Owned resume_on_start is the durable recovery instruction.
        raise


if __name__ == '__main__':
    main()
