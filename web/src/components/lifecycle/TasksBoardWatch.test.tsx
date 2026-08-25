import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { Task } from "../../api/tasks";
import { quietForLabel, TasksList } from "./TasksList";

// Two things the board could not answer once the CEO stopped routing every
// task: "what is STUCK, as opposed to merely quiet" and "what does nobody
// own". Neither had any surface. The stall case is the sharper one — the
// broker has always stamped stalled_since, but the only delivery was a chat
// post from a sender called "system", which the no-system-senders rule
// retires, so the board is now the ONLY place a stall can reach a human.

vi.mock("../../api/lifecycle", () => ({
  getInboxItems: vi.fn(async () => ({ items: [] })),
}));
vi.mock("../../api/scheduler", () => ({
  getScheduler: vi.fn(async () => ({ jobs: [] })),
}));

function makeTask(over: Partial<Task> = {}): Task {
  return {
    id: "task-1",
    title: "Draft the Thursday lead story",
    status: "in_progress",
    lifecycle_state: "running",
    task_type: "issue",
    owner: "writer",
    ...over,
  } as Task;
}

function wrap(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

function board(tasks: Task[]) {
  return wrap(<TasksList initialTasks={tasks} initialInboxItems={[]} />);
}

describe("quietForLabel", () => {
  it("reports how long, in units a person reads at a glance", () => {
    const minutesAgo = (n: number) =>
      new Date(Date.now() - n * 60_000).toISOString();
    expect(quietForLabel(minutesAgo(23))).toBe("23m");
    expect(quietForLabel(minutesAgo(90))).toBe("1h");
    expect(quietForLabel(minutesAgo(60 * 24 * 3))).toBe("3d");
  });

  it("says nothing when there is nothing to say", () => {
    expect(quietForLabel(undefined)).toBeUndefined();
    expect(quietForLabel("  ")).toBeUndefined();
    expect(quietForLabel("not-a-date")).toBeUndefined();
    // Under a minute is not yet worth a marker.
    expect(quietForLabel(new Date().toISOString())).toBeUndefined();
  });

  // Broker and browser clocks disagree; "quiet for -3m" would be worse than
  // no marker at all.
  it("does not render a stall from a future timestamp", () => {
    const future = new Date(Date.now() + 10 * 60_000).toISOString();
    expect(quietForLabel(future)).toBeUndefined();
  });
});

describe("stalled tasks are distinguishable from running ones", () => {
  it("marks a stalled task with how long it has been quiet", () => {
    board([
      makeTask({
        stalled_since: new Date(Date.now() - 23 * 60_000).toISOString(),
      }),
    ]);
    expect(screen.getByTestId("issue-stalled")).toHaveTextContent(
      "Quiet for 23m",
    );
  });

  // The defect: a task that hit an auth wall and one that is merely slow
  // rendered identically.
  it("leaves a healthy running task unmarked", () => {
    board([makeTask()]);
    expect(screen.queryByTestId("issue-stalled")).not.toBeInTheDocument();
  });

  // Honesty: the watchdog observes ABSENCE of activity, which is not proof of
  // failure. The card must not say "failed" or "stuck".
  it("says quiet rather than claiming failure", () => {
    board([
      makeTask({
        stalled_since: new Date(Date.now() - 5 * 60_000).toISOString(),
      }),
    ]);
    const marker = screen.getByTestId("issue-stalled");
    expect(marker).toHaveTextContent(/quiet/i);
    expect(marker).not.toHaveTextContent(/failed|broken|error/i);
  });
});

describe("ownerless work is visible board-wide", () => {
  it("counts what nobody owns", () => {
    board([
      makeTask({ id: "task-1", owner: "writer" }),
      makeTask({ id: "task-2", owner: "" }),
      makeTask({ id: "task-3", owner: undefined }),
    ]);
    expect(screen.getByTestId("issues-unassigned-chip")).toHaveTextContent(
      "2 unassigned",
    );
  });

  it("offers no chip when everything has an owner", () => {
    board([makeTask({ id: "task-1", owner: "writer" })]);
    expect(
      screen.queryByTestId("issues-unassigned-chip"),
    ).not.toBeInTheDocument();
  });

  it("filters the board down to ownerless work and back", () => {
    board([
      makeTask({ id: "task-1", title: "Owned work", owner: "writer" }),
      makeTask({ id: "task-2", title: "Nobody picked this up", owner: "" }),
    ]);
    expect(screen.getByText("Owned work")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("issues-unassigned-chip"));
    expect(screen.queryByText("Owned work")).not.toBeInTheDocument();
    expect(screen.getByText("Nobody picked this up")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("issues-unassigned-chip"));
    expect(screen.getByText("Owned work")).toBeInTheDocument();
  });
});
