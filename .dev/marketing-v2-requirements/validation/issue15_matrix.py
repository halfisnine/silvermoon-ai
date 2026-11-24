#!/usr/bin/env python3
"""Serial MV2-015 verification; explicit phases, immutable logs and source binding.

Usage: issue15_matrix.py recovery migration processes accounting backend frontend capacity
Only the existing isolated silvermoon-marketing-v2 stack is used. No deployment.
Run selected phases to resume; this script never silently skips a previous fail.
"""
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent
ARTIFACTS = ROOT / 'build/dev'
STAMP = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
EVIDENCE = ARTIFACTS / ('mv2-015-' + STAMP)
IMAGE = json.loads((ROOT / '.dev/toolchains.lock.json').read_text())['go']['domestic']
COMPOSE = ['docker', 'compose', '-f', str(HERE / 'compose.yml')]


def sources():
    names = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'], cwd=ROOT).decode().split('\0')
    return {name: hashlib.sha256((ROOT / name).read_bytes()).hexdigest()
            for name in sorted(set(names)) if name and (ROOT / name).is_file()
            and not name.startswith('.dev/marketing-v2-requirements/reviews/')}


def command(label, args, require_tests=False):
    log = EVIDENCE / (label + '.log')
    print(f'BEGIN {label}', flush=True)
    with log.open('w') as output:
        output.write('COMMAND ' + json.dumps([str(a) for a in args]) + '\n')
        output.flush()
        result = subprocess.run([str(a) for a in args], cwd=ROOT, stdout=output, stderr=subprocess.STDOUT)
    content = log.read_text()
    skipped = '--- SKIP:' in content or 'warning: no tests to run' in content
    passed = result.returncode == 0 and (not require_tests or ('--- PASS:' in content and not skipped))
    manifest['checks'].append({'label': label, 'exit': result.returncode, 'pass': passed,
                              'required_tests': require_tests, 'skipped_marker': skipped,
                              'log': str(log.relative_to(ROOT)), 'sha256': hashlib.sha256(log.read_bytes()).hexdigest()})
    persist()
    print(f'END {label} pass={passed} log={log.relative_to(ROOT)}', flush=True)
    if not passed:
        print('\n'.join(content.splitlines()[-25:]), flush=True)
        raise SystemExit(1)
    return content


def persist():
    (EVIDENCE / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')


def recovery():
    command('recovery-build', [ROOT / '.dev/run', 'go', 'test', '-c', '-o', '/artifacts/mv2-015-controller.test', './controller'])
    manifest['binaries']['controller'] = hashlib.sha256((ARTIFACTS / 'mv2-015-controller.test').read_bytes()).hexdigest()
    for engine in ('sqlite', 'mysql', 'postgres'):
        env = ['--env', f'TEST_SECURITY_DIALECT={engine}']
        network = 'none' if engine == 'sqlite' else f'container:silvermoon-marketing-v2-{engine}-1'
        if engine == 'mysql':
            env += ['--env', 'TEST_MYSQL_DSN=root:local-root-test-only@tcp(127.0.0.1:3306)/marketing_test?charset=utf8mb4&parseTime=true&loc=UTC']
        elif engine == 'postgres':
            env += ['--env', 'TEST_POSTGRES_DSN=postgres://marketing_test:local-marketing-test-only@127.0.0.1:5432/marketing_test?sslmode=disable']
        content = command('recovery-' + engine, ['docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g', '--network', network,
                      '--mount', f'type=bind,source={ARTIFACTS},target=/artifacts', '--env', 'GOMAXPROCS=2', *env,
                      '--entrypoint', '/artifacts/mv2-015-controller.test', IMAGE, '-test.run=^TestMarketingScheduledRewardRecovery$', '-test.v', '-test.count=1', '-test.timeout=180s'], True)
        # Only the random fixture databases reported by this invocation.
        for name in sorted(set(re.findall(r'isolated database: (newapi_audit_[0-9]+) \(', content))):
            if engine == 'postgres':
                args = COMPOSE + ['exec', '-T', 'postgres', 'psql', '-U', 'marketing_test', '-d', 'marketing_test', '-v', 'ON_ERROR_STOP=1', '-c', f'DROP DATABASE {name}']
            elif engine == 'mysql':
                args = COMPOSE + ['exec', '-T', 'mysql', 'mysql', '-uroot', '-plocal-root-test-only', '-e', f'DROP DATABASE {name}']
            else:
                continue
            subprocess.run(args, cwd=ROOT, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def migration():
    command('migration', ['python3', HERE / 'migration_matrix.py', 'all'])


def processes():
    for name in ('funding', 'gateway', 'activity', 'lottery', 'payment', 'registration', 'checkin', 'refund'):
        command(name + '-process', ['python3', HERE / (name + '_process.py'), 'all'])


def accounting():
    command('accounting', ['python3', HERE / 'accounting_matrix.py', 'all'])
    command('accounting-reads-and-logs', ['python3', HERE / 'accounting_read_matrix.py'])
    for name in ('payment', 'registration', 'checkin', 'refund'):
        command(name + '-controller', ['python3', HERE / (name + '_process.py'), 'controller'])
    command('admin-build', [ROOT / '.dev/run', 'go', 'test', '-c', '-o', '/artifacts/marketing-v2-012-controller.test', './controller'])
    command('admin-http', ['python3', HERE / 'admin_http_matrix.py'])


def backend():
    command('go-vet', [ROOT / '.dev/run', 'go', 'vet', './...'])
    command('go-tests', [ROOT / '.dev/run', 'go', 'test', './...', '-count=1'])
    for module in ('relaykit', 'tokenkit'):
        command(module + '-build', [ROOT / '.dev/run', 'go', '-C', module, 'build', './...'])
        command(module + '-tests', [ROOT / '.dev/run', 'go', '-C', module, 'test', './...', '-count=1'])


def frontend():
    command('web-typecheck', [ROOT / '.dev/run', 'bun', 'run', 'typecheck'])
    command('web-tests', [ROOT / '.dev/run', 'bun', 'run', 'test', '--maxWorkers=1'])
    command('web-build', [ROOT / '.dev/run', 'bun', 'run', 'build'])


def capacity():
    command('gateway-build', ['python3', HERE / 'gateway_process.py', 'build'])
    command('capacity', ['python3', HERE / 'gateway_process.py', 'capacity'])


if __name__ == '__main__':
    phases = {fn.__name__: fn for fn in (recovery, migration, processes, accounting, backend, frontend, capacity)}
    if len(sys.argv) < 2 or any(name not in phases for name in sys.argv[1:]):
        raise SystemExit(__doc__)
    EVIDENCE.mkdir(parents=True)
    manifest = {'head': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT).decode().strip(),
                'started_at': STAMP, 'phases': sys.argv[1:], 'sources': sources(), 'binaries': {}, 'checks': [], 'complete': False}
    persist()
    try:
        for name in sys.argv[1:]:
            phases[name]()
        if sources() != manifest['sources']:
            raise SystemExit('Source drift during validation; inspect evidence before reuse')
        manifest['complete'] = True
    finally:
        manifest['finished_at'] = datetime.now(timezone.utc).isoformat()
        persist()
        print('MANIFEST ' + str((EVIDENCE / 'manifest.json').relative_to(ROOT)), flush=True)
