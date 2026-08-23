import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Message } from "../../api/client";
import { MessageFeed, messagesAfterClearMarker } from "./MessageFeed";

describe("messagesAfterClearMarker", () => {
  const messages = [
    { id: "msg-1" },
    { id: "msg-2" },
    { id: "msg-3" },
  ] as Message[];

  it("keeps all messages when there is no clear marker", () => {
    expect(messagesAfterClearMarker(messages, null)).toBe(messages);
  });

  it("returns messages after the clear marker", () => {
    expect(messagesAfterClearMarker(messages, "msg-2")).toEqual([
      { id: "msg-3" },
    ]);
  });

  it("keeps the feed cleared when the marker is not in the current page", () => {
    expect(messagesAfterClearMarker(messages, "missing")).toEqual([]);
  });
});

// ── Channel resolution ─────────────────────────────────────────────────
//
// The resolution used to be `channel ?? routeChannel ?? "general"`. `??` is
// NULLISH, so an empty string slipped through and was queried as a channel
// slug, and the "general" tail invented a conversation home for a task that
// has none — pointing the feed at the room the one-room removal retires.

const getMessages = vi.hoisted(() => vi.fn());
const useChannelSlug = vi.hoisted(() => vi.fn());

vi.mock("../../routes/useCurrentRoute", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../../routes/useCurrentRoute")>();
  return { ...actual, useChannelSlug };
});

vi.mock("../../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api/client")>();
  return { ...actual, getMessages };
});

function renderFeed(props: { channel?: string } = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchInterval: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MessageFeed {...props} />
    </QueryClientProvider>,
  );
}

describe("<MessageFeed> channel resolution", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getMessages.mockResolvedValue({ messages: [] });
    useChannelSlug.mockReturnValue(null);
  });

  it("does NOT query #general when there is no channel", () => {
    renderFeed();

    expect(getMessages).not.toHaveBeenCalled();
    expect(screen.getByTestId("messages-no-channel")).toBeInTheDocument();
  });

  it("treats an EMPTY STRING channel as no channel", () => {
    // The nullish-coalescing bug exactly: "" is not null, so it passed through.
    renderFeed({ channel: "" });

    expect(getMessages).not.toHaveBeenCalled();
    expect(screen.getByTestId("messages-no-channel")).toBeInTheDocument();
  });

  it("treats a whitespace-only channel as no channel", () => {
    renderFeed({ channel: "   " });

    expect(getMessages).not.toHaveBeenCalled();
    expect(screen.getByTestId("messages-no-channel")).toBeInTheDocument();
  });

  it("never names #general in the no-channel state", () => {
    renderFeed();
    const panel = screen.getByTestId("messages-no-channel");
    expect(panel.textContent ?? "").not.toMatch(/general/i);
  });

  it("still queries an explicit channel", async () => {
    renderFeed({ channel: "eng" });

    await waitFor(() => expect(getMessages).toHaveBeenCalled());
    expect(getMessages.mock.calls[0][0]).toBe("eng");
  });

  it("falls back to the route channel when no channel prop is given", async () => {
    useChannelSlug.mockReturnValue("design");
    renderFeed();

    await waitFor(() => expect(getMessages).toHaveBeenCalled());
    expect(getMessages.mock.calls[0][0]).toBe("design");
  });
});
