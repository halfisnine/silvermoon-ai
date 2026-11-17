/* oxlint-disable typescript/no-require-imports -- Standalone Node browser check resolves optional tools through NODE_PATH. */
// Browser regression against the built frontend; API responses are test fixtures.
// Requires playwright and @sparticuz/chromium via NODE_PATH. Run after .dev/run bun run build.
// Optional SILVERMOON_REVIEW_FONT supplies a local CJK font on minimal Linux hosts.
const { chromium: pw } = require('playwright')
const chromium = require('@sparticuz/chromium').default
const fs = require('node:fs')
const path = require('node:path')
const http = require('node:http')

const repo = path.resolve(__dirname, '../../../../..')
const out = path.join(
  repo,
  'build/dev/silvermoon-v1.2-review',
  process.env.SILVERMOON_REVIEW_LONG_NAME ? 'long-name' : '.'
)
fs.mkdirSync(out, { recursive: true })
const dist = path.join(repo, 'web/dist')
const server = http.createServer((req, res) => {
  const pathname = decodeURIComponent(
    new URL(req.url, 'http://localhost').pathname
  )
  let file = path.join(dist, pathname)
  if (pathname === '/__review_font.ttf' && process.env.SILVERMOON_REVIEW_FONT) {
    file = process.env.SILVERMOON_REVIEW_FONT
  } else if (!file.startsWith(dist + path.sep)) {
    res.writeHead(403)
    res.end()
    return
  }
  if (!fs.existsSync(file) || !fs.statSync(file).isFile()) {
    file = path.join(dist, 'index.html')
  }
  const mime = {
    '.html': 'text/html',
    '.js': 'application/javascript',
    '.css': 'text/css',
    '.webp': 'image/webp',
    '.woff2': 'font/woff2',
    '.ttf': 'font/ttf',
  }
  res.setHeader(
    'Content-Type',
    mime[path.extname(file)] || 'application/octet-stream'
  )
  fs.createReadStream(file).pipe(res)
})
;(async () => {
  await new Promise((r) => server.listen(0, '127.0.0.1', r))
  const baseUrl = `http://127.0.0.1:${server.address().port}`
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
  const results = [],
    errors = [],
    unknown = new Set()
  const now = Math.floor(Date.now() / 1000)
  const user = {
    id: 1,
    username: 'traveler',
    display_name: process.env.SILVERMOON_REVIEW_LONG_NAME
      ? 'SilvermoonTravelerWithAnUnbrokenVeryLongDisplayName'
      : '旅行者',
    role: 1,
    quota: 64300000,
    used_quota: 11500000,
    request_count: 186,
    status: 1,
    group: 'default',
  }
  const endpoint =
    'https://api.silvermoon.example/compatible/very-long-route-for-mobile-layout-check/v1'
  for (const mode of ['light', 'dark']) {
    const context = await browser.newContext({
      viewport: { width: 1440, height: 1000 },
      permissions: ['clipboard-read', 'clipboard-write'],
    })
    await context.addInitScript((mode) => {
      localStorage.setItem('i18nextLng', 'zhCN')
      localStorage.setItem('newapi:theme:v1:mode', mode)
    }, mode)
    await context.route('**/api/**', async (route) => {
      const path = new URL(route.request().url()).pathname
      let data
      if (path === '/api/status') {
        data = {
          system_name: '银月',
          logo: '/silvermoon/moonlight.webp',
          server_address: 'https://api.silvermoon.example',
          api_info_enabled: true,
          api_info: [
            {
              url: endpoint,
              route: '主线路',
              description: '请按客户端接入说明填写地址',
              color: 'blue',
            },
          ],
          announcements_enabled: true,
          announcements: [
            {
              id: 1,
              content: '欢迎来到银月，接入前请查看模型与价格。',
              type: 'info',
              publishDate: '2026-11-17',
            },
          ],
          faq_enabled: false,
          uptime_kuma_enabled: false,
          display_in_currency: true,
          quota_per_unit: 500000,
        }
      } else if (path === '/api/setup') data = { status: true }
      else if (path === '/api/home_page_content' || path === '/api/notice') {
        data = ''
      } else if (path === '/api/user/auth/refresh') {
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
      } else if (path === '/api/user/self') data = user
      else if (path === '/api/token/' || path === '/api/token') {
        data = {
          items: [{ id: 1, name: 'App key', key: 'masked', status: 1 }],
          total: 1,
        }
      } else if (path === '/api/user/models') data = ['gpt-4o-mini']
      else if (path === '/api/data/self') {
        data = [
          {
            created_at: now - 3600,
            quota: 1500000,
            count: 18,
            token_used: 23000,
          },
        ]
      } else {
        unknown.add(path)
        data = []
      }
      await route.fulfill({ json: { success: true, data } })
    })
    const page = await context.newPage()
    page.on('pageerror', (e) => errors.push(String(e)))
    for (const target of ['home', 'console']) {
      await page.goto(
        baseUrl + (target === 'home' ? '/' : '/dashboard/overview')
      )
      await page
        .locator(target === 'home' ? '.silvermoon-hero' : '.silvermoon-welcome')
        .waitFor()
      if (process.env.SILVERMOON_REVIEW_FONT) {
        await page.addStyleTag({
          content:
            "@font-face{font-family:'Review Sans';src:url('/__review_font.ttf')} body{font-family:'Review Sans',sans-serif;--font-serif:'Review Sans',serif}",
        })
      }
      await page.evaluate(() => document.fonts.ready)
      for (const width of [320, 360, 390, 430, 768, 1024, 1440]) {
        await page.setViewportSize({ width, height: 1000 })
        await page.evaluate(async () => {
          await new Promise((r) =>
            requestAnimationFrame(() => requestAnimationFrame(r))
          )
          await Promise.allSettled(
            document
              .getAnimations()
              .filter(
                (a) => a.effect?.getComputedTiming().iterations !== Infinity
              )
              .map((a) => a.finished)
          )
        })
        const size = await page.evaluate(() => ({
          width: innerWidth,
          scroll: document.documentElement.scrollWidth,
          main: [...document.querySelectorAll('main')].map((e) => ({
            client: e.clientWidth,
            scroll: e.scrollWidth,
          })),
        }))
        if (target === 'home') {
          const footer = page.locator('.silvermoon-footer')
          const bounds = await footer.boundingBox()
          const maximumHeight = width < 640 ? 120 : 80
          if (!bounds || bounds.height > maximumHeight) {
            throw new Error(
              `Homepage footer exceeds compact height at ${width}px: ${bounds?.height}`
            )
          }
          if (await footer.locator('img').count()) {
            throw new Error('Compact homepage footer repeats the brand logo')
          }
          await footer
            .getByRole('link', { name: 'New API', exact: true })
            .waitFor()
        }
        results.push({ mode, target, ...size })
        if (
          size.scroll > width ||
          size.main.some((s) => s.scroll > s.client + 1)
        ) {
          throw new Error(`Overflow ${JSON.stringify(results.at(-1))}`)
        }
        if ([390, 1440].includes(width)) {
          await page.screenshot({
            path: `${out}/${target}-${mode}-${width}.png`,
            fullPage: true,
          })
        }
      }
      if (target === 'home') {
        await page.locator('.silvermoon-endpoint button').click()
        const copied = await page.evaluate(() => navigator.clipboard.readText())
        if (copied !== endpoint) throw new Error('Copy mismatch')
        await page.locator('.silvermoon-news').click()
        await page.getByRole('dialog').waitFor()
        await page.keyboard.press('Escape')
        await page.getByRole('dialog').waitFor({ state: 'hidden' })
        await page.locator('.silvermoon-guide-link').click()
        await page.getByRole('dialog').waitFor()
        await page.keyboard.press('Escape')
        await page.setViewportSize({ width: 390, height: 844 })
        const menu = page.locator('header button').last()
        await menu.click()
        await page
          .locator('nav a[href="/pricing"]:visible')
          .last()
          .click({ trial: true })
        await menu.click()
      } else {
        for (const active of ['false', 'true']) {
          const selector =
            active === 'true' ? '[data-active]' : ':not([data-active])'
          const item = page
            .locator(`[data-slot="sidebar-menu-button"]${selector}:visible`)
            .first()
          for (const state of ['hover', 'focus']) {
            await page.mouse.move(1435, 995)
            if (state === 'hover') {
              await item.hover()
            } else {
              await page.keyboard.press('Tab')
              await item.focus()
            }
            const contrast = await item.evaluate((element) => {
              const style = getComputedStyle(element)
              const luminance = (color) => {
                const channels = color
                  .match(/[\d.]+/g)
                  .slice(0, 3)
                  .map(Number)
                  .map((value) => {
                    const channel = value / 255
                    return channel <= 0.04045
                      ? channel / 12.92
                      : ((channel + 0.055) / 1.055) ** 2.4
                  })
                return (
                  channels[0] * 0.2126 +
                  channels[1] * 0.7152 +
                  channels[2] * 0.0722
                )
              }
              const foreground = luminance(style.color)
              const background = luminance(style.backgroundColor)
              return (
                (Math.max(foreground, background) + 0.05) /
                (Math.min(foreground, background) + 0.05)
              )
            })
            if (contrast < 4.5) {
              throw new Error(
                `${mode} sidebar ${state} (active=${active}) contrast ${contrast.toFixed(2)} < 4.5`
              )
            }
          }
        }
        await page.setViewportSize({ width: 390, height: 844 })
        const trigger = page.locator('[data-slot="sidebar-trigger"]').first()
        await trigger.click()
        await page.locator('[data-mobile="true"]').waitFor()
        await page.screenshot({
          path: `${out}/console-${mode}-mobile-menu.png`,
        })
        await page.keyboard.press('Escape')
      }
    }
    await context.close()
  }
  fs.writeFileSync(
    `${out}/browser-results.json`,
    JSON.stringify({ results, errors, unknown: [...unknown] }, null, 2)
  )
  await browser.close()
  server.close()
  process.stdout.write(
    `${JSON.stringify({ checks: results.length, errors, unknown: [...unknown] })}\n`
  )
  if (errors.length) process.exitCode = 1
})().catch((e) => {
  server.close()
  process.stderr.write(`${String(e)}\n`)
  process.exit(1)
})
