// Browser checks for the dashboard against the simulated server. Not part of
// CI (it needs a Chromium); run it by hand after UI changes:
//
//   MPING_WEB_DEV_PORT=8090 go test -run TestDevServer -timeout 0 ./internal/web &
//   PLAYWRIGHT_CORE=/path/to/node_modules/playwright-core \
//     [CHROMIUM_PATH=/path/to/chrome] node internal/web/jstest/e2e.mjs http://127.0.0.1:8090/
//
// Exits non-zero on the first failed check. Screenshots go to $E2E_OUT when set.
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const base = process.argv[2] ?? "http://127.0.0.1:8090/";
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_CORE ?? "playwright-core");
const out = process.env.E2E_OUT;
const shot = async (page, name) => out && page.screenshot({ path: `${out}/${name}.png` });

const browser = await chromium.launch(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {});
const problems = [];
let dialogs = 0;

async function open(opts) {
  const page = await browser.newPage(opts);
  page.on("console", (m) => ["error", "warning"].includes(m.type()) && problems.push(m.text()));
  page.on("pageerror", (e) => problems.push(e.message));
  page.on("dialog", async (d) => { dialogs++; await d.dismiss(); });
  await page.goto(base);
  await page.waitForSelector("tr.target-row");
  await page.waitForTimeout(2500); // first sparkline refresh
  return page;
}

const check = async (name, fn) => {
  await fn();
  console.log(`ok - ${name}`);
};

try {
  for (const colorScheme of ["light", "dark"]) {
    const page = await open({ viewport: { width: 1400, height: 900 }, colorScheme });
    await shot(page, `main-${colorScheme}`);
    await page.close();
  }

  const page = await open({ viewport: { width: 1400, height: 900 } });

  await check("live badge and grouped rows", async () => {
    assert.match(await page.textContent("#badge"), /Live/);
    assert.deepEqual(await page.$$eval("tr.group-row", (r) => r.map((x) => x.textContent)), ["core (2)", "internet (3)", "Ungrouped (1)"]);
    assert.equal(await page.$$eval("tr.target-row", (r) => r.length), 6);
    assert.ok(await page.isVisible("#http-panel"));
  });

  await check("untrusted names render as text", async () => {
    const hosts = await page.$$eval(".host", (h) => h.map((x) => x.textContent));
    assert.ok(hosts.includes("<script>alert(1)</script>"));
    assert.equal(await page.$$eval("img", (i) => i.length), 0);
    assert.equal(dialogs, 0);
  });

  await check("text selection survives live updates", async () => {
    const before = await page.evaluate(() => {
      const cell = document.querySelector("tr.target-row td.col-ip");
      const range = document.createRange();
      range.selectNodeContents(cell);
      getSelection().removeAllRanges();
      getSelection().addRange(range);
      return getSelection().toString();
    });
    await page.waitForTimeout(2500);
    const after = await page.evaluate(() => getSelection().toString());
    assert.equal(after, before);
    const sent = () => page.$eval("tr.target-row td.col-sent", (td) => Number(td.textContent));
    const held = await sent();
    await page.evaluate(() => getSelection().removeAllRanges());
    await page.waitForTimeout(1500);
    assert.ok((await sent()) > held, "table resumes updating once the selection is cleared");
  });

  await check("detail drawer: chart, hops, tooltip, deep link", async () => {
    await page.click("tr.target-row >> nth=2");
    await page.waitForTimeout(2500);
    assert.deepEqual(await page.$$eval(".detail-section h3", (h) => h.map((x) => x.textContent)), ["Summary", "RTT", "MTR", "Events"]);
    assert.match(await page.evaluate(() => location.hash), /^#target-\d+$/);
    const box = await page.locator("canvas.chart").boundingBox();
    await page.mouse.move(box.x + box.width * 0.6, box.y + box.height / 2);
    assert.match(await page.textContent(".tooltip"), /ms|Lost/);
    await shot(page, "detail");
  });

  await check("switching targets never shows the previous target's events", async () => {
    const firstIP = await page.$eval("tr.target-row >> nth=0", (r) => r.querySelector(".col-ip").textContent);
    await page.route("**/events", async (route) => {
      await new Promise((r) => setTimeout(r, 800));
      await route.continue().catch(() => {});
    });
    await page.click("tr.target-row >> nth=0");
    await page.click("tr.target-row >> nth=1");
    await page.waitForTimeout(2000);
    const events = await page.textContent("#detail-body .events, #detail-body .detail-section:last-child");
    assert.ok(!events.includes(firstIP), `drawer for target 2 shows target 1's events (${firstIP})`);
    await page.unroute("**/events");
  });

  await check("ports section and Escape closes the drawer", async () => {
    await page.click("tr.target-row >> nth=0");
    await page.waitForTimeout(1000);
    const sections = await page.$$eval(".detail-section h3", (h) => h.map((x) => x.textContent));
    assert.ok(sections.includes("Ports"));
    await page.click("#detail-close");
    await page.click("tr.target-row >> nth=0");
    await page.keyboard.press("Escape");
    await page.waitForTimeout(200);
    assert.ok(await page.isHidden("#detail"));
  });

  await check("filter narrows the table", async () => {
    await page.fill("#filter", "flaky");
    await page.waitForTimeout(300);
    assert.equal(await page.$$eval("tr.target-row", (r) => r.length), 1);
    await page.fill("#filter", "");
  });
  await page.close();

  const mobile = await open({ viewport: { width: 390, height: 844 } });
  await check("narrow screens keep the key columns without page overflow", async () => {
    assert.equal(await mobile.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    const headers = await mobile.$$eval("#targets th", (h) => h.filter((x) => x.offsetParent).map((x) => x.textContent));
    assert.deepEqual(headers, ["Status", "Host", "Loss %", "Recv", "Last ms", "Avg ms", "Jitter ms", "RTT trend"]);
    await shot(mobile, "mobile");
  });

  await check("no console errors", async () => assert.deepEqual(problems, []));
} finally {
  await browser.close();
}
