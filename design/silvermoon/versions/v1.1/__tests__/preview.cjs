// Standalone design-preview checks. Run with Playwright and @sparticuz/chromium
// available on NODE_PATH, or set SILVERMOON_BROWSER_EXECUTABLE for another Chromium.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {pathToFileURL} = require('node:url');
const {chromium: playwright} = require('playwright');

(async () => {
  const root = path.resolve(__dirname, '..');
  const launchOptions = {headless: true};
  if (process.env.SILVERMOON_BROWSER_EXECUTABLE) {
    launchOptions.executablePath = process.env.SILVERMOON_BROWSER_EXECUTABLE;
  } else {
    const chromium = require('@sparticuz/chromium').default;
    launchOptions.executablePath = await chromium.executablePath();
    launchOptions.args = chromium.args;
  }
  const browser = await playwright.launch(launchOptions);
  try {
    const context = await browser.newContext({offline: true, viewport: {width:1440,height:1180}, deviceScaleFactor:1});
    const page = await context.newPage();
    const errors = [];
    const externalRequests = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('request', request => {if (/^https?:/.test(request.url())) externalRequests.push(request.url());});
    await page.goto(pathToFileURL(path.join(root, 'index.html')).href);
    await page.evaluate(() => document.fonts.ready);
    assert.equal(await page.locator('img').evaluateAll(images => images.every(image => image.complete && image.naturalWidth > 0)), true);
    assert.equal(await page.evaluate(() => document.fonts.check('16px "Noto Sans SC"', '银月') && document.fonts.check('32px "Noto Serif SC"', '银月')), true);
    await page.screenshot({path:path.join(root,'screenshots/desktop-home.png')});
    await page.screenshot({path:path.join(root,'screenshots/desktop-home-full.png'),fullPage:true});
    await page.getByRole('button',{name:'控制台设计',exact:true}).click();
    assert.equal(await page.locator('#previous-version').getAttribute('href'),'../v1.0/index.html#console');
    await page.screenshot({path:path.join(root,'screenshots/desktop-console.png'),fullPage:true});
    const layouts = [];
    for (const width of [320,360,390,430,768,1024,1440]) {
      await page.setViewportSize({width,height:1000});
      for (const name of ['首页设计','控制台设计']) {
        await page.getByRole('button',{name,exact:true}).click();
        const size = await page.evaluate(() => ({width:document.documentElement.clientWidth,content:document.documentElement.scrollWidth}));
        assert.ok(size.content <= size.width, `${width}px ${name}: horizontal overflow ${JSON.stringify(size)}`);
        layouts.push({width,view:name,horizontal_overflow:false});
        if (width === 390) await page.screenshot({path:path.join(root,'screenshots',name === '首页设计' ? 'mobile-home.png' : 'mobile-console.png')});
      }
    }
    await page.setViewportSize({width:390,height:844});
    await page.getByRole('button',{name:'首页设计',exact:true}).click();
    await page.getByRole('link',{name:'查看模型与价格 ↗',exact:true}).first().click();
    const offset = await page.locator('#models').evaluate(element => element.getBoundingClientRect().top);
    assert.ok(offset >= 0 && offset < 35, `Model section anchor was not brought into view: ${offset}`);
    assert.equal(await page.locator('.model-card:visible').count(),3);
    await page.getByRole('button',{name:'编程开发',exact:true}).click();
    assert.equal(await page.locator('.model-card:visible').count(),2);
    await page.getByRole('searchbox',{name:'搜索模型'}).fill('Gemini');
    assert.equal(await page.locator('.model-card:visible').count(),0);
    assert.equal(await page.getByRole('heading',{name:'没有找到匹配的模型'}).isVisible(),true);
    await page.getByRole('button',{name:'清除筛选',exact:true}).click();
    assert.equal(await page.locator('.model-card:visible').count(),3);
    assert.equal(await page.getByRole('searchbox',{name:'搜索模型'}).inputValue(),'');
    await page.getByRole('searchbox',{name:'搜索模型'}).fill('gEmInI');
    assert.equal(await page.locator('.model-card:visible').count(),1);
    assert.match(await page.locator('.model-card:visible').innerText(),/Gemini/);
    await page.getByRole('searchbox',{name:'搜索模型'}).fill('');
    await page.getByRole('button',{name:'多模态',exact:true}).click();
    assert.equal(await page.locator('.model-card:visible').count(),1);
    await page.getByRole('button',{name:'全部',exact:true}).click();
    await page.setViewportSize({width:320,height:640});
    const trigger = page.locator('[data-dialog="gpt"]');
    await trigger.click();
    assert.equal(await page.getByRole('dialog').isVisible(),true);
    const dialogBox = await page.getByRole('dialog').boundingBox();
    assert.ok(dialogBox.x >= 0 && dialogBox.x + dialogBox.width <= 320);
    assert.ok(dialogBox.y >= 0 && dialogBox.y + dialogBox.height <= 640);
    await page.keyboard.press('Escape');
    assert.equal(await page.getByRole('dialog').isVisible(),false);
    assert.equal(await trigger.evaluate(element => document.activeElement === element),true);
    await page.getByRole('button',{name:'控制台设计',exact:true}).click();
    const menu = page.locator('#console-view .mobile-menu');
    await menu.locator('summary').click();
    assert.equal(await menu.evaluate(element => element.open),true);
    await page.keyboard.press('Escape');
    assert.equal(await menu.evaluate(element => element.open),false);
    await page.getByRole('button',{name:'去充值 ↗',exact:true}).click();
    assert.match(await page.locator('#dialog-note').innerText(),/不会创建订单或发起付款/);
    await page.getByRole('button',{name:'知道了',exact:true}).click();
    await menu.locator('summary').click();
    await menu.getByRole('link',{name:'模型与价格',exact:true}).click();
    assert.equal(await page.locator('#home-view').isVisible(),true);
    assert.equal(await menu.evaluate(element => element.open),false);
    assert.equal(await page.locator('#previous-version').getAttribute('href'),'../v1.0/index.html#home');
    assert.equal(externalRequests.length,0);
    assert.deepEqual(errors,[]);
    const result = {browser:await browser.version(),offline_assets_and_fonts:true,external_requests:0,script_errors:errors,layouts,model_filter_search_empty_reset:true,model_anchor:true,dialog_escape_focus_and_small_screen:true,mobile_menu_escape_and_navigation:true,preview_payment_inert:true,version_link_tracks_view:true};
    fs.writeFileSync(path.join(root,'verification.json'),JSON.stringify(result,null,2)+'\n');
    console.log('PASS: offline assets, 14 responsive layouts, filters/search/empty/reset, anchor navigation, dialog focus and mobile menu, inert payment, version links.');
  } finally {
    await browser.close();
  }
})().catch(error => {console.error(error);process.exitCode = 1;});
