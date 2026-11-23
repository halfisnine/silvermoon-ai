#!/usr/bin/env python3
"""Isolated real-engine fresh/upgrade migration matrix. Never connects to production.

Usage: migration_matrix.py prepare|build-baseline|seed|build-candidate|verify|all
The prepare phase resets only the three fixed marketing_v2_* fixture databases.
Build phases use the repository-pinned .dev/run toolchain and its resource caps.
"""
from datetime import datetime, timezone
import hashlib
import json
import pathlib
import shutil
import subprocess
import sys
import tarfile

ROOT = pathlib.Path(__file__).resolve().parents[3]
VALIDATION = ROOT / '.dev/marketing-v2-requirements/validation'
ARTIFACTS = ROOT / 'build/dev/marketing-v2-validation'
BASELINE = 'ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12'
BASE_TREE = ROOT / '.cache/marketing-v2-baseline' / BASELINE
COMPOSE = ['docker', 'compose', '-f', str(VALIDATION / 'compose.yml')]
IMAGE = json.loads((ROOT / '.dev/toolchains.lock.json').read_text())['go']['domestic']
NETWORK = 'silvermoon-marketing-v2_default'
DATABASES = ('marketing_v2_fresh', 'marketing_v2_upgrade', 'marketing_v2_behavior')
RUN_STAMP = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')


def run(args, **kwargs):
    subprocess.run([str(arg) for arg in args], cwd=ROOT, check=True, **kwargs)


def prepare():
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    run(COMPOSE + ['up', '-d', '--wait'])
    for name in DATABASES:
        run(COMPOSE + ['exec', '-T', 'postgres', 'psql', '-U', 'marketing_test', '-d', 'marketing_test', '-v', 'ON_ERROR_STOP=1', '-c', f'DROP DATABASE IF EXISTS {name}', '-c', f'CREATE DATABASE {name}'])
        run(COMPOSE + ['exec', '-T', 'mysql', 'mysql', '-uroot', '-plocal-root-test-only', '-e', f"DROP DATABASE IF EXISTS {name}; CREATE DATABASE {name} CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; GRANT ALL ON {name}.* TO 'marketing_test'@'%';"])
    for name in ('fresh', 'upgrade', 'behavior'):
        for suffix in ('', '-wal', '-shm'):
            (ARTIFACTS / (name + '.sqlite' + suffix)).unlink(missing_ok=True)


def build_baseline():
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    if not (BASE_TREE / 'go.mod').exists():
        BASE_TREE.mkdir(parents=True, exist_ok=True)
        archive = ARTIFACTS / 'baseline.tar'
        with archive.open('wb') as output:
            run(['git', 'archive', BASELINE], stdout=output)
        with tarfile.open(archive) as source:
            source.extractall(BASE_TREE, filter='data')
    probe = BASE_TREE / '.dev/migration-probe/main.go'
    probe.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(VALIDATION / 'migration/main.go', probe)
    run([ROOT / '.dev/run', 'go', '-C', '/workspace/' + str(BASE_TREE.relative_to(ROOT)), 'build', '-o', '/artifacts/marketing-v2-validation/baseline-probe', './.dev/migration-probe'])


def build_candidate():
    run([ROOT / '.dev/run', 'go', 'build', '-o', '/artifacts/marketing-v2-validation/candidate-probe', './.dev/marketing-v2-requirements/validation/migration'])


def probe(engine, mode, target, fixture):
    args = ['docker', 'run', '--rm', '--pull=never', '--cpus=2', '--memory=3g', '--memory-swap=3g', '--network', NETWORK,
            '--mount', f'type=bind,source={ARTIFACTS},target=/fixtures', '--env', 'GOMAXPROCS=2']
    database = 'marketing_v2_' + fixture
    if engine == 'postgres':
        args += ['--env', f'SQL_DSN=postgres://marketing_test:local-marketing-test-only@postgres:5432/{database}?sslmode=disable']
    elif engine == 'mysql':
        args += ['--env', f'SQL_DSN=marketing_test:local-marketing-test-only@tcp(mysql:3306)/{database}?charset=utf8mb4&parseTime=true&loc=UTC']
    else:
        args += ['--env', f'SQLITE_PATH=/fixtures/{fixture}.sqlite?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)']
    args += ['--entrypoint', f'/fixtures/{target}-probe', IMAGE, mode, f'/fixtures/{engine}-{fixture}.json', target]
    log = ARTIFACTS / f'{engine}-{fixture}-{target}-{mode}-{RUN_STAMP}.log'
    digest = hashlib.sha256((ARTIFACTS / f'{target}-probe').read_bytes()).hexdigest()
    with log.open('a') as output:
        output.write(f'MIGRATION_BINARY_SHA256 target={target} sha256={digest} baseline={BASELINE}\n')
        output.flush()
        run(args, stdout=output, stderr=subprocess.STDOUT)
    print(log.relative_to(ROOT), flush=True)


def seed():
    for engine in ('sqlite', 'mysql', 'postgres'):
        probe(engine, 'seed', 'baseline', 'upgrade')
        probe(engine, 'verify', 'baseline', 'upgrade')


def verify():
    for engine in ('sqlite', 'mysql', 'postgres'):
        probe(engine, 'seed', 'candidate', 'fresh')
        probe(engine, 'verify', 'candidate', 'fresh')
        probe(engine, 'verify', 'candidate', 'upgrade')
        probe(engine, 'verify', 'candidate', 'upgrade')


if __name__ == '__main__':
    phases = {'prepare': prepare, 'build-baseline': build_baseline, 'seed': seed, 'build-candidate': build_candidate, 'verify': verify}
    if len(sys.argv) != 2 or sys.argv[1] not in (*phases, 'all'):
        raise SystemExit(__doc__)
    for action in phases.values() if sys.argv[1] == 'all' else [phases[sys.argv[1]]]:
        action()
