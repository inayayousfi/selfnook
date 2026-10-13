// Keeps one browser page open and runs posted code against it, so sign-in
// cookies survive between steps. POST /run: the body is an async function
// body that receives page and context; its return value comes back as JSON.
// Every run ends with a screenshot in /out/shot.png.
const http = require('http');
const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch();
  const context = await browser.newContext({ viewport: { width: 1400, height: 900 } });
  let page = await context.newPage();
  http.createServer((req, res) => {
    let body = '';
    req.on('data', c => body += c);
    req.on('end', async () => {
      let out;
      try {
        const fn = new Function('page', 'context', `return (async () => { ${body} })()`);
        out = { ok: true, value: await fn(page, context) };
      } catch (e) {
        out = { ok: false, error: String(e && e.stack || e) };
      }
      try { await page.screenshot({ path: '/out/shot.png' }); } catch (e) {}
      out.url = page.url();
      res.end(JSON.stringify(out, null, 1));
    });
  }).listen(9000);
})();
