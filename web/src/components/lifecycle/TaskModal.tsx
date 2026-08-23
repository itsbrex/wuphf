/**
 * TaskModal — the ONE surface every task affordance opens.
 *
 * Clicking a task used to navigate to /tasks/$taskId, whose detail surface is
 * chat-primary: it mounts the task's channel conversation as the main column.
 * With the office shell restored, tasks no longer own channels — all
 * discussion happens in the office channel — so a click that lands the human
 * in a chat room is now wrong. Every click site (board card, sub-task row,
 * chat card, inline `DUNDE-72` reference) opens this modal in place instead.
 *
 * The route is deliberately untouched: /tasks/$taskId still renders
 * TaskDocument, so a pasted URL, a bookmark, and the "Open full task page"
 * link below all keep working. Only the CLICK behaviour changed.
 *
 * Editable here: name, description, owner, status — each through the verb
 * that actually owns it. Owner reuses OwnerPicker (`reassign`), status reuses
 * TaskActionToolbar (the real, state-appropriate broker verb set — no
 * invented transitions), and name + description save together through the
 * broker's `edit` action, which is a form save: both values go up complete on
 * every save, so clearing the description is expressible and one edit
 * produces exactly one change announcement in the channel.
 */

import { useId, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import {
  editTaskFields,
  type Task,
  taskToLifecycleState,
} from "../../api/tasks";
import { router } from "../../lib/router";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/Dialog";
import {
  isAwaitingStaffing,
  LifecycleStatePill,
  StaffingStatePill,
} from "./LifecycleStatePill";
import { OwnerPicker } from "./OwnerPicker";
import { TaskActionToolbar } from "./TaskActionToolbar";
import { useTaskRecord } from "./useTaskRecord";

/** The task's own description text, whichever field the broker filled. */
function descriptionOf(task: Task): string {
  return task.details ?? task.description ?? "";
}

export interface TaskModalProps {
  /** Task to show. null renders nothing (the dialog stays closed). */
  taskId: string | null;
  onClose: () => void;
}

export function TaskModal({ taskId, onClose }: TaskModalProps) {
  const { task, isPending, error, refetch } = useTaskRecord(taskId);

  return (
    <Dialog
      open={!!taskId}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent className="task-modal" aria-describedby={undefined}>
        <DialogTitle className="sr-only">Task details</DialogTitle>
        <DialogDescription className="sr-only">
          View and edit this task's name, description, owner, and status.
        </DialogDescription>
        {task ? (
          // Keyed on the task id so switching tasks starts from fresh field
          // state. Within one task the draft is NEVER re-seeded from the
          // 10s board poll — that would delete text mid-sentence.
          <TaskModalBody key={task.id} task={task} onClose={onClose} />
        ) : (
          <TaskModalPlaceholder
            taskId={taskId}
            isPending={isPending}
            error={error}
            onRetry={refetch}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function TaskModalPlaceholder({
  taskId,
  isPending,
  error,
  onRetry,
}: {
  taskId: string | null;
  isPending: boolean;
  error: Error | null;
  onRetry: () => void;
}) {
  if (isPending) {
    return (
      <div
        className="task-modal-placeholder"
        data-testid="task-modal-loading"
        aria-busy="true"
        role="status"
      >
        Loading {taskId ?? "task"}…
      </div>
    );
  }
  return (
    <div
      className="task-modal-placeholder task-modal-placeholder--error"
      data-testid="task-modal-error"
      role="alert"
    >
      <strong>Could not load {taskId ?? "this task"}</strong>
      <p>{error ? error.message : "No task with that id."}</p>
      <button type="button" className="task-modal-retry" onClick={onRetry}>
        Retry
      </button>
    </div>
  );
}

function TaskModalBody({ task, onClose }: { task: Task; onClose: () => void }) {
  const queryClient = useQueryClient();
  const titleFieldId = useId();
  const descriptionFieldId = useId();

  // The RAW stored title, deliberately not run through
  // formatTaskTitleForDisplay. This is an editable form field holding the
  // authoritative value that goes straight back to the broker — stripping the
  // "[@slug] " self-heal prefix for display would silently delete it on the
  // next save. Read-only surfaces (board cards, chat cards) still format.
  const savedTitle = task.title ?? "";
  const savedDescription = descriptionOf(task);
  const [title, setTitle] = useState(savedTitle);
  const [description, setDescription] = useState(savedDescription);
  const [saveError, setSaveError] = useState<string | null>(null);

  const state = taskToLifecycleState(task);
  // "" when the task has no conversation home, NEVER "general". Substituting
  // the retired room here routed a homeless task's writes into a room nobody
  // reads — the silent-write-to-a-dead-room failure. The broker resolves
  // these mutations against the task's OWN channel, so the honest empty value
  // costs nothing and stops the client asserting a room that does not exist.
  const channel = task.channel?.trim() ?? "";
  const awaitingStaffing = isAwaitingStaffing({
    ownerSlug: task.owner,
    lifecycleState: state,
  });

  /** Invalidate every key that renders this task, so the board, the chat
   *  cards, and the task page all pick the change up together. */
  function invalidateTask() {
    void queryClient.invalidateQueries({ queryKey: ["issues"] });
    void queryClient.invalidateQueries({ queryKey: ["issue", task.id] });
    void queryClient.invalidateQueries({
      queryKey: ["issue", "record", task.id],
    });
    void queryClient.invalidateQueries({ queryKey: ["issue-children"] });
  }

  const trimmedTitle = title.trim();
  const trimmedDescription = description.trim();
  const isDirty =
    trimmedTitle !== savedTitle.trim() ||
    trimmedDescription !== savedDescription.trim();

  const saveMutation = useMutation({
    // ONE call carrying BOTH complete values, never one call per changed
    // field: `edit` is a form save, and a second call would post a second
    // change announcement into the channel for a single save.
    mutationFn: () =>
      editTaskFields(
        task.id,
        { title: trimmedTitle, details: trimmedDescription },
        channel,
      ),
    onSuccess: () => {
      setSaveError(null);
      // The broker announces the edit itself (one `task_changed` post tagging
      // the owner), so this deliberately does NOT post a chat message.
      invalidateTask();
    },
    onError: (err: unknown) => {
      setSaveError(err instanceof Error ? err.message : "Could not save.");
    },
  });

  function save() {
    if (!isDirty || saveMutation.isPending) return;
    if (!trimmedTitle) {
      setSaveError("A task needs a name.");
      return;
    }
    setSaveError(null);
    saveMutation.mutate();
  }

  function openFullPage() {
    onClose();
    void router.navigate({
      to: "/tasks/$taskId",
      params: { taskId: task.id },
    });
  }

  return (
    <div
      className="task-modal-body"
      data-testid="task-modal"
      data-task-id={task.id}
      data-lifecycle-state={state}
    >
      <header className="task-modal-header">
        {awaitingStaffing ? (
          <StaffingStatePill />
        ) : (
          <LifecycleStatePill state={state} />
        )}
        <span className="task-modal-id">{task.id}</span>
      </header>

      <div className="task-modal-fields">
        <label className="task-modal-label" htmlFor={titleFieldId}>
          Name
        </label>
        <input
          id={titleFieldId}
          type="text"
          className="task-modal-title-input"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Task name"
          autoComplete="off"
          data-testid="task-modal-title"
        />

        <label className="task-modal-label" htmlFor={descriptionFieldId}>
          Description
        </label>
        <textarea
          id={descriptionFieldId}
          className="task-modal-description-input"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Add a description…"
          rows={6}
          data-testid="task-modal-description"
        />
      </div>

      <div className="task-modal-row">
        <span className="task-modal-label">Owner</span>
        <OwnerPicker
          taskId={task.id}
          channel={channel}
          currentOwner={task.owner}
          onChanged={invalidateTask}
        />
      </div>

      <div className="task-modal-row task-modal-row--stack">
        <span className="task-modal-label">Status</span>
        <TaskActionToolbar
          taskId={task.id}
          channel={channel}
          lifecycleState={state}
          onAfterAction={invalidateTask}
        />
      </div>

      {saveError ? (
        <p
          className="task-modal-error"
          role="alert"
          data-testid="task-modal-save-error"
        >
          {saveError}
        </p>
      ) : null}

      <footer className="task-modal-footer">
        <button
          type="button"
          className="task-modal-secondary"
          onClick={openFullPage}
          data-testid="task-modal-open-page"
        >
          Open full task page
        </button>
        <div className="task-modal-actions">
          <button
            type="button"
            className="task-modal-secondary"
            onClick={onClose}
            data-testid="task-modal-close"
          >
            Close
          </button>
          <button
            type="button"
            className="task-modal-primary"
            onClick={save}
            disabled={!isDirty || saveMutation.isPending}
            data-testid="task-modal-save"
          >
            {saveMutation.isPending ? "Saving…" : "Save"}
          </button>
        </div>
      </footer>
    </div>
  );
}
