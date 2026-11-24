#!/usr/bin/env python3
"""MV2-014 real database accounting read, HTTP and independent-log regression.

Uses only the existing isolated silvermoon-marketing-v2 fixture stack.
Run from the repository root: python3 .dev/marketing-v2-requirements/validation/accounting_read_matrix.py
Heavy builds and database runs are serial. No production data is accessed.
"""
import hashlib
import json
from pathlib import Path
import subprocess
import secrets

ROOT = Path(__file__).resolve().parents[3]
ARTIFACTS = ROOT / 'build/dev'
IMAGE = json.loads((ROOT / '.dev/toolchains.lock.json').read_text())['go']['domestic']


def run_case(name, binary, network, environment, pattern):
    command = ['docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g',
               '--network', network, '--mount', f'type=bind,source={ARTIFACTS},target=/artifacts',
               '--env', 'GOMAXPROCS=2']
    for key, value in environment.items():
        command += ['--env', f'{key}={value}']
    command += ['--entrypoint', '/artifacts/' + binary, IMAGE,
                '-test.run=' + pattern, '-test.v', '-test.count=1', '-test.timeout=180s']
    result = subprocess.run(command, cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    log = ARTIFACTS / f'mv2-014-final-{name}.log'
    digest = hashlib.sha256((ARTIFACTS / binary).read_bytes()).hexdigest()
    log.write_text(f'BINARY_SHA256={digest}\nPATTERN={pattern}\n' + result.stdout)
    print(f'{name}: exit={result.returncode}; {log}', flush=True)
    if result.returncode or '--- SKIP:' in result.stdout or '--- PASS:' not in result.stdout:
        print(result.stdout, flush=True)
        raise SystemExit(result.returncode or 1)


if __name__ == '__main__':
    changed = subprocess.check_output(['git', 'diff', '--name-only', '-z', 'HEAD'], cwd=ROOT).decode().split('\0')
    added = subprocess.check_output(['git', 'ls-files', '--others', '--exclude-standard', '-z'], cwd=ROOT).decode().split('\0')
    paths = sorted({name for name in changed + added if name and not name.startswith('.dev/')})
    paths.append(str(Path(__file__).resolve().relative_to(ROOT)))
    sources = {name: hashlib.sha256((ROOT / name).read_bytes()).hexdigest() for name in paths if (ROOT / name).is_file()}
    manifest = {'head': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT).decode().strip(),
                'sources': sources}
    (ARTIFACTS / 'mv2-014-final-source-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    for package in ('service', 'controller'):
        subprocess.run([str(ROOT / '.dev/run'), 'go', 'test', '-c', '-o',
                        f'/artifacts/mv2-014-{package}.test', './' + package], cwd=ROOT, check=True)
    compose = ['docker', 'compose', '-f', str(ROOT / '.dev/marketing-v2-requirements/validation/compose.yml'), 'exec', '-T']
    # Recovery scans every pending projection. Each suite needs a fresh database:
    # read-only UI fixtures are intentionally not publishable relay projections.
    for label, pattern in (
        ('reads', '^Test(MarketingAccountingReads|MarketingPricingEvidenceFreezesDisplayConversion)$'),
        ('lifecycle', '^TestMarketingBillingSessionLifecycle$'),
    ):
        database = 'mv2014_' + secrets.token_hex(6)
        subprocess.run(compose + ['postgres', 'psql', '-U', 'marketing_test', '-d', 'marketing_test', '-v', 'ON_ERROR_STOP=1',
                                  '-c', f'CREATE DATABASE {database}'], check=True, stdout=subprocess.DEVNULL)
        subprocess.run(compose + ['mysql', 'mysql', '-uroot', '-plocal-root-test-only', '-e',
                                  f"CREATE DATABASE {database}; GRANT ALL ON {database}.* TO 'marketing_test'@'%';"], check=True)
        try:
            run_case('service-' + label, 'mv2-014-service.test', 'silvermoon-marketing-v2_default', {
                'MV2_REQUIRE_DATABASES': '1',
                'MV2_MYSQL_DSN': f'marketing_test:local-marketing-test-only@tcp(mysql:3306)/{database}?charset=utf8mb4&parseTime=true&loc=UTC',
                'MV2_POSTGRES_DSN': f'postgres://marketing_test:local-marketing-test-only@postgres:5432/{database}?sslmode=disable',
            }, pattern)
        finally:
            subprocess.run(compose + ['postgres', 'psql', '-U', 'marketing_test', '-d', 'marketing_test', '-v', 'ON_ERROR_STOP=1',
                                      '-c', f'DROP DATABASE {database}'], check=True, stdout=subprocess.DEVNULL)
            subprocess.run(compose + ['mysql', 'mysql', '-uroot', '-plocal-root-test-only', '-e',
                                      f'DROP DATABASE {database}'], check=True)
    for engine in ('sqlite', 'mysql', 'postgres'):
        network = 'none' if engine == 'sqlite' else f'container:silvermoon-marketing-v2-{engine}-1'
        environment = {'TEST_SECURITY_DIALECT': engine}
        if engine == 'mysql':
            environment['TEST_MYSQL_DSN'] = 'root:local-root-test-only@tcp(127.0.0.1:3306)/marketing_test?charset=utf8mb4&parseTime=true&loc=UTC'
        if engine == 'postgres':
            environment['TEST_POSTGRES_DSN'] = 'postgres://marketing_test:local-marketing-test-only@127.0.0.1:5432/marketing_test?sslmode=disable'
        run_case('http-' + engine, 'mv2-014-controller.test', network, environment,
                 '^Test(MarketingAccountingHTTP|MarketingUserHTTP|MarketingCheckinHTTP)$')
    fixtures = {
        'sqlite': {},
        'mysql': {'AUDIT_MYSQL_DSN': 'root:local-root-test-only@tcp(127.0.0.1:3306)/marketing_test?charset=utf8mb4&parseTime=true&loc=UTC'},
        'postgres': {'AUDIT_POSTGRES_DSN': 'postgres://marketing_test:local-marketing-test-only@127.0.0.1:5432/marketing_test?sslmode=disable'},
        'clickhouse': {'AUDIT_CLICKHOUSE_DSN': 'clickhouse://marketing_test:local-marketing-test-only@127.0.0.1:9000/default'},
    }
    for engine, environment in fixtures.items():
        network = 'none' if engine == 'sqlite' else f'container:silvermoon-marketing-v2-{engine}-1'
        run_case('logs-' + engine, 'mv2-014-controller.test', network, environment,
                 '^Test(LogExportDatabaseMatrix|IndependentAuditLogStores)$/' + engine)
    run_case('export-rules', 'mv2-014-controller.test', 'none', {},
             '^Test(LogExportRules|AcquireLogExportLock)$')
    for name, digest in sources.items():
        if hashlib.sha256((ROOT / name).read_bytes()).hexdigest() != digest:
            raise SystemExit('Source changed during validation: ' + name)
