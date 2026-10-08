import {
  createDefaultSteps,
  createDiscussionStep,
  stepPhase,
} from "./workflow";
import type {
  AppState,
  Task,
  FlightEvent,
  Project,
  MissionStep,
} from "./types";
import { demoArtifacts } from "./demo-supports";
import { t } from "./i18n";
export const uid = () => crypto.randomUUID();
export const now = () => new Date().toISOString();
export const event = (
  type: FlightEvent["type"],
  title: string,
  detail = "",
  agentId?: string,
): FlightEvent => ({ id: uid(), time: now(), type, title, detail, agentId });
export const phaseLabels = {
  brief: t("data.phase_brief"),
  execution: t("data.phase_execution"),
  review: "Review",
  delivery: t("data.phase_delivery"),
};
export function newTask(
  title: string,
  brief: string,
  project: string,
  provider: Task["provider"],
  model: string,
  options?: {
    projectRecord?: Project;
    steps?: MissionStep[];
    concurrency?: number;
    workflowMode?: Task["workflowMode"];
    /** Route a fresh mission through a read-only qualification pass. */
    autoWorkflow?: boolean;
    indication?: string;
  },
): Task {
  const id = uid();
  const createdAt = now();
  const agentWorkflow =
    options?.autoWorkflow === true ||
    options?.steps?.[0]?.type === "discussion";
  const steps =
    options?.steps?.map((s) => ({
      ...s,
      id: uid(),
      status: "pending" as const,
      startedAt: undefined,
      completedAt: undefined,
      approvedAt: undefined,
      approvedBy: undefined,
    })) ||
    (agentWorkflow || options?.workflowMode === "flexible"
      ? [createDiscussionStep(id, "discussion")]
      : createDefaultSteps(id));
  const projectSnapshot = options?.projectRecord
    ? structuredClone(options.projectRecord)
    : undefined;
  // A mission's initial routing is decided from the user's intent. Keep the
  // project record itself untouched while preventing an inherited workflow
  // policy from blocking that qualification pass.
  if (agentWorkflow && projectSnapshot) {
    projectSnapshot.workflowPolicy = "flexible";
    projectSnapshot.workflows = [];
  }
  return {
    id,
    title: title.trim() || t("data.new_wish"),
    titleSource: title.trim() ? "human" : "placeholder",
    steps,
    activeStepId: steps[0].id,
    selectedStepId: steps[0].id,
    progressionPolicy: "manual",
    workflowMode: agentWorkflow
      ? "flexible"
      : options?.projectRecord?.workflowPolicy === "enforced"
        ? "fixed"
        : options?.workflowMode || "fixed",
    workflowOrigin: agentWorkflow ? "agent" : undefined,
    projectId: options?.projectRecord?.id,
    projectSnapshot: projectSnapshot
      ? { ...projectSnapshot, capturedAt: createdAt }
      : undefined,
    brief,
    project,
    provider,
    model,
    phase: stepPhase(steps[0].type),
    status: "idle",
    createdAt,
    instructions: options?.indication?.trim()
      ? [
          {
            id: uid(),
            text: options.indication.trim(),
            time: createdAt,
            stepId: steps[0].id,
            status: "queued",
          },
        ]
      : [],
    questions: [],
    agents: [],
    events: [
      { ...event("note", t("data.wish_created"), brief), stepId: steps[0].id },
    ],
    artifacts: [],
    feedback: [],
    configuration: {
      prototype: t("data.config_prototype"),
      review: t("data.config_review"),
      deliverables: [
        t("data.deliverable_summary"),
        t("data.deliverable_journal"),
        t("data.deliverable_session"),
      ],
      concurrency: options?.concurrency || 3,
    },
  };
}
export const diagramContent = JSON.stringify({
  nodes: [
    {
      id: "brief",
      label: t("data.diagram_brief"),
      sublabel: t("data.diagram_brief_detail"),
      x: 70,
      y: 130,
    },
    {
      id: "lead",
      label: t("data.diagram_lead"),
      sublabel: t("data.diagram_lead_detail"),
      x: 340,
      y: 130,
    },
    {
      id: "design",
      label: "Design",
      sublabel: t("data.diagram_design_detail"),
      x: 610,
      y: 45,
    },
    {
      id: "build",
      label: "Implementation",
      sublabel: t("data.diagram_build_detail"),
      x: 610,
      y: 215,
    },
    {
      id: "review",
      label: "Review",
      sublabel: t("data.diagram_review_detail"),
      x: 875,
      y: 130,
    },
  ],
  edges: [
    ["brief", "lead"],
    ["lead", "design"],
    ["lead", "build"],
    ["design", "review"],
    ["build", "review"],
  ],
});
export function initialState(): AppState {
  const task = newTask(
    t("data.demo_title"),
    t("data.demo_brief"),
    "",
    "codex",
    "",
  );
  task.demo = true;
  task.phase = "execution";
  task.status = "waiting";
  task.questions = [
    {
      id: "Q01",
      title: t("data.demo_q1_title"),
      context: t("data.demo_q1_context"),
      recommendation: t("data.demo_q1_recommendation"),
      options: [
        {
          id: "a",
          label: t("data.demo_q1_option_a"),
          description: t("data.demo_q1_option_a_detail"),
        },
        {
          id: "b",
          label: t("data.demo_q1_option_b"),
          description: t("data.demo_q1_option_b_detail"),
        },
      ],
      blocking: true,
      unlocks: t("data.demo_q1_unlocks"),
      agentId: "design",
      theme: t("data.demo_q1_theme"),
    },
    {
      id: "Q02",
      title: t("data.demo_q2_title"),
      context: t("data.demo_q2_context"),
      recommendation: t("data.demo_q2_recommendation"),
      options: [
        {
          id: "a",
          label: t("data.demo_q2_option_a"),
          description: t("data.demo_q2_option_a_detail"),
        },
        {
          id: "b",
          label: t("data.demo_q2_option_b"),
          description: t("data.demo_q2_option_b_detail"),
        },
      ],
      blocking: false,
      unlocks: t("data.demo_q2_unlocks"),
      agentId: "build",
      theme: "Interface",
    },
  ];
  task.agents = [
    {
      id: "lead",
      name: "Djinn",
      role: "Orchestration",
      model: t("data.demo_lead_model"),
      status: "running",
      summary: t("data.demo_lead_summary"),
      progress: 48,
    },
    {
      id: "design",
      name: "Atlas",
      role: t("data.demo_design_role"),
      model: t("data.demo_design_model"),
      status: "blocked",
      summary: t("data.demo_design_summary"),
      progress: 65,
      prompt: t("data.demo_design_prompt"),
    },
    {
      id: "build",
      name: "Nova",
      role: "Implementation",
      model: t("data.demo_build_model"),
      status: "queued",
      summary: t("data.demo_build_summary"),
      progress: 32,
      prompt: t("data.demo_build_prompt"),
    },
    {
      id: "review",
      name: "Echo",
      role: t("data.demo_review_role"),
      model: t("data.demo_review_model"),
      status: "queued",
      summary: t("data.demo_review_summary"),
      progress: 0,
      prompt: t("data.demo_review_prompt"),
    },
  ];
  const time = (minutes: number) =>
    new Date(Date.now() - minutes * 60000).toISOString();
  task.events = [
    {
      id: uid(),
      time: time(14),
      type: "note",
      title: t("data.demo_event_scoped"),
      detail: t("data.demo_event_scoped_detail"),
      agentId: "lead",
    },
    {
      id: uid(),
      time: time(11),
      type: "agent",
      title: t("data.demo_event_atlas_joins"),
      detail: t("data.demo_event_atlas_joins_detail"),
      agentId: "design",
      lifecycle: "started",
      runId: "demo-initial",
    },
    {
      id: uid(),
      time: time(11),
      type: "agent",
      title: t("data.demo_event_nova_prepares"),
      detail: t("data.demo_event_nova_prepares_detail"),
      agentId: "build",
      lifecycle: "started",
      runId: "demo-initial",
    },
    {
      id: uid(),
      time: time(8),
      type: "tool",
      title: t("data.demo_event_architecture"),
      detail: t("data.demo_event_architecture_detail"),
      agentId: "design",
    },
    {
      id: uid(),
      time: time(7),
      type: "agent",
      title: t("data.demo_event_states"),
      detail: t("data.demo_event_states_detail"),
      agentId: "build",
      lifecycle: "completed",
      runId: "demo-initial",
    },
    {
      id: uid(),
      time: time(4),
      type: "agent",
      title: t("data.demo_event_atlas_waits"),
      detail: t("data.demo_event_atlas_waits_detail"),
      agentId: "design",
      lifecycle: "blocked",
      runId: "demo-initial",
    },
    {
      id: uid(),
      time: time(4),
      type: "decision",
      title: t("data.demo_event_decisions"),
      detail: t("data.demo_event_decisions_detail"),
      agentId: "lead",
    },
  ];
  task.artifacts = [
    ...demoArtifacts(time(6)),
    {
      id: "architecture",
      title: t("data.demo_architecture_title"),
      type: "diagram",
      content: diagramContent,
      updatedAt: time(8),
    },
    {
      id: "preview",
      title: t("data.demo_preview_title"),
      type: "wireframe",
      content: JSON.stringify({
        heading: t("data.demo_preview_heading"),
        layout: "status",
        archive: "dedicated",
      }),
      updatedAt: time(7),
    },
    {
      id: "plan",
      title: t("data.demo_plan_title"),
      type: "document",
      content: t("data.demo_plan_content"),
      updatedAt: time(9),
    },
  ];
  task.steps![0].status = "paused";
  task.events.push(
    ...task.artifacts
      .filter((a) => a.id.startsWith("demo-"))
      .map((a) => ({
        id: `demo-support-event:${a.id}`,
        time: a.updatedAt,
        type: "note" as const,
        title: t("chat.event_artifact_available", { title: a.title }),
        detail: t("data.demo_support_detail"),
        agentId: "lead",
      })),
  );
  for (const items of [
    task.questions,
    task.agents,
    task.events,
    task.artifacts,
  ])
    for (const item of items) item.stepId = task.activeStepId;
  return {
    version: 2,
    projects: [],
    tasks: [task],
    selectedId: task.id,
    settings: {
      provider: "codex",
      model: "",
      reduceMotion: false,
      sound: false,
    },
  };
}
