/**
 * Deterministic sizing for the smoke study's native HTML text surface.
 *
 * The text grid is measured in CSS pixels. Device-pixel-ratio only affects the
 * optional background canvas, so changing a display's DPR cannot silently
 * increase the amount of HTML work.
 */

export const SMOKE_BASE_FONT_SIZE = 10;
export const SMOKE_BASE_LINE_HEIGHT = 11.4;
export const SMOKE_MAX_CELLS = 18_000;
export const SMOKE_MAX_COLUMNS = 240;
export const SMOKE_MAX_ROWS = 120;
export const SMOKE_MAX_CANVAS_PIXELS = 1_500_000;
export const SMOKE_MAX_CANVAS_DIMENSION = 2_048;
/** Duration used to settle the camera projection after a format resize. */
export const SMOKE_CAMERA_TRANSITION_MS = 180;

export interface WishSmokeLayout {
  /** CSS font size, rounded up to a half-pixel when the grid needs to shrink. */
  readonly fontSize: number;
  /** Width of one glyph in CSS pixels at `fontSize`. */
  readonly charWidth: number;
  /** CSS line height at `fontSize`. */
  readonly lineHeight: number;
  readonly gridWidth: number;
  readonly gridHeight: number;
  /** Covered CSS width and height as fractions of the requested surface. */
  readonly coverage: readonly [number, number];
  /** CSS gap above the bottom-aligned HTML rows. */
  readonly offsetY: number;
  /** Backing dimensions for the optional canvas, in device pixels. */
  readonly canvasWidth: number;
  readonly canvasHeight: number;
  /** Effective conservative scale represented by the backing dimensions. */
  readonly backgroundPixelRatio: number;
}

interface GridSize {
  readonly width: number;
  readonly height: number;
  readonly cells: number;
}

function positiveFinite(value: number, name: string): number {
  if (!Number.isFinite(value) || value <= 0)
    throw new RangeError(`${name} must be a positive finite number.`);
  return value;
}

function gridAtFont(
  surfaceWidth: number,
  surfaceHeight: number,
  baseCharWidth: number,
  fontSize: number,
): GridSize {
  const scale = fontSize / SMOKE_BASE_FONT_SIZE;
  const width = Math.max(
    1,
    // Keep the rightmost glyph inside the native overflow box. The HTML
    // surface is right-aligned below, so any unused fractional width remains
    // a stable gap on the left instead of clipping a character.
    Math.floor(surfaceWidth / (baseCharWidth * scale)),
  );
  const height = Math.max(
    1,
    // A partial final line is clipped by the native pre's overflow box. Keep
    // whole lines inside the surface and align that gap above the rows.
    Math.floor(surfaceHeight / (SMOKE_BASE_LINE_HEIGHT * scale)),
  );
  return { width, height, cells: width * height };
}

/**
 * Return the horizontal camera scale used by the reference smoke projection.
 * Keeping this value separate from the CSS resolution lets callers interpolate
 * a format change without changing the fluid simulation or zoom.
 */
export function computeWishSmokeCameraScale(
  surfaceWidth: number,
  surfaceHeight: number,
): number {
  positiveFinite(surfaceWidth, "surfaceWidth");
  positiveFinite(surfaceHeight, "surfaceHeight");
  const aspect = surfaceWidth / surfaceHeight;
  const fit = Math.min(1.65, aspect * 1.35);
  return aspect / fit;
}

/** Smoothly interpolate one camera scale without changing the fluid state. */
export function interpolateWishSmokeCameraScale(
  start: number,
  target: number,
  elapsedMs: number,
): number {
  if (!Number.isFinite(start) || !Number.isFinite(target)) return target;
  const progress = Math.max(
    0,
    Math.min(1, elapsedMs / SMOKE_CAMERA_TRANSITION_MS),
  );
  const eased = progress * progress * (3 - 2 * progress);
  return start + (target - start) * eased;
}

function fitsGrid(size: GridSize): boolean {
  return (
    size.width <= SMOKE_MAX_COLUMNS &&
    size.height <= SMOKE_MAX_ROWS &&
    size.cells <= SMOKE_MAX_CELLS
  );
}

function roundFontUp(fontSize: number): number {
  // The small epsilon keeps an exact 10px solution from becoming 10.5px due
  // to the last bit of a binary search result.
  return Math.max(10, Math.ceil((fontSize - 1e-9) * 2) / 2);
}

function canvasAtRatio(
  surfaceWidth: number,
  surfaceHeight: number,
  ratio: number,
): { width: number; height: number; pixels: number } {
  const width = Math.max(1, Math.ceil(surfaceWidth * ratio));
  const height = Math.max(1, Math.ceil(surfaceHeight * ratio));
  return { width, height, pixels: width * height };
}

function fitsCanvas(
  surfaceWidth: number,
  surfaceHeight: number,
  ratio: number,
): boolean {
  const canvas = canvasAtRatio(surfaceWidth, surfaceHeight, ratio);
  return (
    canvas.width <= SMOKE_MAX_CANVAS_DIMENSION &&
    canvas.height <= SMOKE_MAX_CANVAS_DIMENSION &&
    canvas.pixels <= SMOKE_MAX_CANVAS_PIXELS
  );
}

function boundedCanvas(
  surfaceWidth: number,
  surfaceHeight: number,
  requestedDpr: number,
): { width: number; height: number; ratio: number } {
  let ratio = requestedDpr;
  if (!fitsCanvas(surfaceWidth, surfaceHeight, ratio)) {
    // Find the largest usable ratio. Keeping this separate from grid sizing
    // makes the HTML output DPR-independent while preserving a sharp canvas
    // whenever the requested backing size is within its explicit budget.
    let low = 0;
    let high = requestedDpr;
    for (let iteration = 0; iteration < 56; iteration++) {
      const middle = low + (high - low) / 2;
      if (fitsCanvas(surfaceWidth, surfaceHeight, middle)) low = middle;
      else high = middle;
    }
    ratio = low;
  }

  // Binary search converges on a boundary that can differ by one rounded
  // device pixel. Move inward a few times if that last rounding bit is over
  // the pixel budget.
  for (
    let attempt = 0;
    attempt < 8 && !fitsCanvas(surfaceWidth, surfaceHeight, ratio);
    attempt++
  )
    ratio *= 0.999999;

  const canvas = canvasAtRatio(surfaceWidth, surfaceHeight, ratio);
  return {
    width: canvas.width,
    height: canvas.height,
    ratio,
  };
}

/**
 * Compute one stable HTML/canvas layout for a CSS surface.
 *
 * `baseCharWidthAt10` is the measured width of one reference glyph at 10px.
 * Font size starts at 10px and grows only as much as necessary to satisfy all
 * three HTML budgets. The result is pure and contains no viewport history or
 * quality heuristic.
 */
export function computeWishSmokeLayout(
  surfaceWidth: number,
  surfaceHeight: number,
  baseCharWidthAt10: number,
  devicePixelRatio: number,
): WishSmokeLayout {
  positiveFinite(surfaceWidth, "surfaceWidth");
  positiveFinite(surfaceHeight, "surfaceHeight");
  positiveFinite(baseCharWidthAt10, "baseCharWidthAt10");
  positiveFinite(devicePixelRatio, "devicePixelRatio");

  const fitsAtReference = gridAtFont(
    surfaceWidth,
    surfaceHeight,
    baseCharWidthAt10,
    SMOKE_BASE_FONT_SIZE,
  );
  let fontSize = SMOKE_BASE_FONT_SIZE;

  if (!fitsGrid(fitsAtReference)) {
    let low = SMOKE_BASE_FONT_SIZE;
    let high = low * 2;
    while (
      !fitsGrid(
        gridAtFont(surfaceWidth, surfaceHeight, baseCharWidthAt10, high),
      )
    ) {
      high *= 2;
      if (!Number.isFinite(high)) {
        high = Number.MAX_VALUE;
        break;
      }
    }

    // The grid is monotonic in font size: every count can only stay the same
    // or decrease as the glyphs grow. Binary search therefore finds the
    // smallest continuous solution before half-pixel quantization.
    for (let iteration = 0; iteration < 56; iteration++) {
      const middle = low + (high - low) / 2;
      const size = gridAtFont(
        surfaceWidth,
        surfaceHeight,
        baseCharWidthAt10,
        middle,
      );
      if (fitsGrid(size)) high = middle;
      else low = middle;
    }

    fontSize = roundFontUp(high);
    while (
      !fitsGrid(
        gridAtFont(surfaceWidth, surfaceHeight, baseCharWidthAt10, fontSize),
      )
    )
      fontSize += 0.5;
  }

  const scale = fontSize / SMOKE_BASE_FONT_SIZE;
  const grid = gridAtFont(
    surfaceWidth,
    surfaceHeight,
    baseCharWidthAt10,
    fontSize,
  );
  const canvas = boundedCanvas(surfaceWidth, surfaceHeight, devicePixelRatio);
  const textHeight = grid.height * SMOKE_BASE_LINE_HEIGHT * scale;

  return {
    fontSize,
    charWidth: baseCharWidthAt10 * scale,
    lineHeight: SMOKE_BASE_LINE_HEIGHT * scale,
    gridWidth: grid.width,
    gridHeight: grid.height,
    coverage: [
      Math.min(
        1,
        (grid.width * baseCharWidthAt10 * scale) / surfaceWidth,
      ),
      Math.min(1, textHeight / surfaceHeight),
    ],
    offsetY: Math.max(0, surfaceHeight - textHeight),
    canvasWidth: canvas.width,
    canvasHeight: canvas.height,
    backgroundPixelRatio: canvas.ratio,
  };
}
