/**
 * TaskModalHost — mounts the single shared TaskModal once, at the app root.
 *
 * Every task affordance in the product calls `openTaskModal(id)` on the app
 * store instead of navigating; this host is what turns that id into a
 * rendered dialog. Mounted alongside the other global dialogs in RootRoute so
 * a card in the chat stream, a board row, and an inline task reference all
 * open the same instance rather than each subtree carrying its own copy.
 */

import { useAppStore } from "../../stores/app";
import { TaskModal } from "./TaskModal";

export function TaskModalHost() {
  const taskId = useAppStore((s) => s.taskModalTaskId);
  const close = useAppStore((s) => s.closeTaskModal);
  return <TaskModal taskId={taskId} onClose={close} />;
}
