/* oxlint-disable typescript/no-require-imports -- Standalone Node browser check resolves optional tools through NODE_PATH. */
// Browser regression against the built frontend; API responses are test fixtures.
// Requires playwright and @sparticuz/chromium via NODE_PATH. Run after .dev/run bun run build.
// Optional SILVERMOON_REVIEW_FONT supplies a local CJK font on minimal Linux hosts.
// REVIEW_PACKAGE / REVIEW_TARGET / REVIEW_WIDTHS (prefixed SILVERMOON_) split long runs; reports stay isolated.
const { chromium: pw } = require('playwright')
const chromium = require('@sparticuz/chromium').default
const fs = require('node:fs')
const path = require('node:path')
const http = require('node:http')

const repo = path.resolve(__dirname, '../../../../..')
const out = path.join(
  repo,
  'build/dev/silvermoon-theme-review',
  process.env.SILVERMOON_REVIEW_PACKAGE || '.',
  process.env.SILVERMOON_REVIEW_TARGET || '.',
  process.env.SILVERMOON_REVIEW_WIDTHS || '.',
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
    '.jpg': 'image/jpeg',
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
  const packages = process.env.SILVERMOON_REVIEW_PACKAGE
    ? [process.env.SILVERMOON_REVIEW_PACKAGE]
    : ['default', 'silvermoon-dream', 'crimson-clouds']
  for (const mode of packages) {
    const context = await browser.newContext({
      viewport: { width: 1440, height: 1000 },
      colorScheme: 'dark',
      permissions: ['clipboard-read', 'clipboard-write'],
    })
    await context.addInitScript((mode) => {
      localStorage.setItem('i18nextLng', 'zhCN')
      if (!sessionStorage.getItem('review-package-seeded')) {
        localStorage.setItem('silvermoon:theme:v1:package', mode)
        sessionStorage.setItem('review-package-seeded', 'yes')
      }
    }, mode)
    let footerSettings = {}
    let populatedKeys = false
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
      else if (
        path === '/api/home_page_content' ||
        path === '/api/notice' ||
        path === '/api/about'
      ) {
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
        const items = populatedKeys
          ? [
              {
                id: 1,
                name: 'Moonlight review key',
                key: 'masked-review-only',
                status: 1,
                remain_quota: 5000000,
                used_quota: 0,
                unlimited_quota: false,
                expired_time: -1,
                created_time: now,
                accessed_time: now,
                group: 'default',
                model_limits_enabled: false,
                model_limits: '',
                allow_ips: '',
              },
            ]
          : []
        data =
          process.env.SILVERMOON_REVIEW_TARGET === 'keys'
            ? { items, total: items.length, page: 1, page_size: 20 }
            : {
                items: [{ id: 1, name: 'App key', key: 'masked', status: 1 }],
                total: 1,
              }
      } else if (path === '/api/token/auto-groups') {
        data = { groups: ['default'], max_count: 5 }
      } else if (path === '/api/user/self/groups') {
        data = { default: { ratio: 1, desc: 'Default' } }
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
    // Block application scripts: the selected surfaces must already be present
    // after parsing HTML/CSS, before any provider can correct a flash.
    const bootPage = await context.newPage()
    await bootPage.route('**/*.js', (route) => route.abort())
    await bootPage.goto(baseUrl)
    const bootState = await bootPage.evaluate(() => ({
      theme: document.documentElement.dataset.themePackage,
      background: getComputedStyle(document.body)
        .getPropertyValue('--background')
        .trim(),
      mounted: document.querySelector('#root').childElementCount > 0,
    }))
    if (
      bootState.theme !== mode ||
      bootState.mounted ||
      bootState.background !==
        {
          default: '#eeedf6',
          'silvermoon-dream': '#f1ecf8',
          'crimson-clouds': '#f7eaf0',
        }[mode]
    ) {
      throw new Error(`Wrong theme before React: ${JSON.stringify(bootState)}`)
    }
    await bootPage.close()
    const page = await context.newPage()
    page.on('pageerror', (e) => errors.push(String(e)))
    const targets = process.env.SILVERMOON_REVIEW_TARGET
      ? [process.env.SILVERMOON_REVIEW_TARGET]
      : ['home', 'console']
    if (process.env.SILVERMOON_REVIEW_TARGET === 'keys') {
      for (const populated of [false, true]) {
        populatedKeys = populated
        await page.goto(`${baseUrl}/keys`)
        await page
          .getByRole('heading', { name: 'API 密钥', exact: true })
          .waitFor()
        await page
          .getByText(populated ? 'Moonlight review key' : '未找到 API 密钥', {
            exact: true,
          })
          .first()
          .waitFor()
        if (process.env.SILVERMOON_REVIEW_FONT) {
          await page.addStyleTag({
            content:
              "@font-face{font-family:'Review Sans';src:url('/__review_font.ttf')}body{font-family:'Review Sans',sans-serif;--font-sans:'Review Sans',sans-serif;--font-ui-sans:'Review Sans',sans-serif;--font-body:'Review Sans',sans-serif}",
          })
          await page.evaluate(() => document.fonts.ready)
        }
        for (const width of [320, 390, 768, 1440, 1920]) {
          await page.setViewportSize({ width, height: 1000 })
          await page.evaluate(async () => {
            await new Promise((resolve) =>
              requestAnimationFrame(() => requestAnimationFrame(resolve))
            )
            await Promise.allSettled(
              document
                .getAnimations()
                .filter(
                  (animation) =>
                    animation.effect?.getComputedTiming().iterations !==
                    Infinity
                )
                .map((animation) => animation.finished)
            )
          })
          await page
            .getByText(populated ? 'Moonlight review key' : '未找到 API 密钥', {
              exact: true,
            })
            .first()
            .waitFor()
          const surface = await page.evaluate(() => ({
            width: innerWidth,
            scroll: document.documentElement.scrollWidth,
            scene: getComputedStyle(
              document.querySelector('[data-slot="sidebar-wrapper"]'),
              '::before'
            ).backgroundImage,
          }))
          await page.screenshot({
            path: `${out}/keys-${mode}-${populated ? 'rows' : 'empty'}-${width}.png`,
          })
          if (surface.scroll > width) {
            throw new Error(
              `Keys page overflow at ${width}px: ${JSON.stringify(
                await page.evaluate(() =>
                  [...document.querySelectorAll('main *')]
                    .filter(
                      (e) => e.getBoundingClientRect().right > innerWidth + 1
                    )
                    .slice(0, 8)
                    .map((e) => ({
                      tag: e.tagName,
                      slot: e.getAttribute('data-slot'),
                      cls: e.className,
                      width: e.getBoundingClientRect().width,
                    }))
                )
              )}`
            )
          }
          if ((mode !== 'default') !== surface.scene.includes('url(')) {
            throw new Error('Scene is missing or leaked into default theme')
          }
          results.push({
            mode,
            target: 'keys',
            populated,
            width,
            scroll: surface.scroll,
          })
        }
      }
      await page.setViewportSize({ width: 390, height: 844 })
      await page
        .getByRole('button', { name: '创建 API 密钥', exact: true })
        .click()
      await page.getByRole('dialog').waitFor()
      await page.screenshot({ path: `${out}/keys-${mode}-drawer-390.png` })
      await page.keyboard.press('Escape')
      await page.getByRole('dialog').waitFor({ state: 'hidden' })
      await context.close()
      continue
    }
    for (const target of targets) {
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
      const widths = process.env.SILVERMOON_REVIEW_WIDTHS
        ? process.env.SILVERMOON_REVIEW_WIDTHS.split(',').map(Number)
        : [320, 360, 390, 430, 640, 641, 768, 1024, 1440, 1600, 1920]
      for (const width of widths) {
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
        if (target === 'console' && mode === 'silvermoon-dream') {
          const fullMoon = await page
            .locator('.silvermoon-welcome > img')
            .evaluate(async (image) => {
              await image.decode()
              const rect = image.getBoundingClientRect()
              const scale = Math.max(
                rect.width / image.naturalWidth,
                rect.height / image.naturalHeight
              )
              const top = (rect.height - image.naturalHeight * scale) * 0.1
              return top + 88 * scale >= 0 && top + 610 * scale <= rect.height
            })
          if (!fullMoon) {
            throw new Error(`Dream welcome moon is cropped at ${width}px`)
          }
        }
        if (target === 'console' && mode === 'crimson-clouds') {
          const subjectsVisible = await page
            .locator('.silvermoon-welcome > img')
            .evaluate(async (image) => {
              await image.decode()
              const rect = image.getBoundingClientRect()
              const scale = Math.max(
                rect.width / image.naturalWidth,
                rect.height / image.naturalHeight
              )
              const [x, y] = getComputedStyle(image)
                .objectPosition.split(' ')
                .map((value) => Number.parseFloat(value) / 100)
              const left = (rect.width - image.naturalWidth * scale) * x
              const top = (rect.height - image.naturalHeight * scale) * y
              return (
                image.naturalWidth === 2016 &&
                left + 910 * scale >= 0 &&
                left + 1235 * scale <= rect.width &&
                top + 600 * scale >= 0 &&
                top + 1010 * scale <= rect.height
              )
            })
          if (!subjectsVisible) {
            throw new Error(
              `Crimson welcome characters are cropped at ${width}px`
            )
          }
        }
        if (target === 'console') {
          const clipped = await page
            .locator('[data-slot="sidebar-wrapper"] > header')
            .evaluate((header) => {
              const bounds = header.getBoundingClientRect()
              return [...header.querySelectorAll('button')].some((button) => {
                const rect = button.getBoundingClientRect()
                return (
                  rect.width > 0 &&
                  (rect.left < bounds.left ||
                    rect.right > bounds.right ||
                    rect.bottom > bounds.bottom)
                )
              })
            })
          if (clipped) {
            throw new Error(`Console header actions clipped at ${width}px`)
          }
        }
        if (target === 'home') {
          await page.evaluate(() =>
            window.scrollTo({ top: 0, behavior: 'instant' })
          )
          await page
            .locator('.silvermoon-hero-art')
            .evaluate((image) => image.decode())
          const moon = await page
            .locator('.silvermoon-hero-art')
            .evaluate((image) => {
              const rect = image.getBoundingClientRect()
              const style = getComputedStyle(image)
              const scale = Math.max(
                rect.width / image.naturalWidth,
                rect.height / image.naturalHeight
              )
              const [x, y] = style.objectPosition
                .split(' ')
                .map((value) => Number.parseFloat(value) / 100)
              const left =
                rect.left + (rect.width - image.naturalWidth * scale) * x
              const top =
                rect.top + (rect.height - image.naturalHeight * scale) * y
              const hero = image.closest('.silvermoon-hero')
              const heroRect = hero.getBoundingClientRect()
              const headerBottom = document
                .querySelector('.silvermoon-landing > header')
                .getBoundingClientRect().bottom
              const fade = Number.parseFloat(
                getComputedStyle(hero, '::after').height
              )
              // Original moon bounds; the landscape instead protects both full characters.
              // Its giant moon intentionally extends beyond the source image.
              const landscape = image.naturalWidth === 2016
              const bounds = landscape
                ? [910, 1235, 600, 1010]
                : [370, 900, 88, 610]
              return {
                left: left + bounds[0] * scale,
                right: left + bounds[1] * scale,
                top: top + bounds[2] * scale,
                bottom: top + bounds[3] * scale,
                visibleLeft: Math.max(rect.left, heroRect.left),
                visibleRight: Math.min(rect.right, heroRect.right),
                visibleTop: Math.max(rect.top, heroRect.top, headerBottom),
                visibleBottom: Math.min(
                  rect.bottom,
                  heroRect.bottom - (innerWidth > 640 ? fade : 0)
                ),
              }
            })
          if (
            moon.left < moon.visibleLeft ||
            moon.right > moon.visibleRight ||
            moon.top < moon.visibleTop + 4 ||
            moon.bottom > moon.visibleBottom
          ) {
            throw new Error(
              `Artwork subject is cropped or covered at ${width}px: ${JSON.stringify(moon)}`
            )
          }
          const header = page.locator('.silvermoon-public-header')
          for (const scrollTop of [0, 360]) {
            await page.evaluate(
              (value) => window.scrollTo({ top: value, behavior: 'instant' }),
              scrollTop
            )
            await page.waitForFunction(
              (scrolled) =>
                document
                  .querySelector('.silvermoon-public-header')
                  .hasAttribute('data-scrolled') === scrolled,
              scrollTop > 0
            )
            await page.evaluate(async () => {
              await Promise.allSettled(
                document
                  .getAnimations()
                  .filter(
                    (animation) =>
                      animation.effect?.getComputedTiming().iterations !==
                      Infinity
                  )
                  .map((animation) => animation.finished)
              )
            })
            const navigation = await header
              .locator('.silvermoon-public-nav')
              .evaluate((nav) => {
                const bounds = nav.getBoundingClientRect()
                const controls = [...nav.querySelectorAll('a, button')].filter(
                  (control) => control.getBoundingClientRect().width > 0
                )
                return {
                  width: bounds.width,
                  overflow: nav.scrollWidth > nav.clientWidth,
                  clipped: controls.some((control) => {
                    const rect = control.getBoundingClientRect()
                    return (
                      rect.left < bounds.left - 1 ||
                      rect.right > bounds.right + 1 ||
                      rect.top < bounds.top - 1 ||
                      rect.bottom > bounds.bottom + 1
                    )
                  }),
                }
              })
            if (navigation.overflow || navigation.clipped) {
              throw new Error(
                `Header controls clipped at ${width}px / scroll ${scrollTop}`
              )
            }
            const bar = await header.evaluate((element) => {
              const bounds = element.getBoundingClientRect()
              return {
                left: bounds.left,
                right: bounds.right,
                top: bounds.top,
                height: bounds.height,
                background: getComputedStyle(element).backgroundImage,
              }
            })
            if (bar.left !== 0 || bar.right !== width || bar.top !== 0) {
              throw new Error(
                'Public navigation must span the viewport and remain at the top'
              )
            }
            if (bar.height !== (width >= 1024 ? 65 : 57)) {
              throw new Error(
                `Unexpected navigation height at ${width}px: ${bar.height}`
              )
            }
            if ((bar.background !== 'none') !== (scrollTop === 0)) {
              throw new Error(
                'Sky gradient must give way to a readable surface after scrolling'
              )
            }
            if (width >= 1024) {
              await header
                .locator('.silvermoon-nav-link[aria-current="page"][href="/"]')
                .waitFor()
              if (width >= 1440) {
                const menuBounds = await header
                  .locator('.silvermoon-nav-desktop')
                  .boundingBox()
                if (
                  Math.abs(menuBounds.x + menuBounds.width / 2 - width / 2) > 2
                ) {
                  throw new Error(
                    'Desktop navigation links must remain centered between brand and actions'
                  )
                }
              }
              if (scrollTop && width >= 1440 && navigation.width < 1000) {
                throw new Error('Scrolled header is too cramped')
              }
            } else {
              const touchSizes = await header
                .locator('.silvermoon-nav-mobile > button')
                .evaluateAll((buttons) =>
                  buttons.map((button) => {
                    const rect = button.getBoundingClientRect()
                    return { width: rect.width, height: rect.height }
                  })
                )
              if (
                touchSizes.some((rect) => rect.width < 44 || rect.height < 44)
              ) {
                throw new Error('Mobile header controls are too small')
              }
            }
            if ([390, 1440].includes(width)) {
              await header.screenshot({
                path: `${out}/header-${mode}-${width}-${scrollTop ? 'scrolled' : 'top'}.png`,
              })
            }
          }
          await page.evaluate(() =>
            window.scrollTo({ top: 0, behavior: 'instant' })
          )
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
        if ([390, 641, 1440, 1600].includes(width)) {
          await page.evaluate(() => {
            document.activeElement?.blur()
            window.scrollTo({ top: 0, behavior: 'instant' })
          })
          await page.screenshot({
            path: `${out}/${target}-${mode}-${width}.png`,
            fullPage: true,
          })
          if (target === 'home') {
            await page
              .locator('.silvermoon-hero')
              .screenshot({ path: `${out}/hero-${mode}-${width}.png` })
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
        const menu = page.getByRole('button', { name: '切换导航菜单' })
        await menu.click()
        if ((await menu.getAttribute('aria-expanded')) !== 'true') {
          throw new Error('Mobile menu expanded state is missing')
        }
        await page
          .locator('nav a[href="/pricing"]:visible')
          .last()
          .click({ trial: true })
        await page
          .locator('.silvermoon-mobile-navigation')
          .evaluate(async (menu) => {
            await Promise.allSettled(
              menu
                .getAnimations({ subtree: true })
                .map((animation) => animation.finished)
            )
          })
        await page.screenshot({ path: `${out}/public-menu-${mode}-390.png` })
        await menu.click()
        if ((await menu.getAttribute('aria-expanded')) !== 'false') {
          throw new Error('Mobile menu did not close')
        }
        if (
          !(await page
            .locator('.silvermoon-mobile-navigation')
            .evaluate((element) => element.inert))
        ) {
          throw new Error('Closed menu still accepts keyboard focus')
        }
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
        // Mobile package chooser must remain accessible and preserve page state.
        const welcome = await page.locator('.silvermoon-welcome').textContent()
        await page.getByRole('button', { name: '打开主题设置' }).click()
        await page.getByRole('radio', { name: '银月梦境', exact: true }).click()
        await page.waitForFunction(
          () => document.body.dataset.themePreset === 'silvermoon-dream'
        )

        await page
          .getByRole('radiogroup', { name: '主题', exact: true })
          .getByRole('radio', { name: '默认', exact: true })
          .click()
        await page.waitForFunction(
          () => !document.body.hasAttribute('data-theme-preset')
        )
        // Arrow keys use the shared radio group keyboard behavior.
        await page
          .getByRole('radiogroup', { name: '主题', exact: true })
          .getByRole('radio', { name: '默认', exact: true })
          .focus()
        await page.keyboard.press('ArrowDown')
        await page.waitForFunction(
          () => document.body.dataset.themePreset === 'silvermoon-dream'
        )
        if (mode !== 'silvermoon-dream') {
          await page
            .getByRole('radiogroup', { name: '主题', exact: true })
            .getByRole('radio', {
              name: mode === 'default' ? '默认' : '绯月流霞',
              exact: true,
            })
            .click()
        }
        await page.screenshot({ path: `${out}/theme-picker-${mode}-390.png` })
        await page.keyboard.press('Escape')
        await page.getByRole('dialog').waitFor({ state: 'hidden' })
        if (
          welcome !== (await page.locator('.silvermoon-welcome').textContent())
        ) {
          throw new Error('Package switch changed page content')
        }
        await page.reload()
        await page.locator('.silvermoon-welcome').waitFor()
        if (process.env.SILVERMOON_REVIEW_FONT) {
          await page.addStyleTag({
            content:
              "@font-face{font-family:'Review Sans';src:url('/__review_font.ttf')} body{font-family:'Review Sans',sans-serif;--font-serif:'Review Sans',serif;--font-sans:'Review Sans',sans-serif;--font-ui-sans:'Review Sans',sans-serif;--font-body:'Review Sans',sans-serif}",
          })
          await page.evaluate(() => document.fonts.ready)
        }
        if (
          (await page.locator('html').getAttribute('data-theme-package')) !==
          mode
        ) {
          throw new Error('Package was not restored on refresh')
        }
        await page.emulateMedia({ colorScheme: 'light' })
        await page.emulateMedia({ colorScheme: 'dark' })
        if (
          !(await page.evaluate(
            () =>
              document.documentElement.classList.contains('light') &&
              !document.documentElement.classList.contains('dark')
          ))
        ) {
          throw new Error('OS appearance changed package surface mode')
        }
        const trigger = page.locator('[data-slot="sidebar-trigger"]').first()
        await trigger.click()
        await page.locator('[data-mobile="true"]').waitFor()
        await page.evaluate(async () => {
          await Promise.allSettled(
            document
              .getAnimations()
              .filter(
                (animation) =>
                  animation.effect?.getComputedTiming().iterations !== Infinity
              )
              .map((animation) => animation.finished)
          )
        })
        await page.screenshot({
          path: `${out}/console-${mode}-mobile-menu.png`,
        })
        await page.keyboard.press('Escape')
        await page.setViewportSize({ width: 1440, height: 1000 })
        await page.locator('a[href="/"]:visible').first().click()
        const publicHeader = page.locator('.silvermoon-public-header')
        await publicHeader.locator('[data-slot="avatar"]').first().waitFor()
        await publicHeader
          .locator('.silvermoon-nav-tools button:has([data-slot="avatar"])')
          .click()
        await page
          .getByRole('menuitem', { name: '个人资料', exact: true })
          .waitFor()
        await page.keyboard.press('Escape')
        await page.getByRole('menu').waitFor({ state: 'hidden' })
        for (const label of ['更改语言', '切换主题']) {
          await publicHeader.getByRole('button', { name: label }).click()
          await page.getByRole('menu', { name: label }).waitFor()
          await page.keyboard.press('Escape')
          await page
            .getByRole('menu', { name: label })
            .waitFor({ state: 'hidden' })
        }
        await publicHeader
          .getByRole('button', { name: '通知', exact: true })
          .click()
        await page.getByRole('dialog').waitFor()
        await page.keyboard.press('Escape')
        await page.getByRole('dialog').waitFor({ state: 'hidden' })
        await page.evaluate(() =>
          window.scrollTo({ top: 360, behavior: 'instant' })
        )
        await page.waitForFunction(() =>
          document
            .querySelector('.silvermoon-public-header')
            .hasAttribute('data-scrolled')
        )
        await page.evaluate(async () => {
          await Promise.allSettled(
            document
              .getAnimations()
              .filter(
                (animation) =>
                  animation.effect?.getComputedTiming().iterations !== Infinity
              )
              .map((animation) => animation.finished)
          )
        })
        await publicHeader.screenshot({
          path: `${out}/header-${mode}-authenticated.png`,
        })
        await page.locator('.silvermoon-nav-link[href="/about"]').click()
        await page.waitForURL('**/about')
        await page.locator('.silvermoon-hero').waitFor({ state: 'hidden' })
        const aboutHeader = page.locator('.silvermoon-public-header')
        await aboutHeader
          .locator('a[aria-current="page"][href="/about"]')
          .first()
          .waitFor()
        if (
          (await aboutHeader.evaluate(
            (element) => getComputedStyle(element).backgroundImage
          )) !== 'none'
        ) {
          throw new Error(
            'Plain public pages must not inherit the sky gradient'
          )
        }
        await aboutHeader.screenshot({
          path: `${out}/header-${mode}-about.png`,
        })
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
  server.closeAllConnections()
  process.stdout.write(
    `${JSON.stringify({ checks: results.length, errors, unknown: [...unknown] })}\n`
  )
  if (errors.length) process.exitCode = 1
})().catch((e) => {
  server.close()
  process.stderr.write(`${String(e)}\n`)
  process.exit(1)
})
