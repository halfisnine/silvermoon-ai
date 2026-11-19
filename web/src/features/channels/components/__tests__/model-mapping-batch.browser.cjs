/* oxlint-disable typescript/no-require-imports -- Standalone browser regression using optional tools from NODE_PATH. */
// Run against the development frontend. All API responses are isolated fixtures.
const assert = require('node:assert/strict')
const { chromium: pw } = require('playwright')
const chromium = require('@sparticuz/chromium').default
const fs = require('node:fs')
const path = require('node:path')

const out = path.resolve(
  __dirname,
  '../../../../../../build/dev/batch-mapping-review'
)
fs.mkdirSync(out, { recursive: true })
const baseUrl = process.env.SILVERMOON_REVIEW_URL || 'http://127.0.0.1:45173'
const longModel =
  'provider-with-a-very-long-name/long-model-name-with-extended-context-and-release-version-2026-all'
const models = [
  'gpt-4o-all',
  'o3-all',
  longModel,
  ...Array.from({ length: 12 }, (_, i) => `model-${i}-all`),
]
const channel = {
  id: 42,
  name: 'Mapping review',
  other: '',
  balance: 0,
  used_quota: 0,
  other_info: '',
  remark: '',
  max_input_tokens: 0,
  settings: '{}',
  channel_info: {
    is_multi_key: false,
    multi_key_size: 0,
    multi_key_polling_index: 0,
    multi_key_mode: 'random',
  },
  type: 1,
  key: '',
  status: 1,
  created_time: 1,
  test_time: 0,
  response_time: 0,
  balance_updated_time: 0,
  models: models.join(','),
  group: 'default',
  base_url: 'https://upstream.example',
  model_mapping: '{"gpt-4o":"previous-model"}',
}
;(async () => {
  const browser = await pw.launch({
    executablePath: await chromium.executablePath(),
    args: chromium.args.filter(
      (a) =>
        ![
          '--single-process',
          '--disable-web-security',
          '--allow-running-insecure-content',
        ].includes(a)
    ),
    headless: true,
  })
  const results = []
  try {
    for (const width of [320, 390, 1440]) {
      const context = await browser.newContext({
        viewport: { width: 1440, height: 900 },
        reducedMotion: 'reduce',
        locale: 'en-US',
      })
      const page = await context.newPage()
      page.setDefaultTimeout(20000)
      await context.addInitScript(() => {
        localStorage.setItem('i18nextLng', 'en')
        localStorage.setItem('silvermoon:theme:v1:package', 'silvermoon-dream')
      })
      let mutations = 0
      const errors = []
      page.on('pageerror', (error) => errors.push(error.stack || error.message))
      await context.route('**/api/**', async (route) => {
        const pathname = new URL(route.request().url()).pathname.replace(
          /\/$/,
          ''
        )
        const user = {
          id: 1,
          username: 'review',
          role: 100,
          group: 'default',
          status: 1,
        }
        const now = Math.floor(Date.now() / 1000)
        let data = []
        if (pathname === '/api/status') {
          await route.fulfill({
            json: { success: true, data: { system_name: '银月', setup: true } },
          })
          return
        }
        if (
          ['/api/notice', '/api/about', '/api/home_page_content'].includes(
            pathname
          )
        ) {
          data = ''
        } else if (pathname === '/api/setup') data = { status: true }
        else if (pathname === '/api/user/auth/refresh') {
          data = {
            access_token: 'review-only',
            token_type: 'Bearer',
            access_expires_at: now + 3600,
            user,
            session: {
              sid: 'review',
              current: true,
              login_method: 'password',
              ip: '127.0.0.1',
              user_agent: 'review',
              created_at: now,
              last_active_at: now,
              expires_at: now + 3600,
            },
          }
        } else if (pathname === '/api/user/self') data = user
        else if (pathname === '/api/channel') {
          await route.fulfill({
            json: {
              success: true,
              data: {
                items: [channel],
                total: 1,
                page: 1,
                page_size: 20,
                type_counts: { 1: 1 },
              },
            },
          })
          return
        } else if (pathname === '/api/channel/42') data = channel
        else if (pathname === '/api/channel/fetch_models/42') data = models
        else if (pathname === '/api/channel/models') {
          data = models.map((id) => ({ id }))
        } else if (pathname === '/api/group') data = ['default']
        else if (pathname === '/api/channel/default_base_urls') {
          data = { 1: 'https://upstream.example' }
        }
        if (
          ['POST', 'PUT', 'DELETE'].includes(route.request().method()) &&
          pathname !== '/api/user/auth/refresh'
        ) {
          mutations++
        }
        await route.fulfill({ json: { success: true, data } })
      })
      try {
        await page.goto(`${baseUrl}/channels`)
        await page
          .getByRole('button', { name: 'Open menu', exact: true })
          .first()
          .click()
        await page.getByRole('menuitem', { name: 'Edit', exact: true }).click()
        await page
          .getByRole('button', { name: 'Fetch from Upstream', exact: true })
          .click()
        await page
          .getByRole('button', { name: 'Redirect panel', exact: true })
          .click()
        await page
          .getByRole('button', { name: 'Batch Add', exact: true })
          .click()
        await page.setViewportSize({ width, height: 900 })
        const editor = page.getByRole('dialog', {
          name: 'Batch add mappings',
          exact: true,
        })
        await editor
          .getByRole('checkbox', { name: 'gpt-4o-all', exact: true })
          .check()
        await editor
          .getByRole('checkbox', { name: longModel, exact: true })
          .check()
        await editor
          .getByRole('textbox', { name: 'Suffix', exact: true })
          .fill('-all')
        await editor
          .getByRole('button', { name: 'Continue adding', exact: true })
          .click()
        assert.equal(await page.getByRole('alertdialog').count(), 0)
        assert.equal(
          await editor
            .getByRole('checkbox', { name: 'gpt-4o-all', exact: true })
            .isChecked(),
          false
        )
        await editor
          .getByRole('checkbox', { name: 'o3-all', exact: true })
          .check()
        await editor
          .getByRole('button', { name: 'Finish adding', exact: true })
          .click()
        const confirm = page.getByRole('alertdialog', {
          name: 'Confirm mappings',
          exact: true,
        })
        await confirm.waitFor()
        assert.equal(
          await confirm.getByText('Will overwrite', { exact: true }).count(),
          1
        )
        assert.equal(
          await confirm.getByText(longModel, { exact: true }).count(),
          1
        )
        const bounds = await confirm.boundingBox()
        assert.ok(
          bounds.x >= 0 && bounds.x + bounds.width <= width + 1,
          'Confirmation fits viewport'
        )
        assert.ok(
          await confirm.evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
          'Long model names wrap without horizontal overflow'
        )

        await page.keyboard.press('Escape')
        await confirm.waitFor({ state: 'hidden' })
        assert.equal(
          await editor
            .getByRole('checkbox', { name: 'o3-all', exact: true })
            .isChecked(),
          true
        )
        assert.equal(
          await editor
            .getByRole('textbox', { name: 'Suffix', exact: true })
            .inputValue(),
          '-all'
        )
        assert.ok(
          await editor.evaluate((el) => el.contains(document.activeElement)),
          'Escape restores focus inside editor'
        )
        await editor
          .getByRole('button', { name: 'Finish adding', exact: true })
          .click()
        assert.equal(await confirm.getByRole('listitem').count(), 3)
        await confirm
          .getByRole('button', {
            name: 'Add 3 mapping(s) and close',
            exact: true,
          })
          .click()
        await editor.waitFor({ state: 'hidden' })
        assert.equal(
          mutations,
          0,
          'Batch editing must not save the channel or call upstream'
        )
        assert.deepEqual(errors, [])
        results.push({ width, status: 'passed' })
      } catch (error) {
        process.stderr.write(`${JSON.stringify(errors)}\n`)
        fs.writeFileSync(
          path.join(out, `failure-${width}.txt`),
          await page.locator('body').innerText()
        )
        throw error
      } finally {
        await context.close()
      }
    }
    fs.writeFileSync(
      path.join(out, 'browser.json'),
      JSON.stringify(results, null, 2)
    )
    process.stdout.write(`${JSON.stringify(results)}\n`)
  } finally {
    await browser.close()
  }
})().catch((error) => {
  process.stderr.write(`${String(error)}\n`)
  process.exitCode = 1
})
