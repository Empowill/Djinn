import type { MissionStep, StepType, WorkflowTemplate } from "./types";
import { STEP_TYPES } from "./workflow";

const FORMAT = "djinn-workflow" as const;
const VERSION = 1 as const;
const MAX_TEXT = 100_000;
const STEP_FIELDS = [
  "id",
  "type",
  "title",
  "objective",
  "status",
  "exitCriteria",
  "expectedArtifacts",
  "skills",
] as const;
const TEMPLATE_FIELDS = ["id", "title", "steps"] as const;

const fail = (field: string): never => {
  throw new Error(`Le workflow portable est invalide (${field}).`);
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);

const text = (value: unknown, field: string, max = MAX_TEXT): string => {
  if (
    typeof value !== "string" ||
    value.length > max ||
    value.includes("\0")
  )
    return fail(field);
  return value;
};

const identifier = (value: unknown, field: string): string => {
  const result = text(value, field, 256);
  if (!result.trim()) return fail(field);
  return result;
};

const strings = (value: unknown, field: string, maxItem = 4_000): string[] => {
  if (!Array.isArray(value) || value.length > 100) return fail(field);
  return value.map((item) => text(item, field, maxItem));
};

const keysAreExactly = (
  value: Record<string, unknown>,
  allowed: readonly string[],
  field: string,
) => {
  const expected = new Set(allowed);
  if (
    Object.keys(value).some((key) => !expected.has(key)) ||
    allowed.some((key) => !Object.prototype.hasOwnProperty.call(value, key))
  )
    return fail(field);
};

function validatePendingStep(value: unknown, field: string): MissionStep {
  if (!isRecord(value)) return fail(field);
  keysAreExactly(value, STEP_FIELDS, field);
  const type = value.type as StepType;
  if (type === "discussion" || !STEP_TYPES.includes(type))
    return fail(`${field}.type`);
  if (value.status !== "pending") return fail(`${field}.status`);
  return {
    id: identifier(value.id, `${field}.id`),
    type,
    title: text(value.title, `${field}.title`, 1_000),
    objective: text(value.objective, `${field}.objective`),
    status: "pending",
    exitCriteria: strings(value.exitCriteria, `${field}.exitCriteria`),
    expectedArtifacts: strings(
      value.expectedArtifacts,
      `${field}.expectedArtifacts`,
    ),
    skills: strings(value.skills, `${field}.skills`),
  };
}

function validatePendingTemplate(value: unknown): WorkflowTemplate {
  if (!isRecord(value)) return fail("workflow");
  keysAreExactly(value, TEMPLATE_FIELDS, "workflow");
  if (!Array.isArray(value.steps) || value.steps.length === 0 || value.steps.length > 100)
    return fail("workflow.steps");
  const steps = value.steps.map((step, index) =>
    validatePendingStep(step, `workflow.steps.${index}`),
  );
  if (new Set(steps.map((step) => step.id)).size !== steps.length)
    return fail("workflow.steps.ids");
  return {
    id: identifier(value.id, "workflow.id"),
    title: text(value.title, "workflow.title", 1_000),
    steps,
  };
}

/** Serialize only the portable, pending workflow contract. */
export function exportWorkflow(template: WorkflowTemplate): string {
  const workflow = validatePendingTemplate(template);
  return `${JSON.stringify({ format: FORMAT, version: VERSION, workflow }, null, 2)}\n`;
}

function parseInput(value: unknown): unknown {
  if (typeof value !== "string") return value;
  if (value.length > 12_000_000 || !value.trim()) return fail("text");
  try {
    return JSON.parse(value);
  } catch {
    return fail("json");
  }
}

/**
 * Import a portable workflow into a new project. Every identity is regenerated
 * so an imported template cannot collide with its source project or carry
 * references to its old timeline.
 */
export function importWorkflow(value: unknown): WorkflowTemplate {
  const parsed = parseInput(value);
  if (!isRecord(parsed)) return fail("document");
  keysAreExactly(parsed, ["format", "version", "workflow"], "document");
  if (parsed.format !== FORMAT || parsed.version !== VERSION)
    return fail("document.header");
  const source = validatePendingTemplate(parsed.workflow);
  return {
    id: crypto.randomUUID(),
    title: source.title,
    steps: source.steps.map((step) => ({
      ...step,
      id: crypto.randomUUID(),
      status: "pending",
    })),
  };
}
