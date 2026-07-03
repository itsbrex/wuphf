// Capture the UI-tab reframe (PR #1159): the agent's ONE live app IS the UI
// tab again, with the artifacts its runs produced (md/html/pdf) collected in a
// strip below the app — the app is no longer an artifact chip. All /api/* and
// /agent/* traffic is mocked so the shots are reproducible without a broker or
// agent service.

import process from "node:process";

import { installCommonMocks, launchBrowser, shotPage } from "./lib.mjs";

const BASE = process.env.BASE_URL ?? "http://localhost:5273";

const OUT = process.env.WUPHF_SCREENSHOTS_OUT;
if (!OUT) {
  console.error("WUPHF_SCREENSHOTS_OUT is not set; run via publish.sh / driver");
  process.exit(2);
}

// Fixed clock so `at` values are stable across runs.
const NOW = Date.parse("2026-07-01T09:00:00Z");
const hrsAgo = (h) => new Date(NOW - h * 3_600_000).toISOString();

const AGENT_ID = "app_c0ffee0badc0ffee";

const AGENT = {
  id: AGENT_ID,
  slug: "pipeline-agent",
  name: "Pipeline Agent",
  icon: "📈",
  summary:
    "Keeps the sales pipeline honest: scores and routes inbound leads, posts a Monday recap of stage moves, and drafts follow-ups for stalled deals.",
  entry: "index.html",
  version: 3,
  status: "ready",
  createdBy: "nex",
  createdAt: hrsAgo(240),
  updatedAt: hrsAgo(2),
  contentHash: "c0ffee",
};

const APP_HTML = `<!doctype html><html><body style="font-family:system-ui;background:#101014;color:#d8d8de;margin:0;padding:28px">
<h2 style="margin:0 0 6px;font-size:16px">Pipeline overview</h2>
<p style="margin:0 0 18px;color:#8b8b95;font-size:12px">Live view produced by this agent's one app.</p>
<table style="border-collapse:collapse;font-size:13px;width:100%">
<tr style="text-align:left;color:#8b8b95"><th style="padding:6px 12px 6px 0">Deal</th><th style="padding:6px 12px 6px 0">Stage</th><th style="padding:6px 0">Owner</th></tr>
<tr><td style="padding:6px 12px 6px 0">Acme</td><td style="padding:6px 12px 6px 0">Evaluation</td><td style="padding:6px 0">Priya</td></tr>
<tr><td style="padding:6px 12px 6px 0">Globex</td><td style="padding:6px 12px 6px 0">Negotiation</td><td style="padding:6px 0">Sam</td></tr>
<tr><td style="padding:6px 12px 6px 0">Umbrella</td><td style="padding:6px 12px 6px 0">Stalled</td><td style="padding:6px 0">Priya</td></tr>
</table></body></html>`;

const ARTIFACTS = [
  {
    id: "art_run01",
    type: "md",
    title: "monday-pipeline-recap-run-4.md",
    producedBy: "Monday pipeline recap",
    at: hrsAgo(2),
    content:
      "# Monday pipeline recap — run 4\n\n**Stage moves (3):** Globex → Negotiation, Acme → Evaluation, Initech → Discovery.\n\n**New leads (2):** Hooli, Stark Industries.\n\n**Stalled:** Umbrella — 21 days, owner Priya, last touch was the pricing call.",
    size: "1.2 KB",
  },
  {
    id: "art_run02",
    type: "pdf",
    title: "q3-pipeline-brief.pdf",
    producedBy: "Monday pipeline recap",
    at: hrsAgo(26),
    size: "182 KB",
  },
];

async function installAgentMocks(context) {
  // /api/* — the broker side: the agents list and this agent's detail. Order
  // matters: register the more specific routes AFTER the generic ones so they
  // win.
  await context.route("**/api/apps", (r) =>
    r.fulfill({ contentType: "application/json", body: JSON.stringify({ apps: [AGENT] }) }),
  );
  await context.route(`**/api/apps/${AGENT_ID}*`, (r) =>
    r.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ app: AGENT, html: APP_HTML }),
    }),
  );

  // /agent/* — the agent service's persistence endpoints. Tools/sessions stay
  // empty; only the artifacts matter for these shots.
  await context.route("**/agent/tools?*", (r) =>
    r.fulfill({ contentType: "application/json", body: JSON.stringify({ tools: [] }) }),
  );
  await context.route("**/agent/artifacts?*", (r) =>
    r.fulfill({ contentType: "application/json", body: JSON.stringify({ artifacts: ARTIFACTS }) }),
  );
}

const { browser, context, page } = await launchBrowser({
  viewport: { width: 1440, height: 900 },
});

try {
  await installCommonMocks(context);
  await installAgentMocks(context);

  await page.goto(`${BASE}/#/operator`, { waitUntil: "domcontentloaded" });
  await page.getByText("Pipeline Agent").first().waitFor({ timeout: 30_000 });
  await page.waitForTimeout(800);

  // Open the agent detail. The detail chunk is lazy — the first dev-mode
  // compile can take a while, so give the tab strip a generous deadline.
  await page.getByText("Pipeline Agent").first().click();
  await page.getByRole("tab", { name: "UI" }).waitFor({ timeout: 45_000 });

  // 1. UI tab (the landing tab) — the one live app on top, the artifacts its
  //    runs produced collected in the strip below. The first artifact's viewer
  //    (the md run outcome) opens by default.
  await page
    .getByText("monday-pipeline-recap-run-4.md")
    .filter({ visible: true })
    .first()
    .waitFor({ timeout: 10_000 });
  await page.waitForTimeout(800);
  await shotPage(page, OUT, "01-ui-tab-app-with-artifacts");

  // 2. A file-ish artifact opened in place — the pdf shows its download card
  //    under the strip; the app stays mounted above.
  await page
    .getByText("q3-pipeline-brief.pdf")
    .filter({ visible: true })
    .first()
    .click();
  await page.getByText("Download").filter({ visible: true }).first().waitFor({ timeout: 10_000 });
  await page.waitForTimeout(400);
  await shotPage(page, OUT, "02-ui-tab-pdf-artifact");

  console.log("captured 2 states");
} finally {
  await context.close();
  await browser.close();
}
