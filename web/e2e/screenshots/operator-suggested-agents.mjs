// Capture the operator sidebar's honest real-vs-suggested split and the
// suggested-agent detail (PR #1162): the Agents badge counts REAL agents
// only, mock drafts sit under a ghosted "Suggested" section, and their
// detail pill reads "Suggested" (no fabricated "from N conversations"
// chip, artifacts headed "Example artifacts").
//
// Run via the wrapper:
//   web/e2e/screenshots/publish.sh operator-suggested-agents <pr-number>

import process from "node:process";

import { DEFAULT_BASE, launchBrowser, shotElement, shotPage } from "./lib.mjs";

const OUT = process.env.WUPHF_SCREENSHOTS_OUT;
if (!OUT) {
  console.error("WUPHF_SCREENSHOTS_OUT is not set; run via publish.sh");
  process.exit(2);
}

const REAL_APPS = [
  {
    id: "app_325c3475e74882f2",
    slug: "lead-routing-agent",
    name: "Lead Routing Agent",
    icon: "🎯",
    summary: "Score inbound demo requests and route hot leads to #ae-handoffs",
    entry: "index.html",
    version: 2,
    status: "ready",
  },
  {
    id: "app_f5fc7295e339ed80",
    slug: "refund-approvals",
    name: "Refund Approvals",
    icon: "🧾",
    summary: "Approve refunds and post confirmations to Slack",
    entry: "index.html",
    version: 1,
    status: "ready",
  },
];

const { browser, context, page } = await launchBrowser();

// The operator shell mounts at /#/operator ahead of the office gates, so no
// bootShell/store flip is needed — just a deterministic /apps payload.
await context.route("**/api/apps", (r) =>
  r.fulfill({ json: { apps: REAL_APPS } }),
);
await context.route("**/api/apps/**", (r) =>
  r.fulfill({ json: { app: REAL_APPS[0], html: "<!doctype html><p>app</p>" } }),
);
await context.route("**/api/requests*", (r) =>
  r.fulfill({ json: { requests: [] } }),
);
await context.route("**/web-token", (r) =>
  r.fulfill({ json: { token: "screenshot-token" } }),
);

await page.goto(`${DEFAULT_BASE}/?token=screenshot-token#/operator`, {
  waitUntil: "load",
});
await page.waitForSelector(".opr-sidebar", { timeout: 15_000 });

// 1. Sidebar: badge counts the 2 real agents; mocks ghosted under SUGGESTED.
await page.waitForSelector(".opr-agent-rail-item.is-suggested", {
  timeout: 10_000,
});
await shotElement(page, ".opr-sidebar", OUT, "01-sidebar-suggested-section");

// 2. Suggested detail: "Suggested" pill, no fabricated source chip,
//    "Example artifacts" heading.
await page
  .locator(".opr-agent-rail-item.is-suggested", {
    hasText: "Support escalation triage",
  })
  .click();
await page.waitForSelector("text=Suggested", { timeout: 10_000 });
await shotPage(page, OUT, "02-suggested-detail-honest");

await browser.close();
