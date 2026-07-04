// Shell-level regression guard for product honesty in the sidebar: the merge
// of REAL agents (broker apps) and MOCK drafts (mock/data TOOLS) must mark the
// mocks as suggestions, so the Agents badge counts real inventory only and the
// mock rows read as suggestions.

import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { CustomApp } from "../api/apps";
import { TOOLS } from "./mock/data";
import { OperatorApp } from "./OperatorApp";

// Drive the apps hook directly (no network, no React Query provider) — the
// same seam InternalToolsSurface.test.tsx uses.
const useOperatorAppsMock = vi.fn();
vi.mock("./apps/useOperatorApps", () => ({
  useOperatorApps: () => useOperatorAppsMock(),
  useDeleteApp: () => ({ mutate: vi.fn(), isPending: false }),
  appBuildState: () => "ready",
  isRealAppId: (id: unknown): boolean =>
    typeof id === "string" && id.startsWith("app_"),
}));
vi.mock("./apps/useRealtimeConfig", () => ({
  useRealtimeConfig: () => ({ available: false, model: "gpt-realtime-2" }),
}));
// ApprovalPrompt polls the broker through React Query; not under test here.
vi.mock("./components/ApprovalPrompt", () => ({
  ApprovalPrompt: () => null,
}));

function app(id: string, name: string): CustomApp {
  return {
    id,
    slug: name.toLowerCase().replace(/\s+/g, "-"),
    name,
    icon: "📋",
    entry: "index.html",
    version: 1,
    createdBy: "app-builder",
    createdAt: "2026-06-30T10:00:00Z",
    updatedAt: "2026-06-30T10:00:00Z",
    contentHash: "h",
  };
}

describe("OperatorApp sidebar composition", () => {
  it("badges the REAL agent count and files every mock draft under Suggested", () => {
    useOperatorAppsMock.mockReturnValue({
      data: [
        app("app_1111111111111111", "Renewal radar"),
        app("app_2222222222222222", "Digest bot"),
      ],
      isLoading: false,
    });
    const { container, getByText } = render(<OperatorApp />);

    // 2 real agents; the mock TOOLS must not inflate the count.
    const badge = container.querySelector(".opr-nav-count");
    expect(badge?.textContent).toBe("2");

    // Every mock draft renders as a ghosted suggestion in the rail.
    expect(getByText("Suggested")).toBeTruthy();
    for (const t of TOOLS) {
      const row = getByText(t.name).closest(".opr-agent-rail-item");
      expect(row?.className).toContain("is-suggested");
    }
  });
});
