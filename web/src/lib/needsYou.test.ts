import { describe, expect, it } from "vitest";

import type { OfficeStats } from "../api/platform";
import { isNoticeRequest, needsYouCount, officeIsQuiet } from "./needsYou";
import type { InboxItemRequest } from "./types/inbox";

// "What needs me?" is the question the Tasks board now exists to answer, and
// three surfaces answer it: the runtime strip, the board's Needs-human lane,
// and the sidebar badge. They used to compute it three ways from one payload
// and disagreed on screen — strip "all quiet", lane 1, badge 2 — while both
// the strip and the badge carried comments claiming that was impossible.
//
// The guard is not a comment. It is that all three call needsYouCount, and
// these tests pin what that function counts.

function stats(over: Partial<OfficeStats> = {}): OfficeStats {
  return {
    tasks: {
      backlog: 0,
      active: 0,
      blocked: 0,
      review: 0,
      needs_human: 0,
      done: 0,
      archive: 0,
    },
    requests: { blocking: 0, notices: 0 },
    inbox_attention: 0,
    wiki_articles: 0,
    agents_active: 0,
    ...over,
  } as OfficeStats;
}

describe("needsYouCount", () => {
  it("counts decisions waiting and blocking asks", () => {
    expect(
      needsYouCount(
        stats({
          tasks: { ...stats().tasks, needs_human: 2 },
          requests: { blocking: 3, notices: 0 },
        }),
      ),
    ).toBe(5);
  });

  // The bug, pinned. A delivered notice is news, not a decision: it is what
  // made the badge and the lane show a number the strip called "all quiet".
  it("does NOT count notices", () => {
    const s = stats({
      requests: { blocking: 0, notices: 4 },
      inbox_attention: 4,
    });
    expect(needsYouCount(s)).toBe(0);
    expect(officeIsQuiet(s)).toBe(true);
  });

  // inbox_attention counts every request kind including notices (see
  // inboxItemNeedsAttention in the broker). Reading it directly is what the
  // sidebar badge used to do, and why it disagreed.
  it("ignores inbox_attention entirely", () => {
    expect(needsYouCount(stats({ inbox_attention: 99 }))).toBe(0);
  });

  it("treats a missing payload as nothing known, not as quiet", () => {
    expect(needsYouCount(undefined)).toBe(0);
    expect(officeIsQuiet(undefined)).toBe(false);
  });
});

describe("officeIsQuiet", () => {
  it("is quiet only when nothing is running, blocked, or waiting", () => {
    expect(officeIsQuiet(stats())).toBe(true);
    expect(officeIsQuiet(stats({ agents_active: 1 }))).toBe(false);
    expect(
      officeIsQuiet(stats({ tasks: { ...stats().tasks, blocked: 1 } })),
    ).toBe(false);
    expect(
      officeIsQuiet(stats({ requests: { blocking: 1, notices: 0 } })),
    ).toBe(false);
  });

  // The exact contradiction seen live on 2026-08-25: something in the
  // Needs-human lane while the strip printed "all quiet".
  it("is NOT quiet when a decision is waiting", () => {
    const s = stats({ tasks: { ...stats().tasks, needs_human: 1 } });
    expect(needsYouCount(s)).toBe(1);
    expect(officeIsQuiet(s)).toBe(false);
  });
});

describe("isNoticeRequest", () => {
  function request(kind: string, blocking?: boolean): InboxItemRequest {
    return {
      kind: "request",
      requestId: "req-1",
      title: "task-9 delivered",
      request: { kind, question: "", from: "cos", blocking },
    } as InboxItemRequest;
  }

  it("matches on the explicit notice kind", () => {
    expect(isNoticeRequest(request("notice"))).toBe(true);
    expect(isNoticeRequest(request("approval"))).toBe(false);
    expect(isNoticeRequest(request("interview"))).toBe(false);
  });

  // Deliberately NOT inferred from `blocking`: the wire shape carries no
  // `required` flag, so a required-but-not-blocking ask would be misread as a
  // notice and dropped from the one lane meant to catch it.
  it("does not treat a non-blocking non-notice as a notice", () => {
    expect(isNoticeRequest(request("approval", false))).toBe(false);
  });
});

// The property that actually prevents the regression: feed ONE payload to the
// formula every surface uses, and there is only one answer to disagree about.
// If someone re-derives a count inline in RuntimeStrip, TasksList, or
// TasksNavButton, this file keeps passing — which is why the real guard is
// that those three import needsYouCount. See needsYou.ts for why a comment
// could not hold this invariant.
describe("one payload, one number", () => {
  it("gives the same answer regardless of which surface asks", () => {
    const s = stats({
      tasks: { ...stats().tasks, needs_human: 1 },
      requests: { blocking: 1, notices: 5 },
      inbox_attention: 7,
    });
    const strip = needsYouCount(s);
    const lane = needsYouCount(s);
    const badge = needsYouCount(s);
    expect(strip).toBe(2);
    expect(new Set([strip, lane, badge]).size).toBe(1);
  });
});
