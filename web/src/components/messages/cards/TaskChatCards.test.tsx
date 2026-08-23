/**
 * The two remaining task cards in the chat stream must behave like every
 * other task affordance: open the shared modal in place, never navigate the
 * reader out of the conversation they are reading.
 *
 * TaskLifecycleCard has its own file; these cover the created + comment
 * cards, which the founder's report named directly ("in chat cards for
 * tasks ... all just open a task modal").
 */

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAppStore } from "../../../stores/app";
import { TaskCommentCard } from "./TaskCommentCard";
import { TaskCreatedCard } from "./TaskCreatedCard";

const navigate = vi.fn();
vi.mock("../../../lib/router", () => ({
  router: { navigate: (...args: unknown[]) => navigate(...args) },
}));

describe("task cards in the chat stream", () => {
  beforeEach(() => {
    useAppStore.setState({ taskModalTaskId: null });
  });

  afterEach(() => {
    cleanup();
    navigate.mockReset();
  });

  it("TaskCreatedCard opens the modal instead of navigating", () => {
    render(
      <TaskCreatedCard
        payload={{
          task_id: "DUNDE-72",
          title: "Ship the Q3 pricing page",
          owner: "pam",
          lifecycle_state: "running",
        }}
      />,
    );

    fireEvent.click(screen.getByTestId("issue-created-card"));

    expect(useAppStore.getState().taskModalTaskId).toBe("DUNDE-72");
    expect(navigate).not.toHaveBeenCalled();
  });

  it("TaskCommentCard opens the modal instead of navigating", () => {
    render(
      <TaskCommentCard
        payload={{
          task_id: "DUNDE-72",
          title: "Ship the Q3 pricing page",
          author: "jim",
          excerpt: "Pricing table copy is signed off.",
        }}
      />,
    );

    fireEvent.click(screen.getByTestId("issue-comment-card"));

    expect(useAppStore.getState().taskModalTaskId).toBe("DUNDE-72");
    expect(navigate).not.toHaveBeenCalled();
  });

  it("stays inert when the payload lost its task id", () => {
    render(<TaskCreatedCard payload={{ title: "Orphaned card" }} />);

    fireEvent.click(screen.getByTestId("issue-created-card"));

    expect(useAppStore.getState().taskModalTaskId).toBeNull();
    expect(navigate).not.toHaveBeenCalled();
  });
});
