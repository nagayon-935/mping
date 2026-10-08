// Browser checks for the dashboard against the simulated server. Not part of
// CI (it needs a Chromium); run it by hand after UI changes:
//
//   MPING_WEB_DEV_PORT=8090 MPING_WEB_DEV_TOKEN=e2e-token \
//     go test -run TestDevServer -timeout 0 ./internal/web &
//   E2E_TOKEN=e2e-token PLAYWRIGHT_CORE=/path/to/node_modules/playwright-core \
//     [CHROMIUM_PATH=/path/to/chrome] node internal/web/jstest/e2e.mjs http://127.0.0.1:8090/
//
// The control checks add, delete and reset on the simulator, so restart it
// before a re-run.
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

async function open(opts, context = browser, url = base) {
  // BrowserContext.newPage takes no options, so size those pages afterwards.
  const page = context === browser ? await browser.newPage(opts) : await context.newPage();
  if (context !== browser && opts.viewport) await page.setViewportSize(opts.viewport);
  page.on("console", (m) => {
    // The duplicate-host check provokes a 409 on purpose; the browser logs
    // every non-2xx fetch as a console error.
    if (/status of 409/.test(m.text())) return;
    if (["error", "warning"].includes(m.type())) problems.push(m.text());
  });
  page.on("pageerror", (e) => problems.push(e.message));
  page.on("dialog", async (d) => { dialogs++; await d.dismiss(); });
  await page.goto(url);
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

  await check("without a token the dashboard is read-only", async () => {
    const ro = await open({ viewport: { width: 1400, height: 900 } });
    assert.ok(await ro.isVisible("#readonly-hint"));
    assert.ok(await ro.isHidden("#add-form"));
    assert.ok(await ro.isHidden("#reset-slot"));
    await ro.click("tr.target-row >> nth=0");
    await ro.waitForTimeout(500);
    const sections = await ro.$$eval(".detail-section h3", (h) => h.map((x) => x.textContent));
    assert.ok(!sections.includes("Actions"));
    await ro.close();
  });

  const token = process.env.E2E_TOKEN;
  assert.ok(token, "set E2E_TOKEN to the simulator's MPING_WEB_DEV_TOKEN");
  const ctx = await browser.newContext();
  const ctl = await open({ viewport: { width: 1400, height: 900 } }, ctx, `${base}#token=${token}`);
  const rowHosts = () => ctl.$$eval("tr.target-row .host", (h) => h.map((x) => x.textContent));

  await check("the token link enables controls and leaves the address bar", async () => {
    await ctl.waitForSelector("#add-form:not([hidden])");
    assert.equal(await ctl.evaluate(() => location.hash), "");
    assert.ok(await ctl.isHidden("#readonly-hint"));
    await shot(ctl, "control");
  });

  await check("adding a host: client-side validation, then a live row", async () => {
    await ctl.fill("#add-host", "bad host");
    await ctl.click("#add-form button");
    assert.match(await ctl.textContent("#notice"), /spaces/);
    await ctl.fill("#add-host", "added.e2e.example");
    await ctl.click("#add-form button");
    await ctl.waitForFunction(() => [...document.querySelectorAll(".host")].some((h) => h.textContent === "added.e2e.example"), null, { timeout: 5000 });
    assert.match(await ctl.textContent("#notice"), /Added added\.e2e\.example/);
    assert.equal(await ctl.inputValue("#add-host"), "");
  });

  await check("a double submit adds once without a spurious error", async () => {
    await ctl.fill("#add-host", "double.e2e.example");
    await ctl.evaluate(() => {
      const form = document.getElementById("add-form");
      form.requestSubmit();
      form.requestSubmit();
    });
    await ctl.waitForFunction(() => [...document.querySelectorAll(".host")].some((h) => h.textContent === "double.e2e.example"), null, { timeout: 5000 });
    await ctl.waitForTimeout(800);
    const isError = await ctl.$eval("#notice", (n) => n.classList.contains("is-error"));
    assert.ok(!isError, `unexpected error notice: ${await ctl.textContent("#notice")}`);
  });

  await check("server-side rejections are shown", async () => {
    await ctl.fill("#add-host", "added.e2e.example");
    await ctl.click("#add-form button");
    await ctl.waitForFunction(() => document.querySelector("#notice").classList.contains("is-error"));
    assert.match(await ctl.textContent("#notice"), /already in the list/);
  });

  await check("deleting needs a confirming second press", async () => {
    const row = ctl.locator("tr.target-row", { hasText: "added.e2e.example" });
    await row.click();
    const del = ctl.getByRole("button", { name: "Delete added.e2e.example" });
    await del.click();
    assert.ok((await rowHosts()).includes("added.e2e.example"), "first press must not delete");
    await ctl.getByRole("button", { name: "Confirm delete added.e2e.example?" }).click();
    await ctl.waitForFunction(() => ![...document.querySelectorAll(".host")].some((h) => h.textContent === "added.e2e.example"), null, { timeout: 5000 });
    assert.ok(await ctl.isHidden("#detail"));
  });

  await check("reset clears counters after confirmation", async () => {
    const sent = () => ctl.$eval("tr.target-row td.col-sent", (td) => Number(td.textContent));
    const before = await sent();
    await ctl.getByRole("button", { name: "Reset stats" }).click();
    await ctl.getByRole("button", { name: "Confirm reset?" }).click();
    await ctl.waitForFunction((b) => Number(document.querySelector("tr.target-row td.col-sent").textContent) < b, before, { timeout: 5000 });
    assert.match(await ctl.textContent("#notice"), /Statistics reset/);
  });

  await check("a stale token falls back to read-only", async () => {
    const stale = await browser.newContext();
    const page = await open({ viewport: { width: 1400, height: 900 } }, stale, `${base}#token=not-the-token`);
    await page.waitForTimeout(500);
    assert.ok(await page.isVisible("#readonly-hint"));
    assert.ok(await page.isHidden("#add-form"));
    await stale.close();
  });

  const mobile = await open({ viewport: { width: 390, height: 844 } }, ctx);
  await check("narrow screens keep the key columns without page overflow (controls shown)", async () => {
    assert.ok(await mobile.isVisible("#add-form"));
    assert.equal(await mobile.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    const headers = await mobile.$$eval("#targets th", (h) => h.filter((x) => x.offsetParent).map((x) => x.textContent));
    assert.deepEqual(headers, ["Status", "Host", "Loss %", "Recv", "Last ms", "Avg ms", "Jitter ms", "RTT trend"]);
    await shot(mobile, "mobile");
  });

  await check("no console errors", async () => assert.deepEqual(problems, []));
} finally {
  await browser.close();
}
