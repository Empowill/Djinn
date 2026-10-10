// What workers spent: tokens (input, output, cache read and written) and the cost when their agent gives one.
// Codex and Antigravity give tokens only: the page shows what exists, and says what it cannot add up.
import type { Resources, Usage } from "../gen/ts/plan/v1/plan_pb";
import { type Spent, tokensOf } from "./data/flight";
import { usd } from "./data/format";
import { language, t } from "./i18n";

const compact = new Intl.NumberFormat(language, {
  notation: "compact",
  maximumFractionDigits: 1,
});

// tokens says a count of tokens in a few characters: 12.3k.
export function tokens(count: bigint): string {
  return compact.format(Number(count));
}

function parts(u: {
  inputTokens?: bigint;
  outputTokens?: bigint;
  cacheReadTokens?: bigint;
  cacheWriteTokens?: bigint;
}): string[] {
  const out: string[] = [];
  const add = (
    key:
      "usage.input" | "usage.output" | "usage.cache_read" | "usage.cache_write",
    value?: bigint,
  ) => {
    if (value) out.push(t(key, { count: tokens(value) }));
  };
  add("usage.input", u.inputTokens);
  add("usage.output", u.outputTokens);
  add("usage.cache_read", u.cacheReadTokens);
  add("usage.cache_write", u.cacheWriteTokens);
  return out;
}

// usageDetail is a task's usage in full, one line: for a title, or the task's facts.
export function usageDetail(usage?: Usage): string {
  if (!usage) return "";
  const out = parts(usage);
  if (usage.costUsd > 0) out.push(usd(usage.costUsd));
  return out.join(" · ");
}

// TaskUsage is a task's usage, short: its tokens and its cost; the detail in its title.
export function TaskUsage({ usage }: { usage?: Usage }) {
  const total = tokensOf(usage);
  if (!usage || (total === 0n && !(usage.costUsd > 0))) return null;
  return (
    <span className="task-usage" title={usageDetail(usage)}>
      {total > 0n && t("usage.tokens", { count: tokens(total) })}
      {total > 0n && usage.costUsd > 0 && " · "}
      {usage.costUsd > 0 && usd(usage.costUsd)}
    </span>
  );
}

// SpentLine is what a wish's tasks spent, summed; nothing when they spent nothing.
export function SpentLine({ spent }: { spent: Spent }) {
  if (!spent.tasks) return null;
  const items = parts(spent);
  if (spent.costUsd > 0) items.push(usd(spent.costUsd));
  return (
    <span className="wish-spent">
      <span className="eyebrow">{t("usage.spent")}</span>
      <span>{items.join(" · ")}</span>
      {spent.withoutCost > 0 && (
        <small>{t("usage.without_cost", { count: spent.withoutCost })}</small>
      )}
    </span>
  );
}

// What a worker uses of the machine (Resources): its CPU, its memory, its processes.
const byteUnits = ["byte", "kilobyte", "megabyte", "gigabyte", "terabyte"];

// memory says a size in bytes in a few characters: 512 MB, 1.2 GB.
export function memory(bytes: bigint): string {
  let value = Number(bytes);
  let unit = 0;
  while (value >= 1024 && unit < byteUnits.length - 1) {
    value /= 1024;
    unit++;
  }
  return new Intl.NumberFormat(language, {
    style: "unit",
    unit: byteUnits[unit],
    unitDisplay: "short",
    maximumFractionDigits: value < 10 ? 1 : 0,
  }).format(value);
}

const percent = new Intl.NumberFormat(language, { maximumFractionDigits: 0 });

// resourcesNow is what the worker uses at the last reading: its CPU and memory, short.
export function resourcesNow(r: Resources): string {
  return [
    t("resources.cpu", { percent: percent.format(r.cpuPercent) }),
    memory(r.memoryBytes),
  ].join(" · ");
}

// resourcesDetail is what the worker uses in full, one line: now while it runs, and the peaks.
export function resourcesDetail(r: Resources, running: boolean): string {
  const out: string[] = [];
  if (running && r.readTime)
    out.push(
      t("resources.now", {
        detail: [
          resourcesNow(r),
          t("resources.processes", { count: r.processes }),
        ].join(" · "),
      }),
    );
  if (r.peakMemoryBytes > 0n)
    out.push(
      t("resources.peak", {
        detail: [
          t("resources.cpu", { percent: percent.format(r.peakCpuPercent) }),
          memory(r.peakMemoryBytes),
        ].join(" · "),
      }),
    );
  return out.join(" · ");
}
