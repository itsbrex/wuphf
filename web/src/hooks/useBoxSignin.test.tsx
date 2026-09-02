import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import * as computerApi from "../api/computer";
import { useBoxSignin } from "./useBoxSignin";

const blocked: computerApi.BoxAccount = {
  keySet: true,
  signedIn: true,
  identifier: "sam@example.com",
  cliInstalled: true,
  canStart: false,
  blockedReason: "subscription_required",
  plan: "trial",
  trialLine: "2 boxes, 25h compute",
  billingUrl: computerApi.BOX_BILLING_URL,
};

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("useBoxSignin", () => {
  it("reads the account on mount and reports the key flag", async () => {
    vi.spyOn(computerApi, "getBoxAccount").mockResolvedValue(blocked);
    const { result } = renderHook(() => useBoxSignin());
    await waitFor(() => expect(result.current.accountLoaded).toBe(true));
    expect(result.current.keySet).toBe(true);
    expect(result.current.account?.canStart).toBe(false);
  });

  it("opens the sign-in tab once and refreshes the account on done", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    const getAccount = vi
      .spyOn(computerApi, "getBoxAccount")
      .mockResolvedValueOnce({
        ...blocked,
        keySet: false,
        signedIn: false,
        canStart: null,
      })
      .mockResolvedValue(blocked);
    vi.spyOn(computerApi, "startBoxSignin").mockResolvedValue({
      status: "awaiting_login",
      authUrl: "https://ascii.dev/api/box/auth/github?state=x",
      installCommand: "",
      reason: "",
    });
    vi.spyOn(computerApi, "getBoxSigninStatus")
      .mockResolvedValueOnce({
        status: "awaiting_login",
        authUrl: "https://ascii.dev/api/box/auth/github?state=x",
        installCommand: "",
        reason: "",
      })
      .mockResolvedValue({
        status: "done",
        authUrl: "",
        installCommand: "",
        reason: "",
      });
    const { result } = renderHook(() => useBoxSignin());
    await waitFor(() => expect(result.current.accountLoaded).toBe(true));
    expect(result.current.keySet).toBe(false);
    act(() => result.current.start());
    await waitFor(() => expect(result.current.phase).toBe("awaiting_login"));
    expect(open).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1600);
    await vi.advanceTimersByTimeAsync(1600);
    await waitFor(() => expect(result.current.keySet).toBe(true));
    expect(result.current.phase).toBe("idle");
    expect(open).toHaveBeenCalledTimes(1);
    expect(getAccount).toHaveBeenCalledWith(true);
  });

  it("signs out and takes the account view the broker returns", async () => {
    vi.spyOn(computerApi, "getBoxAccount").mockResolvedValue(blocked);
    const signOut = vi.spyOn(computerApi, "signOutBox").mockResolvedValue({
      revokedKeys: 1,
      loggedOut: true,
      account: {
        ...blocked,
        keySet: false,
        signedIn: false,
        identifier: "",
        canStart: null,
      },
    });
    const { result } = renderHook(() => useBoxSignin());
    await waitFor(() => expect(result.current.keySet).toBe(true));
    await act(() => result.current.signOut());
    expect(signOut).toHaveBeenCalledTimes(1);
    expect(result.current.keySet).toBe(false);
    expect(result.current.phase).toBe("idle");
  });
});
