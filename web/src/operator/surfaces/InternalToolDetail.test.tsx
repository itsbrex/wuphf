// Product-honesty guard for the mock agent detail: InternalToolDetail only
// ever renders MOCK draft agents (real agents render OperatorAppDetail), so it
// must present as a suggestion — never impersonate real agent state with a
// fake lifecycle pill, a fabricated "from N conversations" chip, or seeded
// artifacts posing as real output.

import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { getTool, type InternalTool } from "../mock/data";
import { InternalToolDetail } from "./InternalToolDetail";

function mustGetTool(id: string): InternalTool {
  const tool = getTool(id);
  if (!tool) throw new Error(`mock tool ${id} missing`);
  return tool;
}

function renderDetail(tool: InternalTool) {
  return render(
    <InternalToolDetail tool={tool} onBack={() => {}} onStartCall={() => {}} />,
  );
}

describe("InternalToolDetail (mock agent)", () => {
  it("pills a mock draft as Suggested, not as a real lifecycle state", () => {
    // support-escalations carries status "draft" in the fixture — the detail
    // must not present that as real agent state.
    const { getByText, queryByText } = renderDetail(
      mustGetTool("support-escalations"),
    );
    expect(getByText("Suggested")).toBeTruthy();
    expect(queryByText("Draft")).toBeNull();
  });

  it("never renders the fabricated 'from N conversations' chip", () => {
    const { queryByText } = renderDetail(mustGetTool("support-escalations"));
    expect(queryByText(/from \d+ conversations?/i)).toBeNull();
  });

  it("labels the seeded artifacts as examples", () => {
    const { getByText, queryByText } = renderDetail(
      mustGetTool("support-escalations"),
    );
    expect(getByText("Example artifacts")).toBeTruthy();
    // The old heading claimed the seeded artifacts were real output.
    expect(queryByText("Artifacts")).toBeNull();
  });

  it("keeps the Build it affordance on a suggested tool", () => {
    const { getByRole } = renderDetail(mustGetTool("expense-exceptions"));
    expect(getByRole("button", { name: /build it/i })).toBeTruthy();
  });

  it("shows Build it on a draft-status fixture too — pill and CTA never desync", () => {
    // support-escalations carries status "draft": the pill says Suggested, so
    // the primary CTA must render as well, not stay gated on the old status.
    const { getByRole } = renderDetail(mustGetTool("support-escalations"));
    expect(getByRole("button", { name: /build it/i })).toBeTruthy();
  });
});
