// Capture the QA-fix batch (PR #1160) against the LIVE local stack — these
// states depend on real broker/agent-service data (a routine with a recorded
// run, real integrations, persisted artifacts), so unlike the other specs this
// one does not mock routes. Requires the dev stack: broker :7890/:7891, agent
// service :8824, vite :5273 with an agent named "Reporting Agent" that has a
// run routine (see the v0.232.0 QA session).

import process from "node:process";

import { launchBrowser, shotPage } from "./lib.mjs";

const BASE = process.env.BASE_URL ?? "http://localhost:5273";

const OUT = process.env.WUPHF_SCREENSHOTS_OUT;
if (!OUT) {
  console.error("WUPHF_SCREENSHOTS_OUT is not set; run via publish.sh / driver");
  process.exit(2);
}

// The live web token, fetched from the broker (not a secret beyond this host).
const tokenRes = await fetch("http://127.0.0.1:7890/web-token");
const { token } = await tokenRes.json();

const { browser, context, page } = await launchBrowser({
  viewport: { width: 1440, height: 900 },
});

try {
  await page.goto(`${BASE}/?token=${token}#/operator`, {
    waitUntil: "domcontentloaded",
  });
  await page.waitForTimeout(2500);
  await page.getByText("Reporting Agent").first().click();
  await page.getByRole("tab", { name: "Routines" }).waitFor({ timeout: 45_000 });

  // 1. Routines — the lazy "Recent runs" disclosure over the broker's run ring.
  await page.getByRole("tab", { name: "Routines" }).click();
  await page.waitForTimeout(1500);
  await page.getByText(/recent runs/i).first().click();
  await page.waitForTimeout(2000);
  await shotPage(page, OUT, "01-routine-recent-runs");

  // 2. Integrations — chips reframed as workspace-connected, not "used by".
  await page.getByRole("tab", { name: "Integrations" }).click();
  await page.waitForTimeout(5000);
  await shotPage(page, OUT, "02-integrations-reframed");

  // 3. UI tab — artifacts strip with humanized stamps.
  await page.getByRole("tab", { name: "UI" }).click();
  await page.waitForTimeout(2500);
  await shotPage(page, OUT, "03-artifacts-human-stamps");

  // 4. Ask Agent — defaults to a manual chat, never the routine's transcript.
  await page.getByRole("button", { name: /^ask agent$/i }).first().click();
  await page.waitForTimeout(2500);
  await shotPage(page, OUT, "04-ask-agent-manual-default");

  console.log("captured 4 states");
} finally {
  await context.close();
  await browser.close();
}
