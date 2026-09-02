import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { BOX_BILLING_URL, type BoxAccount } from "../../api/computer";
import { BoxPlanNotice, boxAccountLine } from "./BoxPlanNotice";

const base: BoxAccount = {
  keySet: true,
  signedIn: true,
  identifier: "sam@example.com",
  cliInstalled: true,
  canStart: true,
  blockedReason: "",
  plan: "box_20",
  trialLine: "",
  billingUrl: BOX_BILLING_URL,
};

afterEach(cleanup);

describe("BoxPlanNotice", () => {
  it("renders nothing when the account can start boxes", () => {
    const { container } = render(<BoxPlanNotice account={base} />);
    expect(container.innerHTML).toBe("");
  });

  it("names the block in plain words and links to billing", () => {
    render(
      <BoxPlanNotice
        account={{
          ...base,
          canStart: false,
          blockedReason: "subscription_required",
          plan: "trial",
          trialLine: "2 boxes, 25h compute",
        }}
      />,
    );
    expect(screen.getByTestId("box-plan-notice").textContent).toContain(
      "needs a plan",
    );
    expect(screen.getByTestId("box-plan-notice").textContent).toContain(
      "2 boxes, 25h compute",
    );
    expect(
      screen.getByTestId("box-plan-notice-link").getAttribute("href"),
    ).toBe(BOX_BILLING_URL);
  });

  it("describes the account in one line", () => {
    expect(boxAccountLine(base)).toBe(
      "Signed in to ascii.dev as sam@example.com (box_20).",
    );
    expect(boxAccountLine({ ...base, signedIn: false, identifier: "" })).toBe(
      "A Box key is saved.",
    );
    expect(
      boxAccountLine({
        ...base,
        signedIn: false,
        identifier: "",
        keySet: false,
      }),
    ).toBe("Not connected to ascii.dev.");
  });
});
