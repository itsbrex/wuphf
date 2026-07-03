import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { DemoCapture } from "../apps/demoCapture";
import { AppBuilderChat } from "./AppBuilderChat";

// A "Demo workflow to Nex" handoff must start the REAL agent build at once
// from the captured context, and set up the captured routine + tools on the
// new agent the moment its id resolves — that is the contract this covers.

const listAppsMock = vi.fn();
vi.mock("../../api/apps", () => ({
  listApps: () => listAppsMock(),
  submitAppEdit: vi.fn(),
}));

const buildMutateMock = vi.fn(
  async (_input: { name: string; description: string }) => ({}),
);
vi.mock("../apps/useOperatorApps", () => ({
  useBuildApp: () => ({ mutateAsync: buildMutateMock }),
  deriveAppName: () => "Pipeline Agent",
  resolveNewAppId: (before: ReadonlySet<string>, apps: { id: string }[]) =>
    apps.find((a) => !before.has(a.id))?.id ?? null,
  appBuildState: (app: { status?: string }) =>
    app.status === "building" ? "building" : "ready",
}));

const createRoutineMock = vi.fn(async (_input: unknown) => ({ id: "job_1" }));
vi.mock("../agents/agentStateClient", () => ({
  tryCreateRoutine: (input: unknown) => createRoutineMock(input),
}));

const buildToolMock = vi.fn(async (_instruction: string, _agent: string) => ({
  tool: {},
  offline: false,
}));
vi.mock("../tools/toolAgentClient", () => ({
  buildToolFromChat: (instruction: string, agent: string) =>
    buildToolMock(instruction, agent),
}));

vi.mock("../../components/apps/AppActivity", () => ({
  AppActivity: () => null,
}));

const DEMO: DemoCapture = {
  mode: "build",
  kind: "both",
  routine: {
    name: "Monday recap",
    prompt: "Summarize last week's pipeline.",
    schedule: "0 9 * * 1",
  },
  tools: [
    { name: "summarizePipeline", purpose: "Read deals and recap moves." },
  ],
  goal: "Recap the pipeline every Monday.",
  summary: "",
  transcript: [],
  screens: [],
  selectors: [],
  apiCalls: [],
  entities: [],
};

function renderChat() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <AppBuilderChat demo={DEMO} onClose={() => {}} onFinish={() => {}} />
    </QueryClientProvider>,
  );
}

describe("AppBuilderChat demo handoff", () => {
  beforeEach(() => {
    listAppsMock.mockReset();
    buildMutateMock.mockClear();
    createRoutineMock.mockClear();
    buildToolMock.mockClear();
    // Baseline snapshot (send) sees no apps; every poll after sees the
    // pre-scaffolded building agent.
    listAppsMock
      .mockResolvedValueOnce([])
      .mockResolvedValue([{ id: "app_new1", status: "building", version: 0 }]);
  });

  it("starts the build from the capture and shows the goal, not the raw seed", async () => {
    const { getByText, queryByText } = renderChat();
    await waitFor(() => expect(buildMutateMock).toHaveBeenCalledTimes(1));
    // The build engine gets the FULL capture (intent + routine + tools)…
    const description = buildMutateMock.mock.calls[0]?.[0]?.description ?? "";
    expect(description).toContain("What to build:");
    expect(description).toContain("Monday recap");
    // …while the transcript shows the narrated goal as the operator's message.
    expect(getByText("Recap the pipeline every Monday.")).toBeTruthy();
    expect(queryByText(/What to build:/)).toBeNull();
  });

  it("creates the captured routine and tools once the agent id resolves", async () => {
    renderChat();
    await waitFor(() => expect(createRoutineMock).toHaveBeenCalledTimes(1));
    expect(createRoutineMock).toHaveBeenCalledWith({
      agent: "app_new1",
      name: "Monday recap",
      prompt: "Summarize last week's pipeline.",
      schedule: "0 9 * * 1",
    });
    await waitFor(() => expect(buildToolMock).toHaveBeenCalledTimes(1));
    expect(buildToolMock).toHaveBeenCalledWith(
      "summarizePipeline — Read deals and recap moves.",
      "app_new1",
    );
  });
});
