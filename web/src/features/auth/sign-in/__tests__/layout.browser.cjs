/* oxlint-disable typescript/no-require-imports -- Uses the shared standalone browser fixture runner. */
const assert = require('node:assert/strict')

exports.reviewSignIn = async ({
  page,
  baseUrl,
  mode,
  out,
  setStatus,
  user,
  now,
}) => {
  page.setDefaultTimeout(30000)
  const results = []
  const widths = process.env.SILVERMOON_REVIEW_WIDTHS
    ? process.env.SILVERMOON_REVIEW_WIDTHS.split(',').map(Number)
    : [320, 390, 1440]
  for (const width of widths) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto(`${baseUrl}/sign-in?redirect=%2Fpricing%3Fview%3Dtable`)
    await page.locator('.silvermoon-auth-panel input[name=username]').waitFor()
    await page.waitForFunction(() => {
      const image = document.querySelector('.silvermoon-auth-artwork')
      return image?.complete && image.naturalWidth > 0
    })
    if (process.env.SILVERMOON_REVIEW_FONT) {
      await page.addStyleTag({
        content:
          "@font-face{font-family:'Review Sans';src:url('/__review_font.ttf')} body{font-family:'Review Sans',sans-serif;--font-body:'Review Sans',sans-serif}",
      })
      await page.evaluate(() => document.fonts.ready)
    }
    const geometry = await page.evaluate(() => {
      const panel = document
        .querySelector('.silvermoon-auth-panel')
        .getBoundingClientRect()
      const image = document.querySelector('.silvermoon-auth-artwork')
      const header = document
        .querySelector('.silvermoon-auth-header')
        .getBoundingClientRect()
      return {
        width: innerWidth,
        scrollWidth: document.documentElement.scrollWidth,
        panel: { left: panel.left, right: panel.right, top: panel.top },
        headerBottom: header.bottom,
        image: image.getAttribute('src'),
      }
    })
    assert.ok(
      geometry.scrollWidth <= width + 1,
      'Sign-in must not overflow horizontally'
    )
    assert.ok(
      geometry.panel.left >= 0 && geometry.panel.right <= width + 1,
      'Login panel must fit the screen'
    )
    assert.ok(
      geometry.panel.top > geometry.headerBottom,
      'Login form must not overlap navigation'
    )
    assert.equal(
      geometry.image,
      mode === 'crimson-clouds'
        ? '/silvermoon/crimson-clouds.jpg'
        : '/silvermoon/moonlight.webp'
    )
    await page.screenshot({
      path: `${out}/sign-in-${mode}-${width}.png`,
      fullPage: true,
    })
    // A reduced viewport protects scrolling when the phone keyboard is open.
    if (width < 768) {
      await page.setViewportSize({ width, height: 420 })
      await page.locator('input[name=password]').focus()
      await page.locator('button[type=submit]').scrollIntoViewIfNeeded()
      const button = await page.locator('button[type=submit]').boundingBox()
      assert.ok(
        button && button.y >= 0 && button.y + button.height <= 420,
        'Login action must remain reachable with a short viewport'
      )
      await page.setViewportSize({ width, height: 900 })
    }
    results.push({ mode, target: 'sign-in', ...geometry })
  }
  await page.setViewportSize({ width: 390, height: 900 })
  const username = page.locator('input[name=username]')
  const password = page.locator('input[name=password]')
  await username.fill('visual-review-user')
  await password.fill('review-only-password')
  await page.getByRole('button', { name: '切换主题', exact: true }).click()
  const next = mode === 'crimson-clouds' ? 'silvermoon-dream' : 'crimson-clouds'
  await page
    .getByRole('menuitemradio', {
      name: next === 'crimson-clouds' ? '绯月流霞' : '银月梦境',
      exact: true,
    })
    .click()
  await page.waitForFunction(
    (next) =>
      document.querySelector('.silvermoon-auth').dataset.authTheme === next,
    next
  )
  assert.equal(
    await username.inputValue(),
    'visual-review-user',
    'Theme switch must preserve entered username'
  )
  assert.equal(
    await password.inputValue(),
    'review-only-password',
    'Theme switch must preserve entered password'
  )
  assert.equal(await password.getAttribute('type'), 'password')
  const failed = page.waitForResponse((response) =>
    response.url().includes('/api/user/login')
  )
  await page.locator('button[type=submit]').click()
  await failed
  assert.ok(
    page.url().includes('/sign-in?redirect='),
    'Failed login must stay on sign-in'
  )
  await page.waitForFunction(
    () => !document.querySelector('button[type=submit]').disabled
  )

  setStatus({
    register_enabled: false,
    github_oauth: true,
    github_client_id: 'review-only',
  })
  await page.reload()
  await username.waitFor()
  await page.getByRole('button', { name: /GitHub/ }).waitFor()
  assert.equal(
    await page.locator('a[href="/sign-up"]').count(),
    0,
    'Disabled registration must remain hidden'
  )
  await username.fill('visual-review-user')
  await password.fill('review-only-password')
  await page
    .context()
    .route('**/api/user/login?*', (route) =>
      route.fulfill({
        json: {
          success: true,
          data: {
            access_token: 'review-only',
            token_type: 'Bearer',
            access_expires_at: now + 3600,
            user,
            session: {
              sid: 'review-only',
              current: true,
              login_method: 'password',
              ip: '',
              user_agent: '',
              created_at: now,
              last_active_at: now,
              expires_at: now + 3600,
            },
          },
        },
      })
    )
  await page.locator('button[type=submit]').click()
  await page.waitForURL('**/pricing?view=table')
  results.push({
    mode,
    target: 'sign-in-interaction',
    fieldsPreserved: true,
    redirectPreserved: true,
    configuredMethodsRespected: true,
  })
  return results
}
