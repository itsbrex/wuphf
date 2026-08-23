import { describe, expect, it } from "vitest";

import { buildTeachSeed, describeCapture } from "./demoSeed";
import type { ObservedScreen } from "./observeClient";

function screen(
  app: string,
  title: string,
  labels: string[],
  text?: string,
): ObservedScreen {
  return {
    app,
    title,
    components: labels.map((label) => ({ role: "Button", label })),
    ...(text ? { text } : {}),
  };
}

describe("buildTeachSeed", () => {
  it("leads with the operator's goal and lists the real screens in order", () => {
    const seed = buildTeachSeed("Route a demo request to the right AE", [
      screen("Google Chrome", "HubSpot | Deals", ["Save", "Owner"], "Acme"),
      screen("Slack", "#ae-handoffs", ["Send"]),
    ]);

    expect(seed).toContain("Route a demo request to the right AE");
    expect(seed).toContain("1. Google Chrome — HubSpot | Deals");
    expect(seed).toContain("Button:Save, Button:Owner");
    expect(seed).toContain("text: Acme");
    expect(seed).toContain("2. Slack — #ae-handoffs");
    // The screens must keep capture order — that order IS the workflow.
    expect(seed.indexOf("1. Google Chrome")).toBeLessThan(
      seed.indexOf("2. Slack"),
    );
  });

  // The seed is sent as a chat message, so it has to survive AppToolsChat's
  // routing heuristics: matchTool() claims anything starting with run/call/use
  // and looksLikeQuestion() claims interrogatives. Either would divert the
  // hand-off away from the authoring path.
  it("opens with a phrase the chat routes to authoring, not to a tool run", () => {
    const seed = buildTeachSeed("run the weekly report", [
      screen("Excel", "Q3", ["Sum"]),
    ]);
    expect(/^\s*(run|call|use)\b/i.test(seed)).toBe(false);
    expect(seed.trim().endsWith("?")).toBe(false);
    expect(
      /^(what|why|when|where|who|how|did|do|does|is|are)\b/i.test(seed.trim()),
    ).toBe(false);
  });

  // Honest-by-default: an empty capture must say nothing was captured rather
  // than implying the observer watched something.
  it("says plainly when no screens were captured", () => {
    const seed = buildTeachSeed("Tidy the pipeline", []);
    expect(seed).toContain("did not capture any screens");
    expect(seed).not.toContain("I just demonstrated it");
  });

  it("bounds the element list so one dense screen cannot flood the prompt", () => {
    const labels = Array.from({ length: 40 }, (_, i) => `b${i}`);
    const seed = buildTeachSeed("Do the thing", [
      screen("Chrome", "Dense", labels),
    ]);
    expect(seed).toContain("Button:b19");
    expect(seed).not.toContain("Button:b20");
  });
});

describe("describeCapture", () => {
  it("counts screens and elements, singular and plural", () => {
    expect(describeCapture([])).toBe("No screens captured");
    expect(describeCapture([screen("Chrome", "A", ["x"])])).toBe(
      "1 screen, 1 element read",
    );
    expect(
      describeCapture([
        screen("Chrome", "A", ["x", "y"]),
        screen("Slack", "B", ["z"]),
      ]),
    ).toBe("2 screens, 3 elements read");
  });
});
