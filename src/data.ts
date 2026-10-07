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
export const uid = () => crypto.randomUUID();
export const now = () => new Date().toISOString();
export const event = (
  type: FlightEvent["type"],
  title: string,
  detail = "",
  agentId?: string,
): FlightEvent => ({ id: uid(), time: now(), type, title, detail, agentId });
export const phaseLabels = {
  brief: "Cadrage",
  execution: "Exécution",
  review: "Review",
  delivery: "Livraison",
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
    title: title.trim() || "Nouvelle mission",
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
    events: [{ ...event("note", "Mission créée", brief), stepId: steps[0].id }],
    artifacts: [],
    feedback: [],
    configuration: {
      prototype: "Interface locale",
      review: "Review visuelle + technique",
      deliverables: ["Synthèse", "Journal", "Session partageable"],
      concurrency: options?.concurrency || 3,
    },
  };
}
export const diagramContent = JSON.stringify({
  nodes: [
    {
      id: "brief",
      label: "Votre intention",
      sublabel: "Besoin & contraintes",
      x: 70,
      y: 130,
    },
    {
      id: "lead",
      label: "Orchestrateur",
      sublabel: "Plan · décisions · intégration",
      x: 340,
      y: 130,
    },
    {
      id: "design",
      label: "Design",
      sublabel: "Parcours & composants",
      x: 610,
      y: 45,
    },
    {
      id: "build",
      label: "Implementation",
      sublabel: "Code & preuves",
      x: 610,
      y: 215,
    },
    {
      id: "review",
      label: "Review",
      sublabel: "Validation humaine",
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
    "Espace projets",
    "Repenser l’espace projets : une vue claire des missions, un suivi partagé et une navigation qui laisse la place au travail.",
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
      title: "Comment organiser la vue des projets ?",
      context:
        "Les projets ont des rythmes différents. Une vue par statut facilite le suivi ; une vue par équipe facilite la répartition. Ce choix détermine la navigation et les filtres.",
      recommendation: "Une vue par statut, avec un filtre par équipe.",
      options: [
        {
          id: "a",
          label: "Par statut",
          description:
            "À faire, en cours, en review. La progression reste visible.",
        },
        {
          id: "b",
          label: "Par équipe",
          description: "Chaque équipe retrouve son propre espace.",
        },
      ],
      blocking: true,
      unlocks: "Débloque la navigation et les composants de la vue projets.",
      agentId: "design",
      theme: "Produit",
    },
    {
      id: "Q02",
      title: "Quelle place pour les projets archivés ?",
      context:
        "Les archives doivent rester accessibles sans prendre de place dans le travail quotidien.",
      recommendation: "Un accès dédié dans la navigation secondaire.",
      options: [
        {
          id: "a",
          label: "Accès dédié",
          description: "Une vue Archives, à l’écart des projets actifs.",
        },
        {
          id: "b",
          label: "Filtre dans la liste",
          description: "Toutes les données dans une seule vue.",
        },
      ],
      blocking: false,
      unlocks: "Finalise les états de la liste.",
      agentId: "build",
      theme: "Interface",
    },
  ];
  task.agents = [
    {
      id: "lead",
      name: "Djinn",
      role: "Orchestration",
      model: "Chef de mission",
      status: "running",
      summary: "Le plan est prêt. Deux décisions attendent votre retour.",
      progress: 48,
    },
    {
      id: "design",
      name: "Atlas",
      role: "Design & parcours",
      model: "Agent de design",
      status: "blocked",
      summary: "Attend votre choix sur la structure des projets.",
      progress: 65,
      prompt: "Concevoir les parcours et les composants de la vue projets.",
    },
    {
      id: "build",
      name: "Nova",
      role: "Implementation",
      model: "Agent de code",
      status: "queued",
      summary: "Prépare les composants après validation du cadrage.",
      progress: 32,
      prompt: "Implémenter la vue projets selon les décisions prises.",
    },
    {
      id: "review",
      name: "Echo",
      role: "Qualité & review",
      model: "Agent de vérification",
      status: "queued",
      summary: "Vérifiera les parcours et leurs états limites.",
      progress: 0,
      prompt: "Vérifier les parcours des projets et produire des preuves.",
    },
  ];
  const time = (minutes: number) =>
    new Date(Date.now() - minutes * 60000).toISOString();
  task.events = [
    {
      id: uid(),
      time: time(14),
      type: "note",
      title: "Cadrage terminé",
      detail:
        "Djinn a réparti la mission en trois périmètres : design, implementation et qualité.",
      agentId: "lead",
    },
    {
      id: uid(),
      time: time(11),
      type: "agent",
      title: "Atlas rejoint la mission",
      detail: "Simulation : analyse de la navigation et des parcours.",
      agentId: "design",
      lifecycle: "started",
      runId: "demo-initial",
    },
    {
      id: uid(),
      time: time(11),
      type: "agent",
      title: "Nova prépare les composants",
      detail:
        "Simulation : préparation des états pendant le travail de design.",
      agentId: "build",
      lifecycle: "started",
      runId: "demo-initial",
    },
    {
      id: uid(),
      time: time(8),
      type: "tool",
      title: "Architecture de la vue préparée",
      detail: "Une première proposition est disponible dans les supports.",
      agentId: "design",
    },
    {
      id: uid(),
      time: time(7),
      type: "agent",
      title: "Les états sont préparés",
      detail: "Simulation : Nova attend la validation des choix de navigation.",
      agentId: "build",
      lifecycle: "completed",
      runId: "demo-initial",
    },
    {
      id: uid(),
      time: time(4),
      type: "agent",
      title: "Atlas attend votre choix",
      detail: "La structure de la vue attend votre décision.",
      agentId: "design",
      lifecycle: "blocked",
      runId: "demo-initial",
    },
    {
      id: uid(),
      time: time(4),
      type: "decision",
      title: "Deux décisions en attente",
      detail:
        "La structure des projets et l’accès aux archives attendent votre réponse.",
      agentId: "lead",
    },
  ];
  task.artifacts = [
    ...demoArtifacts(time(6)),
    {
      id: "architecture",
      title: "La mission, en un regard",
      type: "diagram",
      content: diagramContent,
      updatedAt: time(8),
    },
    {
      id: "preview",
      title: "Espace projets · proposition",
      type: "wireframe",
      content: JSON.stringify({
        heading: "Vos projets",
        layout: "status",
        archive: "dedicated",
      }),
      updatedAt: time(7),
    },
    {
      id: "plan",
      title: "Plan d’implementation",
      type: "document",
      content:
        "# Espace projets\n\n## Intention\nDonner une vue claire de l’avancement de chaque projet.\n\n## Périmètres\n1. Atlas — navigation, structure et interactions.\n2. Nova — composants, états vides et filtres.\n3. Echo — validation des parcours et accessibilité.\n\n## Critères de réussite\n- Retrouver un projet en moins de trois interactions.\n- Distinguer les projets actifs des archives.\n- Partager le contexte sans relire une conversation.\n\nCe document appartient à la mission d’exemple.",
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
        title: `Support disponible : ${a.title}`,
        detail:
          "Exemple interactif sur données fictives, sans exécution de projet.",
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
