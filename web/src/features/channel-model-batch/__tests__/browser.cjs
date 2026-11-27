/* oxlint-disable typescript/no-require-imports -- Standalone browser regression with optional tools from NODE_PATH. */
const assert = require('node:assert/strict')
const { chromium: pw } = require('playwright')
const chromium = require('@sparticuz/chromium').default
const fs = require('node:fs')
const path = require('node:path')

const out = path.resolve(
  __dirname,
  '../../../../../build/dev/channel-model-batch-browser'
)
fs.mkdirSync(out, { recursive: true })
const baseUrl = process.env.SILVERMOON_REVIEW_URL || 'http://127.0.0.1:45473'
const captureScreenshots = process.env.SILVERMOON_REVIEW_SCREENSHOTS === '1'
const longName =
  'provider-with-a-long-name/model-with-extended-context-and-release-version-2026-11-25'
const names = [
  'alpha',
  'beta',
  longName,
  ...Array.from({ length: 17 }, (_, i) => `model-${i + 4}`),
]
const snapshot = {
  channel: {
    id: 7,
    name: 'Example upstream',
    type: 1,
    models: names.join(','),
    model_mapping: null,
  },
  rows: names.map((name) => ({
    source_name: name,
    upstream_name: name,
    metadata: null,
  })),
  vendors: [
    { id: 1, name: 'Saved OpenAI', icon: 'OpenAI', status: 1 },
    { id: 2, name: 'Saved Anthropic', icon: 'Claude.Color', status: 1 },
  ],
  metadata: [],
  channels: [],
  version: 'v1',
  endpoint_options: {
    openai: { path: '/v1/chat/completions', method: 'POST' },
    'openai-response': { path: '/v1/responses', method: 'POST' },
  },
}
;(async () => {
  const results = []
  for (const width of process.env.SILVERMOON_REVIEW_WIDTHS?.split(',').map(
    Number
  ) ?? [390, 768, 1440, 1920]) {
    const browser = await pw.launch({
      executablePath: await chromium.executablePath(),
      args: chromium.args.filter(
        (arg) =>
          ![
            '--single-process',
            '--disable-web-security',
            '--allow-running-insecure-content',
          ].includes(arg)
      ),
      headless: true,
    })
    try {
      const context = await browser.newContext({
        viewport: { width, height: 1000 },
        reducedMotion: 'reduce',
        locale: 'en-US',
      })
      await context.addInitScript(() => {
        localStorage.setItem('i18nextLng', 'en')
      })
      const page = await context.newPage()
      const errors = []
      const writes = []
      const previews = []
      const pricingReads = []
      page.on('pageerror', (error) => errors.push(error.stack || error.message))
      page.on('console', (msg) => {
        if (msg.type() === 'error') process.stderr.write(`${msg.text()}\n`)
      })
      page.setDefaultTimeout(20000)
      await context.route('**/api/**', async (route) => {
        const pathname = new URL(route.request().url()).pathname.replace(
          /\/$/,
          ''
        )
        const now = Math.floor(Date.now() / 1000)
        const user = {
          id: 1,
          username: 'review',
          role: 100,
          group: 'default',
          status: 1,
        }
        let data = []
        if (pathname === '/api/status') {
          data = {
            system_name: 'Silvermoon AI',
            setup: true,
            quota_display_type: 'USD',
          }
        } else if (
          ['/api/notice', '/api/about', '/api/home_page_content'].includes(
            pathname
          )
        ) {
          data = ''
        } else if (pathname === '/api/setup') data = { status: true }
        else if (pathname === '/api/user/auth/refresh') {
          data = {
            access_token: 'fixture-only',
            token_type: 'Bearer',
            access_expires_at: now + 3600,
            user,
            session: {
              sid: 'fixture',
              current: true,
              login_method: 'password',
              ip: '127.0.0.1',
              user_agent: 'browser-fixture',
              created_at: now,
              last_active_at: now,
              expires_at: now + 3600,
            },
          }
        } else if (pathname === '/api/user/self') data = user
        else if (pathname === '/api/channel') {
          data = {
            items: [snapshot.channel],
            total: 1,
            page: 1,
            page_size: 100,
          }
        } else if (pathname === '/api/channel/7/model_batch') {
          if (route.request().method() === 'POST') {
            writes.push(route.request().postDataJSON())
          } else data = snapshot
        } else if (pathname === '/api/option/model_pricing') {
          pricingReads.push(
            new URL(route.request().url()).searchParams.getAll('model')
          )
          data = {
            entries: names.map((name) => ({
              model_name: name,
              version: 'price-v1',
              configured:
                name === 'alpha'
                  ? {
                      'billing_setting.billing_mode': 'tiered_expr',
                      'billing_setting.billing_expr':
                        'tier("base", p * 2 + c * 10 + cr * 0.2 + cc * 2.5)',
                    }
                  : { ModelPrice: 0.025 },
              effective:
                name === 'alpha'
                  ? {
                      'billing_setting.billing_mode': 'tiered_expr',
                      'billing_setting.billing_expr':
                        'tier("base", p * 2 + c * 10 + cr * 0.2 + cc * 2.5)',
                    }
                  : { ModelPrice: 0.025 },
            })),
            options: {},
            empty_version: 'empty',
          }
        } else if (pathname === '/api/option/model_pricing/convert') {
          const request = route.request().postDataJSON()
          data = {
            expression: `tier("request", fixed(${request.pricing.ModelPrice}))`,
            effective: request.pricing,
          }
        } else if (pathname === '/api/option/model_pricing/preview') {
          const request = route.request().postDataJSON()
          previews.push(request)
          data = { effective: request.pricing }
        }
        await route.fulfill({ json: { success: true, data } })
      })
      try {
        await page.goto(`${baseUrl}/channel-model-batch?channel=7`)
        await page
          .getByRole('textbox', {
            name: 'User model name for alpha',
            exact: true,
          })
          .waitFor()
        assert.equal(
          await page
            .getByRole('button', { name: 'Batch settings', exact: true })
            .isDisabled(),
          true
        )
        assert.equal(
          await page
            .getByRole('button', { name: 'Batch pricing', exact: true })
            .count(),
          0
        )
        const endpointInput = page.getByRole('combobox', {
          name: 'Endpoints for alpha',
          exact: true,
        })
        await endpointInput.fill('/responses')
        await page
          .getByRole('option', { name: 'openai-response', exact: true })
          .click()
        await endpointInput.click()
        await endpointInput.fill('/chat/completions')
        await page.getByRole('option', { name: 'openai', exact: true }).click()
        await page
          .getByRole('textbox', { name: 'Search models', exact: true })
          .click()
        const endpointRow = page.locator('tbody tr').first()
        assert.equal(
          await endpointRow.getByText('+1', { exact: true }).count(),
          1
        )
        const endpointHeight = await endpointRow
          .locator('.batch-endpoint-select')
          .evaluate((el) => el.getBoundingClientRect().height)
        assert.ok(
          endpointHeight <= 34,
          'Endpoint summary stays on one compact row'
        )
        await endpointRow
          .getByRole('switch', {
            name: 'Square display for alpha',
            exact: true,
          })
          .check()
        assert.equal(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth + 1
          ),
          true,
          'Page must not overflow horizontally'
        )
        assert.equal(
          await page
            .locator('table')
            .evaluate((el) => el.getBoundingClientRect().width >= 1000),
          true,
          'Editable table retains readable widths and scrolls locally'
        )
        if (width >= 1440) {
          assert.deepEqual(
            await page.getByRole('columnheader').allTextContents(),
            [
              '',
              'User model name',
              'Vendor',
              'Customer pricing (USD)',
              'Upstream model',
              'Square display',
              'Endpoint selection',
              'Status',
              'Actions',
            ],
            'Mapping, vendor, upstream model and actions have separate columns'
          )
          const row = page.locator('tbody tr').first()
          const layout = await page
            .locator('.batch-workspace')
            .evaluate((workspace) => {
              const main = workspace.closest('main').getBoundingClientRect()
              const bounds = workspace.getBoundingClientRect()
              const cells = workspace.querySelector('tbody tr').cells
              const endPrice = cells[3]
                .querySelector('.batch-cache-grid > div:last-child')
                .getBoundingClientRect().right
              return {
                left: bounds.left - main.left,
                right: main.right - bounds.right,
                overview: workspace
                  .querySelector('.batch-overview')
                  .getBoundingClientRect().height,
                model: cells[1].getBoundingClientRect().width,
                upstream: cells[4].getBoundingClientRect().width,
                priceGap: cells[4].getBoundingClientRect().left - endPrice,
              }
            })
          assert.ok(
            layout.left <= 9 && layout.right <= 9,
            'Use the main workspace width with narrow side gutters'
          )
          assert.ok(
            layout.overview <= 60,
            'Channel overview stays in one compact row on desktop'
          )
          assert.ok(
            layout.model >= 140 && layout.upstream >= 140,
            'Model columns share the 40px transferred to price boxes and retain readable desktop widths'
          )
          assert.ok(
            layout.priceGap >= 0 && layout.priceGap <= 24,
            'Price column does not keep spare width before status'
          )
          if (width >= 1920) {
            assert.ok(
              layout.model >= 240 && layout.upstream >= 240,
              'Wide-screen spare width expands both model name columns'
            )
          }
          assert.equal(
            await row
              .locator('td')
              .nth(8)
              .getByRole('button', { name: 'Advanced settings' })
              .count(),
            1
          )
          const prices = await row.getByRole('textbox').evaluateAll((inputs) =>
            inputs
              .filter((input) => input.inputMode === 'decimal')
              .map((input) => ({
                top: input.getBoundingClientRect().top,
                width: input.getBoundingClientRect().width,
              }))
          )
          assert.equal(prices.length, 4)
          const basePriceWidths = await row
            .locator('.batch-price-fields [data-slot="input-group"]')
            .evaluateAll((groups) =>
              groups.map((group) => group.getBoundingClientRect().width)
            )
          assert.ok(
            basePriceWidths.every((priceWidth) => priceWidth >= 104),
            'Input and output price boxes leave more room for decimal prices'
          )
          assert.ok(
            Math.max(...prices.map((input) => input.top)) -
              Math.min(...prices.map((input) => input.top)) <=
              1,
            'All four token price inputs stay on one line'
          )
          assert.ok(
            prices.every((input) => input.width >= 30 && input.width <= 80),
            'Price inputs stay short while retaining space for decimal entry'
          )
          assert.equal(
            await page
              .locator('table')
              .evaluate(
                (el) =>
                  innerWidth < 1680 ||
                  el.getBoundingClientRect().width <=
                    el.parentElement.clientWidth + 1
              ),
            true,
            'Desktop columns fit the workspace even with long model names'
          )
          assert.equal(
            await page
              .locator('table')
              .evaluate((el) =>
                [...el.rows].every((row) =>
                  [...row.cells].every(
                    (cell) => cell.scrollWidth <= cell.clientWidth + 1
                  )
                )
              ),
            true,
            'Long text wraps within desktop cells'
          )
        }
        await page
          .getByRole('combobox', { name: 'Vendor for alpha', exact: true })
          .click()
        assert.ok(
          await page
            .getByRole('option', { name: 'Saved Anthropic', exact: true })
            .evaluate((el) => el.getBoundingClientRect().width >= 200),
          'Compact vendor chip opens a readable full-width catalog'
        )
        await page.keyboard.press('Escape')
        assert.equal(
          await page
            .getByRole('columnheader', {
              name: 'Upstream reference price',
              exact: true,
            })
            .count(),
          0
        )
        assert.equal(
          await page
            .getByRole('button', {
              name: 'Reference price source',
              exact: false,
            })
            .count(),
          0
        )
        assert.equal(
          await page
            .getByRole('switch', {
              name: 'Cache read price for alpha',
              exact: true,
            })
            .isVisible(),
          true
        )
        assert.equal(
          await page
            .getByRole('textbox', {
              name: 'Cache write price for alpha',
              exact: true,
            })
            .isVisible(),
          true
        )
        const invalidInput = page.getByRole('textbox', {
          name: 'Input price for alpha',
          exact: true,
        })
        const validationRow = page.locator('tbody tr').first()
        const heightBefore = await validationRow.evaluate(
          (row) => row.getBoundingClientRect().height
        )
        await invalidInput.fill('-1')
        assert.equal(await invalidInput.getAttribute('aria-invalid'), 'true')
        assert.ok(
          await page
            .getByRole('button', { name: 'Review and save', exact: true })
            .isDisabled()
        )
        const warning = page.getByRole('img', {
          name: 'Invalid pricing value',
          exact: true,
        })
        await warning.focus()
        await page.getByRole('tooltip').waitFor({ state: 'visible' })
        assert.ok(
          (await page.getByRole('tooltip').innerText()).includes(
            'Enter a price of 0 or more. Decimals are supported.'
          )
        )
        assert.equal(
          await validationRow.evaluate(
            (row) => row.getBoundingClientRect().height
          ),
          heightBefore,
          'Price warnings must not add a second line to the row'
        )
        await invalidInput.fill('2')
        assert.equal(
          await page
            .getByRole('img', { name: 'Invalid pricing value', exact: true })
            .count(),
          0
        )
        const readSwitch = page.getByRole('switch', {
          name: 'Cache read price for alpha',
          exact: true,
        })
        await readSwitch.click()
        assert.equal(
          await page
            .getByRole('textbox', {
              name: 'Cache read price for alpha',
              exact: true,
            })
            .isDisabled(),
          true
        )
        await readSwitch.click()
        const readPrice = page.getByRole('textbox', {
          name: 'Cache read price for alpha',
          exact: true,
        })
        await readPrice.pressSequentially('0.0125')
        assert.equal(
          await readPrice.inputValue(),
          '0.0125',
          'Decimal cache typing retains its decimal point'
        )
        assert.equal(
          await page
            .getByRole('switch', {
              name: 'Cache write price for alpha',
              exact: true,
            })
            .isChecked(),
          true
        )
        if (captureScreenshots) {
          await page.screenshot({
            path: path.join(out, `cache-${width}.png`),
            animations: 'disabled',
          })
        }
        await page.keyboard.press('Escape')
        if (width >= 1440) {
          const density = await page
            .locator('tbody tr')
            .evaluateAll((rows) => ({
              top: rows[0].getBoundingClientRect().top,
              height: rows[0].getBoundingClientRect().height,
              visible: rows.filter(
                (row) => row.getBoundingClientRect().bottom < innerHeight
              ).length,
            }))
          assert.ok(density.top < 320, 'Models start near the top')
          assert.ok(
            await page
              .locator('.batch-cache-fields')
              .first()
              .evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
            'Inline cache controls fit their column'
          )
          assert.ok(
            density.height <= 90,
            'Compact rows match the prototype density'
          )
          assert.ok(
            density.visible >= 6,
            'One screen shows at least six editable models'
          )
        }
        await page
          .getByRole('textbox', { name: 'Input price for alpha', exact: true })
          .fill('0.0125')
        await page
          .getByRole('textbox', { name: 'Input price for beta', exact: true })
          .fill('0.03125')
        await page
          .getByRole('img', { name: 'Invalid pricing value', exact: true })
          .waitFor({ state: 'hidden' })
        assert.equal(
          await page
            .getByRole('dialog', { name: 'Batch settings', exact: true })
            .count(),
          0,
          'Quick price editing does not open the editor'
        )
        await page
          .getByRole('checkbox', { name: 'Select visible models', exact: true })
          .check()
        await page
          .getByRole('button', { name: 'Batch settings', exact: true })
          .click()
        const dialog = page.getByRole('dialog')
        await dialog
          .getByRole('combobox', { name: 'Vendor', exact: true })
          .fill('Anthropic')
        await page.keyboard.press('ArrowDown')
        await page.keyboard.press('Enter')
        assert.equal(
          await dialog
            .getByRole('combobox', { name: 'Vendor', exact: true })
            .inputValue(),
          'Saved Anthropic'
        )
        await dialog
          .getByRole('combobox', { name: 'Pricing', exact: true })
          .click()
        await page
          .getByRole('option', {
            name: 'Multiply existing prices',
            exact: true,
          })
          .click()
        await dialog
          .getByRole('textbox', { name: 'Price factor', exact: true })
          .fill('1.25')
        await dialog
          .getByRole('button', { name: 'Preview changes', exact: true })
          .click()
        await dialog
          .getByRole('button', { name: 'Apply to draft', exact: true })
          .waitFor()
        assert.equal(writes.length, 0, 'Preview never saves')
        assert.ok(
          previews.some(
            (preview) =>
              preview.model_name === 'beta' &&
              preview.pricing['billing_setting.billing_mode'] ===
                'tiered_expr' &&
              preview.pricing['billing_setting.billing_expr'].includes(
                'fixed(0.0390625)'
              ) &&
              preview.pricing.ModelPrice === 0.025
          ),
          'Editing a legacy request price uses an expression; subsequent scaling keeps that expression and leaves inactive legacy values unchanged'
        )
        assert.equal(
          await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
          true,
          'Long model names stay within the dialog'
        )
        await dialog
          .getByRole('button', { name: 'Apply to draft', exact: true })
          .click()
        const editedRow = page.locator('tbody tr').first()
        const readDraft = () =>
          editedRow.evaluate((row) => ({
            prices: [...row.querySelectorAll('input[inputmode="decimal"]')].map(
              (input) => input.value
            ),
            cache: [...row.querySelectorAll('[role="switch"]')].map((control) =>
              control.getAttribute('aria-checked')
            ),
          }))
        const beforeRename = await readDraft()
        const readsBeforeRename = pricingReads.length
        await page.evaluate(() => {
          const watch = {
            row: document.querySelector('tbody tr'),
            disabled: [],
          }
          watch.observer = new MutationObserver((records) => {
            for (const record of records) {
              if (record.target.matches('input[inputmode="decimal"]')) {
                watch.disabled.push(record.target.getAttribute('aria-label'))
              }
            }
          })
          watch.observer.observe(document.querySelector('tbody'), {
            subtree: true,
            attributes: true,
            attributeFilter: ['disabled'],
          })
          globalThis.batchRenameWatch = watch
        })
        const nameInput = page.getByRole('textbox', {
          name: 'User model name for alpha',
          exact: true,
        })
        await nameInput.fill('')
        await nameInput.pressSequentially('alpha-renamed')
        assert.ok(
          await nameInput.evaluate((input) => input === document.activeElement),
          'Typing retains input focus'
        )
        assert.ok(
          await page.evaluate(
            () =>
              globalThis.batchRenameWatch.row ===
              document.querySelector('tbody tr')
          ),
          'Typing does not remount the row'
        )
        await page
          .getByRole('button', { name: 'Review and save', exact: true })
          .waitFor({ state: 'visible' })
        assert.deepEqual(
          await readDraft(),
          beforeRename,
          'Renaming retains all batch price and cache drafts'
        )
        assert.equal(
          await page
            .getByRole('combobox', { name: 'Vendor for alpha', exact: true })
            .inputValue(),
          'Saved Anthropic',
          'Renaming retains the explicitly selected batch vendor'
        )
        await page
          .getByRole('button', { name: 'Review and save', exact: true })
          .click()
        await dialog
          .getByRole('button', { name: 'Save changes', exact: true })
          .waitFor()
        assert.equal(writes.length, 0, 'Draft application never saves')
        const disabledChanges = await page.evaluate(() => {
          const watch = globalThis.batchRenameWatch
          watch.observer.disconnect()
          return watch.disabled
        })
        assert.deepEqual(
          disabledChanges,
          [],
          'Renaming never dims or disables existing price controls'
        )
        assert.equal(
          pricingReads.length - readsBeforeRename,
          1,
          'Continuous typing performs only one destination-price lookup'
        )
        assert.ok(pricingReads.at(-1).includes('alpha-renamed'))
        assert.ok(
          previews.some(
            (preview) =>
              preview.model_name === 'alpha-renamed' &&
              preview.pricing['billing_setting.billing_expr'].includes(
                'p * 0.015625'
              )
          ),
          'Final review previews the edited price under the new model name'
        )
        if (captureScreenshots) {
          await page.screenshot({
            path: path.join(out, `review-${width}.png`),
            fullPage: false,
            animations: 'disabled',
          })
        }
        await page.keyboard.press('Escape')
        await dialog.waitFor({ state: 'hidden' })
        assert.equal(
          await page
            .getByRole('button', { name: 'Review and save', exact: true })
            .isEnabled(),
          true
        )
        if (captureScreenshots) {
          await page.screenshot({
            path: path.join(out, `page-${width}.png`),
            fullPage: false,
            animations: 'disabled',
          })
        }
        await page
          .getByRole('button', { name: 'Batch settings', exact: true })
          .click()
        await dialog
          .getByRole('combobox', { name: 'Pricing', exact: true })
          .click()
        await page
          .getByRole('option', {
            name: 'Set uniform token prices',
            exact: true,
          })
          .click()
        const label = dialog.getByText('Customer pricing · USD', {
          exact: true,
        })
        const fields = dialog.locator('.batch-price-fields')
        assert.ok(
          (await fields.boundingBox()).y -
            ((await label.boundingBox()).y +
              (await label.boundingBox()).height) >=
            8,
          'Pricing label has clear spacing above the inputs'
        )
        await dialog
          .getByRole('textbox', {
            name: 'Input price for Selected models',
            exact: true,
          })
          .fill('2')
        await dialog
          .getByRole('textbox', {
            name: 'Output price for Selected models',
            exact: true,
          })
          .fill('10')
        await dialog
          .getByRole('switch', {
            name: 'Cache read price for Selected models',
            exact: true,
          })
          .click()
        await dialog
          .getByRole('textbox', {
            name: 'Cache read price for Selected models',
            exact: true,
          })
          .fill('0.2')
        await dialog
          .getByRole('switch', {
            name: 'Cache write price for Selected models',
            exact: true,
          })
          .click()
        await dialog
          .getByRole('textbox', {
            name: 'Cache write price for Selected models',
            exact: true,
          })
          .fill('2.5')
        assert.ok(
          await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)
        )
        if (captureScreenshots) {
          await page.screenshot({
            path: path.join(out, `batch-cache-${width}.png`),
            animations: 'disabled',
          })
        }
        assert.equal(writes.length, 0)
        await dialog
          .getByRole('button', { name: 'Cancel', exact: true })
          .click()
        assert.deepEqual(errors, [])
        results.push({ width, status: 'passed' })
      } catch (error) {
        process.stderr.write(`Original failure at ${width}: ${String(error)}\n`)
        await page
          .screenshot({
            path: path.join(out, `failure-${width}.png`),
            fullPage: false,
            animations: 'disabled',
            timeout: 5000,
          })
          .catch(() => {})
        fs.writeFileSync(
          path.join(out, `failure-${width}.txt`),
          `${await page.locator('body').innerText()}\n${JSON.stringify(errors)}`
        )
        throw error
      } finally {
        await context.close()
      }
    } finally {
      await browser.close()
    }
  }
  fs.writeFileSync(
    path.join(out, 'results.json'),
    JSON.stringify(results, null, 2)
  )
  process.stdout.write(`${JSON.stringify(results)}\n`)
})().catch((error) => {
  process.stderr.write(`${String(error)}\n`)
  process.exitCode = 1
})
