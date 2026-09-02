import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import * as computerApi from "../../api/computer";
import { BoxCloudConnect } from "./BoxCloudConnect";

const signedIn: computerApi.BoxAccount = {
  keySet: true,
  signedIn: true,
  identifier: "sam@example.com",
  cliInstalled: true,
  canStart: false,
  blockedReason: "subscription_required",
  plan: "trial",
  trialLine: "",
  billingUrl: computerApi.BOX_BILLING_URL,
};

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("BoxCloudConnect", () => {
  it("shows who is signed in, the plan gate, and a sign-out that calls the broker", async () => {
    vi.spyOn(computerApi, "getBoxAccount").mockResolvedValue(signedIn);
    const signOut = vi.spyOn(computerApi, "signOutBox").mockResolvedValue({
      revokedKeys: 1,
      loggedOut: true,
      account: {
        ...signedIn,
        keySet: false,
        signedIn: false,
        identifier: "",
        canStart: null,
      },
    });
    const onChanged = vi.fn();
    render(<BoxCloudConnect onChanged={onChanged} />);
    await waitFor(() =>
      expect(screen.getByTestId("box-account-line").textContent).toContain(
        "sam@example.com",
      ),
    );
    expect(screen.getByTestId("box-plan-notice")).toBeTruthy();
    fireEvent.click(screen.getByTestId("box-signout"));
    await waitFor(() => expect(screen.getByTestId("box-signin")).toBeTruthy());
    expect(signOut).toHaveBeenCalledTimes(1);
    expect(onChanged).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId("box-plan-notice")).toBeNull();
  });

  it("offers sign-in first and the paste form on request", async () => {
    vi.spyOn(computerApi, "getBoxAccount").mockResolvedValue({
      ...signedIn,
      keySet: false,
      signedIn: false,
      identifier: "",
      canStart: null,
    });
    render(<BoxCloudConnect compact={true} />);
    await waitFor(() => expect(screen.getByTestId("box-signin")).toBeTruthy());
    expect(screen.queryByTestId("box-key-input")).toBeNull();
    fireEvent.click(screen.getByTestId("box-paste"));
    expect(screen.getByTestId("box-key-input")).toBeTruthy();
  });
});
