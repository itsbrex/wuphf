/**
 * useTaskRecord — resolve one Task by id for the shared task modal.
 *
 * Reads the board's own query (`["issues","list"]`) first. That key is
 * already fetched and polled by TasksList, so opening the modal from a board
 * row, a chat card, or an inline `DUNDE-72` reference is a cache hit with no
 * extra request, and any save that invalidates the key refreshes the board
 * and the open modal together.
 *
 * The list is filtered server-side (`viewer_slug`), so a task can be missing
 * from it — a freshly created one the poll has not picked up, or one whose
 * channel the viewer cannot see. Only then does the gated fallback fetch
 * GET /tasks/{id}, which serves a task snapshot regardless of list filtering.
 */

import { useQuery } from "@tanstack/react-query";

import { get } from "../../api/client";
import { getOfficeTasks, type Task } from "../../api/tasks";

/**
 * Narrow the GET /tasks/{id} decision-packet response down to its wrapped
 * task record. The packet carries much more; the modal only needs the task,
 * and anything without a string `id` is treated as absent so a malformed
 * payload surfaces as "not found" rather than rendering a blank modal.
 */
export function taskFromDetailResponse(raw: unknown): Task | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const wrapped = (raw as Record<string, unknown>).task;
  if (!wrapped || typeof wrapped !== "object") return undefined;
  const record = wrapped as Record<string, unknown>;
  if (typeof record.id !== "string" || record.id === "") return undefined;
  return record as unknown as Task;
}

export interface TaskRecordResult {
  task: Task | undefined;
  isPending: boolean;
  error: Error | null;
  refetch: () => void;
}

export function useTaskRecord(taskId: string | null): TaskRecordResult {
  const listQuery = useQuery({
    queryKey: ["issues", "list"],
    queryFn: () => getOfficeTasks({ includeDone: true }),
    staleTime: 5_000,
    enabled: !!taskId,
  });

  const fromList = taskId
    ? listQuery.data?.tasks.find((t) => t.id === taskId)
    : undefined;

  // Fallback only once the list has actually resolved without the row —
  // firing it while the list is still in flight would double every open.
  const needsFallback = !!taskId && !fromList && listQuery.isSuccess;

  const detailQuery = useQuery({
    queryKey: ["issue", "record", taskId],
    queryFn: async () => {
      const raw = await get<unknown>(
        `/tasks/${encodeURIComponent(taskId ?? "")}`,
      );
      return taskFromDetailResponse(raw) ?? null;
    },
    staleTime: 5_000,
    enabled: needsFallback,
  });

  const task = fromList ?? detailQuery.data ?? undefined;
  const isPending =
    (listQuery.isPending && !fromList) ||
    (needsFallback && detailQuery.isPending);
  const error =
    (listQuery.error as Error | null) ?? (detailQuery.error as Error | null);

  return {
    task,
    isPending,
    error: task ? null : error,
    refetch: () => {
      void listQuery.refetch();
      if (needsFallback) void detailQuery.refetch();
    },
  };
}
