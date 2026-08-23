import { describe, expect, it } from "vitest";

import {
  KNOWN_AVATAR_SLUG_MAP,
  KNOWN_AVATAR_SPRITES,
  resolveKnownPortraitSprite,
} from "./avatarSprites.generated";
import {
  baseSpriteID,
  getAgentColor,
  getAgentEyeColor,
  paintPixelAvatarData,
  resolvePortraitSprite,
  SPRITE_EYE_CELLS,
} from "./pixelAvatar";

describe("pixel avatar sprite resolution", () => {
  it("maps operation-created agent slugs into the generated avatar catalog", () => {
    const mappings = new Map([
      ["planner", "hybridPm"],
      ["builder", "hybridEng"],
      ["growth", "hybridGtm"],
      ["reviewer", "hybridQa"],
      ["operator", "hybridNex"],
    ]);

    for (const [slug, id] of mappings) {
      expect(resolveKnownPortraitSprite(slug)?.id).toBe(id);
    }
  });

  it("normalizes slugs before resolving portraits", () => {
    expect(resolvePortraitSprite(" Planner ")?.id).toBe("hybridPm");

    const mixedCase = resolvePortraitSprite("Custom-Ops-Agent");
    const normalized = resolvePortraitSprite(" custom-ops-agent ");
    expect(mixedCase.id).toBe(normalized.id);
    expect(mixedCase.palette).toEqual(normalized.palette);
  });

  it("keeps Jim's shared avatar on a square portrait", () => {
    const jim = resolvePortraitSprite("jim");

    expect(jim.id).toBe("office20");
    expect(jim.portrait).toHaveLength(16);
    expect(jim.portrait[0]).toHaveLength(16);
    expect(resolvePortraitSprite("halpert").id).toBe("office20");
    expect(resolvePortraitSprite("jim-halpert").id).toBe("office20");
  });

  it("renders Pam's archivist byline identity with the desk avatar", () => {
    // The wiki desk avatar uses slug "pam"; Pam's commits are authored under
    // the "archivist" git identity, so her bylines/audit/history arrive with
    // that slug. Both must resolve to the same sprite — not a procedural face.
    const desk = resolvePortraitSprite("pam");
    expect(desk.id).toBe("hybridPam");

    for (const slug of ["archivist", "librarian", " Archivist "]) {
      const avatar = resolvePortraitSprite(slug);
      expect(avatar.id).toBe(desk.id);
      expect(avatar.palette).toEqual(desk.palette);
      expect(avatar.portrait).toEqual(desk.portrait);
    }
  });

  it("keeps arbitrary new-agent slugs on generated office sprites", () => {
    const avatar = resolvePortraitSprite("custom-ops-agent");
    const idParts = avatar.id.split(":");
    const baseID = idParts[idParts.length - 1];

    expect(avatar.id).toMatch(/^procedural:custom-ops-agent:hybrid/);
    expect([
      "hybridCeo",
      "hybridGeneric",
      "hybridHuman",
      "hybridJim",
      "hybridPam",
      "hybridPamCute",
    ]).not.toContain(baseID);
    expect(avatar.portrait.length).toBeGreaterThan(0);
  });

  it("procedurally varies generated office palettes by slug", () => {
    const first = resolvePortraitSprite("custom-ops-agent");
    const again = resolvePortraitSprite("custom-ops-agent");
    const second = resolvePortraitSprite("custom-sales-agent");

    expect(first.id).toBe(again.id);
    expect(first.palette).toEqual(again.palette);
    expect(`${first.id}:${first.palette.join(",")}`).not.toBe(
      `${second.id}:${second.palette.join(",")}`,
    );
  });

  it("keeps procedural agent colors stable and accent-like", () => {
    expect(getAgentColor("ceo")).toBe("#E8A838");
    expect(getAgentColor("jim")).toBe("#8FB3D1");
    expect(getAgentColor("custom-ops-agent")).toMatch(/^#[0-9A-F]{6}$/i);
    expect(getAgentColor("custom-ops-agent")).toBe(
      getAgentColor("custom-ops-agent"),
    );
  });

  it("keeps known role aliases on canonical role colors", () => {
    expect(getAgentColor("planner")).toBe(getAgentColor("pm"));
    expect(getAgentColor("builder")).toBe(getAgentColor("eng"));
    expect(getAgentColor("growth")).toBe(getAgentColor("gtm"));
    expect(getAgentColor("halpert")).toBe(getAgentColor("jim"));
    expect(getAgentColor("jim-halpert")).toBe(getAgentColor("jim"));
    expect(getAgentColor("archivist")).toBe(getAgentColor("pam"));
    expect(getAgentColor("librarian")).toBe(getAgentColor("pam"));
    expect(getAgentColor("operator")).toBe(getAgentColor("nex"));
  });

  it("has gawk eye cells for every sprite an agent can actually render", () => {
    // The whole point of the explicit table: if someone adds a sprite to the
    // catalog, or repoints a slug, this fails loudly instead of that agent
    // quietly rendering with no eyes while everyone else has them.
    const reachable = new Set<string>(Object.values(KNOWN_AVATAR_SLUG_MAP));
    for (const slug of [
      "jim",
      "halpert",
      "jim-halpert",
      "archivist",
      "librarian",
    ]) {
      reachable.add(baseSpriteID(resolvePortraitSprite(slug).id));
    }
    // Sample the procedural pool that unknown slugs land in.
    for (let i = 0; i < 250; i++) {
      reachable.add(baseSpriteID(resolvePortraitSprite(`agent-${i}`).id));
    }

    // Guard against the assertion below going vacuous if resolution changes.
    expect(reachable.size).toBeGreaterThan(15);

    const missing = [...reachable].filter((id) => !SPRITE_EYE_CELLS[id]);
    expect(missing).toEqual([]);
  });

  it("keeps every eye cell inside its own sprite's bounds", () => {
    // hybridJim is 24x17 while everything else is 16x16, so a hardcoded 16
    // anywhere in the supersampler or the table would land off-sprite.
    for (const [id, cells] of Object.entries(SPRITE_EYE_CELLS)) {
      const sprite = KNOWN_AVATAR_SPRITES[id];
      expect(sprite, `unknown sprite id in eye table: ${id}`).toBeDefined();
      if (!sprite) continue;
      const rows = sprite.portrait.length;
      const cols = sprite.portrait[0]?.length ?? 0;
      for (const [cx, cy] of cells) {
        expect(cx, `${id} eye column`).toBeGreaterThanOrEqual(0);
        expect(cx, `${id} eye column`).toBeLessThan(cols);
        expect(cy, `${id} eye row`).toBeGreaterThanOrEqual(0);
        expect(cy, `${id} eye row`).toBeLessThan(rows);
      }
    }
  });

  it("derives eye colour from the slug so it is stable and roster-independent", () => {
    expect(getAgentEyeColor("ceo")).toBe(getAgentEyeColor("ceo"));
    expect(getAgentEyeColor(" CEO ")).toBe(getAgentEyeColor("ceo"));
    // Aliases are the same teammate, so they must wear the same eyes.
    expect(getAgentEyeColor("planner")).toBe(getAgentEyeColor("pm"));
    expect(getAgentEyeColor("archivist")).toBe(getAgentEyeColor("pam"));
    expect(getAgentEyeColor("custom-ops-agent")).toMatch(/^#[0-9a-f]{6}$/i);
  });

  it("keeps eye colours clear of the accent, the danger colour, and the skin/brow bands", () => {
    const seen = new Set<string>();
    for (let i = 0; i < 300; i++) seen.add(getAgentEyeColor(`agent-${i}`));

    for (const hex of seen) {
      const [r, g, b] = [1, 3, 5].map((o) =>
        Number.parseInt(hex.slice(o, o + 2), 16),
      ) as [number, number, number];
      const luminance = 0.2126 * r + 0.7152 * g + 0.0722 * b;

      // Mid band only. Darker merges into the brow directly above the eye,
      // lighter merges into the surrounding skin.
      expect(luminance, `${hex} too dark (merges with brow)`).toBeGreaterThan(
        80,
      );
      expect(luminance, `${hex} too light (merges with skin)`).toBeLessThan(
        140,
      );

      // Never the product accent (purple) or the danger colour (red).
      const isPurple = b > r && b > g && r > g && b - g > 60;
      const isRed = r > 150 && r - g > 80 && r - b > 80;
      expect(isPurple, `${hex} collides with the accent`).toBe(false);
      expect(isRed, `${hex} collides with the danger colour`).toBe(false);
    }
    expect(seen.size).toBeGreaterThan(1);
  });

  it("spreads eye colours across a roster instead of collapsing onto a few", () => {
    // A fixed ten-swatch palette collided four ways on a ten-agent roster,
    // which is exactly the failure that makes the feature pointless. Distinct
    // teammates must be distinctly coloured.
    const roster = [
      "ceo",
      "eng",
      "pm",
      "designer",
      "gtm",
      "qa",
      "pam",
      "jim",
      "research",
      "cro",
      "cmo",
      "ai",
    ];
    const colours = new Set(roster.map(getAgentEyeColor));
    expect(colours.size).toBe(roster.length);
  });

  it("treats missing cells in short sprite rows as transparent", () => {
    const data = new Uint8ClampedArray(2 * 2 * 4);

    paintPixelAvatarData(data, [[1], [0, 1]], { 1: [10, 20, 30] }, 2);

    expect(Array.from(data.slice(0, 4))).toEqual([10, 20, 30, 255]);
    expect(Array.from(data.slice(4, 8))).toEqual([0, 0, 0, 0]);
    expect(Array.from(data.slice(8, 12))).toEqual([0, 0, 0, 0]);
    expect(Array.from(data.slice(12, 16))).toEqual([10, 20, 30, 255]);
  });
});
