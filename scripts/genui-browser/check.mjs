import { chromium } from "@playwright/test";
import { mkdirSync, writeFileSync, globSync } from "node:fs";
const out = process.env.GENUI_BROWSER_OUT || "browser-artifacts";
mkdirSync(out, { recursive: true });
const executablePath = process.env.CHROME_PATH || globSync("/ms-playwright/chromium-*/chrome-linux*/chrome")[0];
const browser = await chromium.launch({ headless: true, executablePath });
try {
  const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
  const errors = [];
  page.on("pageerror", e => errors.push(e.message));
  page.on("console", m => { if (m.type() === "error") errors.push(m.text()); });
  await page.goto("http://127.0.0.1:4173", { waitUntil: "networkidle" });
  await page.locator("[data-screen]").waitFor();
  const evidence = await page.evaluate(() => ({
    screenText: document.querySelector("[data-screen]")?.textContent,
    rows: document.querySelectorAll("tbody tr").length,
    columns: document.querySelectorAll("thead th").length,
    cells: document.querySelectorAll("tbody td").length,
    ordinaryText: document.querySelector("[data-message]")?.textContent,
  }));
  await page.screenshot({ path: `${out}/screen.png`, fullPage: true });
  writeFileSync(`${out}/dom.html`, await page.content());
  writeFileSync(`${out}/evidence.json`, JSON.stringify({ ...evidence, errors }, null, 2));
  console.log(JSON.stringify({ ...evidence, ordinaryText: Boolean(evidence.ordinaryText), errors }));
  if (evidence.rows !== 4 || evidence.columns !== 3 || evidence.cells !== 12 || errors.length || !evidence.ordinaryText.includes("Да, оба направления") || !evidence.ordinaryText.includes("говорить, что он оказался быстрее или медленнее")) process.exitCode = 1;
  {
    const checks = [];
    for (const mode of ["old", "unknown", "broken", "missing"]) {
      await page.goto(`http://127.0.0.1:4173/?mode=${mode}`, { waitUntil: "networkidle" });
      await page.getByText("This screen could not be built.", { exact: true }).waitFor();
      if (mode === "old") {
        await page.reload({ waitUntil: "networkidle" });
        await page.getByText("This screen could not be built.", { exact: true }).waitFor();
      }
      const safe = await page.evaluate(() => ({ source: document.querySelector("pre")?.textContent, executed: Boolean(window.pwned), scriptNodes: document.querySelectorAll("pre script").length, buttons: document.querySelectorAll("button").length }));
      if (!safe.source || safe.executed || safe.scriptNodes || safe.buttons) throw Error(`${mode}: unsafe or invisible fallback`);
      await page.screenshot({ path: `${out}/${mode}.png`, fullPage: true });
      checks.push({ mode, safe });
    }
    await page.goto("http://127.0.0.1:4173/?mode=stream", { waitUntil: "networkidle" });
    await page.getByText("Building this screen…", { exact: true }).waitFor();
    await page.evaluate(() => window.dispatchEvent(new Event("complete")));
    await page.locator("tbody tr").nth(3).waitFor();
    if (await page.locator("tbody td").count() !== 12) throw Error("stream completion lost cells");
    checks.push({ mode: "stream", completedCells: 12 });
    await page.goto("http://127.0.0.1:4173/?mode=order", { waitUntil: "networkidle" });
    const sources = await page.locator("pre").allTextContents();
    if (JSON.stringify(sources) !== JSON.stringify(["root = First()", "root = Second()"])) throw Error("screen order changed");
    checks.push({ mode: "order", sources });
    writeFileSync(`${out}/regressions.json`, JSON.stringify({ checks, errors }, null, 2));
    if (errors.length) throw Error(`browser errors: ${errors}`);
  }
} finally { await browser.close(); }
