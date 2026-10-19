// A wish's icon in the menu (L09 v6): its icon of the bank (wishicons/bank.json, the names the Djinn chooses from), a
// fine line in its duo's first colour with a finer filigree, and its gilding, small filled shapes, in the second. The
// duos' colours are tokens of review.css (--wi-*, .wish-duo-*), never decided here. A wish without an icon, or with a
// name the bank does not hold, shows its mark, grey, at the same fine stroke: a short walk on the seal's points,
// mirrored, a puff where it starts, drawn from its identifier, the same in every window. No id in any drawing: a copy
// never refers to another one. Our own drawings.
import type { Wish } from "../gen/ts/plan/v1/plan_pb";
import bankFile from "../wishicons/bank.json";

interface BankIcon {
  name: string;
  duo: string;
  line: string;
  filigree: string;
  gilding: string;
}

const bank = new Map(
  (bankFile.icons as BankIcon[]).map((icon) => [icon.name, icon]),
);

const svg = (size: number) =>
  ({
    width: size,
    height: size,
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    strokeWidth: bankFile.lineStroke,
    strokeLinecap: "round",
    strokeLinejoin: "round",
    "aria-hidden": true,
  }) as const;

// WishIcon draws the wish's icon of the bank, or its mark; size is its side in pixels at the default text size, a rem
// rule of styles.css (svg.lucide[width]) scaling it with the text.
export function WishIcon({
  wish,
  size,
}: {
  wish: Pick<Wish, "id" | "icon">;
  size: number;
}) {
  const icon = bank.get(wish.icon);
  if (!icon)
    return (
      <svg className="lucide wish-icon wish-mark" {...svg(size)}>
        <SealMark id={wish.id} />
      </svg>
    );
  // The bank's drawings are our own, embedded and checked to be plain shapes (wishicons_test.go): no script, no id.
  /* eslint-disable @eslint-react/dom-no-dangerously-set-innerhtml -- The bank's own SVG shapes. */
  return (
    <svg
      className={`lucide wish-icon wish-duo-${icon.duo.toLowerCase()}`}
      {...svg(size)}
    >
      <g className="wish-icon-line">
        <g dangerouslySetInnerHTML={{ __html: icon.line }} />
        {icon.filigree && (
          <g
            strokeWidth={bankFile.filigreeStroke}
            dangerouslySetInnerHTML={{ __html: icon.filigree }}
          />
        )}
      </g>
      <g
        className="wish-icon-gild"
        dangerouslySetInnerHTML={{ __html: icon.gilding }}
      />
    </svg>
  );
  /* eslint-enable @eslint-react/dom-no-dangerously-set-innerhtml */
}

// The mark's walk: from a hash of the wish's identifier, a short walk on the left half of a 3x3 grid (2 or 3 steps,
// never back on itself), which touches the edge and the middle on two rows at least, so that its mirror adds to it.
function hash(text: string): number {
  let h = 2166136261 >>> 0;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

function random(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function walk(id: string, variant = 0): number[] {
  const half = [0, 1, 3, 4, 6, 7];
  for (let attempt = 0; ; attempt++) {
    const r = random(hash(`${id}#${variant}#${attempt}`));
    let at = half[Math.floor(r() * half.length)];
    const points = [at];
    const seen = new Set(points);
    const steps = 2 + Math.floor(r() * 2);
    for (let i = 0; i < steps; i++) {
      const x = at % 3;
      const y = (at / 3) | 0;
      const next: number[] = [];
      for (let dy = -1; dy <= 1; dy++)
        for (let dx = -1; dx <= 1; dx++) {
          const nx = x + dx;
          const ny = y + dy;
          const n = ny * 3 + nx;
          if (
            (dx || dy) &&
            nx >= 0 &&
            nx < 2 &&
            ny >= 0 &&
            ny < 3 &&
            !seen.has(n)
          )
            next.push(n);
        }
      if (!next.length) break;
      at = next[Math.floor(r() * next.length)];
      seen.add(at);
      points.push(at);
    }
    const rows = new Set(points.map((n) => (n / 3) | 0));
    if (
      points.length >= 3 &&
      points.some((n) => n % 3 === 0) &&
      points.some((n) => n % 3 === 1) &&
      rows.size > 1
    )
      return points;
  }
}

// The grid is the seal's: its edge middles the khatam's four tips, its corners the four others, its centre the flame's.
const SEAL_AT = (() => {
  const r = 9;
  const d = +(r * Math.SQRT1_2).toFixed(2);
  return [
    [12 - d, 12 - d],
    [12, 12 - r],
    [12 + d, 12 - d],
    [12 - r, 12],
    [12, 12],
    [12 + r, 12],
    [12 - d, 12 + d],
    [12, 12 + r],
    [12 + d, 12 + d],
  ];
})();

// SealMark is the walk drawn on the seal's points and mirrored, centred, a small one grown to fill the box as the icons
// do (1.5 times at most, its stroke unchanged), a puff where each half starts.
function SealMark({ id }: { id: string }) {
  const raw = walk(id).map((n) => SEAL_AT[n]);
  const all = [...raw, ...raw.map(([x, y]) => [24 - x, y])];
  const xs = all.map((p) => p[0]);
  const ys = all.map((p) => p[1]);
  const w = Math.max(...xs) - Math.min(...xs);
  const h = Math.max(...ys) - Math.min(...ys);
  const cy = (Math.max(...ys) + Math.min(...ys)) / 2;
  const k = Math.min(
    1.5,
    17 / w,
    h ? 15 / h : 1.5,
    Math.max(1, 17 / Math.max(w, h)),
  );
  const xy = raw.map(([x, y]) => [
    +(12 + (x - 12) * k).toFixed(2),
    +(12 + (y - cy) * k).toFixed(2),
  ]);
  const mirror = xy.map(([x, y]) => [+(24 - x).toFixed(2), y]);
  const points = (p: number[][]) => p.map((q) => q.join(",")).join(" ");
  return (
    <>
      <polyline points={points(xy)} />
      <polyline points={points(mirror)} />
      <circle
        cx={xy[0][0]}
        cy={xy[0][1]}
        r="1.6"
        fill="currentColor"
        stroke="none"
      />
      <circle
        cx={mirror[0][0]}
        cy={mirror[0][1]}
        r="1.6"
        fill="currentColor"
        stroke="none"
      />
    </>
  );
}
