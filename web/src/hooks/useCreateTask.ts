import { useMutation, useQueryClient } from "@tanstack/react-query";

import { post } from "../api/client";
import type { Task, TaskResponse } from "../api/tasks";
import { track } from "../lib/analytics";

export interface CreateTaskFormInput {
  title: string;
  details?: string;
  /**
   * Channel slug to file the task under. Optional, and normally omitted:
   * channels are not a user-facing concept, and with the shared room retired
   * there is no default to fall back to. When it is absent the broker routes
   * the task to its OWNER's DM (falling back to the creator's), which is a
   * better answer than any slug this layer could guess.
   */
  channel?: string;
  assignee?: string;
  createdBy?: string;
}

export interface CreateTaskResult {
  task?: Task;
}

/**
 * Mutation wrapper for the primary "new Issue" creation surfaces
 * (dialog, command palette, CEO inline card).
 *
 * Routes through POST /tasks (action=create, task_type=issue) — the SAME
 * path createSubTask uses. Creation is the authorization: the broker
 * lands an owner-set Issue RUNNING (owner dispatched) and an ownerless
 * Issue READY (dispatches on assignment). Parking is a separate,
 * deliberate composer action (/task-plan park=true).
 */
export function useCreateTask() {
  const queryClient = useQueryClient();
  return useMutation<CreateTaskResult, Error, CreateTaskFormInput>({
    mutationFn: async (input) => {
      const body: Record<string, unknown> = {
        action: "create",
        title: input.title.trim(),
        details: input.details?.trim() || "",
        owner: input.assignee?.trim() || "",
        created_by: input.createdBy?.trim() || "human",
        task_type: "issue",
      };
      // Send `channel` only when the caller actually named one. This was
      // `|| "general"`, which stopped resolving the moment the shared room was
      // retired and made every Issue created from the dialog, the command
      // palette, or the inline card fail with "channel not found". Omitting it
      // hands the decision to the broker, which routes to the owner's DM.
      const channel = input.channel?.trim();
      if (channel) body.channel = channel;
      const response = await post<TaskResponse>("/tasks", body);
      return { task: response.task };
    },
    onSuccess: (_result, input) => {
      track("task_created", {
        source: "inline",
        owner_agent: input.assignee?.trim() || "",
        has_details: !!input.details?.trim(),
        start_mode: "start",
      });
      void queryClient.invalidateQueries({ queryKey: ["issues"] });
      void queryClient.invalidateQueries({ queryKey: ["office-tasks"] });
      void queryClient.invalidateQueries({ queryKey: ["lifecycle"] });
    },
  });
}
