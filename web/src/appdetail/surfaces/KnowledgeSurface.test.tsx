import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { KnowledgePage } from "../mock/data";
import { KnowledgeSurface } from "./KnowledgeSurface";

// vi.mock is hoisted above the const declarations, so the mock handle must be
// hoisted too or the factory hits the TDZ. KnowledgeSurface reads through
// getAppKnowledge, which calls this get() — mocking it exercises the real
// client (including the patient-timeout option it passes).
const { get, getText, getBlob } = vi.hoisted(() => ({
  get: vi.fn(),
  getText: vi.fn(),
  getBlob: vi.fn(),
}));
vi.mock("../../api/client", () => ({ get, getText, getBlob }));

function wrap(node: ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

function samplePage(): KnowledgePage {
  return {
    id: "icp",
    title: "Ideal customer profile",
    category: "Sales",
    updatedAt: "Last edited yesterday by your AI",
    summary: "Who buys.",
    infobox: [{ label: "Segment", value: "Mid-market" }],
    lead: "The company most likely to buy.",
    sections: [{ heading: "Fit", paras: ["Named use case."] }],
    references: [],
    categories: ["Sales"],
    seeAlso: [],
  };
}

describe("KnowledgeSurface", () => {
  beforeEach(() => {
    get.mockReset();
    getText.mockReset();
    getBlob.mockReset();
  });

  it("requests knowledge with a patient timeout that outlasts the broker's 90s synthesis bound", async () => {
    get.mockResolvedValue({ pages: [samplePage()] });
    wrap(<KnowledgeSurface appId="app_abc" />);
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(
        "/apps/app_abc/knowledge",
        undefined,
        expect.objectContaining({ timeoutMs: expect.any(Number) }),
      ),
    );
    // The client must not abort mid-synthesis: the window has to exceed the
    // broker's 90s server-side synthesis bound (the default 20s GET timeout was
    // the bug).
    const opts = get.mock.calls[0]?.[2] as { timeoutMs: number };
    expect(opts.timeoutMs).toBeGreaterThan(90_000);
  });

  it("renders the synthesized pages once a slow first read resolves, never the provider error", async () => {
    get.mockResolvedValue({ pages: [samplePage()] });
    const { getAllByText, queryByText } = wrap(
      <KnowledgeSurface appId="app_abc" />,
    );
    await waitFor(() =>
      // The title renders in the nav, infobox, and article heading.
      expect(getAllByText("Ideal customer profile").length).toBeGreaterThan(0),
    );
    expect(queryByText(/not reachable or not configured/i)).toBeNull();
  });

  it("shows 'taking longer', not the provider error, when a slow synthesis times out", async () => {
    // A client timeout/abort surfaces as a thrown transport error with NO
    // response body — the pre-fix code mislabeled this "your AI provider is not
    // reachable". It must read as "still working" instead.
    get.mockRejectedValue(
      new Error("Broker not responding — request timed out."),
    );
    const { getByText, queryByText } = wrap(
      <KnowledgeSurface appId="app_abc" />,
    );
    await waitFor(() =>
      expect(getByText(/taking longer than usual/i)).toBeTruthy(),
    );
    expect(queryByText(/not reachable or not configured/i)).toBeNull();
    expect(queryByText(/Knowledge is unavailable right now/i)).toBeNull();
  });

  it("shows the provider-unreachable message only on a real provider verdict", async () => {
    // A genuine provider outage comes back as HTTP 200 with an error body.
    get.mockResolvedValue({ pages: [], error: "ai_unavailable" });
    const { getByText } = wrap(<KnowledgeSurface appId="app_abc" />);
    await waitFor(() =>
      expect(getByText(/Knowledge is unavailable right now/i)).toBeTruthy(),
    );
    expect(getByText(/not reachable or not configured/i)).toBeTruthy();
  });

  it("shows the busy message on a rate-limit verdict", async () => {
    get.mockResolvedValue({ pages: [], error: "rate_limited" });
    const { getByText } = wrap(<KnowledgeSurface appId="app_abc" />);
    await waitFor(() => expect(getByText(/Knowledge is busy/i)).toBeTruthy());
  });

  it("renders a page's preserved artifacts and views html in a locked-down sandbox", async () => {
    const page = {
      ...samplePage(),
      artifacts: [
        {
          title: "RAG survey figure",
          kind: "html" as const,
          url: "/apps/knowledge/legacy-artifacts/ra_abc.html",
        },
      ],
    };
    get.mockResolvedValue({ pages: [page] });
    getText.mockResolvedValue("<h1>Survey figure</h1>");
    const { container, getByText, findByText } = wrap(
      <KnowledgeSurface appId="app_abc" />,
    );
    await findByText("Artifacts");
    // The artifact fetches through the AUTHED client (an iframe src cannot
    // carry the auth header) only when opened.
    expect(getText).not.toHaveBeenCalled();
    getByText(/RAG survey figure/).click();
    await waitFor(() =>
      expect(getText).toHaveBeenCalledWith(
        "/apps/knowledge/legacy-artifacts/ra_abc.html",
      ),
    );
    await waitFor(() => {
      const iframe = container.querySelector("iframe.opr-artifact-html");
      expect(iframe).toBeTruthy();
      // The EMPTY sandbox attribute is the security boundary for preserved
      // HTML: no scripts, no navigation, no same-origin. Never loosen it.
      expect(iframe?.getAttribute("sandbox")).toBe("");
    });
  });

  it("shows no References heading when a page has none", async () => {
    get.mockResolvedValue({ pages: [samplePage()] });
    const { queryByText, getAllByText } = wrap(
      <KnowledgeSurface appId="app_abc" />,
    );
    await waitFor(() =>
      expect(getAllByText("Ideal customer profile").length).toBeGreaterThan(0),
    );
    expect(queryByText("References")).toBeNull();
  });
});
