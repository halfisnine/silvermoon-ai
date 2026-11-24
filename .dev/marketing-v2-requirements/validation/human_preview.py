#!/usr/bin/env python3
"""Create a dedicated, persistent local MV2-015 human acceptance preview.

Usage: human_preview.py build|start|seed|scenarios
Never reuses the v1 preview or production data. Credentials stay in ignored
.dev/private/mv2-015; only the loopback port 46015 is exposed. Seed is one-shot.
"""
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[3]
PRIVATE = ROOT / '.dev/private/mv2-015'
DATA = ROOT / 'build/dev/mv2-015-preview'
IMAGE = json.loads((ROOT / '.dev/toolchains.lock.json').read_text())['go']['domestic']
BASE = 'http://127.0.0.1:46015'
APP = 'silvermoon-mv2-015-preview'
PROVIDER = 'silvermoon-mv2-015-provider'


def run(args):
    subprocess.run([str(x) for x in args], cwd=ROOT, check=True)


def call(method, path, data=None, token=None):
    headers = {'Content-Type': 'application/json', 'Origin': BASE}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(BASE + path, headers=headers, method=method,
                                 data=None if data is None else json.dumps(data).encode())
    with urllib.request.urlopen(req, timeout=30) as response:
        envelope = json.load(response)
    if not envelope.get('success'):
        raise RuntimeError(path + ': ' + str(envelope.get('message')))
    return envelope.get('data')


def build():
    DATA.mkdir(parents=True, exist_ok=True)
    run([ROOT / '.dev/run', 'go', 'build', '-o', '/artifacts/mv2-015-preview/provider', './.dev/marketing-v2-requirements/validation/preview_provider'])
    run([ROOT / '.dev/run', 'go', 'build', '-o', '/artifacts/mv2-015-preview/gateway', '.'])


def start():
    PRIVATE.mkdir(parents=True, exist_ok=True, mode=0o700)
    DATA.mkdir(parents=True, exist_ok=True)
    credentials = PRIVATE / 'accounts.json'
    if not credentials.exists():
        credentials.write_text(json.dumps({'baseUrl': BASE, 'admin': 'mv2admin', 'adminPassword': secrets.token_urlsafe(18)+'aA9!',
                                           'user': 'mv2user', 'userPassword': secrets.token_urlsafe(18)+'aA9!'}, indent=2))
        credentials.chmod(0o600)
    env = PRIVATE / 'app.env'
    if not env.exists():
        env.write_text('\n'.join(['PORT=3000', 'SQLITE_PATH=/data/preview.db', 'NODE_TYPE=master', 'UPDATE_TASK=false',
                                  'SESSION_SECRET='+secrets.token_hex(32), 'CRYPTO_SECRET='+secrets.token_hex(32),
                                  'PASSWORD_LOGIN_ENCRYPTION_ENABLED=false', 'GIN_MODE=release', 'GOMAXPROCS=2'])+'\n')
        env.chmod(0o600)
    for name, entry, extra in (
        (PROVIDER, '/data/provider', []),
        (APP, '/data/gateway', ['--publish', '127.0.0.1:46015:3000', '--env-file', str(env)]),
    ):
        exists = subprocess.run(['docker', 'inspect', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
        if exists:
            run(['docker', 'start', name])
        else:
            run(['docker', 'run', '-d', '--pull=never', '--name', name, '--cpus=1', '--memory=768m', '--memory-swap=768m',
                 '--network', 'silvermoon-marketing-v2_default', '--workdir', '/data',
                 '--mount', f'type=bind,source={DATA},target=/data', *extra, '--entrypoint', entry, IMAGE])
    for _ in range(50):
        try:
            with urllib.request.urlopen(BASE + '/api/status', timeout=2) as response:
                if response.status == 200:
                    print('PREVIEW_READY ' + BASE + ' credentials=' + str(credentials))
                    return
        except OSError:
            time.sleep(0.2)
    raise RuntimeError('Isolated preview did not become ready')


def seed():
    if (PRIVATE / 'seed-complete.json').exists():
        raise SystemExit('Already seeded; existing human test data is preserved')
    cfg = json.loads((PRIVATE / 'accounts.json').read_text())
    call('POST', '/api/setup', {'username': cfg['admin'], 'password': cfg['adminPassword'], 'confirmPassword': cfg['adminPassword'], 'SelfUseModeEnabled': False, 'DemoSiteEnabled': False})
    admin = call('POST', '/api/user/login', {'username': cfg['admin'], 'password': cfg['adminPassword']})['access_token']
    for key, value in [('general_setting.quota_display_type', 'CNY'), ('USDExchangeRate', '1'), ('ModelPrice', '{"marketing-preview":0.12}'), ('RegisterEnabled', 'true'), ('PasswordRegisterEnabled', 'true'), ('EmailVerificationEnabled', 'false')]:
        call('PUT', '/api/option/', {'key': key, 'value': value}, admin)
    call('POST', '/api/option/payment_compliance', {'confirmed': True}, admin)
    meta = call('GET', '/api/marketing/admin/metadata', token=admin)
    now = int(time.time())
    gift = {'kind': 'gift', 'calculation': 'fixed', 'amount': '0.08', 'currency': 'CNY', 'valid_seconds': 259200}
    activity = {'name': '验收示例：注册赠金', 'kind': 'registration', 'published': True, 'starts_at': now-60, 'ends_at': now+7*86400,
                'condition': 'registration', 'frequency': 'once', 'exclusive_group': '', 'priority': 0, 'rewards': [gift]}
    result = call('POST', '/api/marketing/admin/activities', {'operation_key': secrets.token_hex(16), 'money_revision': meta['money_revision'], 'activity': activity}, admin)
    call('POST', f"/api/marketing/admin/activities/{result['id']}/activate", {'operation_key': secrets.token_hex(16), 'revision': result['revision']}, admin)
    call('POST', '/api/user/register', {'username': cfg['user'], 'password': cfg['userPassword']})
    user = call('POST', '/api/user/login', {'username': cfg['user'], 'password': cfg['userPassword']})['access_token']
    codes = call('POST', '/api/redemption/', {'name': '验收付费卡密', 'count': 6, 'paid_amount_cny': '20', 'credit_amount': '20', 'credit_currency': 'CNY',
                 'money_revision': meta['money_revision'], 'sales_channel': '隔离测试小店', 'sales_batch': 'MV2-015', 'sales_evidence': 'Local fixture; no real payment'}, admin)
    call('POST', '/api/user/topup', {'key': codes[0]}, user)
    call('POST', '/api/channel/', {'mode': 'single', 'channel': {'name': '本地验收模型', 'type': 1, 'status': 1, 'key': 'isolated-fixture-only',
                  'base_url': 'http://'+PROVIDER+':18700', 'models': 'marketing-preview', 'group': 'default'}}, admin)
    call('POST', '/api/token/', {'name': '本地验收模型', 'expired_time': -1, 'unlimited_quota': True, 'group': 'default'}, user)
    tokens = call('GET', '/api/token/', token=user)
    token_id = tokens['items'][0]['id']
    key = call('POST', f'/api/token/{token_id}/key', {}, user)['key']
    request = urllib.request.Request(BASE+'/v1/chat/completions', method='POST',
        headers={'Content-Type': 'application/json', 'Authorization': 'Bearer '+key},
        data=json.dumps({'model': 'marketing-preview', 'messages': [{'role': 'user', 'content': 'Local acceptance example'}]}).encode())
    with urllib.request.urlopen(request, timeout=30) as response:
        generated = json.load(response)
    if not generated.get('choices'):
        raise RuntimeError('Local model example failed')
    result = {'baseUrl': BASE, 'unusedPaidCodes': codes[1:], 'model': 'marketing-preview', 'exampleActivity': activity['name'], 'seededAt': now}
    target = PRIVATE / 'seed-complete.json'
    target.write_text(json.dumps(result, ensure_ascii=False, indent=2)+'\n')
    target.chmod(0o600)
    print('PREVIEW_SEEDED real_registration_and_paid_code=true mock_provider_only=true private_fixture=' + str(target))


def scenarios():
    marker = PRIVATE / 'scenarios.json'
    if marker.exists():
        raise SystemExit('Scenarios already prepared; preserve user changes')
    cfg = json.loads((PRIVATE / 'accounts.json').read_text())
    admin = call('POST', '/api/user/login', {'username': cfg['admin'], 'password': cfg['adminPassword']})['access_token']
    meta = call('GET', '/api/marketing/admin/metadata', token=admin)
    now = int(time.time())
    objects = []
    for style in ('wheel', 'blind_box'):
        pool = {'name': '验收示例：'+('转盘' if style == 'wheel' else '盲盒'), 'style': style,
                'starts_at': now-60, 'ends_at': now+7*86400, 'capacity': 20,
                'prizes': [{'name': '限时赠金', 'weight': 80, 'stock': -1, 'gifts': [{'amount': '0.02', 'currency': 'CNY', 'valid_seconds': 7200}]},
                           {'name': '永久赠金', 'weight': 19, 'stock': -1, 'gifts': [{'amount': '0.05', 'currency': 'CNY', 'valid_seconds': 0}]},
                           {'name': '组合赠金', 'weight': 1, 'stock': 1, 'gifts': [{'amount': '1', 'currency': 'CNY', 'valid_seconds': 604800}, {'amount': '0.1', 'currency': 'CNY', 'valid_seconds': 0}]}]}
        created = call('POST', '/api/marketing/admin/pools', {'operation_key': secrets.token_hex(16), 'money_revision': meta['money_revision'], 'pool': pool}, admin)
        objects.append({'kind': 'pool', 'id': created['id'], 'name': pool['name']})
        activity = {'name': '验收示例：充值送'+('转盘' if style == 'wheel' else '盲盒')+'机会', 'kind': 'recharge', 'published': True,
                    'starts_at': now-60, 'ends_at': now+7*86400, 'condition': 'single_payment', 'frequency': 'every', 'threshold_cny': '1',
                    'rewards': [{'kind': 'chance', 'pool_id': created['id'], 'count': 2, 'chance_mode': 'direct'}]}
        created = call('POST', '/api/marketing/admin/activities', {'operation_key': secrets.token_hex(16), 'money_revision': meta['money_revision'], 'activity': activity}, admin)
        call('POST', f"/api/marketing/admin/activities/{created['id']}/activate", {'operation_key': secrets.token_hex(16), 'revision': created['revision']}, admin)
        objects.append({'kind': 'activity', 'id': created['id'], 'name': activity['name']})
    activity = {'name': '验收示例：每日及连续签到', 'kind': 'checkin', 'published': True, 'starts_at': now-3*86400, 'ends_at': now+7*86400,
                'condition': 'checkin', 'frequency': 'every',
                'rewards': [{'kind': 'gift', 'calculation': 'fixed', 'amount': '0.01', 'currency': 'CNY', 'valid_seconds': 259200}],
                'checkin': {'condition': 'none', 'cycle_days': 7, 'makeup_days': 3, 'makeup_limit': 2, 'makeup_daily': True,
                            'milestones': [{'days': 3, 'rewards': [{'kind': 'gift', 'calculation': 'fixed', 'amount': '0.02', 'currency': 'CNY', 'valid_seconds': 604800}]}]}}
    created = call('POST', '/api/marketing/admin/activities', {'operation_key': secrets.token_hex(16), 'money_revision': meta['money_revision'], 'activity': activity}, admin)
    call('POST', f"/api/marketing/admin/activities/{created['id']}/activate", {'operation_key': secrets.token_hex(16), 'revision': created['revision']}, admin)
    objects.append({'kind': 'activity', 'id': created['id'], 'name': activity['name']})
    # Isolated visual fixture only: backdate eligibility timestamps, never money,
    # rewards, claims or payments. Users still sign/make up through real HTTP.
    import sqlite3
    with sqlite3.connect(DATA / 'preview.db') as db:
        db.execute('UPDATE marketing_v2_activities SET created_at=?,enabled_at=? WHERE id=?', (now-3*86400, now-3*86400, created['id']))
        db.execute('UPDATE users SET created_at=? WHERE username=?', (now-3*86400, cfg['user']))
    seed_data = json.loads((PRIVATE / 'seed-complete.json').read_text())
    user = call('POST', '/api/user/login', {'username': cfg['user'], 'password': cfg['userPassword']})['access_token']
    call('POST', '/api/user/topup', {'key': seed_data['unusedPaidCodes'][0]}, user)
    seed_data['unusedPaidCodes'] = seed_data['unusedPaidCodes'][1:]
    (PRIVATE / 'seed-complete.json').write_text(json.dumps(seed_data, ensure_ascii=False, indent=2)+'\n')
    marker.write_text(json.dumps({'objects': objects, 'eligibilityBackdatedForVisualFixture': True}, ensure_ascii=False, indent=2)+'\n')
    marker.chmod(0o600)
    print('SCENARIOS_READY wheel_blindbox_checkin_makeup=true real_payment_awarded_chances=true')


if __name__ == '__main__':
    phases = {'build': build, 'start': start, 'seed': seed, 'scenarios': scenarios}
    if len(sys.argv) != 2 or sys.argv[1] not in phases:
        raise SystemExit(__doc__)
    phases[sys.argv[1]]()
