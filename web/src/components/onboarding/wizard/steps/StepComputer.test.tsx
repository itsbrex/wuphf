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

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("ComputerChoiceView", () => {
  it("tells the person where to get the key, with the real links", () => {
    render(
      <ComputerChoiceView
        local="missing"
        installHint="Install OrbStack (https://orbstack.dev)."
        keySet={false}
        keyValue=""
        onKeyChange={noop}
        onSaveKey={noop}
        saving={false}
        saveError={null}
      />,
    );
    expect(screen.getByText(COPY.localMissing)).toBeTruthy();
    expect(screen.getByText(COPY.localInstallLabel).getAttribute("href")).toBe(
      COPY.localInstallURL,
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
      <ComputerChoiceView
        local="ready"
        installHint=""
        keySet={true}
        keyValue=""
        onKeyChange={noop}
        onSaveKey={noop}
        saving={false}
        saveError={null}
      />,
    );
    expect(screen.getByTestId("onboarding-computer-key-set").textContent).toBe(
      COPY.keySet,
    );
    expect(screen.queryByTestId("onboarding-computer-box-key")).toBeNull();
    expect(screen.getByText(COPY.localReady)).toBeTruthy();
  });
});

describe("ComputerChoice", () => {
  it("saves the key through /config and flips to the set state", async () => {
    vi.spyOn(computerApi, "getComputerRuntime").mockResolvedValue(runtimeReady);
    vi.spyOn(client, "getConfig").mockResolvedValue({
      box_key_set: false,
    } as never);
    const update = vi
      .spyOn(client, "updateConfig")
      .mockResolvedValue({} as never);
    render(<ComputerChoice />);
    await waitFor(() => expect(screen.getByText(COPY.localReady)).toBeTruthy());
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
    vi.spyOn(computerApi, "getComputerRuntime").mockResolvedValue(runtimeReady);
    vi.spyOn(client, "getConfig").mockResolvedValue({
      box_key_set: false,
    } as never);
    vi.spyOn(client, "updateConfig").mockRejectedValue(
      new ApiError({
        status: 400,
        statusText: "Bad Request",
        bodyText: "that doesn't look like a box API key: they start with box_",
      }),
    );
    render(<ComputerChoice />);
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
    vi.spyOn(client, "getConfig").mockResolvedValue({
      box_key_set: true,
    } as never);
    render(<ComputerChoice />);
    await waitFor(() =>
      expect(screen.getByText(COPY.localStopped)).toBeTruthy(),
    );
    expect(screen.getByTestId("onboarding-computer-key-set")).toBeTruthy();
  });
});

describe("StepComputer", () => {
  it("renders the step copy and the stage clip", async () => {
    vi.spyOn(computerApi, "getComputerRuntime").mockResolvedValue(runtimeReady);
    vi.spyOn(client, "getConfig").mockResolvedValue({
      box_key_set: false,
    } as never);
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
