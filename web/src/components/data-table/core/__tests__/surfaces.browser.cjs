/* oxlint-disable typescript/no-require-imports -- Uses the shared standalone browser fixture runner. */
const assert = require('node:assert/strict')

exports.reviewTableSurfaces = async ({ page, baseUrl, mode, out, now }) => {
  page.setDefaultTimeout(30000)
  const results = []
  let empty = false
  await page.context().route(/\/api\/token\/?(?:\?.*)?$/, (route) =>
    route.fulfill({
      json: {
        success: true,
        data: {
          items: [
            {
              id: 1,
              name: 'App key',
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
          ],
          total: 1,
          page: 1,
          page_size: 20,
        },
      },
    })
  )
  await page.context().route('**/api/audit/**', (route) =>
    route.fulfill({
      json: {
        success: true,
        data: {
          items: empty
            ? []
            : Array.from({ length: 9 }, (_, i) => ({
                event_id: `review-${i}`,
                user_id: 1,
                username: 'traveler',
                actor_role: 1,
                created_at: now - i * 3600,
                category: 'auth',
                action: 'login',
                token_ref: '',
                auth_method: 'password',
                ip: '192.0.2.10',
                user_agent: 'Browser layout review',
                method: 'POST',
                route: '/api/user/login',
                status: 200,
                success: true,
                request_id: `review-${i}`,
                content: 'Visual review event',
                other: null,
              })),
          total: empty ? 0 : 9,
        },
      },
    })
  )
  for (const route of ['/usage-logs/audit', '/keys']) {
    for (const width of [390, 768, 1440]) {
      await page.setViewportSize({ width, height: 900 })
      await page.goto(baseUrl + route)
      const label = route === '/keys' ? 'App key' : 'traveler'
      await page.getByText(label, { exact: true }).first().waitFor()
      if (process.env.SILVERMOON_REVIEW_FONT) {
        await page.addStyleTag({
          content:
            "@font-face{font-family:'Review Sans';src:url('/__review_font.ttf')}body{font-family:'Review Sans',sans-serif;--font-body:'Review Sans',sans-serif}",
        })
        await page.evaluate(() => document.fonts.ready)
      }
      await page.mouse.move(0, 0)
      await page.evaluate(async () => {
        await Promise.allSettled(
          document
            .getAnimations()
            .filter(
              (a) => a.effect?.getComputedTiming().iterations !== Infinity
            )
            .map((a) => a.finished)
        )
      })
      await page.screenshot({
        path: `${out}/${route === '/keys' ? 'keys' : 'audit'}-${width}.png`,
      })
      const row = page
        .locator('[data-slot="table-body"] [data-slot="table-row"]')
        .first()
      assert.ok(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1
        ),
        'Lists must not overflow the viewport'
      )
      if (width >= 768) {
        const background = await row.evaluate(
          (el) => getComputedStyle(el).backgroundColor
        )
        // Browser serializes alpha in rgba() or color(srgb ... / alpha).
        const alpha = background.startsWith('rgba')
          ? Number(background.split(',').at(-1).replace(')', ''))
          : Number(background.split('/')[1]?.replace(')', '') ?? 1)
        if (mode !== 'default') {
          assert.ok(
            alpha <= 0.5,
            `Ordinary rows must let the scene show through: ${background}`
          )
        }
        await row.hover()
        await page.evaluate(async () => {
          await Promise.allSettled(
            document
              .getAnimations()
              .filter(
                (a) => a.effect?.getComputedTiming().iterations !== Infinity
              )
              .map((a) => a.finished)
          )
        })
        const hover = await row.evaluate(
          (el) => getComputedStyle(el).backgroundColor
        )
        assert.notEqual(
          hover,
          background,
          'Hovered row must remain distinguishable'
        )
        const pinned = row.locator('[data-slot="table-cell"].sticky').first()
        if (await pinned.count()) {
          const color = await pinned.evaluate(
            (el) => getComputedStyle(el).backgroundColor
          )
          assert.ok(
            (!color.startsWith('rgba(') && !color.includes('/')) ||
              color.endsWith('/ 1)'),
            `Pinned cells must cover scrolling text: ${color}`
          )
        }
        if (route === '/keys') {
          const select = row.getByRole('checkbox')
          await select.click()
          await page.mouse.move(0, 0)
          assert.equal(await row.getAttribute('data-state'), 'selected')
          assert.notEqual(
            await row.evaluate((el) => getComputedStyle(el).backgroundColor),
            background
          )
        }
      } else if (mode !== 'default') {
        const surface = await page
          .getByText(label, { exact: true })
          .first()
          .evaluate((el) => {
            for (
              let current = el;
              current && !current.matches('[data-slot="data-table-page"]');
              current = current.parentElement
            ) {
              const background = getComputedStyle(current).backgroundColor
              if (
                background !== 'rgba(0, 0, 0, 0)' &&
                background !== 'transparent'
              ) {
                return background
              }
            }
            return 'transparent'
          })
        assert.match(
          surface,
          /rgba\([^)]*,\s*0\.[0-7]\d*\)/,
          `Mobile row must reveal the scene: ${surface}`
        )
      }
      results.push({ mode, route, width })
    }
  }
  empty = true
  await page.goto(`${baseUrl}/usage-logs/audit`)
  await page.locator('[data-slot="empty"]').waitFor()
  assert.ok(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1
    )
  )
  return results
}
