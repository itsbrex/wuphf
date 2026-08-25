import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { OfficeStats } from "../../api/platform";
import * as platformApi from "../../api/platform";
import { useAppStore } from "../../stores/app";
import { RuntimeStrip } from "./RuntimeStrip";

const STATS: OfficeStats = {
  tasks: {
    backlog: 1,
    active: 4,
    blocked: 2,
    review: 1,
    needs_human: 1,
    done: 3,
    archive: 0,
  },
  requests: { blocking: 3, notices: 1 },
  inbox_attention: 4,
  wiki_articles: 7,
  agents_active: 5,
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

describe("<RuntimeStrip>", () => {
  it("renders the shared /office/stats numbers (C1 regression)", async () => {
    useAppStore.setState({ brokerConnected: true });
    vi.spyOn(platformApi, "getOfficeStats").mockResolvedValue(STATS);

    render(wrap(<RuntimeStrip />));

    // active = agents_active, blocked = tasks.blocked, need you =
    // needsYouCount (decisions + blocking asks) — the SHARED definition, so
    // this strip cannot print a different number from the board lane or the
    // sidebar badge. Fixture: needs_human 1 + blocking 3 = 4.
    await waitFor(() => {
      expect(screen.getByText("5 active")).toBeInTheDocument();
    });
    expect(screen.getByText("2 blocked")).toBeInTheDocument();
    expect(screen.getByText("4 need you")).toBeInTheDocument();
  });

  it("renders 'all quiet' only when nothing is running, blocked, or waiting", async () => {
    useAppStore.setState({ brokerConnected: true });
    vi.spyOn(platformApi, "getOfficeStats").mockResolvedValue({
      ...STATS,
      tasks: { ...STATS.tasks, blocked: 0, needs_human: 0 },
      // Notices stay non-zero on purpose: news is not something that needs
      // you, so it must not disturb "all quiet".
      requests: { blocking: 0, notices: 2 },
      inbox_attention: 2,
      agents_active: 0,
    });

    render(wrap(<RuntimeStrip />));

    await waitFor(() => {
      expect(screen.getByText("all quiet")).toBeInTheDocument();
    });
  });

  // The contradiction observed live on 2026-08-25: the strip printed "all
  // quiet" while the board's Needs-human lane showed a waiting decision,
  // because the strip counted only requests.blocking. A decision waiting on
  // the human is not quiet.
  it("is not quiet when a decision is waiting", async () => {
    useAppStore.setState({ brokerConnected: true });
    vi.spyOn(platformApi, "getOfficeStats").mockResolvedValue({
      ...STATS,
      tasks: { ...STATS.tasks, blocked: 0, needs_human: 1 },
      requests: { blocking: 0, notices: 0 },
      agents_active: 0,
    });

    render(wrap(<RuntimeStrip />));

    await waitFor(() => {
      expect(screen.getByText("1 need you")).toBeInTheDocument();
    });
    expect(screen.queryByText("all quiet")).toBeNull();
  });

  it("claims nothing while stats are unknown (honest loading)", () => {
    // Broker disconnected → no stats. The strip must not claim
    // "all quiet" about a state it has not observed.
    render(wrap(<RuntimeStrip />));
    expect(screen.queryByText("all quiet")).toBeNull();
  });
});
