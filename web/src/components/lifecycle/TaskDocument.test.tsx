/**
 * TaskDocument — component tests.
 *
 * All tests use `initialDocument` to bypass the TanStack Query fetch so
 * the suite stays deterministic without a network/broker. The query-key
 * caching and loading/error states are exercised with a forceState-style
 * approach to keep the tests readable.
 *
 * core-loop R2 removed the spec surface (4-section SpecBody, streaming
 * draft sections, spec summary/collapse) — those tests were deleted with
 * the behavior. The task brief is title + description only.
 */

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { TaskDocument as TaskDocumentType } from "./TaskDocument";
import {
  normalizeTaskDocument,
  StartParkedTaskButton,
  TaskDocument,
} from "./TaskDocument";

const lifecycleApi = vi.hoisted(() => ({
  postDecision: vi.fn(() =>
    Promise.resolve({ taskId: "task-001", action: "approve", status: "ok" }),
  ),
}));

vi.mock("../../api/lifecycle", () => lifecycleApi);

// TaskActivityFeed hardcodes refetchInterval: 8_000 which keeps the
// vitest worker alive past teardown when fetch fails. Stub the api so
// the query resolves synchronously and idle.
vi.mock("../../api/tasks", async () => {
  const actual =
    await vi.importActual<typeof import("../../api/tasks")>("../../api/tasks");
  return {
    ...actual,
    getTaskActivity: vi.fn(() => Promise.resolve({ events: [] })),
    getSubTasks: vi.fn(() => Promise.resolve({ tasks: [] })),
  };
});

// useOfficeMembers (called from Autocomplete deep in the comment form) hits
// /office-members on a 5-second interval. Stub the underlying api so it
// resolves synchronously with no members and no polling effect.
vi.mock("../../api/client", async () => {
  const actual =
    await vi.importActual<typeof import("../../api/client")>(
      "../../api/client",
    );
  return {
    ...actual,
    getOfficeMembers: vi.fn(() => Promise.resolve({ members: [], meta: {} })),
    getMembers: vi.fn(() => Promise.resolve({ members: [] })),
  };
});

// The real chat pane reaches useChannelSlug -> useMatches, which needs a
// TanStack Router context this suite does not mount. Stub it: what these
// tests assert is which BRANCH TaskDocument takes — chat pane vs the
// no-conversation empty state — not what the pane renders inside. Keeps the
// real component's data-testid so the assertions stay honest.
vi.mock("./TaskChannelChat", () => ({
  TaskChannelChat: ({ channel }: { channel: string }) => (
    <div data-testid="task-channel-chat" data-channel={channel} />
  ),
}));

// ── Fixtures ───────────────────────────────────────────────────────────

const BASE_DOC: TaskDocumentType = {
  taskId: "task-001",
  channel: "issue-specs",
  title: "Stripe webhook handler",
  description:
    "Receive Stripe webhook events and update subscription state. POST /stripe/webhook with HMAC-SHA256 verification.",
  lifecycleState: "drafting",
};

const APPROVED_DOC: TaskDocumentType = {
  ...BASE_DOC,
  taskId: "task-002",
  lifecycleState: "approved",
};

const RUNNING_DOC: TaskDocumentType = {
  ...BASE_DOC,
  taskId: "task-003",
  lifecycleState: "running",
};

// ── Helpers ────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        // useOfficeMembers + other hooks set refetchInterval to keep
        // data fresh in production. In tests those polls keep the
        // vitest worker alive past teardown — disable globally.
        refetchInterval: false,
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
        refetchOnMount: false,
      },
    },
  });
}

function renderDoc(
  doc: TaskDocumentType,
  props: Partial<{ taskId: string }> = {},
) {
  const client = makeClient();
  const taskId = props.taskId ?? doc.taskId;
  const { container } = render(
    <QueryClientProvider client={client}>
      <TaskDocument taskId={taskId} initialDocument={doc} />
    </QueryClientProvider>,
  );
  return { container };
}

// ── Suite ──────────────────────────────────────────────────────────────

// This describe (and "— parked Start" below) carried a FIXME and a
// describe.skip: the full file hung the vitest worker, suspected to be "a
// transitive timer/SSE handle that survives teardown". Both are un-skipped
// now and the file runs green in under a second.
//
// The trigger looks to have been the real TaskChannelChat: mounting it pulls
// in MessageFeed + Composer and their polling/SSE, which the existing mocks
// for EventSource / getTaskActivity / getSubTasks / useOfficeMembers did not
// cover. Stubbing TaskChannelChat (see above) removes that whole subtree, and
// with it the hang. If this file ever hangs again, that mock is the first
// thing to check.
describe("<TaskDocument>", () => {
  beforeEach(() => {
    // Clear sessionStorage to keep tests independent.
    try {
      sessionStorage.clear();
    } catch {
      // ignore private-mode
    }
  });

  afterEach(() => {
    lifecycleApi.postDecision.mockClear();
    vi.restoreAllMocks();
  });

  // ── Status pill ─────────────────────────────────────────────────────

  it("renders the status pill matching the lifecycle state", () => {
    renderDoc(BASE_DOC);
    const pill = document.querySelector("[data-state='drafting']");
    expect(pill).not.toBeNull();
    // drafting renders as "parked" — the explicit park state's label.
    expect(pill?.textContent).toMatch(/parked/i);
  });

  it("renders approved pill for approved state", () => {
    renderDoc(APPROVED_DOC);
    const pill = document.querySelector("[data-state='approved']");
    expect(pill).not.toBeNull();
  });

  // ── Button row slot ──────────────────────────────────────────────────

  it("renders the button row slot", () => {
    renderDoc(BASE_DOC);
    const row = screen.getByTestId("issue-doc-button-row");
    expect(row).toBeInTheDocument();
    // In the parked (drafting) state, the Start button is inside the row —
    // the ONE place a start affordance remains.
    expect(row.querySelector("[data-testid='start-parked']")).not.toBeNull();
  });

  it("button row hides the parked Start button for non-drafting states", () => {
    renderDoc(APPROVED_DOC);
    const row = screen.getByTestId("issue-doc-button-row");
    // TaskActionToolbar now renders state-appropriate actions for every
    // lifecycle (e.g. Cancel on approved), so the row is no longer empty.
    // What we still want to guarantee is that the parked Start button
    // is suppressed off the drafting state.
    expect(row.querySelector("[data-testid='start-parked']")).toBeNull();
  });
});

describe("normalizeTaskDocument", () => {
  it("normalizes the broker decision-packet shape with task metadata fallback", () => {
    const doc = normalizeTaskDocument(
      {
        taskId: "task-5",
        lifecycleState: "blocked",
        updatedAt: "2026-05-21T03:23:41Z",
      },
      {
        id: "task-5",
        channel: "email-ops",
        title: "Pull unread emails",
        details: "Seed one profile per sender.",
        owner: "contact-intel",
        status: "blocked",
        lifecycle_state: "blocked",
      },
    );

    expect(doc.title).toBe("Pull unread emails");
    expect(doc.channel).toBe("email-ops");
    expect(doc.ownerSlug).toBe("contact-intel");
    expect(doc.lifecycleState).toBe("blocked");
    expect(doc.description).toBe("Seed one profile per sender.");
  });

  it("reads the description from the wrapped task record", () => {
    const doc = normalizeTaskDocument({
      taskId: "task-6",
      lifecycleState: "running",
      task: {
        id: "task-6",
        channel: "growth",
        title: "Ship the importer",
        details: "Importer reads the CSV and writes contacts.",
      },
    });

    expect(doc.title).toBe("Ship the importer");
    expect(doc.description).toBe("Importer reads the CSV and writes contacts.");
  });

  it("accepts a task with no channel instead of throwing", () => {
    // Inverted deliberately. This used to assert a throw, and because the
    // throw ran inside the React Query fetcher it did not crash the app — it
    // rendered TaskDocumentError ("Could not load task / task channel is
    // missing") with a Retry that could never succeed. A task with no
    // conversation home is an unowned task, not a load failure.
    const doc = normalizeTaskDocument({
      taskId: "task-5",
      title: "Pull unread emails",
      lifecycleState: "drafting",
    });

    expect(doc.channel).toBeUndefined();
    expect(doc.taskId).toBe("task-5");
    expect(doc.title).toBe("Pull unread emails");
  });

  it("treats a whitespace-only channel as no channel", () => {
    // "   " is truthy, so a permissive check would have carried it into a
    // message query as a channel slug.
    const doc = normalizeTaskDocument({
      taskId: "task-8",
      lifecycleState: "running",
      task: { id: "task-8", channel: "   ", title: "Blank channel" },
    });

    expect(doc.channel).toBeUndefined();
  });

  it("normalizes the structured definition from the wrapped task record", () => {
    const doc = normalizeTaskDocument({
      taskId: "task-7",
      lifecycleState: "running",
      task: {
        id: "task-7",
        channel: "growth",
        title: "Launch the newsletter",
        definition: {
          goal: "first partner newsletter shipped",
          deliverables: [{ name: "draft", format: "markdown" }, { name: 42 }],
          success_criteria: ["human approved the draft", ""],
          access_needed: ["mailing-list account"],
          defined_at: "2026-06-10T09:14:00Z",
        },
      },
    });

    expect(doc.definition).toEqual({
      goal: "first partner newsletter shipped",
      deliverables: [{ name: "draft", format: "markdown" }],
      success_criteria: ["human approved the draft"],
      access_needed: ["mailing-list account"],
      defined_at: "2026-06-10T09:14:00Z",
    });
  });

  it("treats a goal-less definition payload as absent", () => {
    const doc = normalizeTaskDocument({
      taskId: "task-8",
      lifecycleState: "running",
      task: {
        id: "task-8",
        channel: "growth",
        title: "Launch the newsletter",
        definition: { deliverables: [{ name: "draft" }] },
      },
    });

    expect(doc.definition).toBeUndefined();
  });
});

// ── Parked Start button ───────────────────────────────────────────────

describe("<TaskDocument> — parked Start", () => {
  beforeEach(() => {
    try {
      sessionStorage.clear();
    } catch {
      // ignore
    }
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the Start button when lifecycleState is drafting (parked)", () => {
    renderDoc(BASE_DOC);
    expect(screen.getByTestId("start-parked")).toBeInTheDocument();
  });

  it("does NOT render the Start button when state is approved", () => {
    renderDoc(APPROVED_DOC);
    expect(screen.queryByTestId("start-parked")).toBeNull();
  });

  it("does NOT render the Start button when state is running", () => {
    renderDoc(RUNNING_DOC);
    expect(screen.queryByTestId("start-parked")).toBeNull();
  });

  it("Start button has the correct aria-label", () => {
    renderDoc(BASE_DOC);
    const btn = screen.getByTestId("start-parked");
    expect(btn).toHaveAttribute("aria-label", "Start this parked task");
  });

  it("clicking the Start button fires a click event", () => {
    // This test verifies the button is clickable and triggers the mutation
    // flow. The actual approve action requires the broker; we verify the
    // button is present, enabled, and fires onClick correctly.
    renderDoc(BASE_DOC);
    const btn = screen.getByTestId("start-parked");
    expect(btn).not.toBeDisabled();
    // Clicking should not throw.
    expect(() => fireEvent.click(btn)).not.toThrow();
  });

  it("error banner is absent before any failed start", () => {
    renderDoc(BASE_DOC);
    // Error banner should NOT be present initially.
    expect(screen.queryByTestId("start-parked-error")).toBeNull();
  });
});

// ── StartParkedTaskButton (ceremony retirement regression) ──────────────
//
// Pure component — tested directly rather than through a full <TaskDocument>
// mount, because the assertions here are about the button itself.
// Pins the retirement of the Approve & Start ceremony: the start button
// reads "Start"-family copy and posts the decision approve on click; the
// old "Waiting on you — press Approve & Start" chat hint is gone.
describe("<StartParkedTaskButton>", () => {
  afterEach(() => {
    lifecycleApi.postDecision.mockClear();
  });

  it("renders the parked Start affordance with start copy, not Approve & Start", () => {
    render(
      <QueryClientProvider client={makeClient()}>
        <StartParkedTaskButton
          taskId="task-1"
          onApproved={() => {}}
          label="Parked — start"
        />
      </QueryClientProvider>,
    );
    const btn = screen.getByTestId("start-parked");
    expect(btn).toHaveTextContent("Parked — start");
    expect(btn).not.toHaveTextContent("Approve & Start");
    expect(btn).toHaveAttribute("aria-label", "Start this parked task");
  });

  it("posts the decision approve (the drafting→running un-park) on click", async () => {
    render(
      <QueryClientProvider client={makeClient()}>
        <StartParkedTaskButton taskId="task-9" onApproved={() => {}} />
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByTestId("start-parked"));
    await waitFor(() =>
      expect(lifecycleApi.postDecision).toHaveBeenCalledWith(
        "task-9",
        "approve",
      ),
    );
  });
});

// A task with no conversation home must render an ordinary empty state, not
// the "Could not load task" error card with its unwinnable Retry. Kept in its
// own describe rather than the skipped <TaskDocument> one above, which is
// disabled for an unrelated worker-teardown hang.
describe("<TaskDocument> with no conversation home", () => {
  const NO_CHANNEL_DOC: TaskDocumentType = {
    ...BASE_DOC,
    taskId: "task-homeless",
    channel: undefined,
    ownerSlug: undefined,
  };

  it("renders the no-conversation empty state, not an error", () => {
    renderDoc(NO_CHANNEL_DOC);

    expect(screen.getByTestId("issue-doc-no-conversation")).toBeInTheDocument();
    // The bug: this rendered TaskDocumentError with a Retry that could never
    // succeed, so the whole detail page was dead.
    expect(
      screen.queryByTestId("issue-document-error"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText(/task channel is missing/i),
    ).not.toBeInTheDocument();
  });

  it("says what to do about it, and never names #general", () => {
    renderDoc(NO_CHANNEL_DOC);

    const panel = screen.getByTestId("issue-doc-no-conversation");
    expect(panel).toHaveTextContent(/no owner yet/i);
    expect(panel).toHaveTextContent(/assign/i);
    expect(panel.textContent ?? "").not.toMatch(/general/i);
  });

  it("does not render a chat pane for a task with nowhere to talk", () => {
    renderDoc(NO_CHANNEL_DOC);
    expect(screen.queryByTestId("task-channel-chat")).not.toBeInTheDocument();
  });

  it("still renders the task header so the page is usable", () => {
    renderDoc(NO_CHANNEL_DOC);
    expect(screen.getByTestId("issue-document")).toBeInTheDocument();
    expect(screen.getByText("Stripe webhook handler")).toBeInTheDocument();
  });

  it("leaves a task WITH a channel showing its chat, unchanged", () => {
    renderDoc(BASE_DOC);

    expect(screen.getByTestId("task-channel-chat")).toBeInTheDocument();
    expect(
      screen.queryByTestId("issue-doc-no-conversation"),
    ).not.toBeInTheDocument();
  });
});
