// Capture the operator's new app-EDIT route (PR: operator-app-edit-route):
// the agent detail header carries an "Edit app" action, and clicking it opens
// the build experience in edit mode — the edit-scoped chat docked beside the
// live app. Before this, the only chat affordance (Ask Agent) teaches tools,
// and a UI bug report sent there was misauthored into a garbage tool.
//
// Run via the wrapper:
//   web/e2e/screenshots/publish.sh operator-edit-route <pr-number>

import process from "node:process";

import { DEFAULT_BASE, launchBrowser, shotElement, shotPage } from "./lib.mjs";

const OUT = process.env.WUPHF_SCREENSHOTS_OUT;
if (!OUT) {
  console.error("WUPHF_SCREENSHOTS_OUT is not set; run via publish.sh");
  process.exit(2);
}

const APP = {
  id: "app_69cebd17418462a4",
  slug: "reporting-agent",
  name: "Reporting Agent",
  icon: "📊",
  summary: "Live view of all office tasks with status filters and search",
  entry: "index.html",
  version: 2,
  status: "ready",
};

const { browser, context, page } = await launchBrowser();

// Playwright routes match LIFO: register the catch-all FIRST so the
// specific list + detail mocks (registered after) win.
await context.route("**/api/apps/**", (r) => r.fulfill({ json: {} }));
await context.route("**/api/apps", (r) => r.fulfill({ json: { apps: [APP] } }));
await context.route(`**/api/apps/${APP.id}`, (r) =>
  r.fulfill({
    json: { app: APP, html: "<!doctype html><body><h1>Office Tasks</h1></body>" },
  }),
);
await context.route("**/api/requests*", (r) =>
  r.fulfill({ json: { requests: [] } }),
);
await context.route("**/agent/**", (r) =>
  r.fulfill({ status: 404, json: { error: "not in this capture" } }),
);
await context.route("**/web-token", (r) =>
  r.fulfill({ json: { token: "screenshot-token" } }),
);

await page.goto(`${DEFAULT_BASE}/?token=screenshot-token#/operator`, {
  waitUntil: "load",
});
await page.waitForSelector(".opr-sidebar", { timeout: 15_000 });

// Open the agent detail.
await page
  .locator(".opr-agent-rail-item", { hasText: "Reporting Agent" })
  .click();

// 1. The header actions now carry "Edit app" beside "Ask Agent".
await page.waitForSelector(".opr-detail-actions", { timeout: 10_000 });
await shotElement(page, ".opr-app-detail", OUT, "01-detail-edit-app-action");

// 2. Edit mode: the edit-scoped chat docks beside the live app.
await page.getByRole("button", { name: /edit app/i }).click();
await page.waitForSelector(".opr-build-exp.is-live", { timeout: 10_000 });
await page
  .getByText(/tell me what to change about/i)
  .waitFor({ timeout: 10_000 });
await shotPage(page, OUT, "02-edit-mode-docked-chat");

await browser.close();
