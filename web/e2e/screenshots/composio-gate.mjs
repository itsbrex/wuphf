// Capture the Composio onboarding gate (PR #1159): the operator Integrations
// tab with no Composio key connected renders the shipped ComposioOnboarding
// (sign-in primary, key-paste fallback) instead of an empty catalog. The
// /api/config response is mocked to force the first-run state.

import process from "node:process";

import { installCommonMocks, launchBrowser, shotPage } from "./lib.mjs";

const BASE = process.env.BASE_URL ?? "http://localhost:5273";

const OUT = process.env.WUPHF_SCREENSHOTS_OUT;
if (!OUT) {
  console.error("WUPHF_SCREENSHOTS_OUT is not set; run via publish.sh / driver");
  process.exit(2);
}

const { browser, context, page } = await launchBrowser({
  viewport: { width: 1440, height: 900 },
});

try {
  await installCommonMocks(context);
  // Force the no-key state regardless of the workspace's real config.
  await context.route("**/api/config", (r) => {
    if (r.request().method() !== "GET") return r.fallback();
    r.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ composio_key_set: false }),
    });
  });

  await page.goto(`${BASE}/#/operator`, { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(1200);

  // Open a mock agent's detail and its Integrations tab.
  await page.getByText("Support escalation triage").first().click();
  await page.getByRole("tab", { name: "Integrations" }).waitFor({ timeout: 45_000 });
  await page.getByRole("tab", { name: "Integrations" }).click();
  await page
    .getByText("Add integrations to your office")
    .waitFor({ timeout: 15_000 });
  await page.waitForTimeout(400);
  await shotPage(page, OUT, "01-composio-onboarding-gate");

  console.log("captured 1 state");
} finally {
  await context.close();
  await browser.close();
}
