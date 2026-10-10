/**
 * Allocation-conscious text kernel for the smoke study's ASCII overlay.
 *
 * The WebGL sampler writes RGBA bytes bottom-up. `AsciiGridKernel.render`
 * converts the red channel into the same eight text layers used by the
 * reference renderer. The first render returns complete layer strings;
 * later renders return dirty row/chunk patches. Patches are based on the
 * returned `baseSequence`, so a consumer that drops a frame can request a
 * complete snapshot by passing its last applied sequence to the next render.
 */

export const ASCII_CHARS = " .,:;i!I+tf#%@MW";
export const ASCII_TONES = [0.14, 0.25, 0.39, 0.54, 0.7, 0.88] as const;
export const ASCII_LAYER_COUNT = ASCII_TONES.length + 2;

// Keep the low-end fade below intact so smoke still arrives softly. The
// brighter response belongs to the body of the plume, where it creates more
// separation in the mid tones and reaches the white highlight layer sooner.
const ASCII_BRIGHTNESS_BASE = 0.22;
const ASCII_BRIGHTNESS_EXPONENT = 0.5;
const ASCII_BRIGHTNESS_RANGE = 0.78;

/** The values used by the supplied smoke reference for the initial surface. */
export const ASCII_REFERENCE = Object.freeze({
  zoom: 10,
  fontSize: 10,
  lineHeight: 11.4,
});

type NumericBuffer = ArrayLike<number>;

function computeAsciiFade(level: number): number {
  let fade = Math.max(0, Math.min(1, (level - 0.003) / 0.027));
  fade = fade * fade * (3 - 2 * fade);
  return fade;
}

export function computeAsciiBrightness(level: number): number {
  return (
    (ASCII_BRIGHTNESS_BASE +
      Math.pow(level, ASCII_BRIGHTNESS_EXPONENT) * ASCII_BRIGHTNESS_RANGE) *
    computeAsciiFade(level)
  );
}

export interface AsciiGridPatch {
  /** Layer in the six tone layers plus the two interaction layers. */
  readonly layer: number;
  /** Top-down row in the HTML text surface. */
  readonly row: number;
  /** Inclusive start column of this patch. */
  readonly start: number;
  /** Exclusive end column of this patch. */
  readonly end: number;
  /** Replacement text for [start, end). */
  readonly text: string;
}

export interface AsciiGridFrame {
  /** Monotonic sequence for a changed snapshot. Unchanged renders keep it. */
  readonly sequence: number;
  /** Sequence from which patches can be applied; full frames supersede it. */
  readonly baseSequence: number;
  readonly width: number;
  readonly height: number;
  /** True when `fullStrings` contains all eight complete layer strings. */
  readonly full: boolean;
  /** True when text output changed, or when a requested full snapshot was emitted. */
  readonly changed: boolean;
  readonly fullStrings?: readonly string[];
  readonly patches: readonly AsciiGridPatch[];
}

export interface AsciiGridRenderOptions {
  /** Emit complete strings even when no cell changed. */
  readonly forceFull?: boolean;
  /** Last sequence the DOM consumer has applied. A gap causes a full frame. */
  readonly appliedSequence?: number;
}

export interface AsciiGridKernel {
  readonly width: number;
  readonly height: number;
  readonly area: number;
  /** Resize invalidates the previous frame and makes the next render full. */
  resize(width: number, height: number): void;
  /**
   * Convert RGBA pixels and optional trail influence into text output.
   * `pixels` is sampled red-channel-only, with rows oriented bottom-up as
   * returned by WebGL readPixels. `trail` is top-down and may be omitted.
   */
  render(
    pixels: NumericBuffer,
    trail: NumericBuffer | undefined,
    now: number,
    options?: AsciiGridRenderOptions,
  ): AsciiGridFrame;
  /** Build a complete snapshot from the current cell state. */
  fullStrings(): readonly string[];
}

const EMPTY_PATCHES: readonly AsciiGridPatch[] = Object.freeze([]);
const CHAR_CODES = new Uint16Array(ASCII_CHARS.length);
for (let index = 0; index < ASCII_CHARS.length; index++)
  CHAR_CODES[index] = ASCII_CHARS.charCodeAt(index);

// These tables are shared by every grid. They keep exponentiation, fade,
// tone lookup and visibility checks out of the per-cell loop while retaining
// the reference renderer's Number arithmetic.
const LEVEL_POWER_052 = new Float64Array(256);
const LEVEL_FADE = new Float64Array(256);
const LEVEL_BRIGHTNESS = new Float64Array(256);
const LEVEL_LOWER = new Uint8Array(256);
const LEVEL_UPPER = new Uint8Array(256);
const LEVEL_BLEND = new Float64Array(256);
const LEVEL_VISIBLE = new Uint8Array(256);

for (let byte = 0; byte < 256; byte++) {
  const level = byte / 255;
  LEVEL_POWER_052[byte] = Math.pow(level, 0.52);

  LEVEL_FADE[byte] = computeAsciiFade(level);

  const brightness = computeAsciiBrightness(level);
  LEVEL_BRIGHTNESS[byte] = brightness;
  LEVEL_VISIBLE[byte] = brightness > 0.035 ? 1 : 0;

  let lower = 0;
  while (lower < ASCII_TONES.length - 1 && ASCII_TONES[lower + 1] < brightness)
    lower++;
  const upper = Math.min(lower + 1, ASCII_TONES.length - 1);
  LEVEL_LOWER[byte] = lower;
  LEVEL_UPPER[byte] = upper;
  LEVEL_BLEND[byte] =
    upper === lower
      ? 0
      : Math.max(
          0,
          Math.min(
            1,
            (brightness - ASCII_TONES[lower]) /
              (ASCII_TONES[upper] - ASCII_TONES[lower]),
          ),
        );
}

function checkedDimension(value: number, name: string): number {
  if (!Number.isInteger(value) || value < 1)
    throw new RangeError(`${name} must be a positive integer.`);
  return value;
}

function buildSegment(
  cells: Uint8Array,
  offset: number,
  length: number,
  scratch: Uint16Array,
): string {
  if (length <= 0) return "";
  let text = "";
  let source = offset;
  let remaining = length;
  // Keep the argument list below the engine's spread limit for a large
  // browser window. Normal smoke rows are only a few hundred characters.
  while (remaining > 0) {
    const count = Math.min(8192, remaining);
    for (let index = 0; index < count; index++)
      scratch[index] = CHAR_CODES[cells[source + index]];
    text += String.fromCharCode(...scratch.subarray(0, count));
    source += count;
    remaining -= count;
  }
  return text;
}

function layerString(
  cells: Uint8Array,
  layer: number,
  width: number,
  height: number,
  scratch: Uint16Array,
): string {
  const layerOffset = layer * width * height;
  let text = "";
  for (let row = 0; row < height; row++) {
    if (row) text += "\n";
    text += buildSegment(cells, layerOffset + row * width, width, scratch);
  }
  return text;
}

function allLayerStrings(
  cells: Uint8Array,
  width: number,
  height: number,
  scratch: Uint16Array,
): readonly string[] {
  const result = new Array<string>(ASCII_LAYER_COUNT);
  for (let layer = 0; layer < ASCII_LAYER_COUNT; layer++)
    result[layer] = layerString(cells, layer, width, height, scratch);
  return result;
}

/** Create the reusable kernel for one measured text grid. */
export function createAsciiGrid(
  initialWidth: number,
  initialHeight: number,
  options: { readonly chunkWidth?: number } = {},
): AsciiGridKernel {
  let width = checkedDimension(initialWidth, "width");
  let height = checkedDimension(initialHeight, "height");
  let area = width * height;
  let chunkWidth = options.chunkWidth ?? width;
  if (!Number.isInteger(chunkWidth) || chunkWidth < 1)
    throw new RangeError("chunkWidth must be a positive integer.");

  let cells = new Uint8Array(ASCII_LAYER_COUNT * area);
  let noise = new Float64Array(area);
  let hashUnit = new Float64Array(area);
  let hoverBucket = new Uint8Array(area);
  let hoverParity = new Uint8Array(area);
  let dirtyStart = new Int32Array(ASCII_LAYER_COUNT * height);
  let dirtyEnd = new Int32Array(ASCII_LAYER_COUNT * height);
  let scratch = new Uint16Array(width);
  let sequence = 0;
  let hasFrame = false;
  let resetPending = true;

  const fillCellTables = () => {
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        const slot = y * width + x;
        const noiseIndex = (x * 37 + y * 91) % 17;
        noise[slot] = (noiseIndex / 17) * 0.065 - 0.0325;

        let cellHash =
          Math.imul(x + 1, 374761393) ^ Math.imul(y + 1, 668265263);
        cellHash = Math.imul(cellHash ^ (cellHash >>> 13), 1274126177);
        hashUnit[slot] = (cellHash >>> 0) / 4294967296;

        hoverBucket[slot] = (x * 73 + y * 37) % 101;
        hoverParity[slot] = (x * 17 + y * 13) % 2;
      }
    }
  };

  const clearDirty = () => {
    dirtyStart.fill(width);
    dirtyEnd.fill(-1);
  };

  fillCellTables();
  clearDirty();

  const resize = (nextWidth: number, nextHeight: number) => {
    nextWidth = checkedDimension(nextWidth, "width");
    nextHeight = checkedDimension(nextHeight, "height");
    if (nextWidth === width && nextHeight === height) return;

    width = nextWidth;
    height = nextHeight;
    area = width * height;
    cells = new Uint8Array(ASCII_LAYER_COUNT * area);
    noise = new Float64Array(area);
    hashUnit = new Float64Array(area);
    hoverBucket = new Uint8Array(area);
    hoverParity = new Uint8Array(area);
    dirtyStart = new Int32Array(ASCII_LAYER_COUNT * height);
    dirtyEnd = new Int32Array(ASCII_LAYER_COUNT * height);
    scratch = new Uint16Array(width);
    fillCellTables();
    clearDirty();
    hasFrame = false;
    resetPending = true;
  };

  const fullStrings = (): readonly string[] =>
    allLayerStrings(cells, width, height, scratch);

  const render = (
    pixels: NumericBuffer,
    trail: NumericBuffer | undefined,
    now: number,
    renderOptions: AsciiGridRenderOptions = {},
  ): AsciiGridFrame => {
    const expectedPixels = area * 4;
    if (pixels.length < expectedPixels)
      throw new RangeError(
        `pixels must contain at least ${expectedPixels} RGBA bytes.`,
      );
    if (trail && trail.length < area)
      throw new RangeError(`trail must contain at least ${area} cells.`);

    const previousSequence = sequence;
    const appliedSequence = renderOptions.appliedSequence;
    const lostPatches =
      appliedSequence !== undefined && appliedSequence !== previousSequence;
    const emitFull =
      renderOptions.forceFull === true || !hasFrame || lostPatches;
    clearDirty();

    const tick = Math.floor((Number.isFinite(now) ? now : 0) / 160);
    let changed = false;
    for (let y = 0; y < height; y++) {
      const pixelRow = (height - 1 - y) * width;
      const cellRow = y * width;
      for (let x = 0; x < width; x++) {
        const slot = cellRow + x;
        const level = pixels[(pixelRow + x) * 4] as number;
        const byte = level & 255;
        let index = Math.floor(
          (LEVEL_POWER_052[byte] + noise[slot]) * ASCII_CHARS.length,
        );
        if (index < 0) index = 0;
        else if (index >= ASCII_CHARS.length) index = ASCII_CHARS.length - 1;

        const lower = LEVEL_LOWER[byte];
        const upper = LEVEL_UPPER[byte];
        let tone = hashUnit[slot] < LEVEL_BLEND[byte] ? upper : lower;
        const influence = trail ? trail[slot] : 0;
        if (index && hoverBucket[slot] / 101 < influence * 0.95) {
          const shift = (tick + hoverParity[slot]) % 2 ? 2 : -2;
          index = Math.max(1, Math.min(ASCII_CHARS.length - 1, index + shift));
          tone = ASCII_TONES.length + (influence > 0.5 ? 0 : 1);
        }

        const visible = LEVEL_VISIBLE[byte] !== 0;
        for (let layer = 0; layer < ASCII_LAYER_COUNT; layer++) {
          const next = visible && layer === tone ? index : 0;
          const cellIndex = layer * area + slot;
          if (cells[cellIndex] === next) continue;
          cells[cellIndex] = next;
          changed = true;
          const dirtyIndex = layer * height + y;
          if (x < dirtyStart[dirtyIndex]) dirtyStart[dirtyIndex] = x;
          if (x > dirtyEnd[dirtyIndex]) dirtyEnd[dirtyIndex] = x;
        }
      }
    }

    if (!emitFull && !changed) {
      hasFrame = true;
      return {
        sequence,
        baseSequence: sequence,
        width,
        height,
        full: false,
        changed: false,
        patches: EMPTY_PATCHES,
      };
    }

    sequence++;
    hasFrame = true;
    if (emitFull) {
      const baseSequence = resetPending
        ? 0
        : (appliedSequence ?? previousSequence);
      resetPending = false;
      return {
        sequence,
        baseSequence,
        width,
        height,
        full: true,
        changed: true,
        fullStrings: fullStrings(),
        patches: EMPTY_PATCHES,
      };
    }

    const patches: AsciiGridPatch[] = [];
    for (let layer = 0; layer < ASCII_LAYER_COUNT; layer++) {
      const layerOffset = layer * height;
      for (let row = 0; row < height; row++) {
        const dirtyIndex = layerOffset + row;
        const start = dirtyStart[dirtyIndex];
        const end = dirtyEnd[dirtyIndex];
        if (end < start) continue;
        const firstChunk = Math.floor(start / chunkWidth) * chunkWidth;
        const lastChunk = Math.floor(end / chunkWidth) * chunkWidth;
        for (
          let chunkStart = firstChunk;
          chunkStart <= lastChunk;
          chunkStart += chunkWidth
        ) {
          const chunkEnd = Math.min(width, chunkStart + chunkWidth);
          patches.push({
            layer,
            row,
            start: chunkStart,
            end: chunkEnd,
            text: buildSegment(
              cells,
              layer * area + row * width + chunkStart,
              chunkEnd - chunkStart,
              scratch,
            ),
          });
        }
      }
    }
    return {
      sequence,
      baseSequence: previousSequence,
      width,
      height,
      full: false,
      changed: true,
      patches,
    };
  };

  return {
    get width() {
      return width;
    },
    get height() {
      return height;
    },
    get area() {
      return area;
    },
    resize,
    render,
    fullStrings,
  };
}
