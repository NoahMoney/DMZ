# DMZ design system

The brief is `DESIGN_DIRECTIVE.md`. This is the settled answer to it: the principles, the tokens, and the rules
every component follows. Read the constraints in `DESIGN_DIRECTIVE.md` §1 before changing anything here — no
build step, no external fonts, 18 432 DOC bytes per file.

---

## The thesis, from the mark

The DMZ wordmark is three facts, and the whole system comes out of them.

1. **True black ground.** Not a blue-black, not a charcoal. `#000000`.
2. **Nothing is rounded. Everything is cut.** The letterforms are chamfered — corners removed at an angle, never
   filleted. The radius is replaced by the cut.
3. **A diagonal crosses the mark.** A lime stroke through the `M`, a graphite stroke through the `Z`. That
   diagonal *is* the demilitarized zone: the boundary where two systems meet and neither owns the other.

So: **black ground, cut geometry, one lime diagonal.** Every screen is built from those three.

---

## Principles

1. **The cut replaces the radius.** Chamfer, never fillet. A 45° corner cut on an instrument surface, a hard 0 or
   2px edge everywhere else. If something is rounded, it is wrong.
2. **The diagonal is reserved.** One lime diagonal per view, at most. It marks where you are or what is live. It
   is never decoration.
3. **Lime is a verb.** It means *act* or *alive* — primary action, focus, the block clock. It never appears on a
   price, a chart line, or a container.
4. **Solid surfaces, not stacked translucency.** Three near-black steps. Translucent whites over translucent
   whites is what makes card-in-card look muddy; solid steps never compound.
5. **Data does not live in a box.** Level 3 (§Surfaces) has no background and no border. A number, a label, and
   the space around it.
6. **Numbers are the typography.** Tabular figures, tight tracking, a weight of their own. `1.906 DERO` is an
   instrument reading, not a statistic.
7. **Two voices: human and machine.** The body face speaks to a person; the mono face speaks in hashes, heights
   and addresses. Their contrast is the identity. Neither trespasses.
8. **Density follows the task.** Markets are dense. Portfolio is spacious. Execution is focused. Technical
   detail is packed. One global density is a failure to decide.
9. **State is stated, not glowed.** A 4px mark and precise words beat a badge with a shadow. No glow, anywhere.
10. **Say the true thing.** Ring size 2 is public. A federation holds the reserve. There is no operator. The
    interface never implies a safety that does not exist, and there is no SAFE badge.
11. **Quiet.** If an element does not carry information, hierarchy, state or affordance, delete it.
12. **Fast is a design property.** Sophistication comes from type, spacing and composition — never from blur,
    canvas, or a dependency.

---

## Tokens

Defined once in `app/styles.css` `:root`. Nothing downstream hard-codes a colour.

### Ground and surfaces

| Token | Value | Use |
|---|---|---|
| `--bg` | `#000000` | the ground; the mark's own black |
| `--s1` | `#0a0b0a` | Level 2 module |
| `--s2` | `#111312` | raised: input, hover, sheet |
| `--s3` | `#1a1d1b` | pressed, selected |
| `--edge` | `rgba(233,234,231,.10)` | hairline |
| `--edge2` | `rgba(233,234,231,.22)` | emphasis hairline, focus |

Solid steps, not alpha stacks. A module on the ground is `--s1`; an input inside it is `--s2`. There is no
third nesting level — if you need one, the composition is wrong.

### Ink

| Token | Value | Use |
|---|---|---|
| `--fg` | `#e9eae7` | primary, from the mark's letterforms |
| `--fg2` | `#9a9d97` | secondary |
| `--fg3` | `#63676a` | tertiary, labels |

### Accent and semantics

| Token | Value | Use |
|---|---|---|
| `--acc` | `#b4f03f` | **verb only**: primary action, focus ring, live, active nav |
| `--acc-ink` | `#0a1002` | text on lime |
| `--acc-dim` | `rgba(180,240,63,.13)` | lime at rest (selected row, active tint) |
| `--graphite` | `#383e44` | structural, from the mark's `Z` stroke |
| `--up` | `#3fbf7f` | market direction up — deliberately cooler and dimmer than lime |
| `--down` | `#e05263` | market direction down |
| `--warn` | `#e8a33d` | caution |

Up and down must never be mistaken for something you can click, and lime must always win attention. That is why
`--up` is a cooler, darker green than `--acc`: the hierarchy is **act → direction → structure**.

### Networks

Three ecosystems, expressed as a grammar rather than a palette. A network is a **two-letter mono label** plus a
hairline leading edge in its tint. The tint is never a fill and never a large area.

| Network | Token | Label |
|---|---|---|
| DERO | `--net-dero` `#e9eae7` | `DR` |
| Monero | `--net-xmr` `#d4762f` | `XM` |
| Zcash | `--net-zec` `#c9a227` | `ZC` |

Wrapped assets carry both: origin network and the DERO representation, in that order.

### Geometry

| Token | Value | Use |
|---|---|---|
| `--r0` | `0` | data, rows, tables |
| `--r1` | `2px` | controls, modules, inputs |
| `--cut` | `10px` | the chamfer, applied with `clip-path` |

`--pill`, `--r2`, `--r3`, `--sh`, `--glow-acc` and `--mood` are **deleted**. Nothing is a pill; nothing glows.

### Type

`--font` and `--mono` stay system stacks — the CSP forbids external fonts and a subsetted weight is already over
the DOC cap. The identity comes from what we do with them.

| Role | Rule |
|---|---|
| Display | 600–700, tracking `-.03em`, tabular figures |
| Heading | 600, tracking `-.02em` |
| Body | 400/500, 15px, tracking 0 |
| Label | 10.5px, 700, `letter-spacing:.10em`, uppercase, `--fg3` |
| Technical | `--mono`, .85em, `--fg2`, never for prose |
| Numeric | `font-variant-numeric: tabular-nums`, tracking `-.02em` |

---

## Surfaces: three levels, and only three

**Level 1 — Instrument.** The one thing a screen is about. Portfolio total, the token's price, the trade
quote. `--bg` ground, no border or a single top hairline, a chamfered corner, display type. At most one per view.

**Level 2 — Module.** A functional grouping: a market list, the feed, a form. `--s1`, `1px solid --edge`,
`--r1`. No nesting.

**Level 3 — Data.** Volume, liquidity, FDV, block, confirmations. **No background, no border.** Label above in
Label type, value below in Numeric. Separated by space and, where a row needs it, a hairline.

---

## Components

**Buttons.** Rectangular, `--r1`, no pill anywhere.
- *Primary*: lime fill, `--acc-ink` text, chamfered top-right corner. Press = translate 1px down, no shadow.
- *Secondary*: `--s2`, `1px solid --edge`, `--fg`.
- *Ghost*: transparent, `--fg2`, hairline appears on hover.
- Focus is a 2px `--acc` outline with a 2px offset, always visible on keyboard.

**Selectors / filters.** A horizontal set of rectangular cells separated by hairlines, not floating pills. The
selected cell carries a 2px lime bottom edge and `--fg`; the rest are `--fg3`.

**Tables.** No row is a card. `--r0`, hairline between rows, hover is `--s1` and nothing else. Numeric columns
right-aligned, tabular, consistent decimals. Column heads in Label type. Row height 44px comfortable, 34px
compact.

**Inputs.** `--s2`, `1px solid --edge`, `--r1`. Focus swaps the border to `--acc`. Amount inputs use Numeric type
at display size.

**Status.** A 6px square (never a circle, never a glowing dot) plus precise words in Label type. Colour alone
never carries meaning.

**Network mark.** 2-letter mono label, 1px leading edge in the network tint.

**Technical data.** Mono, `--fg2`, middle-truncated with the head and tail preserved, click to copy, no icon
until hover. A value the user must read in full — a deposit address, a destination, a txid — is never truncated;
it wraps with `word-break:break-all`.

**Empty states.** Label-type title, one sentence of why, one action. No illustration.

**Charts.** Hairline grid, 1px line, no fill gradient, no glow. Axis labels in Label type, values in Numeric.
Up and down use `--up` / `--down`; the crosshair uses `--edge2`.

**Motion.** 120–200 ms, `cubic-bezier(.2,0,0,1)`. Hover and press only, plus number rolls. Everything inside
`@media (prefers-reduced-motion: reduce)` collapses to none.

---

## The mark

Inline SVG in `app/ui.js` (`dmzMark()`), drawn on a 40-unit cap height with a 6-unit chamfer. Three fills:
`--fg` letterforms, `--acc` diagonal through the `M`, `--graphite` diagonal in the `Z`. It is the only place
lime and graphite appear together, and it is never re-coloured.

The **zone rule** — a 1px lime diagonal — is the mark's diagonal reused as an interface element: the active
navigation indicator and the leading edge of a Level 1 surface. One per view.
