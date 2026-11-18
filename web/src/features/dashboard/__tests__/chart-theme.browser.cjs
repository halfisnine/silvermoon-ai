/* oxlint-disable typescript/no-require-imports -- Reuses the standalone browser fixture runner. */
// Run silvermoon-layout.browser.cjs with SILVERMOON_REVIEW_TARGET=dashboard after building.
const assert = require('node:assert/strict')

exports.usageRows = (now) => [
  {
    user_id: 1,
    username: 'alice',
    model_name: 'Claude Sonnet',
    quota: 1500000,
    count: 18,
    token_used: 23000,
    created_at: now - 7200,
    token_id: 1,
    token_name: 'App',
    use_group: 'default',
    channel_id: 1,
    channel_name: 'Primary',
    node_name: 'East',
  },
  {
    user_id: 2,
    username: 'bob',
    model_name: 'Gemini Pro',
    quota: 1000000,
    count: 12,
    token_used: 16000,
    created_at: now - 3600,
    token_id: 2,
    token_name: 'CLI',
    use_group: 'default',
    channel_id: 2,
    channel_name: 'Backup',
    node_name: 'West',
  },
  {
    user_id: 1,
    username: 'alice',
    model_name: 'GPT-4.1',
    quota: 500000,
    count: 7,
    token_used: 8000,
    created_at: now,
    token_id: 1,
    token_name: 'App',
    use_group: 'default',
    channel_id: 1,
    channel_name: 'Primary',
    node_name: 'East',
  },
]

// Check rendered canvas pixels, not component state: catches both a stale
// canvas after switching and specs that accidentally override the package.
async function waitForPalette(page, mode) {
  const palette = {
    default: ['#78618f', '#557e9b', '#438b80'],
    'silvermoon-dream': ['#8b6caf', '#658da6', '#b66e96'],
    'crimson-clouds': ['#a95379', '#6b729d', '#bf8051'],
  }[mode]
  await page.mouse.move(0, 0)
  await page.waitForFunction(
    (palette) => {
      const canvases = [
        ...document.querySelectorAll('.dashboard-chart-panel canvas'),
      ]
      if (!canvases.length) return false
      const colors = palette.map((hex) =>
        [1, 3, 5].map((i) => Number.parseInt(hex.slice(i, i + 2), 16))
      )
      return canvases.every((canvas) => {
        const context = canvas.getContext('2d')
        if (!context || !canvas.width || !canvas.height) return false
        const pixels = context.getImageData(
          0,
          0,
          canvas.width,
          canvas.height
        ).data
        return colors.slice(0, 2).every((color) => {
          let count = 0
          for (let i = 0; i < pixels.length; i += 4) {
            if (
              pixels[i + 3] > 200 &&
              color.every((v, c) => Math.abs(pixels[i + c] - v) < 3)
            ) {
              count++
            }
            if (count > 20) return true
          }
          return false
        })
      })
    },
    palette,
    { polling: 200 }
  )
}

async function switchPackage(page, mode) {
  console.log({ switching: mode })
  await page.getByRole('button', { name: '打开主题设置' }).click()
  await page
    .getByRole('radiogroup', { name: '主题', exact: true })
    .getByRole('radio', {
      name: {
        default: '默认',
        'silvermoon-dream': '银月梦境',
        'crimson-clouds': '绯月流霞',
      }[mode],
      exact: true,
    })
    .click()
  await page.keyboard.press('Escape')
  await page.getByRole('dialog').waitFor({ state: 'hidden' })
  await waitForPalette(page, mode)
}

exports.reviewCharts = async ({ page, baseUrl, mode, out }) => {
  page.setDefaultTimeout(60000)
  const results = []
  const widths = process.env.SILVERMOON_REVIEW_WIDTHS
    ? process.env.SILVERMOON_REVIEW_WIDTHS.split(',').map(Number)
    : [390, 1440]
  for (const width of widths) {
    await page.setViewportSize({ width, height: 1100 })
    for (const section of ['models', 'users', 'flow']) {
      console.log({ mode, width, section })
      await page.goto(`${baseUrl}/dashboard/${section}`)
      await page.locator('.dashboard-chart-panel').first().waitFor()
      if (process.env.SILVERMOON_REVIEW_FONT) {
        await page.addStyleTag({
          content:
            "@font-face{font-family:'Review Sans';src:url('/__review_font.ttf')}body{font-family:'Review Sans',sans-serif;--font-body:'Review Sans',sans-serif}",
        })
        await page.evaluate(() => document.fonts.ready)
      }
      await waitForPalette(page, mode)
      const size = await page.evaluate(() => ({
        viewport: innerWidth,
        page: document.documentElement.scrollWidth,
        panels: [...document.querySelectorAll('.dashboard-chart-panel')].map(
          (el) => {
            const rect = el.getBoundingClientRect()
            return {
              left: rect.left,
              right: rect.right,
              background: getComputedStyle(el).backgroundImage,
            }
          }
        ),
      }))
      assert.ok(
        size.page <= size.viewport + 1,
        `${section} page overflows at ${width}`
      )
      for (const panel of size.panels) {
        assert.ok(
          panel.left >= 0 && panel.right <= width + 1,
          `${section} panel overflows`
        )
        assert.match(
          panel.background,
          /gradient/,
          'Chart needs a reading surface'
        )
      }
      await page.screenshot({
        path: `${out}/${section}-${mode}-${width}.png`,
        fullPage: true,
      })
      if (section === 'models') {
        // Exercise all five model/consumption views, then switch without losing tabs.
        await page.getByRole('button', { name: '面积图', exact: true }).click()
        for (const label of ['调用次数分布', '调用次数排行', '调用趋势']) {
          await page.getByRole('button', { name: label, exact: true }).click()
          console.log({ chart: label })
          await waitForPalette(page, mode)
        }
        await page
          .getByRole('button', { name: '调用次数分布', exact: true })
          .click()
        const selected = await page
          .locator('.dashboard-chart-panel button.bg-background')
          .allTextContents()
        const totals = await page
          .locator('.dashboard-chart-panel .text-muted-foreground')
          .allTextContents()
        const next = mode === 'crimson-clouds' ? 'default' : 'crimson-clouds'
        await switchPackage(page, next)
        assert.deepEqual(
          await page
            .locator('.dashboard-chart-panel button.bg-background')
            .allTextContents(),
          selected
        )
        assert.deepEqual(
          await page
            .locator('.dashboard-chart-panel .text-muted-foreground')
            .allTextContents(),
          totals
        )
        await switchPackage(page, mode)
      }
      if (section === 'flow') {
        await page.getByRole('tab', { name: '按请求数', exact: true }).click()
        const selected = await page
          .locator('[role=tab][aria-selected=true]')
          .allTextContents()
        await switchPackage(
          page,
          mode === 'default' ? 'silvermoon-dream' : 'default'
        )
        assert.deepEqual(
          await page
            .locator('[role=tab][aria-selected=true]')
            .allTextContents(),
          selected
        )
        await switchPackage(page, mode)
      }
      if (section === 'users') {
        await page.getByRole('tab', { name: '7 天', exact: true }).click()
        const selected = await page
          .locator('[role=tab][aria-selected=true]')
          .allTextContents()
        await switchPackage(
          page,
          mode === 'default' ? 'silvermoon-dream' : 'default'
        )
        assert.deepEqual(
          await page
            .locator('[role=tab][aria-selected=true]')
            .allTextContents(),
          selected
        )
        await switchPackage(page, mode)
      }
      results.push({ mode, section, width, renderedPalette: true, ...size })
    }
  }
  return results
}
