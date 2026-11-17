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
    let footerSettings = {}
    await context.route('**/api/**', async (route) => {
      const path = new URL(route.request().url()).pathname
      let data
      if (path === '/api/status') {
        data = {
          ...footerSettings,
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
          if (
            !(await footer.textContent()).includes(
              `${new Date().getFullYear()} 银月 AI Gateway.`
            )
          ) {
            throw new Error('Footer is missing current year or gateway brand')
          }
          if (!(await footer.textContent()).includes('漫步云端与星月之间。')) {
            throw new Error('Footer slogan is missing')
          }
          if (/SLA|99\.98/.test(await footer.textContent())) {
            throw new Error('Footer presents an unverified SLA')
          }
          const protocolTabs = page.getByRole('tablist', { name: 'API 协议' })
          const boundsByTab = await protocolTabs
            .getByRole('tab')
            .evaluateAll((tabs) =>
              tabs.map((tab) => {
                const rect = tab.getBoundingClientRect()
                return { width: rect.width, height: rect.height, top: rect.top }
              })
            )
          if (
            boundsByTab.length !== 4 ||
            boundsByTab.some((rect) => rect.height < 44)
          ) {
            throw new Error('Protocol tabs are missing or too small for touch')
          }
          const tabContrast = await protocolTabs
            .getByRole('tab')
            .evaluateAll((tabs) =>
              tabs.map((tab) => {
                const context = document
                  .createElement('canvas')
                  .getContext('2d')
                context.fillStyle = '#fff'
                context.fillRect(0, 0, 2, 1)
                const ancestors = []
                for (let node = tab; node; node = node.parentElement) {
                  ancestors.unshift(node)
                }
                for (const node of ancestors) {
                  context.fillStyle = getComputedStyle(node).backgroundColor
                  context.fillRect(0, 0, 2, 1)
                }
                context.fillStyle = getComputedStyle(tab).color
                context.fillRect(1, 0, 1, 1)
                const luminance = (offset) => {
                  const rgb = [...context.getImageData(offset, 0, 1, 1).data]
                    .slice(0, 3)
                    .map((value) => {
                      const channel = value / 255
                      return channel <= 0.04045
                        ? channel / 12.92
                        : ((channel + 0.055) / 1.055) ** 2.4
                    })
                  return rgb[0] * 0.2126 + rgb[1] * 0.7152 + rgb[2] * 0.0722
                }
                const bg = luminance(0),
                  fg = luminance(1)
                return (Math.max(bg, fg) + 0.05) / (Math.min(bg, fg) + 0.05)
              })
            )
          if (tabContrast.some((ratio) => ratio < 4.5)) {
            throw new Error(
              `Protocol labels lack contrast: ${JSON.stringify(tabContrast)}`
            )
          }
          const tabRows = new Set(
            boundsByTab.map((rect) => Math.round(rect.top))
          ).size
          if (tabRows !== (width <= 480 ? 2 : 1)) {
            throw new Error(`Unexpected protocol tab layout at ${width}px`)
          }
          for (const [label, requestPath] of [
            ['OpenAI Chat', '/v1/chat/completions'],
            ['OpenAI Responses', '/v1/responses'],
            ['Anthropic', '/v1/messages'],
            ['Gemini', '/v1beta/models/YOUR_MODEL_NAME:generateContent'],
          ]) {
            const tab = protocolTabs.getByRole('tab', {
              name: label,
              exact: true,
            })
            await tab.click()
            await page
              .getByRole('tabpanel', { name: label, exact: true })
              .waitFor()
            if ((await tab.getAttribute('aria-selected')) !== 'true') {
              throw new Error('Protocol selection is not accessible')
            }
            const example = page
              .getByRole('tabpanel', { name: label, exact: true })
              .locator('.silvermoon-integration-code')
            await example.locator('pre code span').first().waitFor()
            const coloring = await example.evaluate((element) => {
              const colors = [
                ...new Set(
                  [...element.querySelectorAll('pre code span')]
                    .filter((token) => token.textContent.trim())
                    .map((token) => getComputedStyle(token).color)
                ),
              ]
              const surfaces = getComputedStyle(element).backgroundImage.match(
                /rgb\([^)]+\)/g
              ) || [getComputedStyle(element).backgroundColor]
              const context = document.createElement('canvas').getContext('2d')
              const luminance = (color) => {
                context.fillStyle = color
                context.fillRect(0, 0, 1, 1)
                const rgb = [...context.getImageData(0, 0, 1, 1).data]
                  .slice(0, 3)
                  .map((value) => {
                    const channel = value / 255
                    return channel <= 0.04045
                      ? channel / 12.92
                      : ((channel + 0.055) / 1.055) ** 2.4
                  })
                return rgb[0] * 0.2126 + rgb[1] * 0.7152 + rgb[2] * 0.0722
              }
              const contrasts = colors.flatMap((color) =>
                surfaces.map((surface) => {
                  const fg = luminance(color),
                    bg = luminance(surface)
                  return (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05)
                })
              )
              return {
                count: colors.length,
                minimumContrast: Math.min(...contrasts),
              }
            })
            if (coloring.count < 4 || coloring.minimumContrast < 4.5) {
              throw new Error(
                `Unreadable or missing code colors: ${JSON.stringify(coloring)}`
              )
            }
            const request = await example.locator('pre').textContent()
            if (!request.includes(requestPath)) {
              throw new Error(`Wrong request for ${label}`)
            }
            if (
              await page.evaluate(
                () => document.documentElement.scrollWidth > innerWidth
              )
            ) {
              throw new Error(
                `Protocol example overflows at ${width}px: ${label}`
              )
            }
          }
          await protocolTabs
            .getByRole('tab', { name: 'OpenAI Chat', exact: true })
            .click()
          const activeCode = page
            .getByRole('tabpanel', { name: 'OpenAI Chat', exact: true })
            .locator('.silvermoon-integration-code pre')
          await activeCode.focus()
          if (
            !(await activeCode.evaluate((e) => e === document.activeElement))
          ) {
            throw new Error('Code example is not keyboard focusable')
          }
          if (
            (await footer
              .locator('a[href="https://github.com/QuantumNous/new-api"]')
              .count()) ||
            /New API|由项目贡献者/.test(await footer.textContent())
          ) {
            throw new Error(
              'Removed project attribution is still rendered in the footer'
            )
          }
        }
        results.push({ mode, target, ...size })
        if (
          size.scroll > width ||
          size.main.some((s) => s.scroll > s.client + 1)
        ) {
          throw new Error(`Overflow ${JSON.stringify(results.at(-1))}`)
        }
        if ([390, 1440].includes(width)) {
          await page.evaluate(() => {
            document.activeElement?.blur()
            window.scrollTo({ top: 0, behavior: 'instant' })
          })
          await page.screenshot({
            path: `${out}/${target}-${mode}-${width}.png`,
            fullPage: true,
          })
          if (target === 'home') {
            await page.locator('.silvermoon-integration-panel').screenshot({
              path: `${out}/integration-${mode}-${width}.png`,
            })
          }
        }
      }
      if (target === 'home') {
        await page.locator('.silvermoon-endpoint button').click()
        const copied = await page.evaluate(() => navigator.clipboard.readText())
        if (copied !== endpoint) throw new Error('Copy mismatch')
        for (const label of [
          'OpenAI Chat',
          'OpenAI Responses',
          'Anthropic',
          'Gemini',
        ]) {
          await page.getByRole('tab', { name: label, exact: true }).click()
          const panel = page.getByRole('tabpanel', { name: label, exact: true })
          await panel.locator('.silvermoon-integration-code button').click()
          const copiedCode = await page.evaluate(() =>
            navigator.clipboard.readText()
          )
          const displayedCode = await panel
            .locator('.silvermoon-integration-code pre')
            .textContent()
          if (
            copiedCode !== displayedCode ||
            !copiedCode.includes('YOUR_MODEL_NAME')
          ) {
            throw new Error(`Code example copy mismatch for ${label}`)
          }
        }
        await page
          .getByRole('tab', { name: 'OpenAI Chat', exact: true })
          .click()
        await page.keyboard.press('ArrowRight')
        await page.keyboard.press('Enter')
        if (
          (await page
            .getByRole('tab', { name: 'OpenAI Responses', exact: true })
            .getAttribute('aria-selected')) !== 'true'
        ) {
          throw new Error('Protocol tabs are not keyboard operable')
        }
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
        for (const legalEnabled of [false, true]) {
          footerSettings = {
            footer_html: '<p>银月自定义页脚</p>',
            user_agreement_enabled: legalEnabled,
            privacy_policy_enabled: legalEnabled,
          }
          await page.reload()
          const customFooter = page.locator('.silvermoon-footer')
          await customFooter.getByText('银月自定义页脚').waitFor()
          if (
            /New API|由项目贡献者/.test(await customFooter.textContent()) ||
            (await customFooter
              .locator('a[href="https://github.com/QuantumNous/new-api"]')
              .count())
          ) {
            throw new Error('Custom footer appends removed project attribution')
          }
          const legalCount = await customFooter
            .locator('a[href="/user-agreement"], a[href="/privacy-policy"]')
            .count()
          if (legalCount !== (legalEnabled ? 2 : 0)) {
            throw new Error(
              'Custom footer legal links no longer follow settings'
            )
          }
          if (
            await page.evaluate(
              () => document.documentElement.scrollWidth > innerWidth
            )
          ) {
            throw new Error('Custom footer causes horizontal overflow')
          }
        }
        footerSettings = {}
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
