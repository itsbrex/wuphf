import { expect, test } from "@playwright/test";

import {
  collectReactErrors,
  expectNoReactErrors,
  waitForReactMount,
} from "./_helpers";

// The office app panels (Graph, Policies, Skills, Dashboard, ...) are back.
//
// This spec used to assert the exact opposite, and said so plainly: it existed
// "so a regression that resurrects the office panels (or breaks their
// redirect) fails loudly". The panels were resurrected deliberately, so the
// test fired exactly as designed — on a decision that had been reversed.
// Mounting is the contract now; a redirect to a retired surface would be the
// regression.
//
// Route-by-route coverage lives in route-matrix.spec.ts, which walks
// APP_PANEL_IDS from the registry. This file pins the historical ids
// explicitly, so that dropping one from the registry fails with a named test
// rather than silently shrinking the matrix's loop.

const OFFICE_APP_IDS = [
  "graph",
  "policies",
  "routines",
  "skills",
  "activity",
  "health-check",
  "integrations",
] as const;

test.describe("office app routes", () => {
  test("every office app panel route mounts its panel", async ({ page }) => {
    const getErrors = collectReactErrors(page);

    for (const app of OFFICE_APP_IDS) {
      await page.goto(`/#/apps/${app}`);
      await waitForReactMount(page);
      await expect(page.getByTestId(`app-page-${app}`)).toBeVisible({
        timeout: 10_000,
      });
      // The panel is the destination, not a waypoint: the route must not
      // bounce somewhere else, and must not strand on not-found.
      await expect(page).toHaveURL(new RegExp(`#/apps/${app}`), {
        timeout: 10_000,
      });
      await expect(page.getByTestId("route-not-found")).toHaveCount(0);
    }

    await expectNoReactErrors(page, getErrors, "while mounting office panels");
  });
});
