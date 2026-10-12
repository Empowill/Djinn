// What workers spent: tokens (input, output, cache read and written, thinking) and the price, one total.
// Codex and Antigravity report tokens only; the page estimates their cost from prices.yaml, and keeps Claude's reported
// cost: the price is the reported cost where there is one, else the estimate; its hover tells the two apart.
import { useSyncExternalStore } from "react";
import {
  Provider,
  type Resources,
  type Task,
  type Usage,
} from "../gen/ts/plan/v1/plan_pb";
import { type ProviderSpent, type Spent, tokensOf } from "./data/flight";
import { usd } from "./data/format";
import { estimateCost } from "./data/prices";
import { language, t } from "./i18n";

export type UsageUnit = "money" | "tokens";

let currentUsageUnit: UsageUnit = "money";
const unitListeners = new Set<() => void>();

export function getUsageUnit(): UsageUnit {
  return currentUsageUnit;
}

export function setUsageUnit(unit: UsageUnit) {
  if (currentUsageUnit !== unit) {
    currentUsageUnit = unit;
    unitListeners.forEach((fn) => fn());
  }
}

export function useUsageUnit(): [UsageUnit, (unit: UsageUnit) => void] {
  const unit = useSyncExternalStore(
    (onStoreChange) => {
      unitListeners.add(onStoreChange);
      return () => {
        unitListeners.delete(onStoreChange);
      };
    },
    () => currentUsageUnit,
    () => currentUsageUnit,
  );
  return [unit, setUsageUnit];
}

const compact = new Intl.NumberFormat(language, {
  notation: "compact",
  maximumFractionDigits: 1,
});

// tokens says a count of tokens in a few characters: 12.3k.
export function tokens(count: bigint): string {
  return compact.format(Number(count));
}

export function providerName(p?: Provider): string {
  switch (p) {
    case Provider.CLAUDE:
    case Provider.UNSPECIFIED:
      return "claude";
    case Provider.CODEX:
      return "codex";
    case Provider.ANTIGRAVITY:
      return "antigravity";
    case Provider.FAKE:
      return "fake";
    case Provider.WATCH:
      return "watch";
    default:
      return "unknown";
  }
}

function parts(u: {
  inputTokens?: bigint;
  outputTokens?: bigint;
  cacheReadTokens?: bigint;
  cacheWriteTokens?: bigint;
  thinkingTokens?: bigint;
}): string[] {
  const out: string[] = [];
  const add = (
    key:
      | "usage.input"
      | "usage.output"
      | "usage.thinking"
      | "usage.cache_read"
      | "usage.cache_write",
    value?: bigint,
  ) => {
    if (value) out.push(t(key, { count: tokens(value) }));
  };
  add("usage.input", u.inputTokens);
  add("usage.output", u.outputTokens);
  add("usage.thinking", u.thinkingTokens);
  add("usage.cache_read", u.cacheReadTokens);
  add("usage.cache_write", u.cacheWriteTokens);
  return out;
}

// Cost is what a price is made of: what the agents reported, and the estimate of what they did not (tokens at the
// table's prices of date), with how many tasks the table priced and how many it could not.
interface Cost {
  reported: number;
  estimated: number;
  priced: number;
  unpriced: number;
  date: string;
  // An Antigravity task is in the estimate: it is at API prices, not the bill.
  antigravity: boolean;
}

// taskCost is a task's cost: the reported cost when its agent gave one, else its tokens at the table's prices.
function taskCost(u: Usage, model?: string, provider?: Provider): Cost {
  const est = estimateCost(u, model, provider);
  const reported = u.costUsd > 0;
  const priced = !reported && est.hasPrice;
  return {
    reported: reported ? u.costUsd : 0,
    estimated: priced ? est.costUsd : 0,
    priced: priced ? 1 : 0,
    unpriced: !reported && !est.hasPrice ? 1 : 0,
    date: est.tableDate,
    antigravity: priced && provider === Provider.ANTIGRAVITY,
  };
}

function spentCost(
  s: Omit<Spent, "byProvider" | "tableDate">,
  date: string,
  antigravity: boolean,
): Cost {
  return {
    reported: s.costUsd,
    estimated: s.estimatedCostUsd,
    priced: s.priced,
    unpriced: s.withoutPrice,
    date,
    antigravity,
  };
}

// price is a cost in one figure, the total; "≈" when an estimate is in it; nothing when nothing has a price.
function price(c: Cost): string {
  if (!(c.reported > 0) && !c.priced) return "";
  const cost = usd(c.reported + c.estimated);
  return c.priced ? t("usage.about", { cost }) : cost;
}

// costSplit is what a price is made of, one line each: for its hover.
function costSplit(c: Cost, one: boolean): string[] {
  const out: string[] = [];
  if (c.reported > 0) out.push(t("usage.reported", { cost: usd(c.reported) }));
  if (c.priced) {
    let estimated = t("usage.estimated", {
      cost: usd(c.estimated),
      date: c.date,
    });
    if (c.antigravity) estimated += ` (${t("usage.antigravity_estimate")})`;
    out.push(estimated);
  }
  if (c.unpriced)
    out.push(
      one
        ? t("usage.no_price")
        : t("usage.without_price", { count: c.unpriced }),
    );
  return out;
}

// costTitle is the hover of a task's price: what it is made of.
export function costTitle(
  usage?: Usage,
  model?: string,
  provider?: Provider,
): string {
  if (!usage) return "";
  return costSplit(taskCost(usage, model, provider), true).join("\n");
}

// usageDetail is a task's usage in full, one line: its tokens and its price; costTitle tells the price's parts.
export function usageDetail(
  usage?: Usage,
  model?: string,
  provider?: Provider,
): string {
  if (!usage) return "";
  const total = tokensOf(usage);
  if (total === 0n && !(usage.costUsd > 0)) return "";
  const out = parts(usage);
  const p = price(taskCost(usage, model, provider));
  if (p) out.push(p);
  return out.join(" · ");
}

// TaskUsage is a task's usage, short: its tokens and its price; the detail and the price's parts in its title.
export function TaskUsage({
  usage,
  model,
  provider,
  task,
}: {
  usage?: Usage;
  model?: string;
  provider?: Provider;
  task?: Task;
}) {
  const [unit] = useUsageUnit();
  const u = task?.usage ?? usage;
  const m = task?.model ?? model;
  const p = task?.provider ?? provider;
  const total = tokensOf(u);
  if (!u || (total === 0n && !(u.costUsd > 0))) return null;

  if (unit === "tokens") {
    return (
      <span className="task-usage" title={parts(u).join(" · ")}>
        {t("usage.tokens", { count: tokens(total) })}
      </span>
    );
  }

  const cost = taskCost(u, m, p);
  const items: string[] = [];
  if (total > 0n) {
    items.push(t("usage.tokens", { count: tokens(total) }));
  }
  const figure = price(cost);
  if (figure) items.push(figure);
  const detail = parts(u).join(" · ");

  return (
    <span
      className="task-usage"
      title={[detail, ...costSplit(cost, true)].filter(Boolean).join("\n")}
    >
      {items.join(" · ")}
    </span>
  );
}

// SpentLine is what a wish's tasks spent, summed; nothing when they spent nothing.
export function SpentLine({
  spent,
  showSwitch = true,
}: {
  spent: Spent;
  showSwitch?: boolean;
}) {
  const [unit, setUnit] = useUsageUnit();
  if (!spent.tasks) return null;
  const total = tokensOf(spent);

  if (unit === "tokens") {
    const items = parts(spent);
    return (
      <span className="wish-spent">
        <span className="eyebrow">{t("usage.spent")}</span>
        {total > 0n && (
          <span className="spent-total">
            {t("usage.tokens", { count: tokens(total) })}
          </span>
        )}
        {items.length > 0 && <span>({items.join(" · ")})</span>}
        {spent.byProvider.length > 1 && (
          <span className="spent-providers">
            {spent.byProvider
              .filter((p) => tokensOf(p) > 0n)
              .map((p) => `${providerName(p.provider)}: ${tokens(tokensOf(p))}`)
              .join(" · ")}
          </span>
        )}
        {showSwitch && (
          <button
            type="button"
            className="usage-unit-switch"
            title={t("usage.switch_unit")}
            onClick={(e) => {
              e.stopPropagation();
              e.preventDefault();
              setUnit("money");
            }}
          >
            {t("usage.unit_money")}
          </button>
        )}
      </span>
    );
  }

  const items = parts(spent);
  const antigravity = (p: ProviderSpent) =>
    p.provider === Provider.ANTIGRAVITY && p.priced > 0;
  const cost = spentCost(
    spent,
    spent.tableDate,
    spent.byProvider.some(antigravity),
  );
  const figure = price(cost);
  // The hover tells the price's parts, then each provider's price when there are several.
  const split = costSplit(cost, false);
  if (spent.byProvider.length > 1)
    for (const p of spent.byProvider) {
      const each = price(spentCost(p, spent.tableDate, antigravity(p)));
      if (each) split.push(`${providerName(p.provider)}: ${each}`);
    }

  return (
    <span className="wish-spent">
      <span className="eyebrow">{t("usage.spent")}</span>
      {figure && (
        <span className="spent-price" title={split.join("\n")}>
          {figure}
        </span>
      )}
      {items.length > 0 && <span>{items.join(" · ")}</span>}
      {/* Without any price, the reason shows: the hover has nowhere to be. */}
      {!figure && spent.withoutPrice > 0 && (
        <small>{t("usage.without_price", { count: spent.withoutPrice })}</small>
      )}
      {showSwitch && (
        <button
          type="button"
          className="usage-unit-switch"
          title={t("usage.switch_unit")}
          onClick={(e) => {
            e.stopPropagation();
            e.preventDefault();
            setUnit("tokens");
          }}
        >
          {t("usage.unit_tokens")}
        </button>
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
