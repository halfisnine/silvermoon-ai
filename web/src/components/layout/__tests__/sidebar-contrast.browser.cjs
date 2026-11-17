/* oxlint-disable typescript/no-require-imports -- Standalone Node browser regression resolves optional tools through NODE_PATH. */
// Run against the local development build. All APIs are intercepted; no real account is used.
const { chromium: playwright } = require('playwright')
const chromium = require('@sparticuz/chromium').default
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')

const baseUrl = process.env.SILVERMOON_REVIEW_URL || 'http://127.0.0.1:43000'
const output = path.resolve(
  __dirname,
  '../../../../../build/dev/submenu-contrast'
)
fs.mkdirSync(output, { recursive: true })

async function checkContrast(item, label, results) {
  const colors = await item.evaluate((element) => {
    const style = getComputedStyle(element)
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = 1
    const context = canvas.getContext('2d')
    const luminance = (color) => {
      context.clearRect(0, 0, 1, 1)
      context.fillStyle = color
      context.fillRect(0, 0, 1, 1)
      const channels = [...context.getImageData(0, 0, 1, 1).data]
        .slice(0, 3)
        .map((v) => {
          const c = v / 255
          return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
        })
      return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722
    }
    const background =
      style.backgroundColor === 'rgba(0, 0, 0, 0)'
        ? style.getPropertyValue('--sidebar')
        : style.backgroundColor
    const bg = luminance(background)
    return [element, ...element.querySelectorAll('span, svg')].map((node) => {
      const color = getComputedStyle(node).color
      const fg = luminance(color)
      return {
        tag: node.tagName,
        color,
        background,
        ratio: (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05),
      }
    })
  })
  results.push({ label, colors })
  for (const color of colors) {
    assert.ok(
      color.ratio >= 4.5,
      `${label} ${color.tag} contrast ${color.ratio.toFixed(2)} < 4.5`
    )
  }
}

;(async () => {
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
  const results = []
  const errors = []
  try {
    for (const theme of ['light', 'dark']) {
      for (const width of [1440, 390]) {
        const context = await browser.newContext({
          locale: 'zh-CN',
          viewport: { width, height: 1000 },
          reducedMotion: 'reduce',
        })
        await context.addInitScript((mode) => {
          localStorage.setItem('i18nextLng', 'zhCN')
          localStorage.setItem('newapi:theme:v1:mode', mode)
        }, theme)
        const user = {
          id: 1,
          username: 'review',
          role: 100,
          display_name: 'review',
          quota: 0,
          used_quota: 0,
          request_count: 0,
        }
        await context.route('**/api/**', async (route) => {
          const pathname = new URL(route.request().url()).pathname
          let data = []
          if (pathname === '/api/setup') data = { status: true }
          else if (pathname === '/api/status') {
            data = {
              system_name: '银月',
              logo: '/silvermoon/moonlight.webp',
              chats: [{ 'Review chat': 'https://example.com/chat' }],
              api_info_enabled: false,
              announcements_enabled: false,
              faq_enabled: false,
              uptime_kuma_enabled: false,
            }
          } else if (pathname === '/api/user/auth/refresh') {
            const now = Math.floor(Date.now() / 1000)
            data = {
              access_token: 'browser-fixture',
              token_type: 'Bearer',
              access_expires_at: now + 3600,
              user,
              session: {
                sid: 'browser-fixture',
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
          else if (pathname === '/api/token/') data = { items: [], total: 0 }
          else if (pathname === '/api/notice') data = ''
          await route.fulfill({ json: { success: true, data } })
        })
        const page = await context.newPage()
        page.on('pageerror', (error) => errors.push(String(error)))
        for (const route of [
          '/dashboard/overview',
          '/system-settings/site/system-info',
        ]) {
          await page.goto(baseUrl + route)
          if (width === 390) {
            await page.locator('[data-slot="sidebar-trigger"]').click()
          }
          const sidebar = page.locator('[data-sidebar="sidebar"]:visible')
          const parent = sidebar
            .locator('[data-sidebar="menu-button"][aria-expanded]')
            .first()
          await parent.waitFor()
          for (const expanded of [false, true]) {
            if (
              (await parent.getAttribute('aria-expanded')) !== String(expanded)
            ) {
              await parent.click()
            }
            assert.equal(
              await parent.getAttribute('aria-expanded'),
              String(expanded)
            )
            for (const state of ['rest', 'hover', 'focus']) {
              await page.mouse.move(width - 2, 998)
              if (state === 'rest') {
                await parent.evaluate((element) => element.blur())
              } else if (state === 'hover') {
                await parent.hover()
              } else {
                await page.keyboard.press('Tab')
                await parent.focus()
              }
              await checkContrast(
                parent,
                `${theme}/${width}/${route}/expanded=${expanded}/${state}`,
                results
              )
            }
          }
          await page.screenshot({
            path: path.join(
              output,
              `${theme}-${width}-${route.includes('system-settings') ? 'settings' : 'chat'}.png`
            ),
          })
          if (width === 390) await page.keyboard.press('Escape')
        }
        await context.close()
      }
    }
    assert.deepEqual(errors, [])
    fs.writeFileSync(
      path.join(output, 'results.json'),
      JSON.stringify({ results, errors }, null, 2)
    )
    process.stdout.write(
      `Passed ${results.length} submenu contrast checks; no page errors.\n`
    )
  } finally {
    await browser.close()
  }
})().catch((error) => {
  process.stderr.write(`${error}\n`)
  process.exitCode = 1
})
