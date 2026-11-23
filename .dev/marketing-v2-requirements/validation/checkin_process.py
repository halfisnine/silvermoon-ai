#!/usr/bin/env python3
"""Exercise checkin entry points on two actual gateways and isolated PG/Redis."""
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
ARTIFACTS = ROOT / 'build/dev/marketing-v2-validation'
COMPOSE = ['docker', 'compose', '-f', str(ROOT / '.dev/marketing-v2-requirements/validation/compose.yml')]
IMAGE = json.loads((ROOT / '.dev/toolchains.lock.json').read_text())['go']['domestic']


def run(args, **kwargs):
    subprocess.run([str(a) for a in args], cwd=ROOT, check=True, **kwargs)


def controller_matrix():
    """Existing controller fixtures require actual loopback database servers."""
    stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
    database = 'marketing_v2_checkin_controller'
    log_database = database + '_log'
    digest = hashlib.sha256((ARTIFACTS / 'checkin-controller.test').read_bytes()).hexdigest()
    failures = []
    common_filter = '^Test(MarketingCheckinHTTP|SecurityLoginCodeCompletesOnce|SecurityLoginRejectsChangedOrExpiredAuthorization)$'
    for engine in ('sqlite', 'mysql', 'postgres'):
        network = ['--network', 'none']
        env = ['--env', 'GOMAXPROCS=2', '--env', f'TEST_TASK_DB_DIALECT={engine}', '--env', f'TEST_SECURITY_DIALECT={engine}']
        if engine != 'sqlite':
            container = subprocess.check_output(COMPOSE + ['ps', '-q', engine], cwd=ROOT, text=True).strip()
            if not container or '\n' in container:
                raise SystemExit(f'Expected one isolated {engine} container')
            network = ['--network', f'container:{container}']
            if engine == 'mysql':
                for name in (database, log_database):
                    run(COMPOSE + ['exec', '-T', 'mysql', 'mysql', '-uroot', '-plocal-root-test-only', '-e', f'DROP DATABASE IF EXISTS {name}; CREATE DATABASE {name} CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'])
                env += ['--env', f'TEST_MYSQL_DSN=root:local-root-test-only@tcp(127.0.0.1:3306)/{database}?charset=utf8mb4&parseTime=true&loc=UTC', '--env', f'TEST_MYSQL_LOG_DSN=root:local-root-test-only@tcp(127.0.0.1:3306)/{log_database}?charset=utf8mb4&parseTime=true&loc=UTC']
            else:
                for name in (database, log_database):
                    run(COMPOSE + ['exec', '-T', 'postgres', 'psql', '-U', 'marketing_test', '-d', 'marketing_test', '-v', 'ON_ERROR_STOP=1', '-c', f'DROP DATABASE IF EXISTS {name}', '-c', f'CREATE DATABASE {name}'])
                env += ['--env', f'TEST_POSTGRES_DSN=postgres://marketing_test:local-marketing-test-only@127.0.0.1:5432/{database}?sslmode=disable', '--env', f'TEST_POSTGRES_LOG_DSN=postgres://marketing_test:local-marketing-test-only@127.0.0.1:5432/{log_database}?sslmode=disable']
        for group, test_filter in [('checkin-auth', common_filter)]:
            log = ARTIFACTS / f'checkin-controller-{engine}-{group}-{stamp}.log'
            command = ['docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g', *network, '--workdir', '/tmp', '--mount', f'type=bind,source={ARTIFACTS},target=/fixtures', *env, '--entrypoint', '/fixtures/checkin-controller.test', IMAGE, f'-test.run={test_filter}', '-test.v', '-test.count=1', '-test.timeout=240s']
            result = subprocess.run(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            log.write_text(f'CHECKIN_CONTROLLER_BINARY_SHA256 {digest}\nCHECKIN_CONTROLLER_FILTER {test_filter}\n' + result.stdout)
            print(f'CHECKIN_CONTROLLER_EVIDENCE engine={engine} group={group} exit={result.returncode} log={log.relative_to(ROOT)}', flush=True)
            # Existing audit fixtures create per-case databases. Clean only the
            # exact names this invocation reported, never a wildcard namespace.
            created = sorted(set(re.findall(r'isolated database: (newapi_audit_[0-9]+) \(', result.stdout)))
            for name in created:
                if engine == 'mysql':
                    run(COMPOSE + ['exec', '-T', 'mysql', 'mysql', '-uroot', '-plocal-root-test-only', '-e', f'DROP DATABASE {name}'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                elif engine == 'postgres':
                    run(COMPOSE + ['exec', '-T', 'postgres', 'psql', '-U', 'marketing_test', '-d', 'marketing_test', '-v', 'ON_ERROR_STOP=1', '-c', f'DROP DATABASE {name}'], stdout=subprocess.DEVNULL)
            if result.returncode != 0 or '--- SKIP:' in result.stdout or 'warning: no tests to run' in result.stdout or '\nPASS\n' not in result.stdout:
                failures.append(str(log.relative_to(ROOT)))
                for line in result.stdout.splitlines():
                    if line.startswith(('--- FAIL:', '--- SKIP:', 'panic:', 'FAIL')):
                        print(line)
    if failures:
        raise SystemExit('Controller matrix failed: ' + ', '.join(failures))
    print('CHECKIN_CONTROLLER_ALL_OK engines=SQLite,MySQL,PostgreSQL skipped=0 separate_log_databases=true')


if len(sys.argv) != 2 or sys.argv[1] not in ('build', 'run', 'all', 'controller', 'controller-build', 'controller-run'):
    raise SystemExit('usage: checkin_process.py build|run|all|controller|controller-build|controller-run')
ARTIFACTS.mkdir(parents=True, exist_ok=True)
if sys.argv[1] in ('controller', 'controller-build'):
    run([ROOT / '.dev/run', 'go', 'test', '-c', '-o', '/artifacts/marketing-v2-validation/checkin-controller.test', './controller'])
if sys.argv[1] in ('controller', 'controller-run'):
    controller_matrix()
if sys.argv[1] in ('build', 'all'):
    run([ROOT / '.dev/run', 'go', 'build', '-o', '/artifacts/marketing-v2-validation/checkin-gateway', '.'])
    run([ROOT / '.dev/run', 'go', 'build', '-o', '/artifacts/marketing-v2-validation/checkin-process', './.dev/marketing-v2-requirements/validation/checkin_process'])
if sys.argv[1] in ('run', 'all'):
    stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
    log = ARTIFACTS / f'checkin-process-{stamp}.log'
    run(COMPOSE + ['exec', '-T', 'postgres', 'psql', '-U', 'marketing_test', '-d', 'marketing_test', '-v', 'ON_ERROR_STOP=1', '-c', 'DROP DATABASE IF EXISTS marketing_v2_checkin_process', '-c', 'CREATE DATABASE marketing_v2_checkin_process'])
    with log.open('w') as output:
        for name in ('checkin-gateway', 'checkin-process'):
            digest = hashlib.sha256((ARTIFACTS / name).read_bytes()).hexdigest()
            output.write(f'CHECKIN_BINARY_SHA256 {name} {digest}\n')
        output.flush()
        try:
            run(['docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g', '--network', 'silvermoon-marketing-v2_default', '--workdir', '/tmp', '--mount', f'type=bind,source={ARTIFACTS},target=/fixtures', '--env', 'GOMAXPROCS=2', '--env', 'SQL_DSN=postgres://marketing_test:local-marketing-test-only@postgres:5432/marketing_v2_checkin_process?sslmode=disable', '--env', 'REDIS_CONN_STRING=redis://redis:6379/9', '--env', f'CHECKIN_FIXTURE_STAMP={stamp}', '--entrypoint', '/fixtures/checkin-process', IMAGE], stdout=output, stderr=subprocess.STDOUT)
        finally:
            print(f'CHECKIN_EVIDENCE {log.relative_to(ROOT)}')
    for line in log.read_text().splitlines():
        if line.startswith('CHECKIN_'):
            print(line)
