import type { ReactNode } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import type { OfficeMember } from "../../api/client";
import type { Task, TaskListResponse } from "../../api/tasks";
import type { LifecycleState } from "../../lib/types/lifecycle";
import { TaskModal } from "./TaskModal";

/**
 * The modal reads its task out of the board's `["issues","list"]` query and
 * its owner roster out of `["office-members"]`. Seeding both in the story
 * keeps it self-contained — no app shell, no broker, no network.
 */
function seeded(task: Task, members: OfficeMember[]): QueryClient {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  const payload: TaskListResponse = { tasks: [task] };
  client.setQueryData(["issues", "list"], payload);
  client.setQueryData(["office-members"], { members });
  return client;
}

const MEMBERS = [
  { slug: "pam", name: "Pam Beesly" },
  { slug: "jim", name: "Jim Halpert" },
  { slug: "dwight", name: "Dwight Schrute" },
] as OfficeMember[];

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: "DUNDE-72",
    title: "Ship the Q3 pricing page",
    details:
      "Two tiers, an annual toggle, and no enterprise CTA. Copy is signed off; " +
      "this is the build plus the pricing table component.",
    status: "in_progress",
    lifecycle_state: "running",
    owner: "pam",
    channel: "general",
    ...overrides,
  };
}

function Frame({ task, children }: { task: Task; children?: ReactNode }) {
  return (
    <QueryClientProvider client={seeded(task, MEMBERS)}>
      {children}
    </QueryClientProvider>
  );
}

const meta: Meta<typeof TaskModal> = {
  title: "Lifecycle/TaskModal",
  component: TaskModal,
  parameters: { layout: "fullscreen" },
};

export default meta;

type Story = StoryObj<typeof TaskModal>;

/** Build one story around a task. */
function storyFor(task: Task): Story {
  return {
    render: () => (
      <Frame task={task}>
        <TaskModal taskId={task.id} onClose={() => {}} />
      </Frame>
    ),
  };
}

/** The everyday case: a running task with an owner and a short description. */
export const Default: Story = storyFor(makeTask());

/** A description long enough to scroll inside the dialog — the footer
 *  actions must stay reachable rather than being pushed off-screen. */
export const LongDescription: Story = storyFor(
  makeTask({
    details: Array.from(
      { length: 12 },
      (_, i) =>
        `Paragraph ${i + 1}. The pricing page rewrite touches the plan matrix, ` +
        "the annual/monthly toggle, and the FAQ block underneath it.",
    ).join("\n\n"),
  }),
);

/** No owner yet. The picker must say "unassigned" rather than inventing a
 *  name, and reassigning from here is the whole point of the row. */
export const UnassignedOwner: Story = storyFor(makeTask({ owner: undefined }));

/** No description written yet. */
export const EmptyDescription: Story = storyFor(
  makeTask({ details: "", description: "" }),
);

/** A long name, to check the title input does not clip or wrap oddly. */
export const LongName: Story = storyFor(
  makeTask({
    title:
      "Ship the Q3 pricing page, the annual toggle, and the migration for existing monthly subscribers",
  }),
);

/** A task the board no longer knows about — the modal must say so plainly
 *  instead of rendering an empty shell. */
export const NotFound: Story = {
  render: () => (
    <Frame task={makeTask()}>
      <TaskModal taskId="DUNDE-999" onClose={() => {}} />
    </Frame>
  ),
};

// ── One story per lifecycle state ────────────────────────────────────
// The status row renders the broker's real, state-appropriate verb set, so
// each state shows a different set of buttons. These cover every state the
// modal can be opened in.

function stateStory(state: LifecycleState, status: string): Story {
  return storyFor(makeTask({ lifecycle_state: state, status }));
}

export const StateDrafting: Story = stateStory("drafting", "open");
export const StatePlanning: Story = stateStory("planning", "open");
export const StateIntake: Story = stateStory("intake", "open");
export const StateReady: Story = stateStory("ready", "open");
export const StateRunning: Story = stateStory("running", "in_progress");
export const StateReview: Story = stateStory("review", "review");
export const StateDecision: Story = stateStory("decision", "review");
export const StateBlocked: Story = stateStory("blocked", "blocked");
export const StateQueuedBehindOwner: Story = stateStory(
  "queued_behind_owner",
  "open",
);
export const StateChangesRequested: Story = stateStory(
  "changes_requested",
  "in_progress",
);
export const StateApproved: Story = stateStory("approved", "done");
export const StateRejected: Story = stateStory("rejected", "cancelled");
export const StateArchived: Story = stateStory("archived", "done");
