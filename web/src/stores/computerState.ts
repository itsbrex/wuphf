// Pure reducers behind `useAppStore.recordComputerEvent`. Kept out of the
// store so the merge rules (frames only move forward, a status poll never
// rewinds a live SSE frame, the build log is a bounded readout) are unit
// testable without zustand. Wire: docs/specs/gawkbot-bot-computers.md, SSE.
import type { ComputerEventPayload } from "../api/computer";

export interface ComputerLiveState {
  state: string;
  problem: string | null;
  frameDataUrl: string | null;
  frameAt: number | null;
  held: boolean;
  helpReason: string | null;
}

export interface ComputerRuntimeBuild {
  building: boolean;
  problem: string | null;
  /** Streamed progress lines, oldest first, capped at MAX_COMPUTER_BUILD_LINES. */
  lines: string[];
}

/** Keep the build log short: it is a progress readout, not a transcript. */
export const MAX_COMPUTER_BUILD_LINES = 40;

export const EMPTY_COMPUTER_LIVE_STATE: ComputerLiveState = {
  state: "",
  problem: null,
  frameDataUrl: null,
  frameAt: null,
  held: false,
  helpReason: null,
};

export const EMPTY_COMPUTER_RUNTIME_BUILD: ComputerRuntimeBuild = {
  building: false,
  problem: null,
  lines: [],
};

function nonEmpty(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

/** Machine-wide events (slug "") drive the shared image build readout. */
export function mergeRuntimeBuild(
  prev: ComputerRuntimeBuild,
  payload: ComputerEventPayload,
): ComputerRuntimeBuild {
  const message =
    typeof payload.message === "string" ? payload.message.trim() : "";
  const building = payload.state === "building";
  const startsFresh = building && !prev.building;
  const base = startsFresh ? [] : prev.lines;
  const lines = message
    ? [...base, message].slice(-MAX_COMPUTER_BUILD_LINES)
    : base;
  const problem = nonEmpty(payload.problem) ?? (building ? null : prev.problem);
  return { building, problem, lines };
}

/** Per-slug events: state, hold, help request, and the live frame. */
export function mergeComputerLiveState(
  prev: ComputerLiveState,
  payload: ComputerEventPayload,
  now: number,
): ComputerLiveState {
  const at = typeof payload.at === "number" ? payload.at : now;
  // A frame only moves forward. The status poll carries the broker's settled
  // last_frame, which can be older than a live SSE frame that arrived
  // between polls; never let the poll rewind the picture.
  const frame = nonEmpty(payload.frame);
  const hasNewerFrame =
    frame !== null && (prev.frameAt === null || at >= prev.frameAt);
  return {
    state: nonEmpty(payload.state) ?? prev.state,
    problem: "problem" in payload ? nonEmpty(payload.problem) : prev.problem,
    frameDataUrl: hasNewerFrame ? frame : prev.frameDataUrl,
    frameAt: hasNewerFrame ? at : prev.frameAt,
    held: typeof payload.held === "boolean" ? payload.held : prev.held,
    helpReason:
      "help_reason" in payload
        ? nonEmpty(payload.help_reason)
        : prev.helpReason,
  };
}

/** Store-level reducer: one payload in, the changed slice out. */
export function applyComputerEvent(
  state: {
    computerStates: Record<string, ComputerLiveState>;
    computerRuntimeBuild: ComputerRuntimeBuild;
  },
  payload: ComputerEventPayload,
  now: number,
): Partial<{
  computerStates: Record<string, ComputerLiveState>;
  computerRuntimeBuild: ComputerRuntimeBuild;
}> {
  if (!payload || typeof payload.slug !== "string") return {};
  const { slug } = payload;
  if (slug.length === 0) {
    return {
      computerRuntimeBuild: mergeRuntimeBuild(
        state.computerRuntimeBuild,
        payload,
      ),
    };
  }
  return {
    computerStates: {
      ...state.computerStates,
      [slug]: mergeComputerLiveState(
        state.computerStates[slug] ?? EMPTY_COMPUTER_LIVE_STATE,
        payload,
        now,
      ),
    },
  };
}
