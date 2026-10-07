import type {
  MissionStep,
  Phase,
  Project,
  ProjectSnapshot,
  SourceOfTruth,
  StepType,
  StepValidation,
  Task,
  WorkflowAmendment,
  WorkflowAmendmentStep,
  WorkflowProposal,
  WorkflowProposalStep,
} from "./types";

export const MAX_SUB_AGENTS = 16;
export const STEP_TYPES: StepType[] = [
  "discussion",
  "exploration",
  "reflection",
  "specification",
  "prototype",
  "implementation",
  "review",
  "delivery",
];
const STATUSES = [
  "pending",
  "running",
  "awaiting_human",
  "completed",
  "blocked",
  "paused",
  "error",
];
const VALIDATIONS: StepValidation[] = ["human", "automatic"];
const fail = (field: string): never => {
  throw new Error(`Le format de la mission est invalide (${field}).`);
};
const object = (v: unknown, field: string): Record<string, any> => {
  if (!v || typeof v !== "object" || Array.isArray(v)) return fail(field);
  return v as Record<string, any>;
};
const text = (v: unknown, field: string, max = 100000): string => {
  if (typeof v !== "string" || v.length > max || v.includes("\0"))
    return fail(field);
  return v;
};
const identifier = (v: unknown, field: string): string => {
  const s = text(v, field, 256);
  if (!s.trim()) return fail(field);
  return s;
};
const timestamp = (v: unknown, field: string): string => {
  const s = text(v, field, 100);
  if (!Number.isFinite(Date.parse(s))) return fail(field);
  return s;
};
const strings = (v: unknown, field: string): string[] => {
  if (!Array.isArray(v) || v.length > 100) return fail(field);
  return v.map((item) => text(item, field, 4000));
};
const DISCUSSION_TYPES = STEP_TYPES.filter(
  (type): type is Exclude<StepType, "discussion" | "review" | "delivery"> =>
    type !== "discussion" && type !== "review" && type !== "delivery",
);
export const INITIAL_DISCUSSION_TYPES = DISCUSSION_TYPES;
export const WORKFLOW_PROPOSAL_TYPES = STEP_TYPES.filter(
  (type): type is Exclude<StepType, "discussion"> => type !== "discussion",
);
export function validateRelativeLocation(value: unknown): string {
  const s = text(value, "project.locations", 4096);
  if (
    !s ||
    /^(?:[\\/]|[A-Za-z]:)/.test(s) ||
    s.includes("\n") ||
    s.includes("\r") ||
    s
      .replaceAll("\\", "/")
      .split("/")
      .some((p) => p === ".." || p === "." || !p)
  )
    return fail("project.locations");
  return s;
}
function validateSourceOfTruth(value: unknown, index: number): SourceOfTruth {
  const source = object(value, `project.sourcesOfTruth.${index}`);
  const title = text(
    source.title,
    `project.sourcesOfTruth.${index}.title`,
    1000,
  );
  if (!title.trim()) return fail(`project.sourcesOfTruth.${index}.title`);
  return {
    id: identifier(source.id, `project.sourcesOfTruth.${index}.id`),
    title,
    path: validateRelativeLocation(source.path),
    description: text(
      source.description,
      `project.sourcesOfTruth.${index}.description`,
      100000,
    ),
  };
}
export function validateProjectPreferences(
  value: unknown,
): NonNullable<Project["preferences"]> {
  const preferences = object(value, "project.preferences");
  const result: NonNullable<Project["preferences"]> = {};
  if (preferences.provider !== undefined) {
    if (!(
      "codex" === preferences.provider || "claude" === preferences.provider
    ))
      return fail("project.preferences.provider");
    result.provider = preferences.provider;
  }
  if (preferences.model !== undefined)
    result.model = text(preferences.model, "project.preferences.model", 256);
  if (preferences.concurrency !== undefined) {
    if (
      typeof preferences.concurrency !== "number" ||
      !Number.isInteger(preferences.concurrency) ||
      preferences.concurrency < 1 ||
      preferences.concurrency > MAX_SUB_AGENTS
    )
      return fail("project.preferences.concurrency");
    result.concurrency = preferences.concurrency;
  }
  return result;
}
export function validateStep(value: unknown, restored = false): MissionStep {
  const s = object(value, "step");
  if (!STEP_TYPES.includes(s.type) || !STATUSES.includes(s.status))
    return fail("step.type/status");
  const result: MissionStep = {
    id: identifier(s.id, "step.id"),
    type: s.type,
    title: text(s.title, "step.title", 1000),
    objective: text(s.objective, "step.objective"),
    status: restored && s.status === "running" ? "paused" : s.status,
    exitCriteria: strings(s.exitCriteria, "step.exitCriteria"),
    expectedArtifacts: strings(s.expectedArtifacts, "step.expectedArtifacts"),
    skills: strings(s.skills, "step.skills"),
  };
  if (s.validation !== undefined) {
    if (!VALIDATIONS.includes(s.validation)) return fail("step.validation");
    if (
      s.validation === "automatic" &&
      (s.type === "review" || s.type === "delivery")
    )
      return fail("step.validation");
    result.validation = s.validation;
  }
  for (const key of ["startedAt", "completedAt", "approvedAt"] as const)
    if (s[key] !== undefined) result[key] = timestamp(s[key], `step.${key}`);
  if (s.approvedBy !== undefined) {
    if (s.approvedBy !== "human") return fail("step.approvedBy");
    result.approvedBy = "human";
  }
  if (s.summary !== undefined) result.summary = text(s.summary, "step.summary");
  if (s.needsRevalidation !== undefined) {
    if (typeof s.needsRevalidation !== "boolean")
      return fail("step.needsRevalidation");
    result.needsRevalidation = s.needsRevalidation;
  }
  if (
    result.status === "pending" &&
    (result.startedAt || result.approvedAt || result.completedAt)
  )
    return fail("step.pending");
  if (result.approvedAt && result.approvedBy !== "human")
    return fail("step.approval");
  const automatic = result.validation === "automatic";
  const humanOnly =
    result.type === "review" || result.type === "delivery" || !automatic;
  if (automatic && (result.approvedAt || result.approvedBy))
    return fail("step.automaticApproval");
  if (result.status === "completed") {
    if (humanOnly && (!result.approvedAt || result.approvedBy !== "human"))
      return fail("step.completed");
    if (!humanOnly && !result.completedAt) return fail("step.completedAt");
    if (!humanOnly && (result.approvedAt || result.approvedBy))
      return fail("step.automaticApproval");
  }
  return result;
}
export function validateSteps(value: unknown, restored = false): MissionStep[] {
  if (!Array.isArray(value) || !value.length || value.length > 100)
    return fail("steps");
  const steps = value.map((v) => validateStep(v, restored));
  if (new Set(steps.map((s) => s.id)).size !== steps.length)
    return fail("steps.ids");
  // A linear workflow has at most one unfinished started step. Future steps remain pending.
  let unfinished = false;
  for (const s of steps) {
    if (
      unfinished &&
      s.status !== "pending" &&
      !(s.status === "completed" && s.needsRevalidation)
    )
      return fail("steps.progression");
    if (s.status !== "completed") unfinished = true;
  }
  return steps;
}

/**
 * Validate the compact timeline emitted during a fresh mission's
 * qualification pass. The provider is only allowed to describe stages here;
 * the orchestrator gives those stages identities and statuses when applying
 * the proposal.
 */
export function validateWorkflowProposal(
  value: unknown,
  requireStepId = false,
): WorkflowProposal {
  const proposal = object(value, "workflowProposal");
  if (
    !Array.isArray(proposal.steps) ||
    proposal.steps.length < 1 ||
    proposal.steps.length > 8
  )
    return fail("workflowProposal.steps");
  const steps: WorkflowProposalStep[] = proposal.steps.map(
    (value: unknown, index: number) => {
      const step = object(value, `workflowProposal.steps.${index}`);
      if (!WORKFLOW_PROPOSAL_TYPES.includes(step.type))
        return fail(`workflowProposal.steps.${index}.type`);
      const title = text(
        step.title,
        `workflowProposal.steps.${index}.title`,
        1000,
      );
      const objective = text(
        step.objective,
        `workflowProposal.steps.${index}.objective`,
        100000,
      );
      if (!title.trim() || !objective.trim())
        return fail(`workflowProposal.steps.${index}.content`);
      return {
        type: step.type,
        title,
        objective,
        exitCriteria:
          step.exitCriteria === undefined
            ? []
            : strings(
                step.exitCriteria,
                `workflowProposal.steps.${index}.exitCriteria`,
              ),
        expectedArtifacts:
          step.expectedArtifacts === undefined
            ? []
            : strings(
                step.expectedArtifacts,
                `workflowProposal.steps.${index}.expectedArtifacts`,
              ),
        skills:
          step.skills === undefined
            ? []
            : strings(step.skills, `workflowProposal.steps.${index}.skills`),
        ...(step.validation === undefined
          ? {}
          : VALIDATIONS.includes(step.validation) &&
              !(step.validation === "automatic" &&
                (step.type === "review" || step.type === "delivery"))
            ? { validation: step.validation as StepValidation }
            : fail(`workflowProposal.steps.${index}.validation`)),
      };
    },
  );
  const reason = text(proposal.reason, "workflowProposal.reason", 100000);
  if (!reason.trim()) return fail("workflowProposal.reason");
  const result: WorkflowProposal = { steps, reason };
  if (proposal.stepId !== undefined || requireStepId) {
    result.stepId = identifier(proposal.stepId, "workflowProposal.stepId");
  }
  return result;
}

/** Validate a lead's pending suffix amendment while it is still attached to a run. */
export function validateWorkflowAmendment(
  value: unknown,
  requireStepId = false,
): WorkflowAmendment & { stepId?: string; runId?: string } {
  const amendment = object(value, "workflowAmendment");
  if (
    !Array.isArray(amendment.steps) ||
    amendment.steps.length < 1 ||
    amendment.steps.length > 8
  )
    return fail("workflowAmendment.steps");
  const normalized = validateWorkflowProposal(
    { steps: amendment.steps, reason: amendment.reason },
    false,
  );
  const result: WorkflowAmendment & { stepId?: string; runId?: string } = {
    steps: normalized.steps,
    reason: normalized.reason,
  };
  const stepId = amendment.stepId ?? amendment.currentStepId;
  if (stepId !== undefined || requireStepId) {
    result.stepId = identifier(stepId, "workflowAmendment.stepId");
    result.currentStepId = result.stepId;
  }
  if (amendment.runId !== undefined)
    result.runId = identifier(amendment.runId, "workflowAmendment.runId");
  return result;
}

export function validateProject(value: unknown): Project {
  const p = object(value, "project");
  const directory = text(p.directory, "project.directory", 4096);
  if (
    directory &&
    (!/^(?:\/|[A-Za-z]:[\\/]|\\\\)/.test(directory) || /[\r\n]/.test(directory))
  )
    return fail("project.directory");
  const rawLocations = object(p.locations, "project.locations");
  const locations: Record<string, string> = {};
  if (Object.keys(rawLocations).length > 100) return fail("project.locations");
  for (const [key, value] of Object.entries(rawLocations)) {
    identifier(key, "project.location.key");
    if (["__proto__", "constructor"].includes(key))
      return fail("project.location.key");
    locations[key] = validateRelativeLocation(value);
  }
  if (!Array.isArray(p.workflows) || p.workflows.length > 100)
    return fail("project.workflows");
  const workflows = p.workflows.map((v: unknown) => {
    const w = object(v, "workflow");
    const steps = validateSteps(w.steps);
    if (steps.some((s) => s.status !== "pending"))
      return fail("workflow.steps");
    return {
      id: identifier(w.id, "workflow.id"),
      title: text(w.title, "workflow.title", 1000),
      steps,
    };
  });
  if (
    new Set(workflows.map((w: { id: string }) => w.id)).size !==
    workflows.length
  )
    return fail("workflow.ids");
  if (p.workflowPolicy === "enforced" && workflows.length === 0)
    return fail("project.workflowPolicy");
  const result: Project = {
    id: identifier(p.id, "project.id"),
    name: text(p.name, "project.name", 1000),
    directory,
    conventions: text(p.conventions, "project.conventions"),
    locations,
    workflows,
    updatedAt: timestamp(p.updatedAt, "project.updatedAt"),
  };
  if (p.preferences !== undefined)
    result.preferences = validateProjectPreferences(p.preferences);
  if (p.workflowPolicy !== undefined) {
    if (!(["flexible", "enforced"] as const).includes(p.workflowPolicy))
      return fail("project.workflowPolicy");
    result.workflowPolicy = p.workflowPolicy;
  }
  if (p.sourcesOfTruth !== undefined) {
    if (!Array.isArray(p.sourcesOfTruth) || p.sourcesOfTruth.length > 100)
      return fail("project.sourcesOfTruth");
    const sources = p.sourcesOfTruth.map((source: unknown, index: number) =>
      validateSourceOfTruth(source, index),
    );
    if (new Set(sources.map((source) => source.id)).size !== sources.length)
      return fail("project.sourcesOfTruth.ids");
    result.sourcesOfTruth = sources;
  }
  return result;
}
export function validateProjectSnapshot(value: unknown): ProjectSnapshot {
  const p = object(value, "projectSnapshot");
  return {
    ...validateProject(p),
    capturedAt: timestamp(p.capturedAt, "projectSnapshot.capturedAt"),
  };
}
export function stepMode(type: StepType): "plan" | "execute" | "review" {
  return type === "prototype" || type === "implementation"
    ? "execute"
    : type === "review" || type === "delivery"
      ? "review"
      : "plan";
}
export function stepPhase(type: StepType): Phase {
  return type === "prototype" || type === "implementation"
    ? "execution"
    : type === "review"
      ? "review"
      : type === "delivery"
        ? "delivery"
        : "brief";
}
const DISCUSSION_TITLES: Record<StepType, string> = {
  discussion: "Discussion automatique",
  exploration: "Exploration",
  reflection: "Réflexion",
  specification: "Spécification",
  prototype: "Prototype",
  implementation: "Implémentation",
  review: "Review",
  delivery: "Livraison",
};
export function createDiscussionStep(
  prefix: string,
  type: StepType = "discussion",
): MissionStep {
  if (!STEP_TYPES.includes(type)) return fail("step.type");
  return {
    id: `${prefix}:discussion`,
    type,
    title: DISCUSSION_TITLES[type],
    objective: "",
    status: "pending",
    exitCriteria: [],
    expectedArtifacts: [],
    skills: [],
  };
}
export function createDefaultSteps(prefix: string): MissionStep[] {
  return (
    [
      ["reflection", "Réflexion"],
      ["implementation", "Implémentation"],
      ["review", "Review"],
      ["delivery", "Livraison"],
    ] as [StepType, string][]
  ).map(([type, title], index) => ({
    id: `${prefix}:step:${index + 1}`,
    type,
    title,
    objective: "",
    status: "pending",
    exitCriteria: [],
    expectedArtifacts: [],
    skills: [],
  }));
}
export function legacyProjectId(directory: string): string {
  // An injective encoding keeps migration stable and never merges different roots by hash collision.
  return `project:${encodeURIComponent(directory.replace(/[\\/]+$/, "") || directory)}`;
}
export function migrateLegacyTask<T extends Record<string, any>>(
  task: T,
): T & Partial<Task> {
  if (task.steps !== undefined) return { ...task };
  const steps = createDefaultSteps(String(task.id));
  // The legacy phase describes navigation, not proof that previous steps were approved.
  // Only the first stage is resumed; ambiguous historical records keep no stepId.
  const started =
    task.status !== "idle" ||
    (task.events?.length || 0) > 0 ||
    (task.artifacts?.length || 0) > 0;
  if (started) steps[0].status = "paused";
  const directory = typeof task.project === "string" ? task.project : "";
  const projectId = directory ? legacyProjectId(directory) : undefined;
  const snapshot = projectId
    ? {
        id: projectId,
        name: directory.split(/[\\/]/).filter(Boolean).at(-1) || directory,
        directory,
        conventions: "",
        locations: {},
        workflows: [],
        updatedAt: task.createdAt,
        capturedAt: task.createdAt,
      }
    : undefined;
  return {
    ...task,
    steps,
    activeStepId: steps[0].id,
    selectedStepId: steps[0].id,
    ...(projectId ? { projectId, projectSnapshot: snapshot } : {}),
    progressionPolicy: "manual",
    titleSource: "human",
    legacyHistory: true,
  };
}
export function validateTaskWorkflow<T extends Record<string, any>>(
  task: T,
  restored = false,
): T & Partial<Task> {
  const t = migrateLegacyTask(task);
  t.steps = validateSteps(t.steps, restored);
  if (t.projectId !== undefined) identifier(t.projectId, "projectId");
  if (t.projectSnapshot !== undefined) {
    t.projectSnapshot = validateProjectSnapshot(t.projectSnapshot);
    if (
      t.projectSnapshot.id !== t.projectId ||
      t.projectSnapshot.directory !== t.project
    )
      return fail("projectSnapshot.reference");
    if (t.projectSnapshot.workflowPolicy === "enforced") {
      const template = t.projectSnapshot.workflows[0];
      const shape = (step: MissionStep) => ({
        type: step.type,
        title: step.title,
        objective: step.objective,
        exitCriteria: step.exitCriteria,
        expectedArtifacts: step.expectedArtifacts,
        skills: step.skills,
      });
      if (
        !template ||
        JSON.stringify(template.steps.map(shape)) !==
          JSON.stringify(t.steps.map(shape))
      )
        return fail("projectSnapshot.workflow");
    }
  }
  if (t.progressionPolicy !== undefined && t.progressionPolicy !== "manual")
    return fail("progressionPolicy");
  if (
    t.workflowMode !== undefined &&
    !(["flexible", "fixed"] as const).includes(t.workflowMode)
  )
    return fail("workflowMode");
  if (
    t.workflowMode === "flexible" &&
    t.projectSnapshot?.workflowPolicy === "enforced"
  )
    return fail("workflowMode.policy");
  if (
    t.workflowOrigin !== undefined &&
    !(t.workflowOrigin === "agent" || t.workflowOrigin === "legacy")
  )
    return fail("workflowOrigin");
  if (t.initialWorkflowProposal !== undefined) {
    const proposal = validateWorkflowProposal(t.initialWorkflowProposal, true);
    const current = t.steps?.find((step) => step.id === proposal.stepId);
    if (
      t.workflowMode !== "flexible" ||
      t.steps?.[0]?.id !== proposal.stepId ||
      t.activeStepId !== proposal.stepId ||
      current?.type !== "discussion" ||
      current.status === "completed" ||
      current.approvedAt ||
      current.approvedBy
    )
      return fail("initialWorkflowProposal.state");
    t.initialWorkflowProposal = proposal as Task["initialWorkflowProposal"];
  }
  if (t.workflowProposal !== undefined) {
    const proposal = validateWorkflowProposal(t.workflowProposal, true);
    t.workflowProposal = proposal as Task["workflowProposal"];
  }
  if (t.workflowAmendment !== undefined) {
    const amendment = validateWorkflowAmendment(t.workflowAmendment, true);
    if (
      t.workflowMode !== "flexible" ||
      t.projectSnapshot?.workflowPolicy === "enforced" ||
      amendment.stepId !== t.activeStepId ||
      !t.steps.some((step) => step.id === amendment.stepId)
    )
      return fail("workflowAmendment.state");
    if (
      amendment.runId !== undefined &&
      t.runId !== undefined &&
      amendment.runId !== t.runId
    )
      return fail("workflowAmendment.runId");
    t.workflowAmendment = amendment as Task["workflowAmendment"];
  }
  const validateProposal = (
    value: unknown,
    field: "nextStepProposal" | "initialStepProposal",
  ) => {
    const proposal = object(value, field);
    if (
      !STEP_TYPES.includes(proposal.type) ||
      (field === "initialStepProposal" &&
        !DISCUSSION_TYPES.includes(proposal.type))
    )
      return fail(`${field}.type`);
    const stepId = identifier(proposal.stepId, `${field}.stepId`);
    if (!t.steps?.some((step) => step.id === stepId))
      return fail(`${field}.stepId`);
    const normalized = {
      type: proposal.type as StepType,
      title: text(proposal.title, `${field}.title`, 1000),
      objective: text(proposal.objective, `${field}.objective`),
      reason: text(proposal.reason, `${field}.reason`),
      stepId,
      ...(proposal.validation === undefined
        ? {}
        : VALIDATIONS.includes(proposal.validation) &&
            !(proposal.validation === "automatic" &&
              (proposal.type === "review" || proposal.type === "delivery"))
          ? { validation: proposal.validation as StepValidation }
          : fail(`${field}.validation`)),
    };
    if (field === "initialStepProposal") {
      const current = t.steps?.find((step) => step.id === stepId);
      if (
        t.workflowMode !== "flexible" ||
        t.activeStepId !== stepId ||
        t.steps?.[0]?.id !== stepId ||
        current?.type !== "discussion" ||
        current.status === "completed" ||
        current.approvedAt ||
        current.approvedBy
      )
        return fail("initialStepProposal.state");
      t.initialStepProposal = normalized as Task["initialStepProposal"];
    } else {
      t.nextStepProposal = normalized;
    }
  };
  if (t.nextStepProposal !== undefined) {
    validateProposal(t.nextStepProposal, "nextStepProposal");
  }
  if (t.initialStepProposal !== undefined) {
    validateProposal(t.initialStepProposal, "initialStepProposal");
  }
  if (
    t.titleSource !== undefined &&
    !["placeholder", "agent", "human"].includes(t.titleSource)
  )
    return fail("titleSource");
  for (const field of ["titleGeneratedAt", "titleEditedAt"] as const)
    if (t[field] !== undefined) timestamp(t[field], field);
  for (const field of ["activeStepId", "selectedStepId"] as const) {
    if (t[field] !== undefined && !t.steps.some((s) => s.id === t[field]))
      return fail(field);
  }
  const active = t.steps.find((s) => s.id === t.activeStepId);
  const unfinishedStarted = t.steps.find(
    (s) => s.status !== "pending" && s.status !== "completed",
  );
  if (unfinishedStarted && unfinishedStarted.id !== t.activeStepId)
    return fail("activeStepId.progression");
  if (
    active &&
    active.status === "pending" &&
    active.id !== t.steps.find((s) => s.status !== "completed")?.id
  )
    return fail("activeStepId.pending");
  const selected = t.steps.find((s) => s.id === t.selectedStepId);
  if (selected?.status === "pending" && selected.id !== t.activeStepId)
    return fail("selectedStepId.pending");
  for (const field of [
    "questions",
    "agents",
    "events",
    "artifacts",
    "feedback",
    "instructions",
    "actions",
  ]) {
    for (const item of (t as Record<string, any>)[field] || []) {
      if (
        item.stepId !== undefined &&
        !t.steps.some((s) => s.id === item.stepId)
      )
        return fail(`${field}.stepId`);
      if (item.runId !== undefined) identifier(item.runId, `${field}.runId`);
    }
  }
  return t;
}
export function canStartStep(task: Task, stepId: string): boolean {
  const steps = task.steps || [];
  const index = steps.findIndex((s) => s.id === stepId);
  if (index < 0 || task.status === "running") return false;
  const predecessorReady = (step: MissionStep): boolean => {
    if (
      step.status !== "completed" ||
      step.needsRevalidation
    )
      return false;
    if (step.validation !== "automatic")
      return step.approvedBy === "human" && !!step.approvedAt;
    return !!step.completedAt && !step.approvedAt && !step.approvedBy;
  };
  if (
    steps.slice(0, index).some((step) => !predecessorReady(step))
  )
    return false;
  if (
    task.questions.some(
      (q) =>
        q.blocking && !q.answer?.trim() && (q.stepId === stepId || !q.stepId),
    )
  )
    return false;
  if (steps[index].type === "delivery") {
    const lastCode = steps
      .slice(0, index)
      .reduce(
        (last, s, i) =>
          s.type === "implementation" || s.type === "prototype" ? i : last,
        -1,
      );
    if (
      lastCode >= 0 &&
      !steps
        .slice(lastCode + 1, index)
        .some(
          (s) =>
            s.type === "review" &&
            s.status === "completed" &&
            s.approvedBy === "human" &&
            !s.needsRevalidation,
        )
    )
      return false;
  }
  return steps[index].status !== "completed";
}
/**
 * Build the initial, still-pending step for a flexible discussion. The lead
 * may propose a continuation, but only a human calls appendStep to materialize
 * it on the timeline.
 */
export function appendStep(task: Task, step: MissionStep): Task {
  if (
    task.workflowMode !== "flexible" ||
    task.projectSnapshot?.workflowPolicy === "enforced" ||
    task.status === "running" ||
    !!task.runId
  )
    return fail("step.append");
  const steps = task.steps || [];
  const last = steps.at(-1);
  if (
    !last ||
    last.status !== "completed" ||
    last.approvedBy !== "human" ||
    !last.approvedAt ||
    last.needsRevalidation
  )
    return fail("step.append.previous");
  if (steps.length >= 100) return fail("steps");
  const candidate = validateStep(step);
  if (candidate.status !== "pending") return fail("step.append.status");
  if (steps.some((existing) => existing.id === candidate.id))
    return fail("step.append.id");
  return {
    ...task,
    activeStepId: candidate.id,
    selectedStepId: candidate.id,
    phase: stepPhase(candidate.type),
    status: "idle",
    steps: [...steps, candidate],
  };
}

type WorkflowAmendmentInput =
  | WorkflowAmendment
  | WorkflowAmendmentStep[]
  | {
      steps?: WorkflowAmendmentStep[];
      pendingSteps?: WorkflowAmendmentStep[];
      suffix?: WorkflowAmendmentStep[];
      reason?: string;
      currentStepId?: string;
      stepId?: string;
    };

function normalizeAmendmentInput(
  value: WorkflowAmendmentInput,
): {
  steps: WorkflowAmendmentStep[];
  reason: string;
  currentStepId?: string;
} {
  if (Array.isArray(value)) {
    return { steps: value, reason: "Timeline ajustée par le chef." };
  }
  const input = object(value, "workflow.amendment");
  const steps = input.steps ?? input.pendingSteps ?? input.suffix;
  if (!Array.isArray(steps)) return fail("workflow.amendment.steps");
  const reason =
    input.reason === undefined
      ? "Timeline ajustée par le chef."
      : text(input.reason, "workflow.amendment.reason", 100000);
  if (!reason.trim()) return fail("workflow.amendment.reason");
  const currentStepId =
    input.currentStepId === undefined && input.stepId === undefined
      ? undefined
      : identifier(
          input.currentStepId ?? input.stepId,
          "workflow.amendment.currentStepId",
        );
  return { steps, reason, currentStepId };
}

function normalizePendingAmendmentStep(
  value: WorkflowAmendmentStep,
  field: string,
  fallbackId: string,
): MissionStep {
  const raw = object(value, field);
  if (!WORKFLOW_PROPOSAL_TYPES.includes(raw.type))
    return fail(`${field}.type`);
  if (raw.status !== undefined && raw.status !== "pending")
    return fail(`${field}.status`);
  for (const key of [
    "startedAt",
    "completedAt",
    "approvedAt",
    "approvedBy",
    "summary",
    "needsRevalidation",
  ])
    if (raw[key] !== undefined) return fail(`${field}.${key}`);
  const validation =
    raw.validation ??
    (raw.type === "implementation" || raw.type === "prototype"
      ? "automatic"
      : "human");
  const candidate = {
    id:
      raw.id === undefined
        ? fallbackId
        : identifier(raw.id, `${field}.id`),
    type: raw.type,
    title: text(raw.title, `${field}.title`, 1000),
    objective: text(raw.objective, `${field}.objective`, 100000),
    status: "pending" as const,
    exitCriteria:
      raw.exitCriteria === undefined
        ? []
        : strings(raw.exitCriteria, `${field}.exitCriteria`),
    expectedArtifacts:
      raw.expectedArtifacts === undefined
        ? []
        : strings(raw.expectedArtifacts, `${field}.expectedArtifacts`),
    skills:
      raw.skills === undefined ? [] : strings(raw.skills, `${field}.skills`),
    validation: VALIDATIONS.includes(validation)
      ? (validation as StepValidation)
      : fail(`${field}.validation`),
  };
  if (!candidate.title.trim() || !candidate.objective.trim())
    return fail(`${field}.content`);
  return validateStep(candidate);
}

/**
 * Replace only the pending suffix after the active stage. The completed and
 * active prefix is copied by identity, while replacement stages receive fresh
 * ids unless the caller explicitly supplies pending ids.
 */
export function amendWorkflow(
  task: Task,
  value: WorkflowAmendmentInput,
  now = new Date().toISOString(),
): Task {
  if (
    task.workflowMode !== "flexible" ||
    task.projectSnapshot?.workflowPolicy === "enforced" ||
    task.status === "running" ||
    task.runId
  )
    return fail("workflow.amend.active");
  const input = normalizeAmendmentInput(value);
  const steps = task.steps || [];
  const currentId = input.currentStepId || task.activeStepId;
  const currentIndex = steps.findIndex((step) => step.id === currentId);
  if (currentIndex < 0) return fail("workflow.amend.currentStepId");
  const previousSuffix = steps.slice(currentIndex + 1);
  if (previousSuffix.some((step) => step.status !== "pending"))
    return fail("workflow.amend.suffix");
  if (steps.length - previousSuffix.length + input.steps.length > 100)
    return fail("steps");
  const prefix = steps.slice(0, currentIndex + 1);
  const occupied = new Set(prefix.map((step) => step.id));
  let generated = 1;
  const nextSteps = input.steps.map((step, index) => {
    const raw = object(step, `workflow.amendment.steps.${index}`);
    const explicitId = raw.id !== undefined;
    let id = explicitId
      ? identifier(raw.id, `workflow.amendment.steps.${index}.id`)
      : `${task.id}:amend:${generated}`;
    if (explicitId && occupied.has(id))
      return fail(`workflow.amendment.steps.${index}.id`);
    while (!explicitId && occupied.has(id))
      id = `${task.id}:amend:${++generated}`;
    occupied.add(id);
    generated += 1;
    return normalizePendingAmendmentStep(
      step,
      `workflow.amendment.steps.${index}`,
      id,
    );
  });
  const amendedSteps = [...prefix, ...nextSteps];
  const firstPending = amendedSteps.find((step) => step.status === "pending");
  const activeStepId =
    task.activeStepId && amendedSteps.some((step) => step.id === task.activeStepId)
      ? task.activeStepId
      : firstPending?.id || prefix.at(-1)?.id;
  const normalizedProposal = task.workflowProposal
    ? {
        ...task.workflowProposal,
        reason: input.reason,
        steps: amendedSteps
          .filter((step) => step.type !== "discussion")
          .map((step) => ({
            type: step.type as Exclude<StepType, "discussion">,
            title: step.title,
            objective: step.objective,
            exitCriteria: step.exitCriteria,
            expectedArtifacts: step.expectedArtifacts,
            skills: step.skills,
            ...(step.validation === undefined
              ? {}
              : { validation: step.validation }),
          })),
      }
    : undefined;
  return {
    ...task,
    status: "idle",
    activeStepId,
    selectedStepId: activeStepId,
    phase: activeStepId
      ? stepPhase(amendedSteps.find((step) => step.id === activeStepId)!.type)
      : task.phase,
    workflowProposal: normalizedProposal,
    steps: amendedSteps,
    events: [
      ...(task.events || []),
      {
        id: `${task.id}:workflow-amend:${now}`,
        time: now,
        type: "phase",
        title: "Timeline ajustée par le chef",
        detail: input.reason,
        stepId: prefix.at(-1)?.id,
        actor: "agent",
      },
    ],
  };
}

/** Alias used by native routing events that call the operation a revision. */
export function revisePendingSteps(
  task: Task,
  suffix: WorkflowAmendmentStep[] | WorkflowAmendment,
  reason?: string,
  now = new Date().toISOString(),
): Task {
  if (Array.isArray(suffix))
    return amendWorkflow(task, { steps: suffix, reason }, now);
  return amendWorkflow(
    task,
    reason === undefined ? suffix : { ...suffix, reason },
    now,
  );
}
export const reviseWorkflow = amendWorkflow;

/**
 * Apply the lead's first routing decision to the automatic discussion slot.
 * The slot is deliberately the only step that can be reclassified: an
 * existing implementation (or any other concrete stage) is never rewritten.
 * The initial provider run must have ended before this function is called.
 */
export function classifyDiscussion(
  task: Task,
  proposal: NonNullable<Task["initialStepProposal"]>,
): Task {
  if (
    task.workflowMode !== "flexible" ||
    task.projectSnapshot?.workflowPolicy === "enforced"
  )
    return fail("discussion.classify.mode");
  if (task.status === "running" || task.runId)
    return fail("discussion.classify.active");
  const stepId = identifier(proposal.stepId, "discussionProposal.stepId");
  const current = task.steps?.find((step) => step.id === stepId);
  const humanApprovalInHistory = task.events?.some(
    (event) =>
      event.stepId === stepId &&
      event.actor === "human" &&
      event.type === "phase" &&
      /valid|approv|approuv/i.test(`${event.title} ${event.detail}`),
  );
  if (
    !current ||
    task.steps?.[0]?.id !== stepId ||
    task.activeStepId !== stepId ||
    current.type !== "discussion" ||
    current.status === "completed" ||
    current.approvedAt ||
    current.approvedBy ||
    humanApprovalInHistory
  )
    return fail("discussion.classify.step");
  if (!DISCUSSION_TYPES.includes(proposal.type))
    return fail("discussion.classify.type");
  if (task.initialStepProposal) {
    const stored = task.initialStepProposal;
    if (
      stored.stepId !== proposal.stepId ||
      stored.type !== proposal.type ||
      stored.title !== proposal.title ||
      stored.objective !== proposal.objective ||
      stored.reason !== proposal.reason
    )
      return fail("discussion.classify.proposal");
  }
  const title = text(proposal.title, "discussionProposal.title", 1000);
  const objective = text(proposal.objective, "discussionProposal.objective");
  const reason = text(proposal.reason, "discussionProposal.reason");
  const validation =
    proposal.validation === undefined
      ? proposal.type === "implementation" || proposal.type === "prototype"
        ? "automatic"
        : "human"
      : VALIDATIONS.includes(proposal.validation)
        ? proposal.validation
        : fail("discussionProposal.validation");
  void reason;
  return {
    ...task,
    status: "idle",
    runId: undefined,
    runMode: undefined,
    phase: stepPhase(proposal.type),
    initialStepProposal: undefined,
    nextStepProposal: undefined,
    activeStepId: stepId,
    selectedStepId: stepId,
    steps: task.steps!.map((step) =>
      step.id !== stepId
        ? step
        : {
            ...step,
            type: proposal.type,
            title,
            objective,
            ...(validation === undefined ? {} : { validation }),
            status: "pending",
            startedAt: undefined,
            completedAt: undefined,
            approvedAt: undefined,
            approvedBy: undefined,
            summary: undefined,
            needsRevalidation: undefined,
          },
    ),
  };
}

/**
 * Materialize the complete timeline chosen during the initial, read-only
 * qualification pass. The first proposal stage reuses the discussion slot so
 * all qualification events keep their original scope; later stages receive
 * stable mission-local identities and stay pending until their predecessor is
 * validated.
 */
export function applyWorkflowProposal(
  task: Task,
  proposal: WorkflowProposal & { stepId: string },
): Task {
  if (
    task.workflowMode !== "flexible" ||
    task.runId ||
    task.status === "running"
  )
    return fail("workflowProposal.apply.active");
  const normalized = validateWorkflowProposal(
    proposal,
    true,
  ) as WorkflowProposal & {
    stepId: string;
  };
  const current = task.steps?.find((step) => step.id === normalized.stepId);
  if (
    !current ||
    task.steps?.[0]?.id !== normalized.stepId ||
    task.activeStepId !== normalized.stepId ||
    current.type !== "discussion" ||
    current.status === "completed" ||
    current.approvedAt ||
    current.approvedBy
  )
    return fail("workflowProposal.apply.step");
  if (
    task.initialWorkflowProposal &&
    JSON.stringify(task.initialWorkflowProposal) !== JSON.stringify(normalized)
  )
    return fail("workflowProposal.apply.proposal");
  const steps = normalized.steps.map((step, index) => ({
    id: index === 0 ? normalized.stepId : `${task.id}:workflow:${index + 1}`,
    type: step.type,
    title: step.title,
    objective: step.objective,
    validation:
      step.validation ??
      (step.type === "implementation" || step.type === "prototype"
        ? "automatic"
        : "human"),
    status: "pending" as const,
    exitCriteria: step.exitCriteria || [],
    expectedArtifacts: step.expectedArtifacts || [],
    skills: step.skills || [],
  }));
  return {
    ...task,
    status: "idle",
    runId: undefined,
    runMode: undefined,
    workflowMode: "flexible",
    workflowOrigin: "agent",
    phase: stepPhase(steps[0].type),
    activeStepId: steps[0].id,
    selectedStepId: steps[0].id,
    initialStepProposal: undefined,
    initialWorkflowProposal: undefined,
    nextStepProposal: undefined,
    workflowProposal: {
      ...normalized,
      stepId: steps[0].id,
      steps: steps.map((step) => ({
        type: step.type as Exclude<StepType, "discussion">,
        title: step.title,
        objective: step.objective,
        exitCriteria: step.exitCriteria,
        expectedArtifacts: step.expectedArtifacts,
        skills: step.skills,
        validation:
          step.validation ??
          (step.type === "implementation" || step.type === "prototype"
            ? "automatic"
            : "human"),
      })),
    },
    steps,
  };
}
// Human input resumes the current stage only; it never validates its result or advances.
export function canResumeAfterHumanInput(task: Task): boolean {
  const step = task.steps?.find((s) => s.id === task.activeStepId);
  return !!step && step.status !== "completed" && canStartStep(task, step.id);
}
export function startStep(
  task: Task,
  stepId: string,
  now = new Date().toISOString(),
): Task {
  if (!canStartStep(task, stepId)) return fail("step.start");
  const step = task.steps!.find((s) => s.id === stepId)!;
  return {
    ...task,
    activeStepId: stepId,
    selectedStepId: stepId,
    phase: stepPhase(step.type),
    status: "running",
    steps: task.steps!.map((s) =>
      s.id === stepId
        ? { ...s, status: "running", startedAt: s.startedAt || now }
        : s,
    ),
  };
}
export function finishStepRun(
  task: Task,
  stepId: string,
  result: "completed" | "error" | "cancelled",
  summary?: string,
  runId?: string,
  now = new Date().toISOString(),
): Task {
  if ((runId && runId !== task.runId) || task.activeStepId !== stepId)
    return task; // Late output cannot complete another stage.
  const step = task.steps?.find((candidate) => candidate.id === stepId);
  const blocked = task.questions.some(
    (q) =>
      q.blocking && !q.answer?.trim() && (!q.stepId || q.stepId === stepId),
  );
  const automatic =
    result === "completed" &&
    step?.validation === "automatic" &&
    step.type !== "review" &&
    step.type !== "delivery" &&
    !blocked;
  const nextStep = automatic
    ? task.steps
        ?.slice((task.steps.findIndex((candidate) => candidate.id === stepId) || 0) + 1)
        .find((candidate) => candidate.status === "pending")
    : undefined;
  return {
    ...task,
    activeStepId: nextStep?.id || task.activeStepId,
    selectedStepId: nextStep?.id || task.selectedStepId,
    phase: nextStep ? stepPhase(nextStep.type) : task.phase,
    status:
      result === "error"
        ? "error"
        : result === "cancelled"
          ? "paused"
          : "waiting",
    steps: task.steps?.map((s) =>
      s.id !== stepId || s.status !== "running"
        ? s
        : {
            ...s,
            status:
              result === "error"
                ? "error"
                : result === "cancelled"
                  ? "paused"
                  : automatic
                    ? "completed"
                  : blocked
                    ? "blocked"
                    : "awaiting_human",
            completedAt: automatic ? now : s.completedAt,
            approvedAt: automatic ? undefined : s.approvedAt,
            approvedBy: automatic ? undefined : s.approvedBy,
            summary: summary || s.summary,
          },
    ),
  };
}
export function approveStep(
  task: Task,
  stepId: string,
  actor: "human" | "agent",
  now = new Date().toISOString(),
): Task {
  const step = task.steps?.find((s) => s.id === stepId);
  if (
    actor !== "human" ||
    task.activeStepId !== stepId ||
    step?.status !== "awaiting_human" ||
    task.questions.some(
      (q) =>
        q.blocking && !q.answer?.trim() && (!q.stepId || q.stepId === stepId),
    )
  )
    return fail("step.approve");
  return {
    ...task,
    status:
      task.steps?.at(-1)?.id === stepId &&
      (step.type === "delivery" || task.workflowMode === "flexible")
        ? "done"
        : "idle",
    steps: task.steps!.map((s) =>
      s.id === stepId
        ? {
            ...s,
            status: "completed",
            approvedBy: "human",
            approvedAt: now,
            completedAt: now,
            needsRevalidation: false,
          }
        : s,
    ),
  };
}
export function applyMissionTitle(
  task: Task,
  title: string,
  now = new Date().toISOString(),
): Task {
  if (task.titleSource === "human" || task.titleEditedAt) return task;
  if (!title.trim()) return task;
  return {
    ...task,
    title: text(title.trim(), "title", 1000),
    titleSource: "agent",
    titleGeneratedAt: now,
  };
}

export function invalidateDependentSteps(task: Task, stepId: string): Task {
  const index = task.steps?.findIndex((s) => s.id === stepId) ?? -1;
  if (index < 0) return task;
  return {
    ...task,
    steps: task.steps?.map((s, i) =>
      i >= index && s.status !== "pending"
        ? { ...s, needsRevalidation: true }
        : s,
    ),
  };
}
export function reopenStep(task: Task, stepId: string): Task {
  const step = task.steps?.find((s) => s.id === stepId);
  if (!step || step.status === "pending" || task.status === "running")
    return fail("step.reopen");
  const invalidated = invalidateDependentSteps(task, stepId);
  return {
    ...invalidated,
    activeStepId: stepId,
    selectedStepId: stepId,
    status: "paused",
    steps: invalidated.steps?.map((s) =>
      s.id === stepId
        ? {
            ...s,
            status: "paused",
            approvedAt: undefined,
            approvedBy: undefined,
            completedAt: undefined,
          }
        : s,
    ),
  };
}
