// What the Djinn's eye decides (djinn-eye.tsx, Decision Q38 A, tilasm L13 v2): where it rests and how it looks, from
// what the window already knows; the path it flies from one place to the next; and how long the calm gate holds it.
// Pure, so the Node tests read it without a page.

// How present the Djinn is, a choice of the personal space: alive (the eye goes where the focus is), discreet (it stays in the
// seal and only changes its state), or none.
export type Presence = "alive" | "discreet" | "none";

export const presences: readonly Presence[] = ["alive", "discreet", "none"];

// The eye's moods: asleep (the lead is stopped), restarting (Djinn's stream is cut), resting (home, awake), reading (a
// question has the focus), watching (a tab has it), listening (Write to the Djinn).
export type Mood =
  "asleep" | "restarting" | "resting" | "watching" | "reading" | "listening";

export interface EyeSignals {
  presence: Presence;
  // Djinn's stream answers.
  live: boolean;
  // The page's lead is stopped, or could not start; undefined on a page without one.
  stopped?: boolean;
  // A question of the page holds up a worker.
  blocking: boolean;
  // Something else waits for the developer on the page: a worker waiting, a lit ifrit.
  needs: boolean;
  // The places shown now: "home" (the seal), "pill" (Write to the Djinn), "q:<question id>", "tab:<view>".
  perches: ReadonlySet<string>;
  // The place of the element that has the focus, if it has one: its question, Write to the Djinn, its tab.
  focus?: string;
}

export interface EyeTarget {
  // Where the eye rests; empty, nowhere.
  perch: string;
  mood: Mood;
  // The flame shines: something waits for the developer.
  shine: boolean;
}

export const questionPerch = (id: string) => `q:${id}`;
export const tabPerch = (view: string) => `tab:${view}`;

// eyeTarget is where the eye rests and how: home while the stream is cut or the lead stopped; else by the element that
// has the focus, and only there (W259): its question, Write to the Djinn, its tab. With the focus on nothing that has a
// place, it listens on Write to the Djinn, the Djinn's own place; with that not shown, home.
export function eyeTarget(s: EyeSignals): EyeTarget {
  if (s.presence === "none")
    return { perch: "", mood: "resting", shine: false };
  const home = s.perches.has("home") ? "home" : "";
  if (!s.live) return { perch: home, mood: "restarting", shine: false };
  if (s.stopped) return { perch: home, mood: "asleep", shine: false };
  const shine = s.needs || s.blocking;
  if (s.presence === "alive") {
    if (s.focus && s.perches.has(s.focus))
      return { perch: s.focus, mood: moodAt(s.focus), shine };
    if (s.perches.has("pill"))
      return { perch: "pill", mood: "listening", shine };
  }
  return { perch: home, mood: "resting", shine };
}

const moodAt = (perch: string): Mood =>
  perch.startsWith("q:")
    ? "reading"
    : perch.startsWith("tab:")
      ? "watching"
      : perch === "pill"
        ? "listening"
        : "resting";

export interface Point {
  x: number;
  y: number;
}

export interface Flight {
  // From 0 to the end, evenly in time: the eye's centre and its scale at each step.
  steps: (Point & { scale: number })[];
  duration: number;
  // The Bezier's control point: the top of the curve.
  control: Point;
}

// The slow start and the slow stop of a flight.
export const easeInOut = (t: number) =>
  t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;

// flight is the path from one place to another: a quadratic Bezier whose top lifts towards the top of the window, or,
// along the questions column (nearly vertical), bows a little into the margin on its left; eased in and out, so the
// eye can be followed, and long with the distance (0.52 s to 1.1 s). Its scale goes from one place's to the other's,
// swelling a little half way.
export function flight(
  from: Point,
  to: Point,
  fromScale: number,
  toScale: number,
  count = 24,
): Flight {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  const dist = Math.hypot(dx, dy) || 1;
  const column = Math.abs(dx) < 12;
  let nx = -dy / dist;
  let ny = dx / dist;
  if (column ? nx > 0 : ny > 0) {
    nx = -nx;
    ny = -ny;
  }
  const bend = column
    ? Math.min(dist * 0.12, 7)
    : Math.min(Math.max(dist * 0.22, 10), 140);
  const control = {
    x: (from.x + to.x) / 2 + nx * bend,
    y: (from.y + to.y) / 2 + ny * bend,
  };
  const steps = [];
  for (let i = 0; i <= count; i++) {
    const s = easeInOut(i / count);
    const u = 1 - s;
    steps.push({
      x: u * u * from.x + 2 * u * s * control.x + s * s * to.x,
      y: u * u * from.y + 2 * u * s * control.y + s * s * to.y,
      scale:
        (fromScale + (toScale - fromScale) * s) *
        (1 + 0.1 * Math.sin(Math.PI * s)),
    });
  }
  return {
    steps,
    duration: Math.round(Math.min(Math.max(480 + dist * 0.45, 520), 1100)),
    control,
  };
}

// What holds the eye where it is: the developer typing (a field has the focus, and the eye is not on its way to that
// field's place), a mouse button down, a dialog open, the window at rest; a scroll or a key a moment ago; a flight not
// landed.
export interface Calm {
  now: number;
  typing: boolean;
  pressed: boolean;
  dialog: boolean;
  still: boolean;
  quietUntil: number;
  flyingUntil: number;
}

// The quiet after a scroll and after a key out of a field, in ms.
export const AFTER_SCROLL = 700;
export const AFTER_KEY = 350;

// calmWait is how long before the eye may move: 0 now, a time in ms, or Infinity until something changes (the field
// loses the focus, the button comes up, the dialog closes, the window wakes).
export function calmWait(c: Calm): number {
  if (c.typing || c.pressed || c.dialog || c.still) return Infinity;
  return Math.max(0, c.quietUntil - c.now, c.flyingUntil - c.now);
}

// clampTo keeps a point inside a box, a margin from its edges: an eye that scrolled out of sight leaves from the edge.
export function clampTo(
  p: Point,
  box: { width: number; height: number },
  margin: number,
): Point {
  return {
    x: Math.min(Math.max(p.x, margin), box.width - margin),
    y: Math.min(Math.max(p.y, margin), box.height - margin),
  };
}
