// Regression guard for the real-vs-mock sidebar split: the Agents count badge
// must count REAL agents only, and mock draft agents render ghosted under a
// "Suggested" section label so they read as suggestions, not inventory.

import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { OperatorSidebar, type SidebarAgent } from "./OperatorSidebar";

function real(id: string, name: string): SidebarAgent {
  return { id, name, glyph: "🤖" };
}

function suggested(id: string, name: string): SidebarAgent {
  return { id, name, glyph: "SG", suggested: true };
}

function renderSidebar(agents: SidebarAgent[]) {
  return render(
    <OperatorSidebar
      active="tools"
      onSelect={() => {}}
      onStartCall={() => {}}
      onBuild={() => {}}
      agents={agents}
    />,
  );
}

describe("OperatorSidebar", () => {
  it("counts only real agents in the Agents badge", () => {
    const { container } = renderSidebar([
      real("app_1111111111111111", "Renewal radar"),
      real("app_2222222222222222", "Digest bot"),
      suggested("inbound-routing", "Inbound demo-request routing"),
      suggested("support-escalations", "Support escalation triage"),
      suggested("expense-exceptions", "Expense exception routing"),
    ]);
    const badge = container.querySelector(".opr-nav-count");
    expect(badge?.textContent).toBe("2");
  });

  it("renders suggested agents ghosted under a Suggested section label, after the real roster", () => {
    const { container, getByText } = renderSidebar([
      real("app_1111111111111111", "Renewal radar"),
      suggested("inbound-routing", "Inbound demo-request routing"),
      suggested("support-escalations", "Support escalation triage"),
    ]);
    const label = getByText("Suggested");
    expect(label).toBeTruthy();

    const ghosted = container.querySelectorAll(
      ".opr-agent-rail-item.is-suggested",
    );
    expect(ghosted.length).toBe(2);

    // The real agent's row is NOT ghosted…
    const realRow = getByText("Renewal radar").closest(".opr-agent-rail-item");
    expect(realRow?.className).not.toContain("is-suggested");
    // …and it sits ABOVE the Suggested section label.
    expect(
      realRow &&
        label.compareDocumentPosition(realRow) &
          Node.DOCUMENT_POSITION_PRECEDING,
    ).toBeTruthy();
  });

  it("shows no Suggested label when every agent is real", () => {
    const { queryByText } = renderSidebar([
      real("app_1111111111111111", "Renewal radar"),
    ]);
    expect(queryByText("Suggested")).toBeNull();
  });

  it("shows no count badge when only suggestions exist", () => {
    const { container, getByText } = renderSidebar([
      suggested("inbound-routing", "Inbound demo-request routing"),
    ]);
    expect(container.querySelector(".opr-nav-count")).toBeNull();
    // The suggestion itself still renders — the affordance stays.
    expect(getByText("Inbound demo-request routing")).toBeTruthy();
  });
});
