#!/usr/bin/env python3
"""Two actual gateway processes; only external model responses are fixtures."""
from datetime import datetime, timezone
import hashlib
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


if len(sys.argv) != 2 or sys.argv[1] not in ('build', 'run', 'all', 'capacity', 'failure'):
    raise SystemExit('usage: gateway_process.py build|run|all|capacity|failure (capacity uses the built binaries)')
if sys.argv[1] in ('build', 'all'):
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    run([ROOT / '.dev/run', 'go', 'build', '-o', '/artifacts/marketing-v2-validation/marketing-gateway', '.'])
    run([ROOT / '.dev/run', 'go', 'build', '-o', '/artifacts/marketing-v2-validation/gateway-process', './.dev/marketing-v2-requirements/validation/gateway_process'])
if sys.argv[1] in ('run', 'all', 'capacity', 'failure'):
    suite = sys.argv[1] if sys.argv[1] in ('capacity', 'failure') else 'functional'
    stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
    log = ARTIFACTS / f'gateway-{suite}-{stamp}.log'
    run(COMPOSE + ['exec', '-T', 'postgres', 'psql', '-U', 'marketing_test', '-d', 'marketing_test', '-v', 'ON_ERROR_STOP=1', '-c', 'DROP DATABASE IF EXISTS marketing_v2_gateway_process', '-c', 'CREATE DATABASE marketing_v2_gateway_process'])
    with log.open('w') as output:
        output.write(f'GATEWAY_ENV image={IMAGE} cpus=2 memory=3GiB shared_container_for_two_gateways_and_load_generator=true\n')
        for name in ('marketing-gateway', 'gateway-process'):
            output.write(f'GATEWAY_BINARY_SHA256 {name} {hashlib.sha256((ARTIFACTS / name).read_bytes()).hexdigest()}\n')
        output.flush()
        try:
            run(['docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g', '--network', 'silvermoon-marketing-v2_default', '--workdir', '/tmp', '--mount', f'type=bind,source={ARTIFACTS},target=/fixtures', '--env', 'GOMAXPROCS=2', '--env', f'MV2_GATEWAY_SUITE={suite}', '--env', 'SQL_DSN=postgres://marketing_test:local-marketing-test-only@postgres:5432/marketing_v2_gateway_process?sslmode=disable', '--env', 'REDIS_CONN_STRING=redis://redis:6379/14', '--entrypoint', '/fixtures/gateway-process', IMAGE], stdout=output, stderr=subprocess.STDOUT)
        finally:
            print(f'GATEWAY_EVIDENCE {log.relative_to(ROOT)}', flush=True)
            for port in ('18371', '18372'):
                source = ARTIFACTS / f'gateway-{port}.log'
                if source.exists():
                    source.rename(ARTIFACTS / f'gateway-{suite}-{port}-{stamp}.log')
            source = ARTIFACTS / 'capacity-results.json'
            if suite == 'capacity' and source.exists():
                source.rename(ARTIFACTS / f'capacity-results-{stamp}.json')
    for line in log.read_text().splitlines():
        if line.startswith(('GATEWAY_', 'CAPACITY_')):
            print(line)
