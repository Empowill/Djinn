// Reporter measuring the e2e suite's wall clock and comparing it with e2e/baseline.json.
// Warns if the wall clock exceeds the baseline by >25% without failing the check.
// Update baseline with UPDATE_BASELINE=1 or by editing e2e/baseline.json directly.
import fs from "node:fs";
import path from "node:path";
import type {
  FullConfig,
  FullResult,
  Reporter,
  Suite,
} from "@playwright/test/reporter";

const WARN_MARGIN = 0.25; // 25% margin
const BASELINE_PATH = path.resolve(__dirname, "baseline.json");

function parseDurationSeconds(v: unknown): number {
  if (typeof v === "number") return v;
  if (typeof v === "string") {
    const s = v.trim();
    if (s.endsWith("ms")) return parseFloat(s) / 1000;
    if (s.endsWith("s")) return parseFloat(s);
    if (s.endsWith("m")) return parseFloat(s) * 60;
    return parseFloat(s);
  }
  return 0;
}

function formatDuration(sec: number): string {
  const rounded = Math.round(sec * 10) / 10;
  if (rounded % 1 === 0) return `${rounded}s`;
  return `${rounded.toFixed(1)}s`;
}

export default class E2EReporter implements Reporter {
  private startTime = 0;

  onBegin(_config: FullConfig, _suite: Suite): void {
    this.startTime = Date.now();
  }

  onEnd(_result: FullResult): void {
    const wallClockSec = (Date.now() - this.startTime) / 1000;
    let baselineSec = 0;

    if (fs.existsSync(BASELINE_PATH)) {
      try {
        const raw = JSON.parse(fs.readFileSync(BASELINE_PATH, "utf-8"));
        baselineSec = parseDurationSeconds(raw.wall_clock);
      } catch {
        // malformed baseline is ignored
      }
    }

    if (process.env.UPDATE_BASELINE) {
      const data = { wall_clock: formatDuration(Math.round(wallClockSec)) };
      fs.writeFileSync(BASELINE_PATH, JSON.stringify(data, null, 2) + "\n");
      console.log(
        `Updated e2e baseline at ${BASELINE_PATH}: ${data.wall_clock}`,
      );
      return;
    }

    const formattedElapsed = formatDuration(wallClockSec);
    if (baselineSec > 0) {
      console.log(
        `E2E total: ${formattedElapsed} wall clock (baseline: ${formatDuration(baselineSec)})`,
      );
      const limit = baselineSec * (1 + WARN_MARGIN);
      if (wallClockSec > limit) {
        const pct = ((wallClockSec - baselineSec) / baselineSec) * 100;
        console.warn(
          `\nWARNING: e2e suite wall clock (${formattedElapsed}) exceeds baseline (${formatDuration(baselineSec)}) by ${pct.toFixed(1)}% (>${WARN_MARGIN * 100}% margin).\n` +
            `Update baseline with UPDATE_BASELINE=1 or edit ${BASELINE_PATH}.\n`,
        );
      }
    } else {
      console.log(`E2E total: ${formattedElapsed} wall clock`);
    }
  }
}
