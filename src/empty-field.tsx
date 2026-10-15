// EmptyField is what an empty workspace panel shows: a small monospace data field, computed and moving at a steady
// tempo, above one line that says what is missing and, when there is one, the action that fills it. The field is
// decoration only (aria-hidden): the line is what a reader hears. One family, a pattern per panel: a queue of bars for
// Tasks, a scanned timeline for Activity, a page typed line by line for Documents.
import { type ReactNode, useEffect, useRef } from "react";

import "./empty-field.css";

export type EmptyFieldPattern = "queue" | "timeline" | "page";

export const FIELD_COLUMNS = 42;
export const FIELD_ROWS = 8;
// The time, in seconds, of the frame shown before the first animation frame: on the server and under reduced motion.
export const FIELD_STILL_TIME = 4.2;
// About eight frames a second: a steady tempo, and a cheap panel.
const FRAME_MS = 125;

// A cell's tone: 0 at rest (dim), 1 live (what moves), 2 hot (the newest mark, the scan line, the cursor).
type Tone = 0 | 1 | 2;
type Grid = { char: string; tone: Tone }[][];

// hash is a small integer hash: the same inputs give the same field, frame after frame.
function hash(...values: number[]) {
  let h = 0x811c9dc5;
  for (const value of values) {
    h = Math.imul(h ^ (value | 0), 0x01000193);
    h ^= h >>> 15;
    h = Math.imul(h, 0x2c1b3c6d);
    h ^= h >>> 12;
  }
  return h >>> 0;
}

const hex = (value: number, digits: number) =>
  (value >>> 0).toString(16).padStart(digits, "0").slice(-digits);

function blank(rows = FIELD_ROWS, columns = FIELD_COLUMNS): Grid {
  return Array.from({ length: rows }, () =>
    Array.from({ length: columns }, () => ({
      char: " ",
      tone: 0 as Tone,
    })),
  );
}

function put(grid: Grid, row: number, col: number, text: string, tone: Tone) {
  [...text].forEach((char, i) => {
    const cell = grid[row]?.[col + i];
    if (cell) {
      cell.char = char;
      cell.tone = tone;
    }
  });
}

// queue: fourteen slots of barcode bars fill one by one, then leave first in, first out; the newest is the brightest.
const SLOTS = 14;
const BARS = ["| ", "||", " |", "|:", ":|", ": "];
const QUEUE_STEP = 0.45;
function queue(time: number): Grid {
  const grid = blank();
  const step = Math.floor(time / QUEUE_STEP);
  const cycle = Math.floor(step / (SLOTS * 2));
  const phase = step % (SLOTS * 2);
  const first = phase < SLOTS ? 0 : phase - SLOTS + 1;
  const last = phase < SLOTS ? phase : SLOTS - 1;
  for (let slot = 0; slot < SLOTS; slot++) {
    const col = slot * 3;
    const filled = slot >= first && slot <= last && first <= last;
    if (filled) {
      const bars = BARS[hash(slot, cycle) % BARS.length];
      const height = 3 + (hash(cycle, slot, 7) % 4);
      const tone: Tone = slot === last && phase < SLOTS ? 2 : 1;
      for (let row = 6 - height; row < 6; row++)
        put(grid, row, col, bars, tone);
    }
    put(grid, 6, col, "--", 0);
    put(grid, 7, col, String(slot).padStart(2, "0"), filled ? 1 : 0);
  }
  return grid;
}

// timeline: a log of timestamps and hex words scrolls up one entry at a time; a scan line sweeps across it.
const ENTRY_STEP = 0.6;
function stamp(entry: number) {
  const ms = Math.round(entry * 417.3);
  const s = Math.floor(ms / 1000);
  return `${String(Math.floor(s / 60) % 60).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}.${String(ms % 1000).padStart(3, "0")}`;
}
function timeline(time: number): Grid {
  const grid = blank();
  const newest = Math.floor(time / ENTRY_STEP) + 40;
  for (let row = 0; row < FIELD_ROWS; row++) {
    const entry = newest - (FIELD_ROWS - 1 - row);
    const tone: Tone = row === FIELD_ROWS - 1 ? 1 : 0;
    const words = Array.from({ length: 8 }, (_, i) =>
      hex(hash(entry, i), 2),
    ).join(" ");
    const meter = "|".repeat(1 + (hash(entry, 9) % 6)).padEnd(6, " ");
    put(grid, row, 0, `${stamp(entry)}  ${words}  ${meter}`, tone);
  }
  const scan = Math.floor((time * 14) % FIELD_COLUMNS);
  for (let row = 0; row < FIELD_ROWS; row++) {
    const cell = grid[row][scan];
    cell.char = cell.char === " " ? ":" : cell.char;
    cell.tone = 2;
  }
  return grid;
}

// page: a page-shaped grid with its addresses; lines of hex glyphs are typed into it, held, then cleared.
const PAGE_LEFT = 6;
const PAGE_WIDTH = 28;
const PAGE_LINES = [22, 24, 16, 23, 11, 19];
const TYPE_RATE = 16;
const PAGE_TOTAL = PAGE_LINES.reduce((sum, length) => sum + length, 0);
const PAGE_CYCLE = PAGE_TOTAL / TYPE_RATE + 1.6;
function page(time: number): Grid {
  const grid = blank();
  const cycle = Math.floor(time / PAGE_CYCLE);
  let typed = Math.floor((time % PAGE_CYCLE) * TYPE_RATE);
  const right = PAGE_LEFT + PAGE_WIDTH - 1;
  put(grid, 0, PAGE_LEFT, "+" + "-".repeat(PAGE_WIDTH - 2) + "+", 0);
  put(
    grid,
    FIELD_ROWS - 1,
    PAGE_LEFT,
    "+" + "-".repeat(PAGE_WIDTH - 2) + "+",
    0,
  );
  PAGE_LINES.forEach((length, line) => {
    const row = line + 1;
    put(grid, row, 0, hex(line * 0x14, 4), 0);
    put(grid, row, PAGE_LEFT, "|", 0);
    put(grid, row, right, "|", 0);
    for (let at = 0; at < PAGE_WIDTH - 4; at += 2)
      put(grid, row, PAGE_LEFT + 2 + at, ".", 0);
    const shown = Math.max(0, Math.min(length, typed));
    for (let at = 0; at < shown; at++)
      put(grid, row, PAGE_LEFT + 2 + at, hex(hash(cycle, line, at), 1), 1);
    if (typed >= 0 && typed < length)
      put(grid, row, PAGE_LEFT + 2 + shown, "_", 2);
    if (typed >= length)
      put(grid, row, right + 2, hex(hash(cycle, line), 2), 0);
    typed -= length;
  });
  return grid;
}

const PATTERNS: Record<EmptyFieldPattern, (time: number) => Grid> = {
  queue,
  timeline,
  page,
};

type Layers = [string[], string[], string[]];

// layersOf splits a grid into one layer per tone, blank where another layer draws.
function layersOf(grid: Grid): Layers {
  const layer = (tone: Tone) =>
    grid.map((row) =>
      row.map((cell) => (cell.tone === tone ? cell.char : " ")).join(""),
    );
  return [layer(0), layer(1), layer(2)];
}

// fieldFrame draws a pattern at a time, in seconds: one layer per tone, each FIELD_ROWS lines of FIELD_COLUMNS
// characters, blank where another layer draws.
export function fieldFrame(pattern: EmptyFieldPattern, time: number): Layers {
  return layersOf(PATTERNS[pattern](time));
}

// What a wish is doing, as its pulse shows it: still when nothing runs, a sparse pulse while the lead alone runs, a
// stream as dense as the workers are many, a steady blink while something waits for the developer.
export type PulseState = "still" | "lead" | "workers" | "waiting";

export const PULSE_COLUMNS = 72;
export const PULSE_ROWS = 11;

// pulseStateOf reads the state from the window's signals: what waits for the developer first, then the workers, then
// the lead; a wish paused or granted, with nothing waiting, is still.
export function pulseStateOf({
  active,
  leadRunning,
  workers,
  waiting,
}: {
  active: boolean;
  leadRunning: boolean;
  workers: number;
  waiting: number;
}): PulseState {
  if (waiting > 0) return "waiting";
  if (!active) return "still";
  if (workers > 0) return "workers";
  if (leadRunning) return "lead";
  return "still";
}

// The matrix behind every state: binary digits, sparse, re-drawn at each tick.
function matrix(grid: Grid, tick: number, density: number) {
  grid.forEach((row, r) =>
    row.forEach((cell, c) => {
      if (c % 2) return;
      const h = hash(r, c, tick);
      if (h % 1000 < density * 1000) {
        cell.char = h & 0x10000 ? "1" : "0";
        cell.tone = 0;
      }
    }),
  );
}

function pulse(state: PulseState, workers: number, time: number): Grid {
  const grid = blank(PULSE_ROWS, PULSE_COLUMNS);
  const middle = Math.floor(PULSE_ROWS / 2);
  if (state === "still") {
    matrix(grid, 0, 0.16);
    return grid;
  }
  if (state === "lead") {
    // A sparse field, re-drawn slowly, and one slow scan across the middle row.
    matrix(grid, Math.floor(time / 1.6), 0.07);
    const at = Math.floor((time * 6) % (PULSE_COLUMNS + 12)) - 6;
    for (let c = 0; c < PULSE_COLUMNS; c++) {
      const distance = at - c;
      if (distance < 0 || distance > 6) continue;
      put(grid, middle, c, distance === 0 ? "|" : ".", distance === 0 ? 2 : 1);
    }
    return grid;
  }
  if (state === "workers") {
    // One lane of hex data per worker (up to the rows there are), streaming left, faster as there are more.
    const lanes = Math.min(PULSE_ROWS, Math.max(1, workers));
    matrix(grid, Math.floor(time / 0.5), 0.05 + 0.02 * lanes);
    const speed = 6 + lanes * 2;
    for (let lane = 0; lane < lanes; lane++) {
      const row = Math.floor(((lane + 0.5) * PULSE_ROWS) / lanes) % PULSE_ROWS;
      put(grid, row, 0, " ".repeat(PULSE_COLUMNS), 0);
      const shift = Math.floor(time * speed + lane * 17);
      for (let c = 0; c < PULSE_COLUMNS; c++) {
        const source = c + shift;
        const h = hash(lane, Math.floor(source / 3));
        // Words of two hex digits, with gaps: a stream, not a wall.
        if (source % 3 === 2 || h % 5 === 0) continue;
        const char = hex(h >>> (source % 3 ? 4 : 0), 1);
        put(grid, row, c, char, c >= PULSE_COLUMNS - 4 ? 2 : 1);
      }
    }
    return grid;
  }
  // waiting: the field holds still; a crosshair through its middle blinks at a steady second.
  matrix(grid, 0, 0.1);
  const on = Math.floor(time) % 2 === 0;
  const centre = Math.floor(PULSE_COLUMNS / 2);
  for (let c = 0; c < PULSE_COLUMNS; c += 2)
    put(grid, middle, c, on ? "-" : " ", on ? 2 : 0);
  for (let r = 0; r < PULSE_ROWS; r++)
    put(grid, r, centre, on ? "|" : " ", on ? 2 : 0);
  put(grid, middle, centre, "+", 2);
  return grid;
}

// pulseFrame draws a state at a time, in seconds: one layer per tone, each PULSE_ROWS lines of PULSE_COLUMNS
// characters. Still holds the same frame at every time.
export function pulseFrame(
  state: PulseState,
  workers: number,
  time: number,
): Layers {
  return layersOf(pulse(state, workers, time));
}

const reducedMotion = () =>
  !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;

// One layer per tone, in the order the frames give them.
const LAYERS = [
  "empty-field-layer is-rest",
  "empty-field-layer is-live",
  "empty-field-layer is-hot",
] as const;

// FieldLayers shows a frame, then moves it at a steady tempo while it is on screen, the window shown and motion
// welcome; else it holds the frame at stillTime. frozen holds it always.
function FieldLayers({
  frame,
  stillTime,
  frozen = false,
  motionKey,
}: {
  frame: (time: number) => Layers;
  stillTime: number;
  frozen?: boolean;
  // Changes when frame draws something else: the animation starts again on it.
  motionKey: string;
}) {
  const root = useRef<HTMLDivElement>(null);
  const layers = useRef<(HTMLPreElement | null)[]>([]);
  const draw = useRef(frame);
  draw.current = frame;
  useEffect(() => {
    const element = root.current;
    if (!element) return;
    const paint = (time: number) =>
      draw.current(time).forEach((lines, i) => {
        const output = layers.current[i];
        if (output) output.textContent = lines.join("\n");
      });
    const motion = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    let onScreen = true;
    let next = 0;
    let last = 0;
    const start = performance.now() - stillTime * 1000;
    const tick = (now: number) => {
      next = 0;
      if (now - last >= FRAME_MS) {
        last = now;
        paint((now - start) / 1000);
      }
      schedule();
    };
    const schedule = () => {
      const still = frozen || reducedMotion();
      const running =
        onScreen && document.visibilityState !== "hidden" && !still;
      if (running && !next) next = requestAnimationFrame(tick);
      if (!running && next) {
        cancelAnimationFrame(next);
        next = 0;
      }
      if (still) paint(stillTime);
    };
    const observer =
      typeof IntersectionObserver === "undefined"
        ? undefined
        : new IntersectionObserver((entries) => {
            onScreen = entries.some((entry) => entry.isIntersecting);
            schedule();
          });
    observer?.observe(element);
    document.addEventListener("visibilitychange", schedule);
    motion?.addEventListener?.("change", schedule);
    schedule();
    return () => {
      if (next) cancelAnimationFrame(next);
      observer?.disconnect();
      document.removeEventListener("visibilitychange", schedule);
      motion?.removeEventListener?.("change", schedule);
    };
  }, [motionKey, stillTime, frozen]);
  return (
    <div ref={root} className="empty-field-grid" aria-hidden="true">
      {frame(stillTime).map((lines, i) => (
        <pre
          key={LAYERS[i]}
          ref={(output) => {
            layers.current[i] = output;
          }}
          className={LAYERS[i]}
        >
          {lines.join("\n")}
        </pre>
      ))}
    </div>
  );
}

export function EmptyField({
  pattern,
  message,
  children,
}: {
  pattern: EmptyFieldPattern;
  // The one line that says what is missing.
  message: ReactNode;
  // The actions that fill the panel, under the line.
  children?: ReactNode;
}) {
  return (
    <div className="empty-field" data-empty-field={pattern}>
      <FieldLayers
        frame={(time) => fieldFrame(pattern, time)}
        stillTime={FIELD_STILL_TIME}
        motionKey={pattern}
      />
      <p className="empty-field-message">{message}</p>
      {children && <div className="empty-field-actions">{children}</div>}
    </div>
  );
}

// WishPulse is the workspace's stage while no panel is open: the wish's pulse, and one quiet line that says it.
export function WishPulse({
  state,
  workers,
  caption,
}: {
  state: PulseState;
  workers: number;
  caption: string;
}) {
  return (
    <div className="wish-pulse" data-wish-pulse={state}>
      <FieldLayers
        frame={(time) => pulseFrame(state, workers, time)}
        stillTime={FIELD_STILL_TIME}
        frozen={state === "still"}
        motionKey={`${state}:${workers}`}
      />
      <p className="wish-pulse-caption">{caption}</p>
    </div>
  );
}
