#!/usr/bin/env python3
"""Real OS-process activity verification, not HTTP authorization verification.
Usage: activity_process.py build|run|all
Resets only marketing_v2_activity_process. Failed run logs are never overwritten.
"""
import datetime
import json
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[3]
HERE = ROOT / '.dev/marketing-v2-requirements/validation'
ARTIFACTS = ROOT / 'build/dev/marketing-v2-validation'
COMPOSE = ['docker', 'compose', '-f', str(HERE / 'compose.yml')]
IMAGE = json.loads((ROOT / '.dev/toolchains.lock.json').read_text())['go']['domestic']


def run(args, **kwargs):
    subprocess.run([str(a) for a in args], cwd=ROOT, check=True, **kwargs)


def build():
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    run([ROOT / '.dev/run', 'go', 'build', '-o', '/artifacts/marketing-v2-validation/activity-process', './.dev/marketing-v2-requirements/validation/activity_process'])


def execute():
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    run(COMPOSE + ['exec', '-T', 'postgres', 'psql', '-U', 'marketing_test', '-d', 'marketing_test', '-v', 'ON_ERROR_STOP=1', '-c', 'DROP DATABASE IF EXISTS marketing_v2_activity_process', '-c', 'CREATE DATABASE marketing_v2_activity_process'])
    args = ['docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g', '--network', 'silvermoon-marketing-v2_default', '--mount', f'type=bind,source={ARTIFACTS},target=/fixtures', '--env', 'GOMAXPROCS=2', '--env', 'SQL_DSN=postgres://marketing_test:local-marketing-test-only@postgres:5432/marketing_v2_activity_process?sslmode=disable', '--env', 'REDIS_CONN_STRING=redis://redis:6379/12', '--entrypoint', '/fixtures/activity-process', IMAGE]
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
    log = ARTIFACTS / f'activity-process-{stamp}.log'
    print(f'Preserved process evidence: {log}', flush=True)
    with log.open('x') as output:
        run(args, stdout=output, stderr=subprocess.STDOUT)
    for line in log.read_text().splitlines():
        if line.startswith(('SCENARIO_OK', 'POSTGRES_LOCK_OBSERVED', 'ACTIVITY_PROCESS_MATRIX_OK')):
            print(line)


if __name__ == '__main__':
    if len(sys.argv) != 2 or sys.argv[1] not in ('build', 'run', 'all'):
        raise SystemExit(__doc__)
    if sys.argv[1] in ('build', 'all'):
        build()
    if sys.argv[1] in ('run', 'all'):
        execute()
