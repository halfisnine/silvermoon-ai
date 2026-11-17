/* oxlint-disable typescript/no-require-imports -- Standalone Node browser check resolves optional tools through NODE_PATH. */
// Verify cold-load HTML without JavaScript or any cached status response.
const { chromium: playwright } = require('playwright')
const chromium = require('@sparticuz/chromium').default
const assert = require('node:assert/strict')

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
    for (const port of [43000, 45173]) {
      const context = await browser.newContext({
        javaScriptEnabled: false,
        locale: 'zh-CN',
      })
      const page = await context.newPage()
      await page.goto(`http://127.0.0.1:${port}/`, {
        waitUntil: 'domcontentloaded',
      })
      assert.equal(
        await page.title(),
        '银月',
        `Port ${port}: initial HTML title must be Silvermoon`
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
        `http://127.0.0.1:${port}/silvermoon/moonlight.webp`
      )
      assert.equal(iconResponse.status(), 200)
      assert.deepEqual(
        await iconResponse.body(),
        await logoResponse.body(),
        'Initial icon must match the configured Silvermoon logo'
      )
      await context.close()
      process.stdout.write(
        `Port ${port}: initial title, metadata and favicon passed with JavaScript disabled.\n`
      )
    }
  } finally {
    await browser.close()
  }
}

verifyInitialBranding().catch((error) => {
  process.stderr.write(`${error}\n`)
  process.exitCode = 1
})
