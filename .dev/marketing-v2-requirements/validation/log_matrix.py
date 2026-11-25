#!/usr/bin/env python3
"""Exercise real independent log stores without exposing ports or production data."""
import json
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
ARTIFACTS = ROOT / 'build/dev'
IMAGE = json.loads((ROOT / '.dev/toolchains.lock.json').read_text())['go']['domestic']


def build():
    subprocess.run([str(ROOT / '.dev/run'), 'go', 'test', '-c', '-o',
                    '/artifacts/marketing-v2-controller.test', './controller'], cwd=ROOT, check=True)


def run():
    fixtures = {
        'sqlite': None,
        'mysql': 'AUDIT_MYSQL_DSN=root:local-root-test-only@tcp(127.0.0.1:3306)/marketing_test?charset=utf8mb4&parseTime=true&loc=UTC',
        'postgres': 'AUDIT_POSTGRES_DSN=postgres://marketing_test:local-marketing-test-only@127.0.0.1:5432/marketing_test?sslmode=disable',
        'clickhouse': 'AUDIT_CLICKHOUSE_DSN=clickhouse://marketing_test:local-marketing-test-only@127.0.0.1:9000/default',
    }
    for engine, environment in fixtures.items():
        command = ['docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g',
                   '--mount', f'type=bind,source={ARTIFACTS},target=/artifacts', '--env', 'GOMAXPROCS=2']
        if environment:
            # Existing tests deliberately only accept loopback test servers.
            # Share this isolated fixture's network namespace, never host networking.
            command += ['--network', f'container:silvermoon-marketing-v2-{engine}-1', '--env', environment]
        else:
            command += ['--network', 'none']
        command += ['--entrypoint', '/artifacts/marketing-v2-controller.test', IMAGE,
                    f'-test.run=^Test(LogExportDatabaseMatrix|IndependentAuditLogStores)$/{engine}', '-test.v', '-test.count=1', '-test.timeout=120s']
        result = subprocess.run(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        log = ARTIFACTS / f'marketing-v2-log-{engine}.log'
        log.write_text(result.stdout)
        print(log, flush=True)
        if result.returncode or '--- SKIP:' in result.stdout:
            print(result.stdout)
            raise SystemExit(result.returncode or 1)


def run_submission():
    compose = ['docker', 'compose', '-f', str(ROOT / '.dev/marketing-v2-requirements/validation/compose.yml'), 'exec', '-T']
    # The task fixture uses prefixed baseline tables and explicit marketing table
    # names. Give it a separate disposable database, not the model/service suite.
    subprocess.run(compose + ['postgres', 'psql', '-U', 'marketing_test', '-d', 'marketing_test', '-v', 'ON_ERROR_STOP=1',
                              '-c', 'DROP DATABASE IF EXISTS marketing_v2_submit', '-c', 'CREATE DATABASE marketing_v2_submit'], cwd=ROOT, check=True)
    subprocess.run(compose + ['mysql', 'mysql', '-uroot', '-plocal-root-test-only', '-e',
                              "DROP DATABASE IF EXISTS marketing_v2_submit; CREATE DATABASE marketing_v2_submit CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; GRANT ALL ON marketing_v2_submit.* TO 'marketing_test'@'%';"], cwd=ROOT, check=True)
    for engine in ('sqlite', 'mysql', 'postgres'):
        command = ['docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g',
                   '--mount', f'type=bind,source={ARTIFACTS},target=/artifacts', '--env', 'GOMAXPROCS=2',
                   '--network', 'silvermoon-marketing-v2_default', '--env', f'TEST_TASK_DB_DIALECT={engine}',
                   '--env', 'TEST_MYSQL_DSN=marketing_test:local-marketing-test-only@tcp(mysql:3306)/marketing_v2_submit?charset=utf8mb4&parseTime=true&loc=UTC',
                   '--env', 'TEST_POSTGRES_DSN=postgres://marketing_test:local-marketing-test-only@postgres:5432/marketing_v2_submit?sslmode=disable',
                   '--entrypoint', '/artifacts/marketing-v2-controller.test', IMAGE,
                   '-test.run=^Test(MarketingTaskSubmissionDatabase|MarketingMidjourneySubmissionDatabase|MarketingSingleWebSocketDatabase|ImmediateTaskSettlementDatabase)$', '-test.v', '-test.count=1', '-test.timeout=120s']
        result = subprocess.run(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        log = ARTIFACTS / f'marketing-v2-submit-{engine}.log'
        log.write_text(result.stdout)
        print(log, flush=True)
        if result.returncode or '--- SKIP:' in result.stdout:
            print(result.stdout)
            raise SystemExit(result.returncode or 1)


if __name__ == '__main__':
    if len(sys.argv) != 2 or sys.argv[1] not in ('build', 'run', 'all'):
        raise SystemExit('usage: log_matrix.py build|run|all')
    if sys.argv[1] in ('build', 'all'):
        build()
    if sys.argv[1] in ('run', 'all'):
        run()
        run_submission()
