import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { OfficeStats } from "../../api/platform";
import * as platformApi from "../../api/platform";
import { useAppStore } from "../../stores/app";
import { TasksNavButton } from "./TasksNavButton";

vi.mock("../../routes/useCurrentRoute", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../../routes/useCurrentRoute")>();
  return {
    ...actual,
    useCurrentApp: () => null,
  };
});

vi.mock("../../lib/notificationSound", () => ({
  playInboxDing: vi.fn(),
}));

const STATS: OfficeStats = {
  tasks: {
    backlog: 0,
    active: 1,
    blocked: 0,
    review: 0,
    needs_human: 1,
    done: 0,
    archive: 0,
  },
  requests: { blocking: 1, notices: 1 },
  inbox_attention: 11,
  wiki_articles: 3,
  agents_active: 1,
  generated_at: "2026-06-11T00:00:00Z",
};

function wrap(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchInterval: false, gcTime: 0 },
    },
  });
  return <QueryClientProvider client={client}>{ui}</QueryClientProvider>;
}

afterEach(() => {
  vi.restoreAllMocks();
  useAppStore.setState({ brokerConnected: false });
});

describe("<TasksNavButton>", () => {
  it("labels the primary Work entry as Tasks", () => {
    render(wrap(<TasksNavButton />));
    expect(screen.getByText("Tasks")).toBeInTheDocument();
  });

  it("renders the shared needs-you count, not inbox_attention", async () => {
    useAppStore.setState({ brokerConnected: true });
    vi.spyOn(platformApi, "getOfficeStats").mockResolvedValue(STATS);

    render(wrap(<TasksNavButton />));

    // The badge is the SHARED definition every surface uses: decisions
    // waiting (needs_human 1) + blocking asks (blocking 1) = 2. The fixture
    // sets inbox_attention to 11 precisely so this test fails loudly if the
    // badge ever goes back to echoing it — that field counts every request
    // kind including notices, which is what made the badge show a number the
    // runtime strip called "all quiet".
    await waitFor(() => {
      expect(screen.getByTestId("inbox-unread-badge")).toHaveTextContent("2");
    });
    expect(screen.getByTestId("inbox-unread-badge")).not.toHaveTextContent(
      "11",
    );
  });

  it("renders no badge while the count is unknown or zero", () => {
    render(wrap(<TasksNavButton />));
    expect(screen.queryByTestId("inbox-unread-badge")).toBeNull();
  });
});
