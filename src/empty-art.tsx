// The empty states in the Djinn's world (Decision Q43 B, tilasm L14): a drawing above the sentence of each list,
// column or panel that fills its space and holds nothing. One family, made of a few parts (the tile of the machine's
// plinth, the lamp, its smoke, the stars, the khatam and its flame, the dunes, the Djinn): one line weight, the
// window's tokens only, so the theme changes them all. The drawing is decorative, the sentence says the fact first.
// Nothing loops: the smoke rises once as the state shows, then the drawing holds still (no html[data-still] entry).
import { type ReactNode, useState } from "react";

import { t, type TextKey } from "./i18n";
import "./empty-art.css";

// A drawing's size, its class, in pixels at the default text size: the class sets it in rem (empty-art.css).
const box = {
  "empty-art-column": [200, 130],
  "empty-art-wide": [220, 140],
  "empty-art-home": [260, 172],
  "empty-art-small": [120, 72],
  "empty-art-note": [160, 90],
  "empty-art-terminal": [170, 86],
} as const;
type Size = keyof typeof box;

function Art({ size, children }: { size: Size; children: ReactNode }) {
  const [w, h] = box[size];
  return (
    <svg
      className={`empty-art ${size}`}
      viewBox={`0 0 ${w} ${h}`}
      fill="none"
      aria-hidden="true"
    >
      {children}
    </svg>
  );
}

// ---- the parts ----------------------------------------------------------------------------------------------------

// Tile is the machine's plinth: an isometric top face, then its edge.
function Tile({
  x,
  y,
  w,
  edge = 4,
}: {
  x: number;
  y: number;
  w: number;
  edge?: number;
}) {
  const h = Math.round(w * 5.77) / 10;
  return (
    <>
      <path
        className="ea-ground ea-faint"
        d={`M${x - w} ${y}L${x} ${y - h}L${x + w} ${y}L${x} ${y + h}Z`}
      />
      <path
        className="ea-faint"
        d={`M${x - w} ${y}v${edge}L${x} ${y + h + edge}L${x + w} ${y + edge}v${-edge}`}
      />
    </>
  );
}

// Lamp is the lamp seen from the side, its foot at (x, y), its spout to the right: the tip of the spout is at
// (x + 47s, y - 21.5s), where smoke and the Djinn rise.
function Lamp({ x, y, s = 1 }: { x: number; y: number; s?: number }) {
  return (
    <g transform={`translate(${x} ${y}) scale(${s})`}>
      <path className="ea-brass ea-brass-fill" d="M-7 0l2-5h10l2 5Z" />
      <path
        className="ea-brass ea-brass-fill"
        d="M-26 -14C-25 -6 -12 -5 0 -5C12 -5 21 -6 24 -11C33 -12 40 -16 45 -22L48.5 -20.5C42 -12 33 -8 21 -8"
      />
      <path className="ea-brass" d="M-26 -14H26C33 -15 40 -17 45 -22" />
      <path
        className="ea-brass"
        d="M-25 -11C-36 -12 -38 -25 -28 -25C-24 -25 -22 -21 -20 -17"
      />
      <path
        className="ea-brass ea-brass-fill"
        d="M-13 -14C-13 -22 13 -22 13 -14Z"
      />
      <path className="ea-brass" d="M0 -20.5v-2.5" />
      <circle className="ea-brass ea-brass-fill" cx="0" cy="-25" r="2.2" />
    </g>
  );
}

// Wisp is a thread of smoke rising from (x, y), h high, leaning by lean; light is its paler tone.
function Wisp({
  x,
  y,
  h,
  lean = 0,
  light = false,
}: {
  x: number;
  y: number;
  h: number;
  lean?: number;
  light?: boolean;
}) {
  const k = h / 4;
  return (
    <path
      className={light ? "ea-smoke ea-smoke-light" : "ea-smoke"}
      d={`M${x} ${y}c-5 ${-k} ${6 + lean / 2} ${-k * 1.4} ${2 + lean / 2} ${-k * 2}s-7 ${-k * 1.2} ${lean / 2 - 1} ${-k * 2}`}
    />
  );
}

// Spark is a star of four points; Dot a far one.
function Spark({ x, y, r }: { x: number; y: number; r: number }) {
  return (
    <path
      className="ea-star"
      d={`M${x} ${y - r}Q${x} ${y} ${x + r} ${y}Q${x} ${y} ${x} ${y + r}Q${x} ${y} ${x - r} ${y}Q${x} ${y} ${x} ${y - r}Z`}
    />
  );
}
function Dot({ x, y, r = 1.1 }: { x: number; y: number; r?: number }) {
  return <circle className="ea-star" cx={x} cy={y} r={r} />;
}

// Khatam is the star of the icon: two squares, one turned by 45 degrees.
function khatamPath(x: number, y: number, r: number) {
  const square = (a: number) =>
    "M" +
    [0, 1, 2, 3]
      .map((i) => {
        const t = a + (i * Math.PI) / 2;
        return `${(x + r * Math.cos(t)).toFixed(1)} ${(y + r * Math.sin(t)).toFixed(1)}`;
      })
      .join("L") +
    "Z";
  return square(0) + square(Math.PI / 4);
}
function Khatam({
  x,
  y,
  r,
  faint = false,
}: {
  x: number;
  y: number;
  r: number;
  faint?: boolean;
}) {
  return (
    <path className={faint ? "ea-faint" : "ea-brass"} d={khatamPath(x, y, r)} />
  );
}

// Flame is the seal's saffron flame, its base at (x, y), h high: the one the eye and the machine's screen carry.
export function flamePaths(x: number, y: number, h: number) {
  const w = h * 0.42;
  return {
    outer: `M${x} ${y}C${x - w} ${y} ${x - w} ${y - h * 0.55} ${x} ${y - h}C${x + w * 0.2} ${y - h * 0.62} ${x + w} ${y - h * 0.5} ${x + w * 0.8} ${y - h * 0.25}C${x + w * 0.7} ${y - h * 0.05} ${x + w * 0.4} ${y} ${x} ${y}Z`,
    inner: `M${x} ${y - 1}C${x - w * 0.45} ${y - 1} ${x - w * 0.4} ${y - h * 0.35} ${x} ${y - h * 0.55}C${x + w * 0.45} ${y - h * 0.3} ${x + w * 0.4} ${y - 1} ${x} ${y - 1}Z`,
  };
}
function Flame({ x, y, h }: { x: number; y: number; h: number }) {
  const { outer, inner } = flamePaths(x, y, h);
  return (
    <>
      <path className="ea-flame" d={outer} />
      <path className="ea-flame-in" d={inner} />
    </>
  );
}

// Dunes are two soft lines w wide from (x, y).
function Dunes({ x, y, w }: { x: number; y: number; w: number }) {
  return (
    <>
      <path
        className="ea-faint"
        d={`M${x} ${y}C${x + w * 0.2} ${y - 14} ${x + w * 0.38} ${y - 14} ${x + w * 0.55} ${y - 3}S${x + w * 0.85} ${y - 12} ${x + w} ${y - 2}`}
      />
      <path
        className="ea-faint"
        d={`M${x + w * 0.1} ${y + 10}C${x + w * 0.35} ${y + 2} ${x + w * 0.6} ${y + 2} ${x + w * 0.9} ${y + 10}`}
      />
    </>
  );
}

// Eye is the Djinn's eye as djinn-eye.tsx draws it, still and small, at (x, y) its top left: the almond, the khatam
// for the iris, the flame for the pupil; the lid the dark theme lowers (--eye-lid-down).
function Eye({ x, y, s }: { x: number; y: number; s: number }) {
  const almond = "M2 13C10 1.5 34 1.5 42 13C34 24.5 10 24.5 2 13Z";
  return (
    <g transform={`translate(${x} ${y}) scale(${s})`} className="ea-eye">
      <path className="ea-eye-white" d={almond} />
      <path
        className="ea-eye-star"
        d="M16.6 7.6h10.8v10.8h-10.8ZM22 5.4L29.6 13L22 20.6L14.4 13Z"
      />
      <circle className="ea-eye-iris" cx="22" cy="13" r="4.4" />
      <path
        className="ea-eye-flame"
        d="M22 9.2C24.5 12 24.1 15.6 22 16.3C19.9 15.6 19.5 12 22 9.2Z"
      />
      <path
        className="ea-eye-lid"
        d="M2 13C10 1.5 34 1.5 42 13C34 7 10 7 2 13Z"
      />
      <path className="ea-eye-line" d={almond} />
    </g>
  );
}

// Djinn is the Djinn of smoke rising from a spout tip (x, y): a tail, a torso with folded arms, a head; awake it
// looks with its eye, asleep its eyes are two closed arcs.
function Djinn({
  x,
  y,
  asleep = false,
}: {
  x: number;
  y: number;
  asleep?: boolean;
}) {
  const [tx, ty, hx, hy] = [x - 2, y - 34, x - 3, y - 74];
  return (
    <>
      <path
        className="ea-smoke"
        d={`M${x} ${y}c8 -6 6 -14 -2 -18s-8 -12 0 -16`}
      />
      <path
        className="ea-smoke ea-smoke-fill"
        d={`M${tx} ${ty}C${tx - 14} ${ty - 6} ${tx - 17} ${ty - 22} ${tx - 8} ${ty - 29}C${tx + 1} ${ty - 33} ${tx + 14} ${ty - 30} ${tx + 14} ${ty - 19}C${tx + 14} ${ty - 10} ${tx + 6} ${ty - 4} ${tx} ${ty}Z`}
      />
      <path className="ea-smoke" d={`M${tx - 10} ${ty - 18}q9 5 20 -1`} />
      <circle className="ea-smoke ea-smoke-fill" cx={hx} cy={hy} r="8.5" />
      {asleep ? (
        <path
          className="ea-smoke"
          d={`M${hx - 5.5} ${hy}q2.5 2.2 5 0M${hx + 0.5} ${hy}q2.5 2.2 5 0`}
        />
      ) : (
        <Eye x={hx - 7.5} y={hy - 4.5} s={0.34} />
      )}
    </>
  );
}

// Capsule is a roll of parchment from (x1, y1) to (x2, y2), r wide.
function Capsule({
  x1,
  y1,
  x2,
  y2,
  r,
}: {
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  r: number;
}) {
  const n = Math.hypot(x2 - x1, y2 - y1);
  const [nx, ny] = [(-(y2 - y1) / n) * r, ((x2 - x1) / n) * r];
  const f = (v: number) => v.toFixed(1);
  return (
    <path
      className="ea-line ea-paper"
      d={`M${f(x1 + nx)} ${f(y1 + ny)}L${f(x2 + nx)} ${f(y2 + ny)}A${r} ${r} 0 0 0 ${f(x2 - nx)} ${f(y2 - ny)}L${f(x1 - nx)} ${f(y1 - ny)}A${r} ${r} 0 0 0 ${f(x1 + nx)} ${f(y1 + ny)}Z`}
    />
  );
}

// ---- the drawings -------------------------------------------------------------------------------------------------

// The questions column draws one of these five per session (useSessionPick).
function QuestionsLamp() {
  return (
    <Art size="empty-art-column">
      <Tile x={100} y={86} w={62} />
      <Lamp x={92} y={88} />
      <g className="empty-rise">
        <Wisp x={139} y={66} h={44} lean={4} />
        <Wisp x={144} y={58} h={28} lean={-2} light />
      </g>
      <Spark x={42} y={30} r={4} />
      <Dot x={60} y={16} />
      <Spark x={170} y={22} r={3} />
      <Dot x={160} y={44} r={1} />
    </Art>
  );
}
function QuestionsScroll() {
  return (
    <Art size="empty-art-column">
      <Tile x={100} y={86} w={62} />
      <path className="ea-line ea-paper" d="M62 74L118 42L146 58L90 90Z" />
      <Capsule x1={60} y1={73} x2={90} y2={90} r={3.6} />
      <Capsule x1={118} y1={41} x2={148} y2={58} r={3.6} />
      <path
        className="ea-faint"
        d="M84 74l34 -19M92 79l26 -15"
        strokeDasharray="2 4"
      />
      <path
        className="ea-brass ea-brass-fill"
        d="M128 82C140 70 156 62 172 60C164 70 150 78 134 84Z"
      />
      <path className="ea-brass" d="M126 86L160 66" />
      <g className="empty-rise">
        <Wisp x={60} y={30} h={20} lean={2} light />
      </g>
      <Spark x={170} y={26} r={3} />
      <Dot x={40} y={40} />
    </Art>
  );
}
function QuestionsEye() {
  return (
    <Art size="empty-art-column">
      <Tile x={96} y={90} w={58} />
      <Lamp x={84} y={92} s={0.9} />
      <g className="empty-rise">
        <Djinn x={126} y={72} />
      </g>
      <Spark x={160} y={24} r={3.5} />
      <Dot x={150} y={50} r={1} />
      <Dot x={36} y={30} />
    </Art>
  );
}
function QuestionsSky() {
  return (
    <Art size="empty-art-column">
      <path
        className="ea-brass ea-brass-fill"
        d="M150 19A11 11 0 1 0 150 41A8.25 11 0 1 1 150 19Z"
      />
      <Spark x={56} y={22} r={4} />
      <Spark x={96} y={38} r={2.5} />
      <Dot x={78} y={14} />
      <Dot x={120} y={18} r={1} />
      <Dot x={178} y={56} r={1} />
      <Dot x={30} y={48} r={1} />
      <Dunes x={14} y={92} w={172} />
      <Lamp x={64} y={87} s={0.55} />
    </Art>
  );
}
function QuestionsSeal() {
  return (
    <Art size="empty-art-column">
      <Tile x={100} y={86} w={62} />
      <circle className="ea-brass ea-brass-fill" cx="100" cy="56" r="25" />
      <Khatam x={100} y={56} r={17} />
      <Flame x={100} y={64} h={16} />
      <path className="ea-faint" d="M100 26v-6M100 86v6M70 56h-6M130 56h6" />
      <Dot x={160} y={24} />
      <Spark x={42} y={30} r={3} />
    </Art>
  );
}

// Calm is the one place the Djinn sleeps, because it is true there: nothing waits and nothing runs.
function Calm() {
  return (
    <Art size="empty-art-column">
      <Tile x={96} y={90} w={58} />
      <Lamp x={84} y={92} s={0.9} />
      <Djinn x={126} y={72} asleep />
      <path className="ea-snore" d="M150 24h6l-6 7h6M162 14h4l-4 5h4" />
      <Dot x={36} y={30} />
    </Art>
  );
}

// Still is the lamp at rest, no smoke: nothing moves among the tasks, though a question may wait for you elsewhere, so
// the Djinn does not sleep there.
function Still() {
  return (
    <Art size="empty-art-column">
      <Tile x={100} y={86} w={62} />
      <Lamp x={92} y={88} />
      <Spark x={150} y={30} r={4} />
      <Dot x={58} y={26} />
      <Dot x={170} y={52} r={1} />
    </Art>
  );
}

function Home() {
  return (
    <Art size="empty-art-home">
      <Tile x={130} y={116} w={84} edge={5} />
      <Lamp x={120} y={118} s={1.35} />
      <Spark x={190} y={34} r={6} />
      <Dot x={70} y={40} r={1.2} />
      <Dot x={212} y={70} />
      <Spark x={52} y={74} r={3} />
    </Art>
  );
}
function Tasks() {
  return (
    <Art size="empty-art-wide">
      <Tile x={110} y={88} w={76} />
      <Lamp x={74} y={92} s={0.75} />
      <g className="empty-rise">
        <Wisp x={109} y={76} h={26} lean={2} light />
      </g>
      <path
        className="ea-brass"
        d="M100 98C120 104 140 92 150 84S170 72 176 76"
        strokeDasharray="1 5"
      />
      <path className="ea-line" d="M176 76v-20l10 4-10 4" />
      <Spark x={186} y={26} r={3} />
    </Art>
  );
}
function Decisions() {
  return (
    <Art size="empty-art-wide">
      <Tile x={110} y={90} w={76} />
      <path className="ea-line ea-paper" d="M62 76L128 38L160 56L94 94Z" />
      <path
        className="ea-faint"
        d="M86 76l40-23M94 81l30-17M102 86l20-11"
        strokeDasharray="2 4"
      />
      <circle className="ea-brass ea-brass-fill" cx="168" cy="82" r="9" />
      <Khatam x={168} y={82} r={5} />
      <path className="ea-brass" d="M168 73v-12M163 61h10" />
      <Spark x={60} y={30} r={3.5} />
      <Dot x={186} y={36} />
    </Art>
  );
}
function Review() {
  return (
    <Art size="empty-art-wide">
      <Tile x={110} y={92} w={76} />
      <path className="ea-line ea-paper" d="M76 76.4v-22L110 74V96Z" />
      <path className="ea-line ea-paper" d="M144 76.4v-22L110 74V96Z" />
      <path
        className="ea-line ea-paper"
        d="M76 54.4L110 34.8L144 54.4L110 74Z"
      />
      <path className="ea-line" d="M76 64.4L110 84L144 64.4" />
      <path className="ea-brass ea-brass-fill" d="M106 80h8v10h-8Z" />
      <Spark x={170} y={30} r={3.5} />
      <Dot x={56} y={44} />
    </Art>
  );
}
function Agents() {
  return (
    <Art size="empty-art-wide">
      <Tile x={110} y={90} w={76} />
      <Lamp x={58} y={92} s={0.6} />
      <Lamp x={150} y={78} s={0.6} />
      <path
        className="ea-smoke ea-smoke-light"
        d="M86 79C104 50 140 40 168 62"
        strokeDasharray="2 5"
      />
      <Spark x={118} y={30} r={3.5} />
      <Dot x={186} y={40} />
    </Art>
  );
}
function Tilasms() {
  return (
    <Art size="empty-art-wide">
      <Tile x={110} y={90} w={76} />
      <path
        className="ea-brass"
        d="M110 36L140 52V80L110 96L80 80V52Z"
        strokeDasharray="4 4"
      />
      <Khatam x={110} y={66} r={11} faint />
      <g className="empty-rise">
        <path className="ea-line ea-paper" d="M126 14h20l6 6v24h-26Z" />
        <path className="ea-line" d="M146 14v6h6M131 26h14M131 32h10" />
      </g>
      <Spark x={60} y={30} r={3} />
      <Dot x={178} y={70} />
    </Art>
  );
}
function NoMatch() {
  return (
    <Art size="empty-art-small">
      <Dunes x={8} y={58} w={104} />
      <circle className="ea-line ea-paper" cx="66" cy="30" r="13" />
      <path className="ea-line" d="M75 39l12 12" />
    </Art>
  );
}
function Comments() {
  return (
    <Art size="empty-art-note">
      <path className="ea-faint" d="M24 70h112" />
      <path className="ea-line ea-paper" d="M54 70v-10c0-6 16-6 16 0v10" />
      <path className="ea-line" d="M54 60h16" />
      <path
        className="ea-brass ea-brass-fill"
        d="M86 66C96 48 114 34 134 28C124 44 108 58 92 68Z"
      />
      <path className="ea-brass" d="M84 72L122 40" />
      <Spark x={40} y={28} r={3} />
    </Art>
  );
}
function Terminal() {
  return (
    <Art size="empty-art-terminal">
      <Lamp x={52} y={78} s={0.8} />
      <g className="empty-rise">
        <Wisp x={90} y={61} h={20} lean={2} light />
        <path className="ea-smoke" d="M98 30l8 6-8 6M112 44h12" />
      </g>
      <Dot x={140} y={20} />
      <Spark x={30} y={26} r={3} />
    </Art>
  );
}
function NoProject() {
  return (
    <Art size="empty-art-terminal">
      <Lamp x={48} y={78} s={0.8} />
      <path
        className="ea-line"
        d="M96 72V36h18l5 6h29v30Z"
        strokeDasharray="3 4"
      />
      <Spark x={150} y={18} r={3} />
    </Art>
  );
}
function Clean() {
  return (
    <Art size="empty-art-wide">
      <Tile x={110} y={88} w={76} />
      <path
        className="ea-faint"
        d="M64 80C90 66 120 60 156 70M74 90C100 76 130 70 166 80M84 98C110 86 136 82 160 88"
      />
      <circle className="ea-line ea-paper" cx="108" cy="58" r="7" />
      <Spark x={172} y={30} r={3.5} />
      <Dot x={50} y={40} />
    </Art>
  );
}
function NoWorktree() {
  return (
    <Art size="empty-art-wide">
      <Tile x={110} y={88} w={76} />
      {[0, 1, 2, 3, 4, 5].map((i) => {
        const [x, y] = [86 + i * 17, 96 - i * 11 + (i % 2 ? -3 : 3)];
        return (
          <ellipse
            key={i}
            className={i > 3 ? "ea-step ea-step-far" : "ea-step"}
            cx={x}
            cy={y}
            rx="3.4"
            ry="2"
            transform={`rotate(-30 ${x} ${y})`}
          />
        );
      })}
      <Spark x={186} y={22} r={3.5} />
      <Dot x={52} y={40} />
    </Art>
  );
}

const drawings = {
  calm: Calm,
  still: Still,
  home: Home,
  tasks: Tasks,
  decisions: Decisions,
  review: Review,
  agents: Agents,
  tilasms: Tilasms,
  nomatch: NoMatch,
  comments: Comments,
  terminal: Terminal,
  noproject: NoProject,
  clean: Clean,
  noworktree: NoWorktree,
};
export type EmptyArt = keyof typeof drawings;

// EmptyState is a drawing above its sentence, and what follows it (a button, a second line); small lays them side
// by side, left, for a narrow place.
export function EmptyState({
  art,
  children,
  after,
  small = false,
}: {
  art: EmptyArt;
  children: ReactNode;
  after?: ReactNode;
  small?: boolean;
}) {
  const Drawing = drawings[art];
  return (
    <div className={small ? "empty-state small" : "empty-state"}>
      <Drawing />
      <p className="empty-state-text">{children}</p>
      {after}
    </div>
  );
}

// The questions column's variants: a drawing and its sentence, the fact first; each key quoted whole for the locales'
// test.
const questionVariants: readonly { art: () => ReactNode; line: TextKey }[] = [
  { art: QuestionsLamp, line: "question.none_lamp" },
  { art: QuestionsScroll, line: "question.none_scroll" },
  { art: QuestionsEye, line: "question.none_eye" },
  { art: QuestionsSky, line: "question.none_sky" },
  { art: QuestionsSeal, line: "question.none_seal" },
];

// sessionPick draws an index below n once per session of the window, kept in the page's session storage: the same
// variant on the flying carpet and in every wish until the window closes. Without storage (the tests' pages), the
// first.
const PICK = "djinn.empty-pick";
let picked: number | undefined;
export function sessionPick(n: number): number {
  if (picked === undefined) {
    try {
      const kept = Number(window.sessionStorage.getItem(PICK) ?? "");
      picked =
        Number.isInteger(kept) && kept >= 0
          ? kept
          : Math.floor(Math.random() * n);
      window.sessionStorage.setItem(PICK, String(picked));
    } catch {
      picked = 0;
    }
  }
  return picked % n;
}

// QuestionsEmpty is the questions column with no open question: the variant of the session, or the Djinn asleep when
// nothing waits and nothing runs.
export function QuestionsEmpty({ calm = false }: { calm?: boolean }) {
  const [index] = useState(() => sessionPick(questionVariants.length));
  const { art: Drawing, line } = calm
    ? { art: Calm, line: "plan.calm" as TextKey }
    : questionVariants[index];
  return (
    <div className="empty-state question-none">
      <Drawing />
      <p className="empty-state-text">{t(line)}</p>
    </div>
  );
}
