# gawkbot brand

## Mark

`gawkbot-mark.svg`. Two hollow eyes, gawking. Eleven by fifteen pixel grid,
whole-pixel coordinates, eight rects. Long and level, never tilted.

Everything is `currentColor`, so the mark inverts for free: set `color` and it
follows. The `color` attribute on the file is only a standalone fallback for
`<img>` use, where CSS cannot reach it.

### Four construction rules

Each of these was learned by getting it wrong.

1. **Never fill the eyes.** The emptiness is the entire joke. Filled in, it
   stops being a parody and becomes the thing it parodies.
2. **Keep the eight rects merged**, one per column and one per cap. Emitting
   one rect per pixel row is the obvious way to build it and it is wrong:
   under `scaleY` the antialiasing paints a seam between every pair and the
   narrowed state comes out visibly striped.
3. **No `shape-rendering="crispEdges"` on an animated copy.** A fractional
   scale snaps row by row and the motion goes steppy. Static copies (favicon,
   og-image, brand exports, and any mark small enough not to animate) should
   carry it, so they stay sharp.
4. **Below roughly 24px, ship the static open state with no animation.** The
   whole travel is a couple of pixels at that size and it reads as a wobble.

### Animation

Two eased states, not frames. The eyes narrow and widen, like someone actually
looking at something.

```css
.gawkbot-mark .gawkbot-eyes {
  transform-box: fill-box; transform-origin: center;
  animation: gawkbot-look 3.2s ease-in-out infinite alternate;
}
@keyframes gawkbot-look { from { transform: scaleY(1); } to { transform: scaleY(0.42); } }
@media (prefers-reduced-motion: reduce) { .gawkbot-mark .gawkbot-eyes { animation: none; } }
```

Inline the SVG rather than referencing it through `<symbol>` and `<use>`. CSS
selectors do not cross the `<use>` shadow boundary, so the rule above will
never match an instance created that way.

## Wordmark

`gawkbot`. One word, always lowercase, including at the start of a sentence.
Rewrite a sentence rather than capitalising it. Tight tracking (`-0.03em` to
`-0.045em`), set slightly small for the space it occupies. It is not shouting.

Never `gawk` on its own. That is GNU awk, a core Unix tool.

## Palette

Six semantics and one accent. Never pure black, never pure white.

| token     | light     | dark      | use                                   |
|-----------|-----------|-----------|---------------------------------------|
| `ink`     | `#0E0E11` | `#EDEBE4` | foreground                            |
| `paper`   | `#F4F3EF` | `#121214` | page background                       |
| `raised`  | `#FBFAF7` | `#191919` | cards and panels                      |
| `line`    | `#D8D5CD` | `#2E2D2A` | hairline borders                      |
| `mute`    | `#6E6C66` | `#8B8880` | secondary text                        |
| `accent`  | `#C8402F` | `#E0563F` | the eye, errors, destructive. Nothing else. |

The accent is bloodshot. It is never decoration.

In `website/index.html` these live in one `:root` block, expressed as channel
triplets (`--ink-rgb`, `--paper-rgb`, `--accent-rgb`) so translucent variants
derive instead of duplicating. The pixel-office canvas reads the same tokens
at runtime and builds its greyscale from them, so a palette change repaints
the illustration too.

### If you add anything to the canvas

Never write a literal hex there. Colours in the scene come from
`PALETTE.ramp(t)`, which mixes paper into ink, or from `PALETTE.ink` /
`.mute` / `.line` / `.accent`. A hardcoded hex will look correct in light
mode and silently stay wrong in dark mode and under every future palette.

Almost everything resolves inside the draw loop, so it follows a token change
for free. The two exceptions are cached at setup for speed: `TILE_COLORS` and
the per-character colours. Both are rebuilt by `applyPalette()`, which also
runs on a live `prefers-color-scheme` change. If you add a third cached
colour table, add it to `applyPalette()` or it will go stale the moment a
viewer switches their OS theme with the page open.

## Voice

Placid, literal, unhelpfully honest. Flat declaratives. It never brags and it
never apologises. It is the inverse of the thing it parodies, not a louder
version, so it is never edgy or spicy.

No em-dashes, no contractions, Oxford comma. No exclamation marks and no emoji
in product copy.

## Legal

Ship this verbatim wherever the brand appears publicly:

> gawkbot is an independent, source-available project. It is a parody. It is
> not affiliated with, endorsed by, or sponsored by xAI, SpaceXAI, X Corp, or
> Cursor. Grok and Grok Bot are trademarks of their respective owners.

Never reproduce or recolour another company's mark, the X logo included, and
never draw a circle cut by a diagonal slash. Do not put "Grok" in the name,
wordmark, domain, title tag, or metadata. Any pricing comparison must be dated
and must link its source.

## Files

```
brand/
  gawkbot-mark.svg              primary, scales to any size
  gawkbot-mark-inverted.svg     for stamping on a filled ink field
  png/
    gawkbot-mark-16.png           tab favicon
    gawkbot-mark-32.png           standard favicon
    gawkbot-mark-64.png
    gawkbot-mark-128.png
    gawkbot-mark-180.png          apple-touch-icon
    gawkbot-mark-192.png          Android chrome icon
    gawkbot-mark-256.png
    gawkbot-mark-512.png          PWA splash
    gawkbot-mark-1024.png         App Store and marketing hero
    gawkbot-mark-inverted-*.png   same sizes
```

The site favicon is the 👀 emoji rather than the mark, because the platform
draws the emoji and it survives at 16px.

## Clear space

Leave at least one eye-width of empty space on every side. Do not pack the
mark against text, borders, or other marks. The square PNG exports already
carry this margin.

## Do

- Use the SVG wherever possible. It stays crisp at every size.
- Set `color` on the SVG and let the mark follow. That is the whole point.
- Inline the SVG when it needs to animate.

## Do not

- Do not fill the eyes, add pupils, or put anything inside them.
- Do not add drop shadows, glows, or blur. There is no ornament in this brand.
- Do not rotate, tilt, stretch, or round the mark. It stays long and level.
- Do not tint it with the accent. The accent is for errors and destructive
  actions, and for the bloodshot eyes of the bots in the pixel scene.

## Regenerating the PNG exports

The exports are square, so they use a centred 19x19 wrapper rather than the
11x15 mark directly. Rendering the 11x15 file into a square would stretch it.

```bash
cd brand
EYES=$(sed -n 's/.*\(<g class="gawkbot-eyes".*<\/g>\).*/\1/p' gawkbot-mark.svg)
for c in "ink #0E0E11 #F4F3EF" "paper #F4F3EF #0E0E11"; do
  set -- $c
  printf '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 19 19" shape-rendering="crispEdges" color="%s"><g transform="translate(4,2)">%s</g></svg>' \
    "$2" "$EYES" > /tmp/sq-$1.svg
done
for size in 16 32 64 128 180 192 256 512 1024; do
  rsvg-convert -w $size -h $size -b '#F4F3EF' /tmp/sq-ink.svg \
    -o png/gawkbot-mark-${size}.png
  rsvg-convert -w $size -h $size -b '#0E0E11' /tmp/sq-paper.svg \
    -o png/gawkbot-mark-inverted-${size}.png
done
cp png/gawkbot-mark-180.png ../website/apple-touch-icon.png
```

Requires `rsvg-convert` (`brew install librsvg`).
