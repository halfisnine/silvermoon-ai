/* oxlint-disable typescript/no-require-imports -- Standalone Node browser check resolves optional tools through NODE_PATH. */
// Verify cold-load HTML without JavaScript or any cached status response.
const { chromium: playwright } = require('playwright')
const chromium = require('@sparticuz/chromium').default
const assert = require('node:assert/strict')
const { randomBytes } = require('node:crypto')
const fs = require('node:fs')
const path = require('node:path')

// Only point this at a disposable, uninitialized local instance.
async function verifyFreshInstallation(browser, baseUrl) {
  assert.match(baseUrl, /^http:\/\/127\.0\.0\.1:\d+$/)
  const context = await browser.newContext({ locale: 'zh-CN' })
  await context.addInitScript(() => localStorage.setItem('i18nextLng', 'zhCN'))
  const setup = await (await context.request.get(`${baseUrl}/api/setup`)).json()
  assert.equal(
    setup.data.status,
    false,
    'Fresh-install test requires an uninitialized instance'
  )
  assert.equal(
    setup.data.root_init,
    false,
    'Do not run against an existing administrator'
  )
  const status = await (
    await context.request.get(`${baseUrl}/api/status`)
  ).json()
  assert.equal(status.data.system_name, '银月')
  assert.equal(status.data.logo, '/silvermoon/moonlight.webp')
  const page = await context.newPage()
  if (process.env.SILVERMOON_REVIEW_FONT) {
    await page.route('**/__branding-font.ttf', (route) =>
      route.fulfill({
        path: process.env.SILVERMOON_REVIEW_FONT,
        contentType: 'font/ttf',
      })
    )
  }
  const errors = []
  page.on('pageerror', (error) => errors.push(String(error)))
  const out = path.resolve(
    __dirname,
    '../../../../build/dev/fresh-branding-review'
  )
  fs.mkdirSync(out, { recursive: true })
  await page.goto(`${baseUrl}/setup`)
  if (process.env.SILVERMOON_REVIEW_FONT) {
    await page.addStyleTag({
      content:
        "@font-face{font-family:'Review Sans';src:url('/__branding-font.ttf')} body{font-family:'Review Sans',sans-serif}",
    })
    await page.evaluate(() => document.fonts.ready)
  }
  await page
    .getByRole('heading', { name: '初始化 银月', exact: true })
    .waitFor()
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 })
    const logo = page.locator('img[src="/silvermoon/moonlight.webp"]')
    await logo.evaluate((element) => element.decode())
    assert.equal(await page.title(), '银月')
    assert.equal(await page.locator('img[src="/logo.png"]').count(), 0)
    assert.equal(
      await page.evaluate(
        () => document.documentElement.scrollWidth > innerWidth
      ),
      false
    )
    await page.screenshot({ path: `${out}/setup-${width}.png`, fullPage: true })
  }
  await page.getByRole('button', { name: '下一步', exact: true }).click()
  const password = randomBytes(24).toString('hex')
  await page.getByLabel('管理员用户名', { exact: true }).fill('brandcheck')
  await page.getByLabel('密码', { exact: true }).fill(password)
  await page.getByLabel('确认密码', { exact: true }).fill(password)
  await page.getByRole('button', { name: '下一步', exact: true }).click()
  await page.getByRole('button', { name: '下一步', exact: true }).click()
  await page
    .getByRole('heading', { name: '初始化 银月', exact: true })
    .waitFor()
  await page.getByRole('button', { name: '初始化系统', exact: true }).click()
  await page.waitForURL(`${baseUrl}/`)
  await page.locator('.silvermoon-hero').waitFor()
  await page.locator('[data-sonner-toast]').waitFor({ state: 'hidden' })
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 })
    const header = page.locator('.silvermoon-public-header')
    await header.getByText('银月', { exact: true }).waitFor()
    await header
      .locator('img[src="/silvermoon/moonlight.webp"]')
      .evaluate((element) => element.decode())
    assert.equal(await page.title(), '银月')
    assert.equal(await header.getByText('New API', { exact: true }).count(), 0)
    await header.screenshot({ path: `${out}/initialized-header-${width}.png` })
  }
  const initialized = await (
    await context.request.get(`${baseUrl}/api/setup`)
  ).json()
  assert.equal(initialized.data.status, true)
  assert.deepEqual(errors, [])
  await context.close()
  process.stdout.write(
    'Fresh database: setup wizard and initialized homepage use Silvermoon on desktop and mobile.\n'
  )
}

async function verifyInitialBranding() {
  const browser = await playwright.launch({
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
    const freshUrl = process.env.SILVERMOON_FRESH_BASE_URL
    const urls = freshUrl
      ? [freshUrl]
      : ['http://127.0.0.1:43000', 'http://127.0.0.1:45173']
    for (const baseUrl of urls) {
      const context = await browser.newContext({
        javaScriptEnabled: false,
        locale: 'zh-CN',
      })
      const page = await context.newPage()
      await page.goto(`${baseUrl}/`, {
        waitUntil: 'domcontentloaded',
      })
      assert.equal(
        await page.title(),
        '银月',
        `${baseUrl}: initial HTML title must be Silvermoon`
      )
      assert.equal(
        await page.locator('meta[name="title"]').getAttribute('content'),
        '银月'
      )
      const icon = page.locator('link[rel="icon"]')
      assert.equal(await icon.count(), 1)
      const iconUrl = new URL(await icon.getAttribute('href'), page.url()).href
      const iconResponse = await context.request.get(iconUrl)
      const logoResponse = await context.request.get(
        `${baseUrl}/silvermoon/moonlight.webp`
      )
      assert.equal(iconResponse.status(), 200)
      assert.deepEqual(
        await iconResponse.body(),
        await logoResponse.body(),
        'Initial icon must match the configured Silvermoon logo'
      )
      await context.close()
      process.stdout.write(
        `${baseUrl}: initial title, metadata and favicon passed with JavaScript disabled.\n`
      )
    }
    if (freshUrl) await verifyFreshInstallation(browser, freshUrl)
  } finally {
    await browser.close()
  }
}

verifyInitialBranding().catch((error) => {
  process.stderr.write(`${error}\n`)
  process.exitCode = 1
})
