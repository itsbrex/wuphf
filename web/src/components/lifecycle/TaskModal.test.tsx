/**
 * Tests for the shared task modal.
 *
 * The founder's bar: every task affordance opens ONE modal carrying the full
 * task, and all four fields (name, description, owner, status) are editable
 * from it. These tests pin the render and each of the four save paths, plus
 * the property that a click never navigates.
 */

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { OfficeMember } from "../../api/client";
import type { Task, TaskListResponse } from "../../api/tasks";
import { router } from "../../lib/router";
import { useAppStore } from "../../stores/app";
import { TaskModal } from "./TaskModal";

const editTaskFields = vi.hoisted(() => vi.fn());
const reassignTask = vi.hoisted(() => vi.fn());
const updateTaskStatus = vi.hoisted(() => vi.fn());

vi.mock("../../api/tasks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api/tasks")>();
  return { ...actual, editTaskFields, reassignTask, updateTaskStatus };
});

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: "DUNDE-72",
    title: "Ship the Q3 pricing page",
    details: "Two tiers, annual toggle, no enterprise CTA.",
    status: "in_progress",
    lifecycle_state: "running",
    owner: "pam",
    channel: "general",
    ...overrides,
  };
}

const MEMBERS: OfficeMember[] = [
  { slug: "pam", name: "Pam" } as OfficeMember,
  { slug: "jim", name: "Jim" } as OfficeMember,
];

function renderModal(
  task: Task = makeTask(),
  opts: { onClose?: () => void } = {},
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const payload: TaskListResponse = { tasks: [task] };
  client.setQueryData(["issues", "list"], payload);
  client.setQueryData(["office-members"], { members: MEMBERS });
  const view = render(
    <QueryClientProvider client={client}>
      <TaskModal taskId={task.id} onClose={opts.onClose ?? (() => {})} />
    </QueryClientProvider>,
  );
  return { ...view, client };
}

describe("<TaskModal>", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    editTaskFields.mockResolvedValue({ task: makeTask() });
    reassignTask.mockResolvedValue({ task: makeTask() });
    updateTaskStatus.mockResolvedValue({ task: makeTask() });
    useAppStore.setState({ taskModalTaskId: null });
  });

  it("renders the task's id, name, description, owner, and state", () => {
    renderModal();
    expect(screen.getByTestId("task-modal")).toHaveAttribute(
      "data-task-id",
      "DUNDE-72",
    );
    expect(screen.getByText("DUNDE-72")).toBeInTheDocument();
    expect(screen.getByTestId("task-modal-title")).toHaveValue(
      "Ship the Q3 pricing page",
    );
    expect(screen.getByTestId("task-modal-description")).toHaveValue(
      "Two tiers, annual toggle, no enterprise CTA.",
    );
    expect(screen.getByTestId("issue-owner-pill")).toHaveTextContent("@pam");
    expect(screen.getByTestId("task-modal")).toHaveAttribute(
      "data-lifecycle-state",
      "running",
    );
  });

  it("falls back to task.description when the broker wrote no details", () => {
    renderModal(makeTask({ details: undefined, description: "Legacy body" }));
    expect(screen.getByTestId("task-modal-description")).toHaveValue(
      "Legacy body",
    );
  });

  it("saves an edited NAME through the edit verb", async () => {
    renderModal();
    const title = screen.getByTestId("task-modal-title");
    await userEvent.clear(title);
    await userEvent.type(title, "Ship the Q4 pricing page");
    await userEvent.click(screen.getByTestId("task-modal-save"));

    // Form save: the untouched description goes up complete alongside it.
    expect(editTaskFields).toHaveBeenCalledWith(
      "DUNDE-72",
      {
        title: "Ship the Q4 pricing page",
        details: "Two tiers, annual toggle, no enterprise CTA.",
      },
      "general",
    );
  });

  it("saves an edited DESCRIPTION through the edit verb", async () => {
    renderModal();
    const description = screen.getByTestId("task-modal-description");
    await userEvent.clear(description);
    await userEvent.type(description, "Three tiers now.");
    await userEvent.click(screen.getByTestId("task-modal-save"));

    expect(editTaskFields).toHaveBeenCalledWith(
      "DUNDE-72",
      { title: "Ship the Q3 pricing page", details: "Three tiers now." },
      "general",
    );
  });

  it("clears a description by sending an empty details string", async () => {
    // The whole reason `edit` is a form save rather than a patch: an omitted
    // or blank field elsewhere in the broker means "leave alone", so a delete
    // would silently not stick.
    renderModal();
    await userEvent.clear(screen.getByTestId("task-modal-description"));
    await userEvent.click(screen.getByTestId("task-modal-save"));

    expect(editTaskFields).toHaveBeenCalledWith(
      "DUNDE-72",
      { title: "Ship the Q3 pricing page", details: "" },
      "general",
    );
  });

  it("saves both fields in ONE call, never one call per field", async () => {
    // Two calls would post two change announcements into the channel for a
    // single human save.
    renderModal();
    await userEvent.type(screen.getByTestId("task-modal-title"), " v2");
    await userEvent.type(
      screen.getByTestId("task-modal-description"),
      " Also a FAQ.",
    );
    await userEvent.click(screen.getByTestId("task-modal-save"));

    expect(editTaskFields).toHaveBeenCalledTimes(1);
    expect(editTaskFields).toHaveBeenCalledWith(
      "DUNDE-72",
      {
        title: "Ship the Q3 pricing page v2",
        details: "Two tiers, annual toggle, no enterprise CTA. Also a FAQ.",
      },
      "general",
    );
  });

  it("saves a changed OWNER through the reassign verb", async () => {
    renderModal();
    await userEvent.click(screen.getByTestId("issue-owner-pill"));
    await userEvent.selectOptions(
      screen.getByTestId("issue-owner-select"),
      "jim",
    );

    expect(reassignTask).toHaveBeenCalledWith("DUNDE-72", "jim", "general");
  });

  it("saves a changed STATUS through a real broker verb", async () => {
    renderModal();
    // "running" offers submit_for_review / complete / block / cancel — the
    // real, state-appropriate verb set, not an invented status list.
    await userEvent.click(screen.getByTestId("action-complete"));

    expect(updateTaskStatus).toHaveBeenCalledWith(
      "DUNDE-72",
      "complete",
      "general",
      "human",
      { overrideReason: undefined },
    );
  });

  it("invalidates the board query so Tasks and chat both refresh", async () => {
    const { client } = renderModal();
    const invalidate = vi.spyOn(client, "invalidateQueries");

    await userEvent.type(screen.getByTestId("task-modal-title"), "!");
    await userEvent.click(screen.getByTestId("task-modal-save"));

    // ["issues"] is the prefix of the board's ["issues","list"], so this
    // invalidation reaches the Tasks board and every other issues query.
    await vi.waitFor(() => {
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ["issues"] });
    });
    invalidate.mockRestore();
  });

  it("keeps Save disabled until something changes", async () => {
    renderModal();
    expect(screen.getByTestId("task-modal-save")).toBeDisabled();
    await userEvent.type(screen.getByTestId("task-modal-title"), "x");
    expect(screen.getByTestId("task-modal-save")).toBeEnabled();
  });

  it("refuses to save an empty name", async () => {
    // The broker rejects this too (400 "title required"); catching it here
    // saves the round-trip and gives a friendlier message.
    renderModal();
    await userEvent.clear(screen.getByTestId("task-modal-title"));
    await userEvent.click(screen.getByTestId("task-modal-save"));

    expect(editTaskFields).not.toHaveBeenCalled();
    expect(screen.getByTestId("task-modal-save-error")).toHaveTextContent(
      "A task needs a name.",
    );
  });

  it("surfaces a save failure instead of swallowing it", async () => {
    editTaskFields.mockRejectedValue(new Error("unknown action"));
    renderModal();
    await userEvent.type(screen.getByTestId("task-modal-title"), "!");
    await userEvent.click(screen.getByTestId("task-modal-save"));

    expect(
      await screen.findByTestId("task-modal-save-error"),
    ).toHaveTextContent("unknown action");
  });

  it("surfaces a broker rejection of an unauthorised edit", async () => {
    // `edit` is human + CEO only; a rejection must reach the human, not be
    // swallowed into a modal that looks like it saved.
    editTaskFields.mockRejectedValue(new Error("forbidden"));
    renderModal();
    await userEvent.type(screen.getByTestId("task-modal-title"), "!");
    await userEvent.click(screen.getByTestId("task-modal-save"));

    expect(
      await screen.findByTestId("task-modal-save-error"),
    ).toHaveTextContent("forbidden");
  });

  it("shows an unassigned owner without inventing a name", () => {
    renderModal(makeTask({ owner: undefined }));
    expect(screen.getByTestId("issue-owner-pill")).toHaveTextContent(
      "unassigned",
    );
  });

  it("never substitutes #general for a task with no conversation home", async () => {
    // `task.channel?.trim() || "general"` routed a homeless task's writes into
    // the retired room. The honest empty value goes up instead; the broker
    // resolves the edit against the task's own channel regardless.
    renderModal(makeTask({ channel: undefined }));
    await userEvent.type(screen.getByTestId("task-modal-title"), "!");
    await userEvent.click(screen.getByTestId("task-modal-save"));

    expect(editTaskFields).toHaveBeenCalledTimes(1);
    expect(editTaskFields.mock.calls[0][2]).toBe("");
  });

  it("does not substitute #general on an owner change either", async () => {
    renderModal(makeTask({ channel: undefined }));
    await userEvent.click(screen.getByTestId("issue-owner-pill"));
    await userEvent.selectOptions(
      screen.getByTestId("issue-owner-select"),
      "jim",
    );

    expect(reassignTask).toHaveBeenCalledWith("DUNDE-72", "jim", "");
  });

  it("still passes a real channel through untouched", async () => {
    renderModal(makeTask({ channel: "eng" }));
    await userEvent.type(screen.getByTestId("task-modal-title"), "!");
    await userEvent.click(screen.getByTestId("task-modal-save"));

    expect(editTaskFields.mock.calls[0][2]).toBe("eng");
  });

  it("keeps the full task page reachable", async () => {
    const navigate = vi.spyOn(router, "navigate").mockResolvedValue(undefined);
    renderModal();
    await userEvent.click(screen.getByTestId("task-modal-open-page"));

    expect(navigate).toHaveBeenCalledWith({
      to: "/tasks/$taskId",
      params: { taskId: "DUNDE-72" },
    });
    navigate.mockRestore();
  });
});
