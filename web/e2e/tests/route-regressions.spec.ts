import { expect, test } from "@playwright/test";

import {
  collectReactErrors,
  expectNoReactErrors,
  resetBroker,
  waitForReactMount,
} from "./_helpers";

// Named pins for individual routes whose behaviour is easy to break silently
// and hard to notice: retired app ids that must not resolve to a panel, and
// the one app id that is a redirect rather than a surface.
//
// The previous version of this file pinned the office-shell RETIREMENT — that
// "the operator surface is the only front door, and every legacy office hash
// normalizes to /#/operator". That reversal is complete, so the pins are
// re-aimed rather than deleted.
//
// DELIBERATELY NOT PINNED HERE: conversation routes. #general and named
// channels are mid-retirement behind reversible switches in internal/channel
// (generalEnabled / groupDMsEnabled / namedChannelsEnabled, all currently
// true). Pinning "/#/channels/general mounts a composer" today would encode a
// state that is scheduled to change, and would then fail as though the flip
// were a regression. Conversation coverage returns once the channel work
// settles and DMs are the stable surface.

test.afterEach(async ({ request }) => {
  await resetBroker(request);
});

test.describe("route pins", () => {
  for (const legacy of ["console", "threads"] as const) {
    test(`/apps/${legacy} falls back to a generic app page, not an error`, async ({
      page,
    }) => {
      const getErrors = collectReactErrors(page);
      await page.goto(`/#/apps/${legacy}`);
      await waitForReactMount(page);

      // MEASURED, not assumed. These ids were dropped from the panel
      // registry (#1055), and the first version of this test asserted they
      // must therefore NOT mount. They do: the office renders a generic app
      // page for any /#/apps/<id>, which is its unknown-app fallback. That
      // is a deliberate affordance, so an old bookmark degrades to an empty
      // panel instead of an error.
      //
      // Pinned because it is load-bearing in BOTH directions: it must not
      // become a hard error (breaking old links), and it must not start
      // resolving real panel content for an id the registry does not know.
      await expect(page.getByTestId(`app-page-${legacy}`)).toHaveCount(1);
      await expect(page.getByTestId("error-boundary")).toHaveCount(0);
      await expectNoReactErrors(page, getErrors, `legacy /apps/${legacy}`);
    });
  }

  test("/apps/requests folds into the task board", async ({ page }) => {
    const getErrors = collectReactErrors(page);
    await page.goto("/#/apps/requests");
    await waitForReactMount(page);

    // The Inbox was consolidated into the Task board; requests fold into its
    // Needs-human lane instead of rendering a panel of their own. This is a
    // redirect, so it is pinned by URL rather than by a panel testid.
    await expect(page).toHaveURL(/#\/tasks$/, { timeout: 10_000 });
    await expect(page.getByTestId("route-not-found")).toHaveCount(0);
    await expectNoReactErrors(page, getErrors, "/apps/requests redirect");
  });
});
