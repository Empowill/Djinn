// Price table parsed from internal/usage/prices.yaml and model cost estimation.
import { Provider } from "../../gen/ts/plan/v1/plan_pb";
import rawPrices from "../../internal/usage/prices.yaml?raw";
import { defaultModel, resolveModelAlias } from "./format";

export interface ModelPrice {
  label?: string;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  thinking: number;
  source: string;
  date: string;
}

export interface PriceTable {
  date: string;
  currency: string;
  models: Record<string, ModelPrice>;
  order?: string[];
}

export function providerForModel(model: string): Provider {
  const m = model.trim().toLowerCase();
  if (
    m.startsWith("claude-") ||
    m.includes("sonnet") ||
    m.includes("opus") ||
    m.includes("haiku") ||
    m.includes("fable")
  ) {
    return Provider.CLAUDE;
  }
  if (m.startsWith("gpt-")) {
    return Provider.CODEX;
  }
  if (m.startsWith("gemini-") || m.includes("argon") || m.includes("flash")) {
    return Provider.ANTIGRAVITY;
  }
  return Provider.UNSPECIFIED;
}

// parsePriceTable parses the simple 2-level YAML structure of prices.yaml.
export function parsePriceTable(yamlText: string): PriceTable {
  const lines = yamlText.split("\n");
  let date = "";
  let currency = "USD";
  const models: Record<string, ModelPrice> = {};
  const order: string[] = [];

  let currentModel: string | null = null;
  let inModels = false;

  for (const rawLine of lines) {
    const line = rawLine.replace(/#.*$/, "");
    if (!line.trim()) continue;

    const indent = line.search(/\S/);
    const trimmed = line.trim();

    if (indent === 0) {
      if (trimmed.startsWith("date:")) {
        date = trimmed.replace(/^date:\s*["']?([^"']+)["']?/, "$1").trim();
      } else if (trimmed.startsWith("currency:")) {
        currency = trimmed
          .replace(/^currency:\s*["']?([^"']+)["']?/, "$1")
          .trim();
      } else if (trimmed.startsWith("models:")) {
        inModels = true;
      }
    } else if (inModels && indent === 2) {
      if (trimmed.endsWith(":")) {
        currentModel = trimmed.slice(0, -1).trim();
        order.push(currentModel);
        models[currentModel] = {
          label: "",
          input: 0,
          output: 0,
          cacheRead: 0,
          cacheWrite: 0,
          thinking: 0,
          source: "",
          date: "",
        };
      }
    } else if (
      inModels &&
      indent >= 4 &&
      currentModel &&
      models[currentModel]
    ) {
      const colonIdx = trimmed.indexOf(":");
      if (colonIdx !== -1) {
        const key = trimmed.slice(0, colonIdx).trim();
        const val = trimmed
          .slice(colonIdx + 1)
          .trim()
          .replace(/^["']|["']$/g, "");
        const entry = models[currentModel];
        switch (key) {
          case "label":
            entry.label = val;
            break;
          case "input":
            entry.input = parseFloat(val) || 0;
            break;
          case "output":
            entry.output = parseFloat(val) || 0;
            break;
          case "cache_read":
            entry.cacheRead = parseFloat(val) || 0;
            break;
          case "cache_write":
            entry.cacheWrite = parseFloat(val) || 0;
            break;
          case "thinking":
            entry.thinking = parseFloat(val) || 0;
            break;
          case "source":
            entry.source = val;
            break;
          case "date":
            entry.date = val;
            break;
        }
      }
    }
  }

  return { date, currency, models, order };
}

let customTable: PriceTable | null = null;
let cachedTable: PriceTable | null = null;

// setPriceTable overrides the price table used for cost estimation (useful for tests and fixtures).
export function setPriceTable(table: PriceTable | null): void {
  customTable = table;
}

export function getDefaultPriceTable(): PriceTable {
  if (!cachedTable) {
    cachedTable = parsePriceTable(rawPrices);
  }
  return cachedTable;
}

export function getPriceTable(): PriceTable {
  if (customTable) return customTable;
  return getDefaultPriceTable();
}

export function catalogFromPriceTable(table = getDefaultPriceTable()): {
  id: string;
  label: string;
  provider: Provider;
}[] {
  const keys = table.order ?? Object.keys(table.models);
  return keys.map((id) => ({
    id,
    label: table.models[id]?.label || id,
    provider: providerForModel(id),
  }));
}

// resolveModelToTable resolves a model string and provider to a canonical key in the price table.
export function resolveModelToTable(
  model?: string,
  provider?: Provider,
): string | undefined {
  const table = getPriceTable();
  let m = model?.trim() || "";
  if (!m) {
    m = defaultModel(provider);
  }
  if (!m) return undefined;

  m = m.toLowerCase();
  // Strip vendor prefixes
  if (m.includes("/")) {
    m = m.slice(m.indexOf("/") + 1);
  }

  // Exact match
  if (table.models[m]) return m;

  // Registered alias lookup
  const resolvedAlias = resolveModelAlias(m);
  if (resolvedAlias) {
    const clean = resolvedAlias.toLowerCase().replace(/^[a-z0-9-._]+\//, "");
    if (table.models[clean]) return clean;
  }

  // Common aliases
  switch (m) {
    case "sonnet":
    case "claude-sonnet":
      return "claude-sonnet-5-5";
    case "opus":
    case "claude-opus":
      return "claude-opus-5-5";
    case "haiku":
    case "claude-haiku":
      return "claude-haiku-5-5";
    case "flash":
    case "gemini-flash":
      return "gemini-3.8-flash-high";
    case "codex":
      return "gpt-5.3-codex";
  }

  // Prefix match against known models (e.g. "claude-sonnet-4-5-20250929" -> "claude-sonnet-4-5")
  for (const key of Object.keys(table.models)) {
    if (m.startsWith(key)) {
      return key;
    }
  }

  return undefined;
}

export interface CostEstimate {
  costUsd: number;
  hasPrice: boolean;
  tableDate: string;
  model: string;
}

// estimateCost computes estimated USD cost from tokens using the price table.
export function estimateCost(
  usage?: {
    inputTokens?: bigint;
    outputTokens?: bigint;
    cacheReadTokens?: bigint;
    cacheWriteTokens?: bigint;
    thinkingTokens?: bigint;
  },
  model?: string,
  provider?: Provider,
): CostEstimate {
  const table = getPriceTable();
  const resolved = resolveModelToTable(model, provider);
  if (!resolved || !table.models[resolved]) {
    return {
      costUsd: 0,
      hasPrice: false,
      tableDate: table.date,
      model: model || "",
    };
  }

  const p = table.models[resolved];
  const inTok = Number(usage?.inputTokens ?? 0n);
  const outTok = Number(usage?.outputTokens ?? 0n);
  const thinkTok = Number(usage?.thinkingTokens ?? 0n);
  const readTok = Number(usage?.cacheReadTokens ?? 0n);
  const writeTok = Number(usage?.cacheWriteTokens ?? 0n);

  let regularOut = outTok;
  if (thinkTok > 0 && regularOut >= thinkTok) {
    regularOut = regularOut - thinkTok;
  }

  const cost =
    (inTok * p.input +
      regularOut * p.output +
      thinkTok * p.thinking +
      readTok * p.cacheRead +
      writeTok * p.cacheWrite) /
    1_000_000.0;

  return {
    costUsd: cost,
    hasPrice: true,
    tableDate: table.date,
    model: resolved,
  };
}
