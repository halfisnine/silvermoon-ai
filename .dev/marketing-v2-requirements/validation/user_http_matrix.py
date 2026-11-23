#!/usr/bin/env python3
"""Run MV2-013 real authenticated user activity HTTP tests.

Requires migration_matrix.py prepare and a current controller binary:
.dev/run go test -c -o /artifacts/marketing-v2-013-controller.test ./controller
Uses only the isolated silvermoon-marketing-v2 stack. Security fixtures create
random databases on each engine and clean them after the tests.
"""
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[3]
ARTIFACTS = ROOT / 'build/dev'
COMPOSE = ['docker', 'compose', '-f', str(Path(__file__).with_name('compose.yml'))]
IMAGE = json.loads((ROOT / '.dev/toolchains.lock.json').read_text())['go']['domestic']
BINARY = ARTIFACTS / 'marketing-v2-013-controller.test'


def run_case(name, network, environment, pattern):
    command = ['docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g',
               '--network', network, '--mount', f'type=bind,source={ARTIFACTS},target=/artifacts',
               '--env', 'GOMAXPROCS=2']
    for key, value in environment.items():
        command.extend(['--env', f'{key}={value}'])
    command.extend(['--entrypoint', '/artifacts/marketing-v2-013-controller.test', IMAGE,
                    '-test.run=' + pattern, '-test.v', '-test.count=1', '-test.timeout=120s'])
    result = subprocess.run(command, cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    log = ARTIFACTS / f'marketing-v2-013-{name}.log'
    log.write_text('BINARY_SHA256=' + hashlib.sha256(BINARY.read_bytes()).hexdigest() + '\n' + result.stdout)
    print(result.stdout, end='', flush=True)
    result.check_returncode()
    if '--- SKIP:' in result.stdout:
        raise RuntimeError(f'{name}: required test skipped')


if __name__ == '__main__':
    for engine in ('sqlite', 'mysql', 'postgres'):
        env = {'TEST_SECURITY_DIALECT': engine}
        network = 'silvermoon-marketing-v2_default'
        if engine != 'sqlite':
            container = subprocess.check_output(COMPOSE + ['ps', '-q', engine], text=True).strip()
            if not container:
                raise RuntimeError(f'isolated {engine} container is not running')
            network = 'container:' + container
        if engine == 'mysql':
            env['TEST_MYSQL_DSN'] = 'root:local-root-test-only@tcp(127.0.0.1:3306)/marketing_test?charset=utf8mb4&parseTime=true&loc=UTC'
        if engine == 'postgres':
            env['TEST_POSTGRES_DSN'] = 'postgres://marketing_test:local-marketing-test-only@127.0.0.1:5432/marketing_test?sslmode=disable'
        run_case('user-http-' + engine, network, env, '^Test(MarketingUserHTTP|MarketingCheckinHTTP)$')
