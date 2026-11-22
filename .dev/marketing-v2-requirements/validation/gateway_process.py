#!/usr/bin/env python3
"""Two actual gateway processes; only external model responses are fixtures."""
import json
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
ARTIFACTS = ROOT / 'build/dev/marketing-v2-validation'
COMPOSE = ['docker', 'compose', '-f', str(ROOT / '.dev/marketing-v2-requirements/validation/compose.yml')]
IMAGE = json.loads((ROOT / '.dev/toolchains.lock.json').read_text())['go']['domestic']


def run(args, **kwargs):
    subprocess.run([str(a) for a in args], cwd=ROOT, check=True, **kwargs)


if len(sys.argv) != 2 or sys.argv[1] not in ('build', 'run', 'all'):
    raise SystemExit('usage: gateway_process.py build|run|all')
if sys.argv[1] in ('build', 'all'):
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    run([ROOT / '.dev/run', 'go', 'build', '-o', '/artifacts/marketing-v2-validation/marketing-gateway', '.'])
    run([ROOT / '.dev/run', 'go', 'build', '-o', '/artifacts/marketing-v2-validation/gateway-process', './.dev/marketing-v2-requirements/validation/gateway_process'])
if sys.argv[1] in ('run', 'all'):
    run(COMPOSE + ['exec', '-T', 'postgres', 'psql', '-U', 'marketing_test', '-d', 'marketing_test', '-v', 'ON_ERROR_STOP=1', '-c', 'DROP DATABASE IF EXISTS marketing_v2_gateway_process', '-c', 'CREATE DATABASE marketing_v2_gateway_process'])
    with (ARTIFACTS / 'gateway-process.log').open('w') as output:
        run(['docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g', '--network', 'silvermoon-marketing-v2_default', '--workdir', '/tmp', '--mount', f'type=bind,source={ARTIFACTS},target=/fixtures', '--env', 'GOMAXPROCS=2', '--env', 'SQL_DSN=postgres://marketing_test:local-marketing-test-only@postgres:5432/marketing_v2_gateway_process?sslmode=disable', '--env', 'REDIS_CONN_STRING=redis://redis:6379/14', '--entrypoint', '/fixtures/gateway-process', IMAGE], stdout=output, stderr=subprocess.STDOUT)
    for line in (ARTIFACTS / 'gateway-process.log').read_text().splitlines():
        if line.startswith(('GATEWAY_',)):
            print(line)
