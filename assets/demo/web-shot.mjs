// Takes the README's Web UI screenshots through the Chrome DevTools Protocol.
// No npm dependencies (Node 22+ for the built-in WebSocket). See README.md here.
//
//   node assets/demo/web-shot.mjs <chrome> [url] [host-to-select]
//
// <chrome> is a Chrome or chrome-headless-shell binary. Writes
// assets/mping-web-light.png and assets/mping-web-dark.png at 1440x900, 2x.
import { spawn } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const [chrome, url = "http://127.0.0.1:8080/", host = "fra-de-ping.vultr.com"] = process.argv.slice(2);
if (!chrome) {
  console.error("usage: node assets/demo/web-shot.mjs <chrome> [url] [host-to-select]");
  process.exit(2);
}
const port = 9333;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function pageTarget() {
  for (let i = 0; i < 50; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
      const page = list.find((t) => t.type === "page");
      if (page) return page;
    } catch {
      // Chrome is still starting.
    }
    await sleep(200);
  }
  throw new Error(`Chrome exposed no page on port ${port}`);
}

function connect(wsURL) {
  const ws = new WebSocket(wsURL);
  const pending = new Map();
  let id = 0;
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    const call = pending.get(msg.id);
    if (!call) return;
    pending.delete(msg.id);
    if (msg.error) call.reject(new Error(`${call.method}: ${msg.error.message}`));
    else call.resolve(msg.result);
  };
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    pending.set(++id, { method, resolve, reject });
    ws.send(JSON.stringify({ id, method, params }));
  });
  return new Promise((resolve, reject) => {
    ws.onopen = () => resolve({ send, close: () => ws.close() });
    ws.onerror = () => reject(new Error(`cannot connect to ${wsURL}`));
  });
}

// Clicks the monitor row for host so its Summary is shown; fails if absent.
const selectHost = (name) => `(() => {
  const row = [...document.querySelectorAll("tr.target-row")]
    .find((r) => r.textContent.includes(${JSON.stringify(name)}));
  if (!row) return false;
  row.click();
  return true;
})()`;

const profile = await mkdtemp(join(tmpdir(), "mping-web-shot-"));
const proc = spawn(chrome, [
  `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, "--hide-scrollbars", "about:blank",
], { stdio: "ignore" });

try {
  const cdp = await connect((await pageTarget()).webSocketDebuggerUrl);
  await cdp.send("Emulation.setDeviceMetricsOverride", { width: 1440, height: 900, deviceScaleFactor: 2, mobile: false });
  await cdp.send("Page.enable");
  for (const scheme of ["light", "dark"]) {
    await cdp.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: scheme }] });
    await cdp.send("Page.navigate", { url });
    await sleep(4000); // first snapshot and RTT history
    const { result } = await cdp.send("Runtime.evaluate", { expression: selectHost(host), returnByValue: true });
    if (!result.value) throw new Error(`no monitor row for ${host}; is mping running with assets/demo/hosts.yaml?`);
    await sleep(1500);
    const { data } = await cdp.send("Page.captureScreenshot", { format: "png" });
    const out = `assets/mping-web-${scheme}.png`;
    await writeFile(out, Buffer.from(data, "base64"));
    console.log(`wrote ${out}`);
  }
  cdp.close();
} finally {
  proc.kill();
  await rm(profile, { recursive: true, force: true });
}
