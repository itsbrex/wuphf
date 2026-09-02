import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import * as client from "../../../../api/client";
import { ApiError } from "../../../../api/client";
import * as computerApi from "../../../../api/computer";
import { ONBOARDING_COMPUTER_COPY as COPY } from "../wizardSteps";
import {
  ComputerChoice,
  ComputerChoiceView,
  StepComputer,
} from "./StepComputer";

const noop = () => undefined;

const runtimeReady: computerApi.ComputerRuntime = {
  available: true,
  runtime: "docker",
  daemonUp: true,
  image: false,
  imageRef: "",
  driverVersion: "0.20.0",
  building: false,
  installHint: "",
  runtimeStartHint: "",
  problem: "",
};

const viewDefaults = {
  installHint: "",
  account: null,
  onSignOut: noop,
  signin: "idle" as const,
  authUrl: "",
  installCommand: "",
  signinError: "",
  onStartSignin: noop,
  showPaste: true,
  onShowPaste: noop,
  keyValue: "",
  onKeyChange: noop,
  onSaveKey: noop,
  saving: false,
  saveError: null,
};

const account = (keySet: boolean): computerApi.BoxAccount => ({
  keySet,
  signedIn: keySet,
  identifier: keySet ? "sam@example.com" : "",
  cliInstalled: true,
  canStart: keySet ? false : null,
  blockedReason: keySet ? "subscription_required" : "",
  plan: keySet ? "trial" : "",
  trialLine: "",
  billingUrl: computerApi.BOX_BILLING_URL,
});

function seedContainer(keySet = false) {
  vi.spyOn(computerApi, "getComputerRuntime").mockResolvedValue(runtimeReady);
  vi.spyOn(computerApi, "getBoxAccount").mockResolvedValue(account(keySet));
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("ComputerChoiceView", () => {
  it("tells the person where to get the key, with the real links", () => {
    render(
      <ComputerChoiceView
        {...viewDefaults}
        local="missing"
        installHint="Install OrbStack (https://orbstack.dev)."
        keySet={false}
      />,
    );
    expect(screen.getByText(COPY.localMissing)).toBeTruthy();
    expect(screen.getByText(COPY.localInstallLabel).getAttribute("href")).toBe(
      COPY.localInstallURL,
    );
    expect(screen.getByTestId("onboarding-computer-signin").textContent).toBe(
      COPY.signinCta,
    );
    for (const step of COPY.howTo) {
      expect(screen.getByText(step)).toBeTruthy();
    }
    expect(
      screen.getByTestId("onboarding-computer-docs").getAttribute("href"),
    ).toBe("https://docs.ascii.dev/box/api-keys");
    expect(screen.getByText(COPY.signupLabel).getAttribute("href")).toBe(
      "https://box.ascii.dev",
    );
    expect(screen.getByText(COPY.skipHint)).toBeTruthy();
  });

  it("hides the form and instructions once a key is set", () => {
    render(
      <ComputerChoiceView {...viewDefaults} local="ready" keySet={true} />,
    );
    expect(screen.getByTestId("onboarding-computer-key-set").textContent).toBe(
      COPY.keySet,
    );
    expect(screen.queryByTestId("onboarding-computer-box-key")).toBeNull();
    expect(screen.queryByTestId("onboarding-computer-signin")).toBeNull();
    expect(screen.getByText(COPY.localReady)).toBeTruthy();
  });

  it("shows the manual install command when the CLI could not be installed", () => {
    render(
      <ComputerChoiceView
        {...viewDefaults}
        local="ready"
        keySet={false}
        signin="cli_missing"
        installCommand="curl -fsSL https://ascii.dev/api/box/install | sh"
      />,
    );
    expect(
      screen.getByText("curl -fsSL https://ascii.dev/api/box/install | sh"),
    ).toBeTruthy();
  });
});

describe("ComputerChoice sign-in", () => {
  it("opens the sign-in tab once, polls, and flips to set on done", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.spyOn(computerApi, "getComputerRuntime").mockResolvedValue(runtimeReady);
    vi.spyOn(computerApi, "getBoxAccount")
      .mockResolvedValueOnce(account(false))
      .mockResolvedValue(account(true));
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    vi.spyOn(computerApi, "startBoxSignin").mockResolvedValue({
      status: "awaiting_login",
      authUrl: "https://ascii.dev/api/box/auth/github?state=abc",
      installCommand: "",
      reason: "",
    });
    const status = vi
      .spyOn(computerApi, "getBoxSigninStatus")
      .mockResolvedValueOnce({
        status: "awaiting_login",
        authUrl: "https://ascii.dev/api/box/auth/github?state=abc",
        installCommand: "",
        reason: "",
      })
      .mockResolvedValue({
        status: "done",
        authUrl: "",
        installCommand: "",
        reason: "",
      });
    render(<ComputerChoice />);
    await waitFor(() => expect(screen.getByText(COPY.localReady)).toBeTruthy());
    fireEvent.click(screen.getByTestId("onboarding-computer-signin"));
    await waitFor(() =>
      expect(screen.getByText(/Finish signing in/)).toBeTruthy(),
    );
    expect(open).toHaveBeenCalledTimes(1);
    expect(open).toHaveBeenCalledWith(
      "https://ascii.dev/api/box/auth/github?state=abc",
      "_blank",
      "noopener",
    );
    await vi.advanceTimersByTimeAsync(1600);
    await vi.advanceTimersByTimeAsync(1600);
    await waitFor(() =>
      expect(screen.getByTestId("onboarding-computer-key-set")).toBeTruthy(),
    );
    expect(status).toHaveBeenCalled();
    expect(open).toHaveBeenCalledTimes(1);
  });

  it("surfaces the broker's reason when sign-in fails", async () => {
    seedContainer();
    vi.spyOn(computerApi, "startBoxSignin").mockResolvedValue({
      status: "error",
      authUrl: "",
      installCommand: "",
      reason: "the Box CLI could not start",
    });
    render(<ComputerChoice />);
    fireEvent.click(screen.getByTestId("onboarding-computer-signin"));
    await waitFor(() =>
      expect(
        screen.getByTestId("onboarding-computer-signin-status").textContent,
      ).toContain("could not start"),
    );
  });
});

describe("ComputerChoice paste fallback", () => {
  it("saves the key through /config and flips to the set state", async () => {
    vi.spyOn(computerApi, "getComputerRuntime").mockResolvedValue(runtimeReady);
    vi.spyOn(computerApi, "getBoxAccount")
      .mockResolvedValueOnce(account(false))
      .mockResolvedValue(account(true));
    const update = vi
      .spyOn(client, "updateConfig")
      .mockResolvedValue({} as never);
    render(<ComputerChoice />);
    await waitFor(() => expect(screen.getByText(COPY.localReady)).toBeTruthy());
    fireEvent.click(screen.getByTestId("onboarding-computer-paste"));
    fireEvent.change(screen.getByTestId("onboarding-computer-box-key"), {
      target: { value: "box_abc123" },
    });
    fireEvent.click(screen.getByTestId("onboarding-computer-save"));
    await waitFor(() =>
      expect(screen.getByTestId("onboarding-computer-key-set")).toBeTruthy(),
    );
    expect(update).toHaveBeenCalledWith({ box_api_key: "box_abc123" });
  });

  it("shows the provider's refusal instead of a generic error", async () => {
    seedContainer();
    vi.spyOn(client, "updateConfig").mockRejectedValue(
      new ApiError({
        status: 400,
        statusText: "Bad Request",
        bodyText: "that doesn't look like a box API key: they start with box_",
      }),
    );
    render(<ComputerChoice />);
    fireEvent.click(screen.getByTestId("onboarding-computer-paste"));
    fireEvent.change(screen.getByTestId("onboarding-computer-box-key"), {
      target: { value: "sk-wrong" },
    });
    fireEvent.click(screen.getByTestId("onboarding-computer-save"));
    await waitFor(() =>
      expect(
        screen.getByTestId("onboarding-computer-error").textContent,
      ).toContain("start with box_"),
    );
    expect(screen.getByTestId("onboarding-computer-box-key")).toBeTruthy();
  });

  it("reports an installed but stopped runtime honestly", async () => {
    vi.spyOn(computerApi, "getComputerRuntime").mockResolvedValue({
      ...runtimeReady,
      daemonUp: false,
    });
    vi.spyOn(computerApi, "getBoxAccount").mockResolvedValue(account(true));
    render(<ComputerChoice />);
    await waitFor(() =>
      expect(screen.getByText(COPY.localStopped)).toBeTruthy(),
    );
    expect(screen.getByTestId("onboarding-computer-key-set")).toBeTruthy();
    // A key alone is not enough: the plan gate and the account are named.
    expect(screen.getByTestId("box-plan-notice")).toBeTruthy();
    expect(
      screen.getByTestId("onboarding-computer-account").textContent,
    ).toContain("sam@example.com");
    expect(screen.getByTestId("onboarding-computer-signout")).toBeTruthy();
  });
});

describe("StepComputer", () => {
  it("renders the step copy and the stage clip", async () => {
    seedContainer();
    render(
      <StepComputer
        active={true}
        answers={{} as never}
        setAnswers={noop}
        blueprints={[]}
      />,
    );
    expect(screen.getByTestId("onboarding-step-computer")).toBeTruthy();
    expect(screen.getByText("Give your bots a computer.")).toBeTruthy();
    expect(screen.getByRole("img").getAttribute("src")).toBe(
      "/media/onboarding/bot-computer.gif",
    );
  });
});
