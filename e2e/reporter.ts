// Reporter measuring the e2e suite's wall clock and comparing it with e2e/baseline.json,
// and listing tests that passed on retry (Playwright `flaky` status) in its summary and $GITHUB_STEP_SUMMARY.
// Warns if the wall clock exceeds the baseline by >25% without failing the check.
// Update baseline with UPDATE_BASELINE=1 or by editing e2e/baseline.json directly.
import fs from "node:fs";
import path from "node:path";
import type {
  FullConfig,
  FullResult,
  Reporter,
  Suite,
  TestCase,
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

export function formatFlakyTest(
  test: Pick<TestCase, "title"> & {
    location?: { file: string; line?: number; column?: number };
    titlePath?: () => string[];
  },
  rootDir: string = __dirname,
): string {
  const file = test.location?.file
    ? path.relative(rootDir, test.location.file).replaceAll("\\", "/")
    : "";
  const loc = file
    ? test.location?.line
      ? `${file}:${test.location.line}`
      : file
    : "";
  const rawTitles = test.titlePath
    ? test.titlePath().filter(Boolean)
    : [test.title];
  const titles = rawTitles.filter(
    (part, idx) =>
      !(idx === 0 && part === "chromium") &&
      !(file && (part === file || part === path.basename(file))),
  );
  const title = (titles.length ? titles : [test.title]).join(" › ");
  return loc ? `${loc} › ${title}` : title;
}

export function flakyTests(
  suite?: Pick<Suite, "allTests">,
  rootDir: string = __dirname,
): string[] {
  if (!suite) return [];
  return suite
    .allTests()
    .filter((test) => test.outcome() === "flaky")
    .map((test) => formatFlakyTest(test, rootDir));
}

export default class E2EReporter implements Reporter {
  private startTime = 0;
  private suite?: Suite;
  private rootDir = __dirname;

  onBegin(config: FullConfig, suite: Suite): void {
    this.startTime = Date.now();
    this.suite = suite;
    if (config?.rootDir) {
      this.rootDir = config.rootDir;
    }
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

    const flaky = flakyTests(this.suite, this.rootDir);
    if (flaky.length > 0) {
      console.log(
        `Flaky e2e tests (${flaky.length} passed on retry):\n` +
          flaky.map((name) => `  flaky: ${name}`).join("\n"),
      );
      const summaryPath = process.env.GITHUB_STEP_SUMMARY;
      if (summaryPath) {
        const md =
          `### Flaky e2e tests (${flaky.length} passed on retry)\n\n` +
          flaky.map((name) => `- \`${name}\``).join("\n") +
          "\n";
        fs.appendFileSync(summaryPath, md);
      }
    }
  }
}
