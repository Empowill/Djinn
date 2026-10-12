// What workers spent: tokens (input, output, cache read and written, thinking) and the cost (reported and estimated).
// Codex and Antigravity report tokens only; the page estimates cost from prices.yaml, and preserves Claude's reported cost.
import { Footprints } from "lucide-react";
import { useSyncExternalStore } from "react";
import {
  Provider,
  type Resources,
  type Task,
  TaskStatus,
  type Usage,
} from "../gen/ts/plan/v1/plan_pb";
import { type Spent, tokensOf } from "./data/flight";
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

// usageDetail is a task's usage in full, one line: for a title, or the task's facts.
export function usageDetail(
  usage?: Usage,
  model?: string,
  provider?: Provider,
): string {
  if (!usage) return "";
  const total = tokensOf(usage);
  if (total === 0n && !(usage.costUsd > 0)) return "";
  const out = parts(usage);
  if (usage.costUsd > 0) {
    out.push(t("usage.reported", { cost: usd(usage.costUsd) }));
  }
  const est = estimateCost(usage, model, provider);
  if (est.hasPrice) {
    let estText = t("usage.estimated", {
      cost: usd(est.costUsd),
      date: est.tableDate,
    });
    if (provider === Provider.ANTIGRAVITY) {
      estText += ` (${t("usage.antigravity_estimate")})`;
    }
    out.push(estText);
  } else {
    out.push(t("usage.no_price"));
  }
  return out.join(" · ");
}

// TaskUsage is a task's usage, short: its tokens and its cost (reported and estimated); the detail in its title.
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

  const est = estimateCost(u, m, p);
  const items: string[] = [];
  if (total > 0n) {
    items.push(t("usage.tokens", { count: tokens(total) }));
  }
  if (est.hasPrice) {
    if (u.costUsd > 0) {
      items.push(t("usage.reported", { cost: usd(u.costUsd) }));
    }
    items.push(
      t("usage.estimated", { cost: usd(est.costUsd), date: est.tableDate }),
    );
  }

  return (
    <span className="task-usage" title={usageDetail(u, m, p)}>
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
  const costItems: string[] = [];
  if (spent.costUsd > 0) {
    costItems.push(t("usage.reported", { cost: usd(spent.costUsd) }));
  }
  if (spent.estimatedCostUsd > 0 || spent.tasks > spent.withoutPrice) {
    costItems.push(
      t("usage.estimated", {
        cost: usd(spent.estimatedCostUsd),
        date: spent.tableDate,
      }),
    );
  }

  const allItems = [...items, ...costItems];

  return (
    <span className="wish-spent">
      <span className="eyebrow">{t("usage.spent")}</span>
      {allItems.length > 0 && <span>{allItems.join(" · ")}</span>}
      {spent.withoutPrice > 0 && (
        <small>{t("usage.without_price", { count: spent.withoutPrice })}</small>
      )}
      {spent.byProvider.length > 1 && (
        <span className="spent-providers">
          {spent.byProvider
            .filter((p) => p.tasks > 0)
            .map((p) => {
              const pItems: string[] = [];
              if (p.costUsd > 0) {
                pItems.push(t("usage.reported", { cost: usd(p.costUsd) }));
              }
              if (p.tasks > p.withoutPrice) {
                pItems.push(
                  t("usage.estimated", {
                    cost: usd(p.estimatedCostUsd),
                    date: spent.tableDate,
                  }),
                );
              }
              if (p.withoutPrice > 0) {
                pItems.push(
                  t("usage.without_price", { count: p.withoutPrice }),
                );
              }
              return `${providerName(p.provider)}: ${pItems.join(", ")}`;
            })
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

// TaskTurns is how many model turns the task's worker took against the limit past which Djinn stops a run: its
// tone warns past 80% of it. The hover says what it counts, and the total over its runs when it ran more than once.
export function TaskTurns({ task }: { task: Task }) {
  const turns = task.turns;
  if (!turns || turns.limit <= 0) return null;
  const warn = turns.run * 5 > turns.limit * 4;
  const title = [
    t(
      task.status === TaskStatus.RUNNING
        ? "task.turns_run"
        : "task.turns_last_run",
      { run: turns.run, limit: turns.limit },
    ),
  ];
  if (turns.total > turns.run)
    title.push(t("task.turns_total", { total: turns.total }));
  return (
    <span
      className={warn ? "task-turns warning" : "task-turns"}
      title={title.join(" · ")}
    >
      <Footprints size={12} aria-label={t("task.turns")} />
      {`${turns.run}/${turns.limit}`}
    </span>
  );
}
