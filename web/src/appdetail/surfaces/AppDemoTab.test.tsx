import { fireEvent, render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppDemoTab } from "./AppDemoTab";

// Only the HTTP boundary is mocked: observeClient, readEventStream,
// reduceObserved and the seed formatter all run for real, so these tests cover
// the whole capture -> hand-off chain rather than a stubbed middle.
const { postStream } = vi.hoisted(() => ({ postStream: vi.fn() }));
vi.mock("../../api/client", () => ({ postStream }));

interface FakeObserveStream {
  push: (frame: string) => void;
}

/**
 * Stand in for the broker streaming runner/cua_observe.py. The abort signal is
 * wired to error the stream exactly as a real aborted fetch would, so pressing
 * Stop exercises the component's real abort path.
 */
function mockObserveStream(): FakeObserveStream {
  let ctrl!: ReadableStreamDefaultController<Uint8Array>;
  const enc = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      ctrl = c;
    },
  });
  const response = new Response(body, { status: 200 });
  postStream.mockImplementation(
    (_path: string, _body: unknown, opts?: { signal?: AbortSignal }) => {
      opts?.signal?.addEventListener("abort", () => {
        try {
          ctrl.error(new DOMException("aborted", "AbortError"));
        } catch {
          // Already closed or errored — nothing to abort.
        }
      });
      return Promise.resolve(response);
    },
  );
  return { push: (frame) => ctrl.enqueue(enc.encode(frame)) };
}

function snapshotFrame(
  app: string,
  title: string,
  labels: string[],
  text?: string,
): string {
  const payload = {
    type: "snapshot",
    tick: 1,
    app,
    title,
    components: labels.map((label) => ({ role: "Button", label })),
    ...(text ? { text_excerpt: text } : {}),
  };
  return `data: ${JSON.stringify(payload)}\n\n`;
}

function renderTab(props: Partial<Parameters<typeof AppDemoTab>[0]> = {}) {
  const view = render(<AppDemoTab appName="Pipeline" {...props} />);
  const start = (goal: string) => {
    fireEvent.change(
      view.getByLabelText("What are you about to demonstrate?"),
      { target: { value: goal } },
    );
    fireEvent.click(view.getByRole("button", { name: /start recording/i }));
  };
  return { ...view, start };
}

describe("AppDemoTab", () => {
  beforeEach(() => postStream.mockReset());

  it("requires the operator to say what they are demonstrating", () => {
    const { getByRole, getByLabelText } = renderTab();
    const start = getByRole("button", { name: /start recording/i });
    expect(start).toBeDisabled();
    fireEvent.change(getByLabelText("What are you about to demonstrate?"), {
      target: { value: "Route a demo request" },
    });
    expect(start).toBeEnabled();
  });

  it("records against the real observe endpoint", async () => {
    mockObserveStream();
    const view = renderTab();
    view.start("Route a demo request");
    await waitFor(() =>
      expect(postStream).toHaveBeenCalledWith(
        "/observe/browser",
        {},
        expect.objectContaining({ signal: expect.anything() }),
      ),
    );
    expect(view.getByText(/Reading your screen/)).toBeInTheDocument();
  });

  it("renders only the screens the observer actually reported", async () => {
    const stream = mockObserveStream();
    const view = renderTab();
    view.start("Route a demo request");
    await waitFor(() => expect(postStream).toHaveBeenCalled());

    stream.push(snapshotFrame("Google Chrome", "HubSpot | Deals", ["Owner"]));
    await waitFor(() =>
      expect(view.getByText("HubSpot | Deals")).toBeInTheDocument(),
    );
    expect(view.getByText("Google Chrome")).toBeInTheDocument();
    expect(view.getByText(/Button:Owner/)).toBeInTheDocument();
    // Nothing beyond what the observer reported.
    expect(view.queryByText(/Slack/)).not.toBeInTheDocument();
  });

  it("hands the captured screens to the chat as a teach seed", async () => {
    const stream = mockObserveStream();
    const onHandoff = vi.fn();
    const view = renderTab({ onHandoff });
    view.start("Route a demo request to the right AE");
    await waitFor(() => expect(postStream).toHaveBeenCalled());

    stream.push(snapshotFrame("Google Chrome", "HubSpot | Deals", ["Owner"]));
    await waitFor(() =>
      expect(view.getByText("HubSpot | Deals")).toBeInTheDocument(),
    );

    fireEvent.click(view.getByRole("button", { name: /^Stop$/ }));
    const handoff = await waitFor(() =>
      view.getByRole("button", { name: /hand this to the chat/i }),
    );
    fireEvent.click(handoff);

    expect(onHandoff).toHaveBeenCalledTimes(1);
    const seed = onHandoff.mock.calls[0][0] as string;
    expect(seed).toContain("Route a demo request to the right AE");
    expect(seed).toContain("Google Chrome — HubSpot | Deals");
    expect(seed).toContain("Button:Owner");
  });

  // The honest-degradation contract: no observer on this host means SAY SO and
  // point at the chat path that works. It must never render a fake recording.
  it("says plainly when the host has no observer, and offers the chat path", async () => {
    postStream.mockResolvedValue(new Response(null, { status: 503 }));
    const onTeach = vi.fn();
    const view = renderTab({ onTeach });
    view.start("Route a demo request");

    await waitFor(() =>
      expect(
        view.getByText("This computer cannot watch your screen"),
      ).toBeInTheDocument(),
    );
    expect(view.queryByText(/Reading your screen/)).not.toBeInTheDocument();
    expect(
      view.queryByRole("button", { name: /hand this to the chat/i }),
    ).not.toBeInTheDocument();

    fireEvent.click(
      view.getByRole("button", { name: /teach a tool in chat/i }),
    );
    expect(onTeach).toHaveBeenCalledTimes(1);
  });

  it("reports a failed capture instead of pretending it worked", async () => {
    postStream.mockResolvedValue(new Response(null, { status: 500 }));
    const view = renderTab();
    view.start("Route a demo request");
    await waitFor(() =>
      expect(view.getByText("The recording stopped")).toBeInTheDocument(),
    );
    expect(view.getByText(/nothing was sent/i)).toBeInTheDocument();
  });
});
