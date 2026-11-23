#!/usr/bin/env python3
"""Run the accounting tests against isolated real SQLite/MySQL/PostgreSQL.

First run migration_matrix.py prepare to create the isolated fixture databases.
Usage: accounting_matrix.py build|run|all
"""
import json
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
ARTIFACTS = ROOT / 'build/dev'
IMAGE = json.loads((ROOT / '.dev/toolchains.lock.json').read_text())['go']['domestic']


def build():
    for package in ('model', 'service'):
        subprocess.run([str(ROOT / '.dev/run'), 'go', 'test', '-c', '-o',
                        f'/artifacts/marketing-v2-{package}.test', f'./{package}'], cwd=ROOT, check=True)


def run(package="model"):
    test_filter = "^Test(Marketing(MoneyConversion|Persistence|Lottery|Activity|Checkin|Maintenance)|PaidRedemption|EpayMarketing)$" if package == "model" else "^TestMarketingBillingSessionLifecycle$"
    command = [
        'docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g',
        '--network', 'silvermoon-marketing-v2_default',
        '--mount', f'type=bind,source={ARTIFACTS},target=/artifacts',
        '--env', 'GOMAXPROCS=2', '--env', 'MV2_REQUIRE_DATABASES=1',
        '--env', 'MV2_MYSQL_DSN=marketing_test:local-marketing-test-only@tcp(mysql:3306)/marketing_v2_behavior?charset=utf8mb4&parseTime=true&loc=UTC',
        '--env', 'MV2_POSTGRES_DSN=postgres://marketing_test:local-marketing-test-only@postgres:5432/marketing_v2_behavior?sslmode=disable',
        '--entrypoint', f'/artifacts/marketing-v2-{package}.test', IMAGE,
        f'-test.run={test_filter}', '-test.v', '-test.count=1', '-test.timeout=120s',
    ]
    result = subprocess.run(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    (ARTIFACTS / f'marketing-v2-accounting-{package}.log').write_text(result.stdout)
    print(result.stdout, end='')
    result.check_returncode()
    if '--- SKIP:' in result.stdout:
        raise SystemExit('Required database matrix cannot pass with skipped cases')


if __name__ == '__main__':
    if len(sys.argv) != 2 or sys.argv[1] not in ('build', 'run', 'all'):
        raise SystemExit(__doc__)
    if sys.argv[1] in ('build', 'all'):
        build()
    if sys.argv[1] in ('run', 'all'):
        run('model')
        run('service')
