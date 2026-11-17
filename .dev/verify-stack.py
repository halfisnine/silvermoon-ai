#!/usr/bin/env python3
"""Verify development isolation without printing credentials or changing application data."""
import json
from pathlib import Path
import subprocess
from urllib.parse import urlparse
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parent.parent
PROJECT = 'silvermoon-ai-dev'


def output(args):
    return subprocess.check_output(args, text=True).strip()


ids = output(['docker', 'ps', '-aq', '--filter', f'label=com.docker.compose.project={PROJECT}']).split()
assert len(ids) == 4, 'Expected exactly app, web, postgres and redis'
containers = json.loads(output(['docker', 'inspect', *ids]))
services = {c['Config']['Labels']['com.docker.compose.service']: c for c in containers}
assert set(services) == {'app', 'web', 'postgres', 'redis'}
for service, container in services.items():
    assert container['State']['Running'], f'{service} is not running'
    assert container['State']['Health']['Status'] == 'healthy', f'{service} is not healthy'
    expected = {f'{PROJECT}_storage'} if service in {'postgres', 'redis'} else {f'{PROJECT}_application'}
    if service == 'app':
        expected.add(f'{PROJECT}_storage')
    assert set(container['NetworkSettings']['Networks']) == expected, f'{service} network mismatch'
    bindings = container['HostConfig']['PortBindings'] or {}
    if service in {'postgres', 'redis'}:
        assert not bindings, f'{service} must not publish ports'
    else:
        expected_port = '43000' if service == 'app' else '45173'
        assert len(bindings) == 1
        assert list(bindings.values()) == [[{'HostIp': '127.0.0.1', 'HostPort': expected_port}]]

network = json.loads(output(['docker', 'network', 'inspect', f'{PROJECT}_storage']))[0]
assert network['Internal'] is True
app_env = dict(value.split('=', 1) for value in services['app']['Config']['Env'] if '=' in value)
assert urlparse(app_env['SQL_DSN']).hostname == 'postgres'
assert urlparse(app_env['SQL_DSN']).path == '/silvermoon_dev'
assert urlparse(app_env['REDIS_CONN_STRING']).hostname == 'redis'
assert not app_env.get('LOG_SQL_DSN'), 'Unexpected external log database'

volumes = {m['Name'] for c in containers for m in c['Mounts'] if m['Type'] == 'volume'}
assert volumes == {f'{PROJECT}_{suffix}' for suffix in ['postgres_data', 'redis_data', 'app_data']}
other_ids = set(output(['docker', 'ps', '-aq']).split()) - set(ids)
if other_ids:
    others = json.loads(output(['docker', 'inspect', *sorted(other_ids)]))
    for other in others:
        assert not volumes.intersection(m.get('Name') for m in other['Mounts'])
        assert not {f'{PROJECT}_storage', f'{PROJECT}_application'}.intersection(other['NetworkSettings']['Networks'])

for port in [43000, 45173]:
    with urlopen(f'http://127.0.0.1:{port}/api/status', timeout=15) as response:
        status = json.load(response)
        assert status['success'] is True
        expected_version = (ROOT / 'build/dev/runtime/version').read_text().strip()
        assert status['data']['version'] == expected_version
    with urlopen(f'http://127.0.0.1:{port}/', timeout=15) as response:
        assert 'id="root"' in response.read().decode()
    with urlopen(f'http://127.0.0.1:{port}/silvermoon/moonlight.webp', timeout=15) as response:
        assert response.read() == (ROOT / 'web/public/silvermoon/moonlight.webp').read_bytes()

postgres_version = output(['docker', 'exec', services['postgres']['Id'], 'psql', '-U', 'silvermoon_dev', '-d', 'silvermoon_dev', '-Atc', 'SHOW server_version'])
redis_version = output(['docker', 'exec', services['redis']['Id'], 'redis-server', '--version'])
tables = output(['docker', 'exec', services['postgres']['Id'], 'psql', '-U', 'silvermoon_dev', '-d', 'silvermoon_dev', '-Atc', "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'"])
assert int(tables) > 0, 'Application schema was not initialized'
print(json.dumps({'project': PROJECT, 'healthy_services': sorted(services), 'database': 'silvermoon_dev', 'postgres': postgres_version, 'redis': redis_version, 'public_tables': int(tables), 'version': expected_version, 'storage_published_ports': [], 'app_url': 'http://127.0.0.1:43000', 'live_frontend_url': 'http://127.0.0.1:45173', 'separate_volumes_and_networks': True}, ensure_ascii=False, indent=2))
