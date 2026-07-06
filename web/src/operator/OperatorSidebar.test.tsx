// Regression guard for the sidebar Agents rail: it renders ONLY the agents it
// is given (the real inventory), the count badge equals that number, and no
// "Suggested" section or mock-draft affordance exists anywhere.

import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { OperatorSidebar, type SidebarAgent } from "./OperatorSidebar";

function agent(id: string, name: string): SidebarAgent {
  return { id, name, glyph: "🤖" };
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
  it("counts exactly the agents it is given", () => {
    const { container } = renderSidebar([
      agent("app_1111111111111111", "Renewal radar"),
      agent("app_2222222222222222", "Digest bot"),
    ]);
    const badge = container.querySelector(".opr-nav-count");
    expect(badge?.textContent).toBe("2");
  });

  it("renders one rail row per agent and nothing else", () => {
    const { container } = renderSidebar([
      agent("app_1111111111111111", "Renewal radar"),
      agent("app_2222222222222222", "Digest bot"),
    ]);
    const rows = container.querySelectorAll(".opr-agent-rail-item");
    expect(rows.length).toBe(2);
  });

  it("never renders a Suggested section", () => {
    const { queryByText } = renderSidebar([
      agent("app_1111111111111111", "Renewal radar"),
    ]);
    expect(queryByText("Suggested")).toBeNull();
  });

  it("shows no badge and no rail when there are no agents", () => {
    const { container } = renderSidebar([]);
    expect(container.querySelector(".opr-nav-count")).toBeNull();
    expect(container.querySelector(".opr-agent-rail-item")).toBeNull();
  });
});
