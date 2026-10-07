import { useState, useEffect, useRef } from "react";
import { ActionsPanel } from "./actions-panel";
import { AnimatePresence, motion } from "motion/react";
import {
  Plus,
  Command,
  ArrowUpRight,
  ArrowRight,
  Upload,
  Download,
  Settings2,
  ChevronDown,
  ChevronRight,
  Check,
  X,
  Search,
  LayoutDashboard,
  Activity,
  Shapes,
  MessageSquare,
  PackageCheck,
  FolderOpen,
  GitBranch,
  Pause,
  Play,
  Ellipsis,
  CircleHelp,
  Sparkles,
  Terminal,
  ShieldCheck,
  RotateCcw,
  Trash2,
  FileText,
  ExternalLink,
  Copy,
  CheckCircle2,
  Circle,
  Link2,
  Keyboard,
  Flag,
  ArrowUp,
  Bell,
  Clock3,
} from "lucide-react";
import { useDjinn, download, getNextRunMode } from "./use-djinn";
import { event, now, uid, phaseLabels } from "./data";
import {
  StepTimeline,
  WorkflowEditor,
  ConcurrencyField,
  stepLabels,
} from "./step-timeline";
import {
  createDefaultSteps,
  createDiscussionStep,
  STEP_TYPES,
  validateProject,
} from "./workflow";
import { stepView, mergeStepView, legacyView } from "./mission-view";
import type {
  Task,
  Agent,
  ProviderId,
  Project,
  MissionStep,
  StepType,
  PermissionDecision,
  PermissionRequest,
  TaskAction,
} from "./types";
import {
  Overview,
  MissionHeader,
  Timeline,
  AgentDrawer,
  QuestionCard,
  formatTime,
  agentIsReadOnly,
} from "./mission-panels";
import { Orb } from "./visuals";
import ArtifactWorkspace from "./artifact-workspace";
import { MarkdownBody } from "./markdown-body";
import { ModelPicker } from "./model-picker";
import { ProjectSettings } from "./project-settings";
import { ProjectSidebar } from "./project-sidebar";
import { ProviderSwitch } from "./provider-switch";
import { useReducedMotion } from "./reduced-motion";
import { MissionTestBoard } from "./mission-test-board";
import { StageReport } from "./mission-progress";
import { AgentAvatars, AgentPickerDrawer } from "./agent-avatars";
const tabs = [
  { id: "overview", label: "Mission", icon: LayoutDashboard },
  { id: "timeline", label: "Timeline", icon: Activity },
  { id: "artifacts", label: "Restitution", icon: Shapes },
  { id: "review", label: "Review", icon: MessageSquare },
  { id: "delivery", label: "Livrables", icon: PackageCheck },
  { id: "history", label: "Historique v1", icon: Activity },
];
type Modal =
  | "new"
  | "project"
  | "settings"
  | "mission"
  | "next-step"
  | "palette"
  | "delete"
  | null;
type AgentScheduling = Agent & {
  writeScope?: string[];
  dependsOn?: string[];
  readOnly?: boolean;
  waitReason?: string;
  waitingForAgentIds?: string[];
};
const scheduling = (agent: Agent) => agent as AgentScheduling;
const scopeSyntaxHint = (value: string) => {
  const path = value.trim().replaceAll("\\", "/");
  if (!path) return "";
  if (path === "*")
    return "* réserve tout le projet : vérifiez que c’est intentionnel.";
  if (/^(?:[\\/]|[A-Za-z]:)/.test(path))
    return "Utilisez un chemin relatif au dossier du projet.";
  if (/[\0\r\n]/.test(path))
    return "Retirez les retours à la ligne et caractères invisibles.";
  if (/[?\[\]{}]/.test(path))
    return "Les jokers de chemin ne sont pas interprétés ici.";
  if (path.split("/").some((part) => !part || part === "." || part === ".."))
    return "Utilisez des segments relatifs sans . ni ..";
  return "";
};
function Brand({ small = false }: { small?: boolean }) {
  return (
    <span className={`brand ${small ? "small" : ""}`}>
      <span className="brand-mark">
        <svg viewBox="0 0 30 30" fill="none">
          <path
            d="M15 2 28 9.5v11L15 28 2 20.5v-11Z"
            stroke="currentColor"
            strokeWidth="1.2"
          />
          <path
            d="m8 11 7-4 7 4v8l-7 4-7-4Zm7-4v16M8 11l14 8m0-8L8 19"
            stroke="currentColor"
            strokeWidth="1.2"
          />
        </svg>
      </span>
      {!small && (
        <>
          djinn<span className="brand-dot">.</span>
        </>
      )}
    </span>
  );
}
function ModalFrame({
  title,
  eyebrow,
  children,
  onClose,
  wide = false,
}: {
  title: string;
  eyebrow: string;
  children: React.ReactNode;
  onClose: () => void;
  wide?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  const previousFocus = useRef<HTMLElement | null>(
    typeof document === "undefined"
      ? null
      : (document.activeElement as HTMLElement | null),
  );
  useEffect(() => {
    const el = ref.current;
    const focusable = () =>
      Array.from(
        el?.querySelectorAll<HTMLElement>(
          'button,input,textarea,select,[tabindex="0"]',
        ) || [],
      ).filter(
        (node) =>
          !node.matches(":disabled") && node.getClientRects().length > 0,
      );
    focusable()?.[0]?.focus({ preventScroll: true });
    const handle = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCloseRef.current();
      if (e.key === "Tab") {
        const nodes = focusable();
        if (!nodes?.length) return;
        const first = nodes[0],
          last = nodes[nodes.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", handle);
    return () => {
      document.removeEventListener("keydown", handle);
      if (previousFocus.current?.isConnected)
        previousFocus.current.focus({ preventScroll: true });
    };
  }, []);
  return (
    <motion.div
      className="modal-backdrop"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <motion.div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={`modal ${wide ? "wide" : ""}`}
        initial={{ opacity: 0, y: 24, scale: 0.98 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: 15, scale: 0.98 }}
        transition={{ duration: 0.22 }}
      >
        <div className="modal-top">
          <span className="eyebrow">{eyebrow}</span>
          <button
            className="icon-button"
            onClick={onClose}
            aria-label="Fermer la fenêtre"
          >
            <X size={18} />
          </button>
        </div>
        <h2>{title}</h2>
        {children}
      </motion.div>
    </motion.div>
  );
}
export default function App() {
  const d = useDjinn();
  type UiDjinn = typeof d & {
    permissions?: PermissionRequest[];
    busyPermissionIds?: string[];
    respondPermission?: (
      request: PermissionRequest,
      decision: PermissionDecision,
      answers?: Record<string, string>,
    ) => Promise<boolean>;
    recordTestResult?: (
      action: TaskAction,
      outcome: "passed" | "problem" | "deferred",
      detail?: string,
    ) => Promise<boolean>;
  };
  const ui = d as UiDjinn;
  const { state, task: mission } = d;
  const task = stepView(mission);
  const selectedStepId = mission.selectedStepId || mission.activeStepId;
  const consulting = selectedStepId !== mission.activeStepId;
  const availableTabs = tabs.filter(
    (t) =>
      (t.id === "history"
        ? mission.legacyHistory === true
        : t.id !== "delivery" && t.id !== "review") ||
      (t.id === "delivery" &&
        mission.steps?.find((s) => s.id === selectedStepId)?.type ===
          "delivery") ||
      (t.id === "review" &&
        ["prototype", "implementation", "review"].includes(
          mission.steps?.find((s) => s.id === selectedStepId)?.type || "",
        )),
  );
  const selectedStage = mission.steps?.find((s) => s.id === selectedStepId);
  const [tab, setTab] = useState("overview");
  useEffect(() => {
    if (!availableTabs.some((t) => t.id === tab)) setTab("overview");
  }, [selectedStepId, tab, availableTabs.map((t) => t.id).join(",")]);
  const [modal, setModal] = useState<Modal>(null);
  const [selectedProjectId, setSelectedProjectId] = useState(
    mission.projectId ||
      state.projects?.find((p) => p.directory === mission.project)?.id ||
      state.projects?.[0]?.id ||
      "",
  );
  const [editingProjectId, setEditingProjectId] = useState<string>();
  const [returnToMissionStart, setReturnToMissionStart] = useState(false);
  useEffect(() => {
    if (!selectedProjectId && state.projects?.length)
      setSelectedProjectId(
        mission.projectId ||
          state.projects.find((p) => p.directory === mission.project)?.id ||
          state.projects[0].id,
      );
  }, [selectedProjectId, state.projects, mission.projectId, mission.project]);
  const [agentId, setAgentId] = useState<string | null>(null);
  const [agentPickerOpen, setAgentPickerOpen] = useState(false);
  const [openedArtifactId, setOpenedArtifactId] = useState<string>();
  const [artifactRequestVersion, setArtifactRequestVersion] = useState(0);
  useEffect(() => setOpenedArtifactId(undefined), [selectedStepId]);
  const [notificationsOpen, setNotificationsOpen] = useState(false);
  const [menu, setMenu] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const [demoRunning, setDemoRunning] = useState(false);
  const demoTimer = useRef<ReturnType<typeof setInterval>>(undefined);
  const importRef = useRef<HTMLInputElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const update = (t: Task) =>
    d.updateTask(t.id, (previous) => {
      const scoped = stepView(previous, t.selectedStepId || t.activeStepId);
      const changed =
        JSON.stringify([t.artifacts, t.feedback, t.questions]) !==
        JSON.stringify([scoped.artifacts, scoped.feedback, scoped.questions]);
      return {
        ...mergeStepView(previous, t),
        reviewApprovedAt: changed ? undefined : t.reviewApprovedAt,
        status: changed && previous.status === "done" ? "idle" : t.status,
        phase: previous.phase,
      };
    });
  const agent =
    task.agents.find((a) => a.id === agentId) ||
    mission.agents.find((a) => a.id === agentId);
  const chatTask =
    agent && !task.agents.some((candidate) => candidate.id === agent.id)
      ? stepView(mission, mission.activeStepId)
      : task;
  const permissions = ui.permissions || mission.permissions || [];
  const respondPermission = async (
    request: PermissionRequest,
    decision: PermissionDecision,
    answers?: Record<string, string>,
  ) => {
    if (!ui.respondPermission) {
      d.notify("Cette demande nécessite l’application locale Djinn.");
      return false;
    }
    return ui.respondPermission(request, decision, answers);
  };
  const recordTestResult = async (
    action: TaskAction,
    outcome: "passed" | "problem" | "deferred",
    detail?: string,
  ) => {
    if (!ui.recordTestResult) {
      d.notify("Le résultat du test ne peut pas être enregistré ici.");
      return false;
    }
    return ui.recordTestResult(action, outcome, detail);
  };
  const openQuestions = task.questions.filter((q) => !q.answer);
  const blocking = openQuestions.some((q) => q.blocking);
  const questionCount = state.tasks.reduce(
    (n, t) => n + t.questions.filter((q) => !q.answer).length,
    0,
  );
  const actionCount = state.tasks.reduce(
    (n, t) =>
      n +
      (t.actions || []).filter((a) => ["pending", "error"].includes(a.status))
        .length,
    0,
  );
  useEffect(() => {
    setAgentId(null);
    setAgentPickerOpen(false);
    setOpenedArtifactId(undefined);
    setTab("overview");
    setDemoRunning(false);
    clearInterval(demoTimer.current);
  }, [task.id]);
  useEffect(() => {
    document.title = `${mission.title.replace(/\n/g, " ")} — Djinn`;
  }, [mission.title]);
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: 0, behavior: "instant" });
  }, [tab]);
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setAgentId(null);
        setModal((m) => (m === "palette" ? null : "palette"));
      }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "n") {
        e.preventDefault();
        setAgentId(null);
        setModal("new");
      }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "j") {
        e.preventDefault();
        const chat = document.querySelector<HTMLTextAreaElement>(
          ".agent-chat textarea",
        );
        if (chat && !e.shiftKey) {
          chat.focus();
          return;
        }
        if (e.shiftKey) {
          setAgentId(null);
          setTab("review");
        }
        window.dispatchEvent(
          new CustomEvent("djinn:compose", {
            detail: { feedback: e.shiftKey },
          }),
        );
      }
      if ((e.metaKey || e.ctrlKey) && /^[1-5]$/.test(e.key)) {
        e.preventDefault();
        const requested = tabs[Number(e.key) - 1].id;
        setTab(
          requested === "delivery" || requested === "review"
            ? "artifacts"
            : requested,
        );
        setAgentId(null);
      }
      if (e.key === "Escape") {
        setModal(null);
        setAgentId(null);
        setAgentPickerOpen(false);
        setMenu(false);
      }
      if (
        e.key === "?" &&
        !(e.target instanceof HTMLInputElement) &&
        !(e.target instanceof HTMLTextAreaElement)
      ) {
        setModal("palette");
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);
  useEffect(() => {
    const focus = (event: Event) => {
      const target = (
        event as CustomEvent<{ taskId: string; questionId: string }>
      ).detail;
      setTab("overview");
      setAgentId(null);
      if (target.taskId === mission.id && mission.activeStepId) d.selectStep(mission.activeStepId);
      setTimeout(
        () =>
          document
            .getElementById(`question-${target.questionId}`)
            ?.scrollIntoView({ behavior: "smooth", block: "center" }),
        350,
      );
    };
    window.addEventListener("djinn:question-focus", focus);
    const focusAction = (event: Event) => {
      const target = (event as CustomEvent<{ taskId?: string; actionId?: string }>).detail;
      if (target?.taskId === mission.id && mission.activeStepId) d.selectStep(mission.activeStepId);
      setTab("overview");
      setAgentId(null);
      setTimeout(() => {
        const card = document.getElementById(target?.actionId ? `action-${target.actionId}` : "actions-section");
        const group = card?.closest("details");
        if (group) group.open = true;
        card?.scrollIntoView({ behavior: "smooth", block: "center" });
      }, 350);
    };
    window.addEventListener("djinn:action-focus", focusAction);
    const focusPermission = (event: Event) => {
      const target = (
        event as CustomEvent<{ taskId: string; requestId: string }>
      ).detail;
      setTab("overview");
      setAgentId(null);
      setAgentPickerOpen(false);
      if (target.taskId === mission.id && mission.activeStepId) d.selectStep(mission.activeStepId);
      setTimeout(
        () =>
          document
            .getElementById(`permission-${target.requestId}`)
            ?.scrollIntoView({ behavior: "smooth", block: "center" }),
        350,
      );
    };
    window.addEventListener("djinn:permission-focus", focusPermission);
    const focusStep = (event: Event) => {
      const { type } = (event as CustomEvent<{ type: string }>).detail;
      setTab(
        type === "review"
          ? "review"
          : type === "delivery"
            ? "delivery"
            : "artifacts",
      );
      setAgentId(null);
    };
    window.addEventListener("djinn:step-focus", focusStep);
    return () => {
      window.removeEventListener("djinn:question-focus", focus);
      window.removeEventListener("djinn:action-focus", focusAction);
      window.removeEventListener("djinn:permission-focus", focusPermission);
      window.removeEventListener("djinn:step-focus", focusStep);
    };
  }, [mission.id, mission.activeStepId]);
  useEffect(() => () => clearInterval(demoTimer.current), []);
  const navigate = (next: string) => {
    setTab(availableTabs.some((t) => t.id === next) ? next : "artifacts");
    setAgentId(null);
  };
  const selectTask = (id: string) => {
    setModal(null);
    if (demoRunning && id !== task.id) {
      clearInterval(demoTimer.current);
      setDemoRunning(false);
      d.updateTask(task.id, (t) => ({
        ...t,
        status: "paused",
        agents: t.agents.map((a) =>
          a.status === "running" ? { ...a, status: "queued" } : a,
        ),
      }));
    }
    const selected = state.tasks.find((t) => t.id === id);
    setSelectedProjectId(
      selected?.projectId ||
        state.projects?.find((p) => p.directory === selected?.project)?.id ||
        "",
    );
    d.select(id);
  };
  const runDemo = () => {
    if (blocking) {
      d.notify("Répondez à la décision bloquante pour reprendre la démo.");
      return;
    }
    if (demoRunning) return;
    const demoRunId = `demo-${uid()}`;
    const lifecycle = (
      type: Task["events"][number]["type"],
      title: string,
      agentId: string,
      life: "started" | "completed" | "blocked",
    ) => ({
      ...event(type, title, "Simulation de la mission d’exemple.", agentId),
      runId: demoRunId,
      lifecycle: life,
    });
    setDemoRunning(true);
    d.updateTask(task.id, (t) => ({
      ...t,
      status: "running",
      agents: t.agents.map((a) => ({
        ...a,
        status: a.id === "review" ? "queued" : "running",
        summary:
          a.id === "design"
            ? "Applique vos choix au support de cadrage."
            : a.summary,
      })),
      events: [
        ...t.events,
        lifecycle("phase", "La simulation reprend", "lead", "started"),
        lifecycle("agent", "Atlas démarre", "design", "started"),
        lifecycle("agent", "Nova démarre", "build", "started"),
      ],
    }));
    const asksQuestion = !task.questions.some((q) => q.id === "Q03");
    let step = 0;
    demoTimer.current = setInterval(() => {
      step++;
      if ((step === 3 && asksQuestion) || step >= 4) {
        clearInterval(demoTimer.current);
        setDemoRunning(false);
        if (step >= 4) d.notify("Démo terminée.");
      }
      d.updateTask(task.id, (t) => {
        const progressed = {
          ...t,
          agents: t.agents.map((a) => ({
            ...a,
            progress: Math.min(95, a.progress + (a.id === "review" ? 5 : 9)),
          })),
        };
        if (step === 1)
          return {
            ...progressed,
            events: [
              ...t.events,
              event(
                "tool",
                "Les choix sont intégrés au parcours",
                "Simulation : le support projets reflète votre décision.",
                "design",
              ),
            ],
          };
        if (step === 2)
          return {
            ...progressed,
            agents: progressed.agents.map((a) =>
              a.id === "design"
                ? {
                    ...a,
                    status: "done",
                    progress: 100,
                    summary: "Navigation alignée avec vos décisions.",
                  }
                : a,
            ),
            events: [
              ...t.events,
              lifecycle(
                "agent",
                "Atlas termine son passage",
                "design",
                "completed",
              ),
              event(
                "tool",
                "Nova construit les états",
                "Simulation de la construction des composants et des états.",
                "build",
              ),
            ],
          };
        if (step === 3 && asksQuestion) {
          return {
            ...progressed,
            status: "waiting",
            agents: progressed.agents.map((a) =>
              a.id === "build"
                ? {
                    ...a,
                    status: "blocked",
                    summary: "Un choix d’interaction attend votre retour.",
                  }
                : a,
            ),
            questions: [
              ...t.questions,
              {
                id: "Q03",
                title: "Comment signaler l’arrivée d’une question ?",
                context:
                  "Une nouvelle décision doit attirer l’attention, sans interrompre la lecture de la timeline.",
                recommendation:
                  "Une apparition douce avec un signal discret dans la mission.",
                options: [
                  {
                    id: "a",
                    label: "Transition douce",
                    description:
                      "La carte apparaît, le compteur s’anime. Le contexte reste visible.",
                  },
                  {
                    id: "b",
                    label: "Notification plus visible",
                    description:
                      "Un bandeau temporaire accompagne la nouvelle question.",
                  },
                ],
                blocking: true,
                unlocks:
                  "Débloque les transitions et la vérification des interactions.",
                agentId: "build",
                theme: "Interaction",
              },
            ],
            events: [
              ...t.events,
              event(
                "decision",
                "Une nouvelle question arrive",
                "Q03 · Choisir le signal d’arrivée des décisions.",
                "build",
              ),
              lifecycle(
                "agent",
                "Nova attend votre réponse",
                "build",
                "blocked",
              ),
              lifecycle(
                "agent",
                "Djinn attend votre réponse",
                "lead",
                "blocked",
              ),
            ],
          };
        }
        if (step === 3 && !asksQuestion)
          return {
            ...progressed,
            agents: progressed.agents.map((a) =>
              a.id === "review" ? { ...a, status: "running" } : a,
            ),
            events: [
              ...t.events,
              lifecycle(
                "agent",
                "Nova termine son passage",
                "build",
                "completed",
              ),
              lifecycle(
                "agent",
                "Echo vérifie le parcours",
                "review",
                "started",
              ),
            ],
          };
        if (step >= 4) {
          return {
            ...t,
            status: "idle",
            phase: "review",
            actions: [
              {
                id: "demo-review",
                kind: "manual",
                title: "Vérifier le résultat",
                detail:
                  "Ouvrez les supports dans Review et ajoutez vos retours.",
                status: "pending",
                createdAt: now(),
                updatedAt: now(),
                agentId: "review",
              },
            ],
            agents: t.agents.map((a) => ({
              ...a,
              status: "done",
              progress: 100,
              summary:
                a.id === "review"
                  ? "Le parcours de la simulation est prêt pour votre review."
                  : "Passage de démonstration terminé.",
            })),
            artifacts: [
              ...t.artifacts.filter((a) => a.id !== "demo-report"),
              {
                id: "demo-report",
                title: "Rapport de la simulation",
                type: "document",
                content:
                  "# Rapport de la mission d’exemple\n\nLes décisions ont été appliquées au support de cadrage. Les trois périmètres ont parcouru leurs états : attente, travail, décision et fin.\n\nCe rapport décrit une simulation de l’interface. Aucun code de projet, modèle payant ou test externe n’a été exécuté.\n\n## Votre prochaine étape\nAnnotez le support dans Review, puis validez le passage en livraison.",
                updatedAt: now(),
              },
            ],
            events: [
              ...t.events,
              lifecycle(
                "review",
                "Echo termine ses vérifications",
                "review",
                "completed",
              ),
              lifecycle(
                "agent",
                "Djinn intègre les contributions",
                "lead",
                "completed",
              ),
            ],
          };
        }
        return progressed;
      });
    }, 2200);
  };
  const pause = () => {
    clearInterval(demoTimer.current);
    setDemoRunning(false);
    if (task.demo)
      d.updateTask(task.id, (t) => ({
        ...t,
        events: [
          ...t.events,
          ...t.agents
            .filter((a) => a.status === "running")
            .map((a) => ({
              ...event(
                "agent",
                `${a.name} en pause`,
                "Simulation interrompue par vous.",
                a.id,
              ),
              lifecycle: "blocked" as const,
              runId: [...t.events]
                .reverse()
                .find((e) => e.agentId === a.id && e.lifecycle === "started")
                ?.runId,
            })),
        ],
      }));
    d.pause();
  };
  const action = () => {
    if (task.demo) {
      runDemo();
      return;
    }
    d.start(getNextRunMode(task));
  };
  const approveReview = () => {
    if (task.runId || demoRunning) {
      d.notify("Attendez la fin du passage en cours avant de valider.");
      return;
    }
    if (task.feedback.some((f) => !f.resolved)) {
      d.notify(
        "Résolvez les retours de review avant de préparer la livraison.",
      );
      return;
    }
    if (blocking) {
      d.notify("Répondez aux décisions bloquantes avant de valider la review.");
      return;
    }
    if (selectedStage?.type !== "review") {
      d.notify("La validation appartient à l’étape de review active.");
      return;
    }
    d.validateStepResult(selectedStage.id);
  };
  const mainActionLabel = task.demo
    ? "Lancer la démo"
    : getNextRunMode(task) === "plan"
      ? "Cadrer"
      : getNextRunMode(task) === "review"
        ? "Vérifier"
        : "Exécuter";
  const restitution = (
    <ActionsPanel
      key={`${mission.id}:${selectedStepId}`}
      actions={task.actions || []}
      artifacts={task.artifacts}
      onAction={d.performAction}
      busyIds={d.busyActionIds}
      readOnly={consulting}
      runOrder={task.events
        .map((event) => event.runId)
        .filter((id): id is string => !!id)}
      activeRunId={
        !consulting && mission.runId
          ? mission.activity?.activeAgents.at(-1)?.runId || mission.runId
          : undefined
      }
      onRecordTestResult={recordTestResult}
      onOpenArtifact={(id) => {
        setOpenedArtifactId(id);
        setArtifactRequestVersion((version) => version + 1);
        navigate("artifacts");
      }}
    />
  );
  const selectedProjectForNewMission = state.projects?.find(
    (project) => project.id === selectedProjectId,
  );
  const newMissionKey = `${selectedProjectId || "no-project"}:${
    selectedProjectForNewMission?.preferences?.provider ||
    state.settings.provider
  }:${
    selectedProjectForNewMission?.preferences?.model || state.settings.model
  }`;
  return (
    <div className={`app ${collapsed ? "sidebar-collapsed" : ""}`}>
      <aside className="sidebar">
        <div className="sidebar-brand">
          <button
            onClick={() => setCollapsed(!collapsed)}
            title={
              collapsed ? "Déplier la navigation" : "Réduire la navigation"
            }
          >
            <Brand small={collapsed} />
          </button>
          {!collapsed && <span className="version-tag">EARLY ACCESS</span>}
        </div>
        <button
          className="new-mission"
          onClick={() => setModal("new")}
          title="Nouvelle mission"
        >
          <Plus size={16} />
          {!collapsed && (
            <>
              <span>Nouvelle mission</span>
              <kbd>⌘ N</kbd>
            </>
          )}
        </button>
        <button
          className="sidebar-search"
          onClick={() => setModal("palette")}
          title="Recherche et commandes"
        >
          <Search size={15} />
          {!collapsed && (
            <>
              <span>Rechercher…</span>
              <kbd>⌘ K</kbd>
            </>
          )}
        </button>
        <ProjectSidebar
          projects={state.projects || []}
          tasks={state.tasks}
          selectedTaskId={task.id}
          selectedProjectId={selectedProjectId}
          collapsed={collapsed}
          onSelectTask={selectTask}
          onSelectProject={setSelectedProjectId}
          onNewProject={() => {
            setReturnToMissionStart(false);
            setEditingProjectId(undefined);
            setModal("project");
          }}
          onEditProject={(id) => {
            setReturnToMissionStart(false);
            setEditingProjectId(id);
            setModal("project");
          }}
          onNewMission={(id) => {
            setSelectedProjectId(id);
            setModal("new");
          }}
        />
        <button
          className="nav-item import-nav"
          onClick={() =>
            window.djinn ? d.importTask() : importRef.current?.click()
          }
          title="Importer une mission"
        >
          <Upload size={15} />
          {!collapsed && <span>Importer une mission</span>}
        </button>
        <div className="sidebar-bottom">
          <button
            className="profile"
            onClick={() => setModal("settings")}
            title="Connexions et préférences"
          >
            <span className="profile-avatar">C</span>
            {!collapsed && (
              <>
                <span>
                  Espace personnel
                  <small>
                    {window.djinn ? "Application locale" : "Aperçu navigateur"}
                  </small>
                </span>
                <Settings2 size={15} />
              </>
            )}
          </button>
        </div>
      </aside>
      <main className="main-shell">
        {modal === "new" ? (
          <NewMission
            key={newMissionKey}
            onClose={() => setModal(null)}
            provider={state.settings.provider}
            model={state.settings.model}
            projects={state.projects || []}
            selectedProjectId={selectedProjectId}
            onManageProjects={() => {
              setReturnToMissionStart(true);
              setEditingProjectId(undefined);
              setModal("project");
            }}
            onCreate={(title, brief, project, provider, model, options) => {
              if (demoRunning) {
                clearInterval(demoTimer.current);
                setDemoRunning(false);
                d.updateTask(task.id, (t) => ({
                  ...t,
                  status: "paused",
                  agents: t.agents.map((a) =>
                    a.status === "running" ? { ...a, status: "queued" } : a,
                  ),
                }));
              }
              d.addTask(title, brief, project, provider, model, options);
              setModal(null);
              d.notify("Djinn prépare la timeline de votre mission.");
            }}
            onToast={d.notify}
          />
        ) : mission.steps?.find((step) => step.id === mission.activeStepId)
            ?.type === "discussion" ? (
          <MissionPreparation
            task={mission}
            providerControl={
              <ProviderSwitch
                task={mission}
                providers={d.environment.providers}
                starting={d.starting}
                onSwitch={d.switchProvider}
              />
            }
            onPause={pause}
            onRetry={() => void d.start()}
            onAnswer={d.answer}
            onSend={d.indicate}
          />
        ) : (
          <>
            <header className="topbar">
              <div className="breadcrumbs">
                {(mission.projectSnapshot?.name || mission.project) && (
                  <span className="header-project" title={mission.project}>
                    {mission.projectSnapshot?.name ||
                      mission.project.split(/[\\/]/).filter(Boolean).at(-1)}
                    <ChevronRight size={12} aria-hidden="true" />
                  </span>
                )}
                <strong>{task.title.replace(/\n/g, " ")}</strong>
                {task.demo && <span className="badge tiny muted">EXEMPLE</span>}
              </div>
              <div className="topbar-actions">
                <button
                  className="question-bell"
                  aria-label="Questions et actions"
                  onClick={() => setNotificationsOpen(!notificationsOpen)}
                >
                  <Bell size={15} />
                  {questionCount + actionCount > 0 && (
                    <span>{questionCount + actionCount}</span>
                  )}
                </button>
                <button
                  className="connection-indicator"
                  onClick={() => setModal("settings")}
                >
                  <span
                    className={`status-dot ${d.environment.providers.some((p) => p.available) ? "green" : "neutral"}`}
                  />
                  {d.environment.providers.some((p) => p.available)
                    ? "CLI disponible"
                    : "Connexions"}
                  <ChevronDown size={12} />
                </button>
                <button
                  className="button secondary small"
                  onClick={d.exportTask}
                >
                  <Download size={14} />
                  <span>Partager</span>
                </button>
              </div>
            </header>
            <div className="mission-scroll" ref={scrollRef}>
              <MissionHeader
                task={mission}
                providerControl={
                  <ProviderSwitch
                    task={mission}
                    providers={d.environment.providers}
                    starting={d.starting}
                    onSwitch={d.switchProvider}
                  />
                }
              />
              <StepTimeline
                task={mission}
                onSelect={(id) => {
                  d.selectStep(id);
                  setAgentId(null);
                  setTab("overview");
                }}
                onStart={(id) => void d.start(undefined, mission.id, id)}
                onReopen={d.resumeStep}
                onFocus={d.focusStep}
                onConfigure={() => setModal("mission")}
                onAdd={() => setModal("next-step")}
                starting={d.starting}
              />
              <div className="mission-bar">
                <nav className="tabbar" aria-label="Vues de la mission">
                  {availableTabs.map((t) => (
                    <button
                      className={tab === t.id ? "active" : ""}
                      key={t.id}
                      onClick={() => navigate(t.id)}
                    >
                      <t.icon size={15} />
                      {t.label}
                      {t.id === "overview" && openQuestions.length + permissions.filter((p) => p.status === "pending").length > 0 && (
                        <span className="tab-count">
                          {openQuestions.length + permissions.filter((p) => p.status === "pending").length}
                        </span>
                      )}
                      {t.id === "artifacts" &&
                        task.artifacts.length + (task.actions?.length || 0) >
                          0 && (
                          <span className="tab-count muted">
                            {task.artifacts.length +
                              (task.actions?.length || 0)}
                          </span>
                        )}
                      {t.id === "review" &&
                        task.feedback.some((f) => !f.resolved) && (
                          <span className="tab-count">
                            {task.feedback.filter((f) => !f.resolved).length}
                          </span>
                        )}
                      {tab === t.id && (
                        <motion.span
                          layoutId="tab-underline"
                          className="tab-underline"
                          transition={{ duration: 0.23 }}
                        />
                      )}
                    </button>
                  ))}
                </nav>
                <AgentAvatars
                  task={mission}
                  agents={mission.agents}
                  onAgent={(selected) => {
                    setAgentPickerOpen(false);
                    setAgentId(selected.id);
                  }}
                  onOverflow={() => {
                    setAgentId(null);
                    setAgentPickerOpen(true);
                  }}
                />
                <div className="mission-controls">
                  {selectedStage?.type === "reflection" &&
                    selectedStage.id === mission.activeStepId && (
                      <button
                        className="text-button step-grill-action"
                        onClick={() =>
                          void d.indicate(
                            "Approfondis cette réflexion avec la skill grill-me : vérifie sa disponibilité, puis pose une seule question à la fois. Reste en réflexion, conserve les supports et ne démarre aucune implémentation.",
                          )
                        }
                      >
                        <Sparkles size={13} />
                        Préciser le besoin
                      </button>
                    )}
                  {task.runId || demoRunning ? (
                    <button className="button secondary small" onClick={pause}>
                      <Pause size={13} />
                      Mettre en pause
                    </button>
                  ) : null}
                  {mission.agents.some((a) => a.id === "lead") && (
                    <button
                      className="text-button mission-chief-action"
                      onClick={() => {
                        d.selectStep(mission.activeStepId!);
                        setTab("overview");
                        setAgentId("lead");
                      }}
                    >
                      <MessageSquare size={13} />
                      Parler au chef
                    </button>
                  )}
                  <div className="menu-wrap">
                    <button
                      className="icon-button"
                      onClick={() => setMenu(!menu)}
                      aria-label="Options de la mission"
                      aria-expanded={menu}
                    >
                      <Ellipsis size={18} />
                    </button>
                    {menu && (
                      <div className="dropdown">
                        <button
                          onClick={() => {
                            setModal("mission");
                            setMenu(false);
                          }}
                        >
                          <Settings2 size={14} />
                          Configurer la mission
                        </button>
                        <button
                          onClick={() => {
                            d.exportTask();
                            setMenu(false);
                          }}
                        >
                          <Download size={14} />
                          Exporter le contexte
                        </button>
                        <button
                          className="danger"
                          onClick={() => {
                            setModal("delete");
                            setMenu(false);
                          }}
                        >
                          <Trash2 size={14} />
                          Supprimer la mission
                        </button>
                      </div>
                    )}
                  </div>
                </div>
              </div>
              <div className="workspace">
                <div className="main-scroll">
                  <AnimatePresence mode="wait">
                    <motion.div
                      key={`${task.id}-${mission.selectedStepId}-${tab}`}
                      initial={{ opacity: 0, y: 8 }}
                      animate={{ opacity: 1, y: 0 }}
                      exit={{ opacity: 0, y: -5 }}
                      transition={{ duration: 0.2 }}
                      className="tab-content"
                    >
                      {tab === "overview" &&
                        selectedStage &&
                        ["awaiting_human", "completed"].includes(
                          selectedStage.status,
                        ) && (
                          <section className="step-result">
                            <span>Résultat · {selectedStage.title}</span>
                            <h3>
                              {selectedStage.needsRevalidation
                                ? "Résultat à revalider"
                                : selectedStage.status === "completed"
                                  ? selectedStage.validation === "automatic"
                                    ? "Résultat terminé · review à suivre"
                                    : "Résultat validé"
                                  : "Le passage est terminé"}
                            </h3>
                            <StageReport step={selectedStage} />
                            {selectedStage.status === "awaiting_human" && (
                              <button
                                className="button accent small"
                                disabled={
                                  !!mission.runId ||
                                  selectedStage.id !== mission.activeStepId ||
                                  task.questions.some(
                                    (q) => q.blocking && !q.answer,
                                  ) ||
                                  (selectedStage.type === "review" &&
                                    task.feedback.some((f) => !f.resolved))
                                }
                                onClick={() =>
                                  d.validateStepResult(selectedStage.id)
                                }
                              >
                                Valider ce résultat <Check size={14} />
                              </button>
                            )}
                            {selectedStage.approvedAt && (
                              <small>
                                Validé par vous le{" "}
                                {new Date(
                                  selectedStage.approvedAt,
                                ).toLocaleDateString("fr-FR")}{" "}
                                à {formatTime(selectedStage.approvedAt)}
                              </small>
                            )}
                            {mission.workflowMode === "flexible" &&
                              selectedStage.id === mission.activeStepId &&
                              selectedStage.id ===
                                mission.steps?.at(-1)?.id && (
                                <div className="next-step-suggestion">
                                  {mission.nextStepProposal?.stepId ===
                                  selectedStage.id ? (
                                    <>
                                      <strong>
                                        Suite proposée ·{" "}
                                        {mission.nextStepProposal.title}
                                      </strong>
                                      <p>{mission.nextStepProposal.reason}</p>
                                    </>
                                  ) : (
                                    <p>
                                      Vous pouvez terminer ici ou prolonger la
                                      discussion avec une nouvelle étape.
                                    </p>
                                  )}
                                  <button
                                    className="button secondary small"
                                    disabled={
                                      selectedStage.status !== "completed" ||
                                      !!mission.runId
                                    }
                                    onClick={() => setModal("next-step")}
                                  >
                                    Choisir la suite <Plus size={14} />
                                  </button>
                                </div>
                              )}
                          </section>
                        )}
                      {tab === "overview" && selectedStage && (selectedStage.report || selectedStage.summary) && !["awaiting_human", "completed"].includes(selectedStage.status) && (
                        <section className="step-result">
                          <StageReport step={selectedStage} />
                        </section>
                      )}
                      {tab === "overview" && (
                        <Overview
                          task={task}
                          onAnswer={d.answer}
                          onReopen={d.reopen}
                          onAgent={(a) => setAgentId(a.id)}
                          onTab={navigate}
                          onDemo={runDemo}
                          readOnly={consulting}
                          permissions={permissions}
                          onRespondPermission={respondPermission}
                          busyPermissionIds={ui.busyPermissionIds}
                          onSteer={d.indicate}
                          onResume={() => void d.start(undefined)}
                          workSummary={!consulting ? (
                            <MissionTestBoard task={mission} actions={mission.actions || []} onAction={(action) => {
                          void d.startTest(action);
                        }} renderActions={(action) => action.testStartedAt || (action.kind === "server" && ["pending", "stopped", "error"].includes(action.status)) ? (
                          <button className="button secondary small" onClick={() => d.openAction(mission.id, action.id)}>
                            {action.testStartedAt ? "Donner un retour" : "Préparer le test"}
                          </button>
                        ) : null} />
                          ) : undefined}
                          actions={restitution}
                        />
                      )}
                      {tab === "history" && (
                        <LegacyHistory
                          task={legacyView(mission)}
                          onAnswer={d.answer}
                        />
                      )}
                      {tab === "timeline" && (
                        <Timeline
                          task={task}
                          onAgent={(a) => setAgentId(a.id)}
                        />
                      )}
                      {(tab === "artifacts" || tab === "review") && (
                        <div className="artifact-wrapper">
                          <div className="panel-heading">
                            <h1>
                              {tab === "review" ? "Review" : "Restitution"}
                            </h1>
                            {tab === "review" && (
                              <div className="review-actions">
                                <button
                                  className="button secondary"
                                  onClick={() =>
                                    task.demo
                                      ? d.notify(
                                          "Les retours de la démo sont conservés dans la session partageable.",
                                        )
                                      : d.start("review")
                                  }
                                  disabled={
                                    consulting ||
                                    !!task.runId ||
                                    d.starting ||
                                    selectedStage?.status === "completed"
                                  }
                                >
                                  <RotateCcw size={14} />
                                  Envoyer les retours
                                </button>
                                <button
                                  className="button accent"
                                  onClick={approveReview}
                                  disabled={
                                    consulting ||
                                    selectedStage?.type !== "review" ||
                                    selectedStage.status !== "awaiting_human" ||
                                    !!mission.runId
                                  }
                                >
                                  <Check size={15} />
                                  Valider
                                  <ArrowRight size={14} />
                                </button>
                              </div>
                            )}
                          </div>
                          {tab === "artifacts" && restitution}
                          {(tab === "review" || openedArtifactId) && (
                            <ArtifactWorkspace
                              requestedArtifactId={openedArtifactId}
                              requestVersion={artifactRequestVersion}
                              task={task}
                              onUpdate={update}
                              onToast={d.notify}
                              review={tab === "review"}
                            />
                          )}
                          {tab === "artifacts" && !openedArtifactId && (
                            <p className="restitution-hint">
                              Ouvrez un support depuis son tour pour le
                              consulter ou l’éditer.
                            </p>
                          )}
                        </div>
                      )}
                      {tab === "delivery" && (
                        <Delivery
                          task={task}
                          onExport={d.exportTask}
                          onValidate={() =>
                            selectedStage &&
                            d.validateStepResult(selectedStage.id)
                          }
                          reviewRequired={
                            mission.steps?.some(
                              (s) =>
                                ["prototype", "implementation"].includes(
                                  s.type,
                                ) && s.status !== "pending",
                            ) || false
                          }
                          onToast={d.notify}
                          onReview={() => {
                            const stage = mission.steps?.find(
                              (s) =>
                                s.type === "review" && s.status !== "pending",
                            );
                            if (stage) {
                              d.selectStep(stage.id);
                              setTab("review");
                            }
                          }}
                        />
                      )}
                    </motion.div>
                  </AnimatePresence>
                </div>
              </div>
            </div>
            {tab !== "history" && (
              <MissionComposer
                key={`${task.id}-${mission.activeStepId}`}
                task={task}
                onSend={d.indicate}
                review={tab === "review"}
                consulting={consulting}
              />
            )}
            <footer className="app-statusbar">
              <span>
                <span className="status-dot green" />
                {!d.ready
                  ? "Chargement…"
                  : d.persistenceEnabled
                    ? "Sauvegarde locale"
                    : "Sauvegarde suspendue"}
              </span>
              <span>
                {task.demo
                  ? "MISSION D’EXEMPLE"
                  : task.project || "Aucun dossier sélectionné"}
              </span>
              <button onClick={() => setModal("palette")}>
                <Keyboard size={12} />
                Raccourcis<span>⌘ K</span>
              </button>
            </footer>
          </>
        )}
      </main>
      <AnimatePresence>
        {agentPickerOpen && (
          <AgentPickerDrawer
            task={mission}
            agents={mission.agents}
            onClose={() => setAgentPickerOpen(false)}
            onAgent={(selected) => {
              setAgentPickerOpen(false);
              setAgentId(selected.id);
            }}
          />
        )}
      </AnimatePresence>
      <AnimatePresence>
        {agent && (
          <>
            <motion.div
              className="drawer-backdrop"
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              onClick={() => setAgentId(null)}
            />
            <AgentDrawer
              key={`${chatTask.activeStepId}-${agent.id}`}
              agent={agent}
              task={chatTask}
              onClose={() => setAgentId(null)}
              onUpdate={update}
              onFilter={() => navigate("timeline")}
              onSend={
                consulting
                  ? async () => {
                      d.notify(
                        "Vous consultez une étape passée. Reprenez cette étape depuis la timeline avant d’envoyer un message.",
                      );
                      return false;
                    }
                  : d.indicate
              }
            />
          </>
        )}
      </AnimatePresence>
      <AnimatePresence>
        {modal === "project" && (
          <ModalFrame
            title={editingProjectId ? "Réglages du projet" : "Nouveau projet"}
            eyebrow="PROJET"
            onClose={() => {
              setModal(returnToMissionStart ? "new" : null);
              setReturnToMissionStart(false);
            }}
            wide
          >
            <ProjectSettings
              project={state.projects?.find((p) => p.id === editingProjectId)}
              defaultProvider={state.settings.provider}
              defaultModel={state.settings.model}
              onClose={() => {
                setModal(returnToMissionStart ? "new" : null);
                setReturnToMissionStart(false);
              }}
              onToast={d.notify}
              onSave={async (project) => {
                await d.saveProject(project);
                setSelectedProjectId(project.id);
                setModal(returnToMissionStart ? "new" : null);
                setReturnToMissionStart(false);
                d.notify(
                  "Projet enregistré. Les prochaines missions héritent de ses réglages.",
                );
              }}
            />
          </ModalFrame>
        )}
        {modal === "settings" && (
          <SettingsModal d={d} onClose={() => setModal(null)} />
        )}
        {modal === "next-step" && (
          <NextStepModal
            task={mission}
            onClose={() => setModal(null)}
            onAdd={(type, title, objective) => {
              if (d.addNextStep(type, title, objective)) setModal(null);
            }}
          />
        )}
        {modal === "mission" && (
          <MissionSettings
            task={mission}
            onUpdate={(t) =>
              d.updateTask(t.id, (live) =>
                live.runId
                  ? live
                  : {
                      ...live,
                      title: t.title,
                      titleSource: t.titleSource,
                      titleEditedAt: t.titleEditedAt,
                      brief: t.brief,
                      project: t.project,
                      projectSnapshot: t.projectSnapshot,
                      providerSessions:
                        live.project !== t.project ||
                        live.provider !== t.provider ||
                        live.model !== t.model
                          ? undefined
                          : live.providerSessions,
                      provider: t.provider,
                      model: t.model,
                      agents: t.agents,
                      configuration: t.configuration,
                      steps: t.steps,
                      activeStepId: t.activeStepId,
                      selectedStepId: t.selectedStepId,
                      reviewApprovedAt: t.reviewApprovedAt,
                      events: [
                        ...live.events,
                        {
                          ...event("note", "Configuration mise à jour"),
                          stepId: live.activeStepId,
                          actor: "human",
                        },
                      ],
                    },
              )
            }
            onToast={d.notify}
            onClose={() => setModal(null)}
          />
        )}
        {modal === "palette" && (
          <Palette
            tasks={state.tasks}
            onClose={() => setModal(null)}
            onSelect={(id) => {
              selectTask(id);
              setModal(null);
            }}
            onAction={(action) => {
              setModal(null);
              if (action === "new") setModal("new");
              else if (action === "settings") setModal("settings");
              else if (action === "export") d.exportTask();
              else if (action === "compose" || action === "feedback") {
                if (action === "feedback") navigate("review");
                setTimeout(
                  () =>
                    window.dispatchEvent(
                      new CustomEvent("djinn:compose", {
                        detail: { feedback: action === "feedback" },
                      }),
                    ),
                  200,
                );
              } else navigate(action);
            }}
          />
        )}
        {modal === "delete" && (
          <ModalFrame
            title="Supprimer cette mission ?"
            eyebrow="ESPACE LOCAL"
            onClose={() => setModal(null)}
          >
            <p className="modal-description">
              Les décisions, la timeline et les supports de «{" "}
              {task.title.replace(/\n/g, " ")} » seront retirés de cet espace.
              Exportez la mission pour en garder une copie.
            </p>
            <div className="modal-footer">
              <button className="button secondary" onClick={d.exportTask}>
                <Download size={14} />
                Exporter d’abord
              </button>
              <button
                className="button danger-button"
                disabled={!!task.runId}
                onClick={() => {
                  d.removeTask();
                  setModal(null);
                }}
              >
                <Trash2 size={14} />
                Supprimer
              </button>
            </div>
          </ModalFrame>
        )}
      </AnimatePresence>
      <AnimatePresence>
        {(notificationsOpen ||
          d.questionAlerts.length > 0 ||
          d.actionAlerts.length > 0) && (
          <motion.aside
            className="question-alerts"
            aria-label="Nouvelles questions et actions"
            role="region"
            initial={{ opacity: 0, y: -8, scale: 0.97 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -6, scale: 0.97 }}
          >
            <div className="question-alert-heading">
              <span>
                <Bell size={14} />
                Questions & actions
              </span>
              <button
                aria-label="Fermer les alertes"
                onClick={() => {
                  setNotificationsOpen(false);
                  d.questionAlerts.forEach((a) => d.dismissQuestionAlert(a.id));
                  d.actionAlerts.forEach((a) => d.dismissActionAlert(a.id));
                }}
              >
                <X size={14} />
              </button>
            </div>
            <div aria-live="polite">
              {[
                ...d.questionAlerts.map((a) => ({
                  ...a,
                  kind: "question" as const,
                })),
                ...d.actionAlerts.map((a) => ({
                  ...a,
                  kind: "action" as const,
                })),
                ...(notificationsOpen &&
                !d.questionAlerts.length &&
                !d.actionAlerts.length
                  ? state.tasks.flatMap((t) => [
                      ...t.questions
                        .filter((q) => !q.answer)
                        .map((q) => ({
                          id: `${t.id}:${q.id}`,
                          taskId: t.id,
                          questionId: q.id,
                          title: t.title,
                          body: q.title,
                          kind: "question" as const,
                        })),
                      ...(t.actions || [])
                        .filter((a) =>
                          ["pending", "ready", "error"].includes(a.status),
                        )
                        .map((a) => ({
                          id: `${t.id}:${a.id}`,
                          taskId: t.id,
                          actionId: a.id,
                          title: t.title,
                          body: a.title,
                          kind: "action" as const,
                        })),
                    ])
                  : []),
              ]
                .slice(0, 5)
                .map((alert) => (
                  <button
                    className="question-alert"
                    key={alert.id}
                    onClick={() => {
                      if (alert.kind === "question")
                        d.openQuestion(alert.taskId, alert.questionId);
                      else d.openAction(alert.taskId, alert.actionId);
                      setNotificationsOpen(false);
                    }}
                  >
                    <small>{alert.title}</small>
                    <strong>{alert.body}</strong>
                    <span>
                      {alert.kind === "question" ? "Répondre" : "Voir l’action"}
                      <ArrowUpRight size={13} />
                    </span>
                  </button>
                ))}
              {questionCount + actionCount === 0 &&
                !d.actionAlerts.length &&
                !state.tasks.some((t) =>
                  t.actions?.some((a) => a.status === "ready"),
                ) && <p>Rien en attente.</p>}
            </div>
          </motion.aside>
        )}
      </AnimatePresence>
      <AnimatePresence>
        {d.toast && (
          <motion.div
            role="status"
            className="toast"
            initial={{ opacity: 0, y: 20, scale: 0.96 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 10 }}
          >
            <span className="toast-symbol">✳</span>
            {d.toast}
            <button
              onClick={() => d.notify("")}
              aria-label="Fermer la notification"
            >
              <X size={14} />
            </button>
          </motion.div>
        )}
      </AnimatePresence>
      <input
        type="file"
        accept=".json,.djinn"
        hidden
        ref={importRef}
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) d.importTask(f);
          e.target.value = "";
        }}
      />
    </div>
  );
}
function MoreIcon() {
  return <span className="sidebar-label-line" />;
}
export function NextStepModal({
  task,
  onClose,
  onAdd,
}: {
  task: Task;
  onClose: () => void;
  onAdd: (type: StepType, title: string, objective: string) => void;
}) {
  const proposal =
    task.nextStepProposal?.stepId === task.activeStepId
      ? task.nextStepProposal
      : undefined;
  const [type, setType] = useState<StepType>(proposal?.type || "reflection");
  const [title, setTitle] = useState(proposal?.title || stepLabels.reflection);
  const [objective, setObjective] = useState(proposal?.objective || "");
  return (
    <ModalFrame
      title="Ajouter une étape"
      eyebrow="SUITE DE LA MISSION"
      onClose={onClose}
    >
      <p className="modal-description">
        La nouvelle étape est ajoutée à la timeline. Vous la lancerez quand vous
        le souhaiterez.
      </p>
      {proposal && (
        <p className="form-tip">Proposition de Djinn : {proposal.reason}</p>
      )}
      <div className="form-fields">
        <label>
          Type de la prochaine étape
          <select
            value={type}
            onChange={(e) => {
              const next = e.target.value as StepType;
              if (title === stepLabels[type]) setTitle(stepLabels[next]);
              setType(next);
            }}
          >
            {STEP_TYPES.filter((t) => t !== "discussion").map((t) => (
              <option key={t} value={t}>
                {stepLabels[t]}
              </option>
            ))}
          </select>
        </label>
        <label>
          Titre de la prochaine étape
          <input
            value={title}
            maxLength={1000}
            onChange={(e) => setTitle(e.target.value)}
          />
        </label>
        <label>
          Objectif de la prochaine étape
          <textarea
            value={objective}
            rows={3}
            onChange={(e) => setObjective(e.target.value)}
          />
        </label>
        {type === "specification" && (
          <p className="form-tip">
            Une spécification produit un document. Elle ne déclenche pas de code
            ni de prototype.
          </p>
        )}
      </div>
      <div className="modal-footer">
        <button className="button secondary" onClick={onClose}>
          Annuler
        </button>
        <button
          className="button accent"
          disabled={!title.trim() || !!task.runId}
          onClick={() => onAdd(type, title, objective)}
        >
          Ajouter à la timeline
        </button>
      </div>
    </ModalFrame>
  );
}

export function ProjectSourcesEditor({
  sources,
  onChange,
}: {
  sources: NonNullable<Project["sourcesOfTruth"]>;
  onChange: (sources: NonNullable<Project["sourcesOfTruth"]>) => void;
}) {
  return (
    <section className="project-sources-editor">
      <h3>Sources de vérité du projet</h3>
      <p className="form-tip">
        Documents de référence à lire avant de décider. Les chemins sont
        relatifs au projet ; la copie de chaque mission est conservée.
      </p>
      {sources.map((source, index) => (
        <fieldset key={source.id}>
          <legend>Référence {index + 1}</legend>
          <label>
            Nom de la référence
            <input
              value={source.title}
              maxLength={1000}
              onChange={(e) =>
                onChange(
                  sources.map((s) =>
                    s.id === source.id ? { ...s, title: e.target.value } : s,
                  ),
                )
              }
            />
          </label>
          <label>
            Chemin de la référence
            <input
              value={source.path}
              placeholder="docs/specification.md"
              onChange={(e) =>
                onChange(
                  sources.map((s) =>
                    s.id === source.id ? { ...s, path: e.target.value } : s,
                  ),
                )
              }
            />
          </label>
          <label>
            Rôle de la référence
            <input
              value={source.description}
              placeholder="Décisions produit acceptées"
              onChange={(e) =>
                onChange(
                  sources.map((s) =>
                    s.id === source.id
                      ? { ...s, description: e.target.value }
                      : s,
                  ),
                )
              }
            />
          </label>
          <button
            type="button"
            className="text-button"
            onClick={() => onChange(sources.filter((s) => s.id !== source.id))}
          >
            Retirer cette référence
          </button>
        </fieldset>
      ))}
      <button
        type="button"
        className="button secondary small"
        disabled={sources.length >= 100}
        onClick={() =>
          onChange([
            ...sources,
            { id: uid(), title: "", path: "", description: "" },
          ])
        }
      >
        <Plus size={13} /> Ajouter une référence
      </button>
    </section>
  );
}

export function NewMission({
  onClose,
  provider,
  model,
  onCreate,
  onToast,
  projects,
  selectedProjectId,
  onManageProjects,
}: {
  projects: Project[];
  selectedProjectId?: string;
  onManageProjects?: () => void;
  onClose: () => void;
  provider: ProviderId;
  model: string;
  onToast: (s: string) => void;
  onCreate: (
    title: string,
    brief: string,
    project: string,
    provider: ProviderId,
    model: string,
    options?: {
      projectRecord?: Project;
      steps?: MissionStep[];
      concurrency?: number;
      workflowMode?: Task["workflowMode"];
      autoWorkflow?: boolean;
    },
  ) => void;
}) {
  const chosen =
    projects.find((p) => p.id === selectedProjectId) || projects[0];
  const [brief, setBrief] = useState("");
  const [selectedProvider, setSelectedProvider] = useState<ProviderId>(
    chosen?.preferences?.provider || provider,
  );
  const [selectedModel, setSelectedModel] = useState(
    chosen?.preferences?.model ??
      (chosen?.preferences?.provider && chosen.preferences.provider !== provider
        ? ""
        : model),
  );
  const create = () => {
    if (!chosen || !brief.trim()) return;
    onCreate(
      "",
      brief.trim(),
      chosen.directory,
      selectedProvider,
      selectedModel,
      {
        projectRecord: chosen,
        concurrency: chosen.preferences?.concurrency || 3,
        steps: [createDiscussionStep(uid(), "discussion")],
        workflowMode: "flexible",
        autoWorkflow: true,
      },
    );
  };
  return (
    <section className="mission-start-screen" aria-label="Nouvelle mission">
      <header className="mission-start-header">
        <Brand />
        <button className="button secondary small" onClick={onClose}>
          Retour
        </button>
      </header>
      <div className="mission-start-content">
        <span className="eyebrow">NOUVELLE MISSION</span>
        <h1>Qu’allez-vous créer ?</h1>
        {chosen ? (
          <>
            <p className="mission-start-project">
              <FolderOpen size={16} /> {chosen.name}
            </p>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                create();
              }}
            >
              <label className="mission-prompt-label" htmlFor="mission-prompt">
                Votre intention
              </label>
              <textarea
                id="mission-prompt"
                className="mission-prompt"
                autoFocus
                value={brief}
                onChange={(event) => setBrief(event.target.value)}
                rows={7}
                maxLength={20000}
                placeholder="Décrivez le résultat que vous souhaitez. Djinn construira la timeline adaptée à votre demande."
                onKeyDown={(event) => {
                  if (
                    (event.metaKey || event.ctrlKey) &&
                    event.key === "Enter"
                  ) {
                    event.preventDefault();
                    create();
                  }
                }}
              />
              <div className="mission-model-selection">
                <label htmlFor="mission-provider">Fournisseur</label>
                <select
                  id="mission-provider"
                  value={selectedProvider}
                  onChange={(event) => {
                    const next = event.target.value as ProviderId;
                    setSelectedProvider(next);
                    setSelectedModel("");
                  }}
                >
                  <option value="codex">Codex</option>
                  <option value="claude">Claude Code</option>
                </select>
                <div className="mission-model-picker">
                  <span>Modèle</span>
                  <ModelPicker
                    provider={selectedProvider}
                    value={selectedModel}
                    onChange={setSelectedModel}
                  />
                </div>
              </div>
              <div className="mission-start-submit">
                <p>Le modèle choisi prépare votre mission.</p>
                <button
                  className="button accent"
                  type="submit"
                  disabled={!brief.trim()}
                >
                  Préparer la mission <ArrowRight size={16} />
                </button>
              </div>
            </form>
          </>
        ) : (
          <>
            <p>
              Créez un projet pour enregistrer son dossier et ses connaissances
              une seule fois.
            </p>
            <button className="button accent" onClick={onManageProjects}>
              Créer un projet <Plus size={16} />
            </button>
          </>
        )}
      </div>
    </section>
  );
}

export function MissionPreparation({
  task,
  providerControl,
  onPause,
  onRetry,
  onAnswer,
  onSend,
}: {
  task: Task;
  providerControl?: import("react").ReactNode;
  onPause: () => void;
  onRetry: () => void;
  onAnswer: (id: string, answer: string) => void;
  onSend: (value: string) => Promise<boolean>;
}) {
  const running = !!task.runId || task.status === "running";
  const questions = task.questions.filter((question) => !question.answer);
  return (
    <section
      className="mission-start-screen mission-preparation"
      aria-label="Préparation de la mission"
    >
      <header className="mission-start-header">
        <Brand />
        {providerControl || (
          <span>{task.provider === "claude" ? "Claude" : "Codex"}</span>
        )}
      </header>
      <div className="mission-start-content">
        <span className="eyebrow">VOTRE MISSION</span>
        <h1>
          {running
            ? "Djinn prépare votre timeline"
            : task.status === "error"
              ? "La préparation a été interrompue"
              : questions.length
                ? "Une précision pour votre timeline"
                : "Préparation en pause"}
        </h1>
        <p className="mission-start-project">
          <FolderOpen size={16} /> {task.projectSnapshot?.name || task.project}
        </p>
        <blockquote className="mission-intent">{task.brief}</blockquote>
        <p role="status">
          {running
            ? "L’agent choisit les étapes utiles à votre demande. La mission s’affichera lorsque le workflow sera défini."
            : "Votre demande est conservée. Vous pouvez compléter vos indications et reprendre la préparation."}
        </p>
        {task.status === "error" && (
          <p className="mission-preparation-error">
            {task.events.filter((event) => event.type === "error").at(-1)
              ?.detail ||
              "Consultez l’historique de ce passage si l’erreur persiste."}
          </p>
        )}
        {questions.map((question) => (
          <QuestionCard
            key={question.id}
            question={question}
            onAnswer={(value) => onAnswer(question.id, value)}
            onReopen={() => {}}
          />
        ))}
        {running ? (
          <button className="button secondary" onClick={onPause}>
            <Pause size={15} /> Mettre en pause
          </button>
        ) : (
          <button
            className="button accent"
            disabled={questions.some((q) => q.blocking)}
            onClick={onRetry}
          >
            <Play size={15} /> Reprendre la préparation
          </button>
        )}
      </div>
      <MissionComposer task={task} onSend={onSend} review={false} />
    </section>
  );
}
function SettingsModal({
  d,
  onClose,
}: {
  d: ReturnType<typeof useDjinn>;
  onClose: () => void;
}) {
  const [connecting, setConnecting] = useState<ProviderId | null>(null);
  const [loginMessage, setLoginMessage] = useState("");
  const [loadingDemo, setLoadingDemo] = useState(false);
  const demoLoader = (
    d as unknown as {
      loadDemo?: () => void | Promise<void>;
    }
  ).loadDemo;
  const login = async (p: ProviderId) => {
    if (!window.djinn) {
      d.notify(
        "La connexion à votre abonnement nécessite l’application Electron.",
      );
      return;
    }
    setConnecting(p);
    setLoginMessage(
      "La connexion s’ouvre dans votre navigateur. Suivez les instructions du fournisseur.",
    );
    try {
      const result = await window.djinn.loginProvider(p);
      if (result && typeof result === "object") {
        const r = result as Record<string, unknown>;
        if (r.url)
          setLoginMessage(
            `Ouvrez ${String(r.url)} pour terminer la connexion.`,
          );
      }
      await d.refreshEnvironment();
    } catch (err) {
      setLoginMessage((err as Error).message);
      d.notify((err as Error).message);
    } finally {
      setConnecting(null);
    }
  };
  const loadDemo = async () => {
    if (!demoLoader || loadingDemo) return;
    setLoadingDemo(true);
    try {
      await demoLoader();
      d.notify("La démo complète est chargée.");
      onClose();
    } catch (error) {
      d.notify((error as Error).message || "Impossible de charger la démo.");
    } finally {
      setLoadingDemo(false);
    }
  };
  return (
    <ModalFrame
      title="Connexions"
      eyebrow="CONNEXIONS & PRÉFÉRENCES"
      onClose={onClose}
      wide
    >
      <p className="modal-description">
        Utilise les abonnements et connexions de vos CLI.
      </p>
      <div className="provider-settings">
        {d.environment.providers.map((p) => (
          <div className="provider-setting" key={p.id}>
            <span className="provider-glyph">
              {p.id === "codex" ? "⬡" : "✳"}
            </span>
            <div>
              <h3>{p.name}</h3>
              <span className="muted-text">
                {!p.available
                  ? "CLI non détectée"
                  : p.authenticated
                    ? "Abonnement connecté"
                    : p.authenticated === null
                      ? "CLI détectée · connexion à vérifier"
                      : "Connexion nécessaire"}
              </span>
              {p.version && <small>{p.version}</small>}
            </div>
            <span
              className={`status-dot ${p.available ? "green" : "neutral"}`}
            />
            <button
              className="button secondary small"
              disabled={!p.available || connecting !== null}
              onClick={() => login(p.id)}
            >
              {connecting === p.id
                ? "Connexion…"
                : p.authenticated
                  ? "Reconnecter"
                  : "Connecter"}
              <ArrowUpRight size={13} />
            </button>
          </div>
        ))}
      </div>
      {(d.authStatus || loginMessage) && (
        <p className="login-message">{d.authStatus || loginMessage}</p>
      )}
      <div className="cli-help">
        <Terminal size={16} />
        <div>
          <strong>Installer une CLI</strong>
          <p>
            Codex : <code>npm install -g @openai/codex</code>
            <br />
            Claude Code : <code>npm install -g @anthropic-ai/claude-code</code>
          </p>
          <span>
            Authentifiez-vous ensuite avec <code>codex login</code> ou{" "}
            <code>claude auth login</code>.
          </span>
        </div>
      </div>
      <button className="text-button refresh" onClick={d.refreshEnvironment}>
        <RotateCcw size={14} />
        Actualiser les connexions
      </button>
      <div className="settings-divider" />
      <div className="setting-row">
        <div>
          <strong>Fournisseur par défaut</strong>
          <p>Pour vos prochaines missions.</p>
        </div>
        <select
          value={d.state.settings.provider}
          onChange={(e) =>
            d.setState((s) => ({
              ...s,
              settings: {
                ...s.settings,
                provider: e.target.value as ProviderId,
              },
            }))
          }
        >
          <option value="codex">Codex</option>
          <option value="claude">Claude Code</option>
        </select>
      </div>
      <div className="setting-row">
        <div>
          <strong>Réduire les animations</strong>
          <p>Conserver les transitions essentielles avec moins de mouvement.</p>
        </div>
        <button
          role="switch"
          aria-checked={d.state.settings.reduceMotion}
          className={`toggle ${d.state.settings.reduceMotion ? "on" : ""}`}
          onClick={() =>
            d.setState((s) => ({
              ...s,
              settings: {
                ...s.settings,
                reduceMotion: !s.settings.reduceMotion,
              },
            }))
          }
        >
          <span />
        </button>
      </div>
      <div className="setting-row">
        <div>
          <strong>Notifications</strong>
          <p>
            {window.djinn
              ? "Nouvelles questions sur macOS"
              : d.browserPermission === "granted"
                ? "Notifications activées"
                : "Alertes dans Djinn ; notifications du navigateur en option."}
          </p>
          {d.notificationStatus && (
            <p className="notification-status">{d.notificationStatus}</p>
          )}
        </div>
        <button
          className="button secondary small"
          onClick={d.enableNotifications}
        >
          {window.djinn ? "Tester" : "Activer"}
        </button>
      </div>
      <div className="setting-row setting-row-demo">
        <div>
          <strong>Démo complète</strong>
          <p>
            Ajouter les supports interactifs de démonstration à l’espace local.
          </p>
        </div>
        <button
          className="button secondary small"
          disabled={!demoLoader || loadingDemo}
          title={
            demoLoader
              ? "Charger la démo complète"
              : "Disponible dans l’application locale"
          }
          onClick={() => void loadDemo()}
        >
          {loadingDemo ? "Chargement…" : "Charger la démo complète"}
        </button>
      </div>
      <div className="settings-foot">
        <button
          className="button secondary small"
          onClick={() => void d.exportMissionJournal()}
        >
          Exporter le journal de la mission
        </button>
        <Brand small />
        <span>Djinn {d.environment.appVersion} · Dark mode · Local first</span>
      </div>
    </ModalFrame>
  );
}
function MissionSettings({
  task,
  onUpdate,
  onToast,
  onClose,
}: {
  task: Task;
  onUpdate: (t: Task) => void;
  onToast: (s: string) => void;
  onClose: () => void;
}) {
  const [draft, setDraft] = useState(task);
  const [titleEdited, setTitleEdited] = useState(false);
  const title = titleEdited ? draft.title : task.title;
  const startedSteps = (task.steps || []).filter((s) => s.status !== "pending");
  const startedIds = new Set(startedSteps.map((s) => s.id));
  const editableSteps = [
    ...startedSteps,
    ...(draft.steps || []).filter((s) => !startedIds.has(s.id)),
  ];
  const pick = async () => {
    try {
      const p = await window.djinn?.selectDirectory();
      if (p) setDraft({ ...draft, project: p });
    } catch (e) {
      onToast((e as Error).message);
    }
  };
  const updateAgent = (agentId: string, patch: Partial<AgentScheduling>) =>
    setDraft({
      ...draft,
      agents: draft.agents.map((agent) =>
        agent.id === agentId ? { ...agent, ...patch } : agent,
      ),
    });
  const workerAgents = draft.agents.filter((agent) => agent.id !== "lead");
  return (
    <ModalFrame
      title="Configuration"
      eyebrow="CONFIGURATION DE LA MISSION"
      onClose={onClose}
    >
      {task.projectSnapshot && (
        <details className="project-context">
          <summary>
            Conventions de {task.projectSnapshot.name} utilisées par cette
            mission
          </summary>
          <small>
            Copie du{" "}
            {new Date(task.projectSnapshot.capturedAt).toLocaleDateString(
              "fr-FR",
            )}{" "}
            · {task.projectSnapshot.directory}
          </small>
          <MarkdownBody
            text={
              task.projectSnapshot.conventions || "Aucune convention définie."
            }
          />
          {Object.entries(task.projectSnapshot.locations).map(
            ([usage, location]) => (
              <p key={usage}>
                {usage} · <code>{location}</code>
              </p>
            ),
          )}
          {task.projectSnapshot.sourcesOfTruth?.map((source) => (
            <p key={source.id}>
              <strong>{source.title}</strong> · <code>{source.path}</code>
              <br />
              {source.description}
            </p>
          ))}
        </details>
      )}
      <div className="form-fields">
        <label>
          Nom
          <input
            value={title}
            onChange={(e) => {
              setTitleEdited(true);
              setDraft({ ...draft, title: e.target.value });
            }}
          />
        </label>
        <label>
          Intention
          <textarea
            rows={3}
            value={draft.brief}
            onChange={(e) => setDraft({ ...draft, brief: e.target.value })}
          />
        </label>
        <label>
          Dossier de travail
          <div className="directory-field">
            <input
              value={draft.project}
              onChange={(e) => setDraft({ ...draft, project: e.target.value })}
            />
            <button onClick={pick} aria-label="Choisir le dossier">
              <FolderOpen size={16} />
            </button>
          </div>
        </label>
        <div className="form-two">
          <label>
            Prototypes
            <input
              value={draft.configuration.prototype}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  configuration: {
                    ...draft.configuration,
                    prototype: e.target.value,
                  },
                })
              }
            />
          </label>
          <label>
            Process de review
            <input
              value={draft.configuration.review}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  configuration: {
                    ...draft.configuration,
                    review: e.target.value,
                  },
                })
              }
            />
          </label>
        </div>
        <div className="form-two">
          <label>
            Fournisseur
            <select
              value={draft.provider}
              onChange={(e) => {
                const provider = e.target.value as ProviderId;
                setDraft((current) =>
                  current.provider === provider
                    ? current
                    : { ...current, provider, model: "" },
                );
              }}
            >
              <option value="codex">Codex</option>
              <option value="claude">Claude Code</option>
            </select>
          </label>
          <div className="model-picker-field">
            <span className="model-picker-label">Modèle</span>
            <ModelPicker
              provider={draft.provider}
              value={draft.model}
              onChange={(model) =>
                setDraft((current) => ({ ...current, model }))
              }
            />
          </div>
        </div>
        <ConcurrencyField
          value={draft.configuration.concurrency}
          onChange={(n) =>
            setDraft({
              ...draft,
              configuration: { ...draft.configuration, concurrency: n },
            })
          }
        />
        <section className="agent-scope-settings">
          <h3>Périmètres des workers</h3>
          <p className="form-tip scope-intro">
            Les workers dont les périmètres sont explicitement disjoints peuvent
            avancer ensemble jusqu’au plafond ci-dessus. Un périmètre absent ou
            vide reste exclusif par prudence. Les dépendances sont des IDs de
            workers et ne lancent aucune activité à elles seules.
          </p>
          <div className="scope-supervisor-note">
            <strong>Djinn · lecture seule pendant le travail</strong>
            <span>
              Il supervise les workers puis intègre après leurs sorties.
            </span>
          </div>
          {workerAgents.length ? (
            workerAgents.map((agent) => {
              const value = scheduling(agent);
              const scope = value.writeScope || [];
              const dependencies = value.dependsOn || [];
              const isReadOnly = agentIsReadOnly(agent);
              const dependencyIssues = dependencies.filter(
                (id) =>
                  id === agent.id ||
                  !draft.agents.some((candidate) => candidate.id === id),
              );
              const scopeIssues = scope.map(scopeSyntaxHint).filter(Boolean);
              return (
                <fieldset
                  className="agent-scope-editor"
                  key={agent.id}
                  disabled={!!task.runId}
                >
                  <legend>
                    {agent.name} <span>{agent.id}</span>
                  </legend>
                  <label>
                    Scopes proposés (un chemin relatif par ligne)
                    <textarea
                      rows={2}
                      value={scope.join("\n")}
                      disabled={!!task.runId || isReadOnly}
                      placeholder="src/features/projects"
                      onChange={(e) =>
                        updateAgent(agent.id, {
                          writeScope: e.target.value
                            .split("\n")
                            .map((entry) => entry.trim().replaceAll("\\", "/"))
                            .filter(Boolean),
                        })
                      }
                    />
                    <small className="scope-help">
                      {isReadOnly
                        ? "Lecture seule · aucune écriture attendue."
                        : scope.length
                          ? `${scope.length} chemin${scope.length > 1 ? "s" : ""} déclaré${scope.length > 1 ? "s" : ""}.`
                          : "Aucun scope déclaré → écriture réservée par prudence."}
                    </small>
                    {scopeIssues.map((issue, index) => (
                      <small
                        className="scope-validation"
                        key={`${agent.id}-scope-${index}`}
                      >
                        Vérification informative : {issue}
                      </small>
                    ))}
                  </label>
                  <label>
                    Dépend de (IDs, séparés par des virgules)
                    <input
                      value={dependencies.join(", ")}
                      disabled={!!task.runId}
                      placeholder="worker-a, worker-b"
                      onChange={(e) =>
                        updateAgent(agent.id, {
                          dependsOn: e.target.value
                            .split(",")
                            .map((entry) => entry.trim())
                            .filter(Boolean),
                        })
                      }
                    />
                    {dependencyIssues.length > 0 && (
                      <small className="scope-validation">
                        Vérification informative : ID inconnu ou auto-dépendance
                        : {dependencyIssues.join(", ")}.
                      </small>
                    )}
                  </label>
                </fieldset>
              );
            })
          ) : (
            <p className="scope-help">
              Les workers apparaîtront après le cadrage.
            </p>
          )}
          {task.runId && (
            <p className="form-tip scope-active-note">
              Passage actif : ces propositions sont consultables ici et restent
              verrouillées jusqu’à la fin du passage.
            </p>
          )}
        </section>
        <WorkflowEditor
          steps={editableSteps}
          onChange={(steps) => setDraft({ ...draft, steps })}
          disabled={
            !!task.runId || task.projectSnapshot?.workflowPolicy === "enforced"
          }
          allowAdd={
            task.workflowMode !== "flexible" ||
            (task.steps?.at(-1)?.status === "completed" &&
              !task.steps?.at(-1)?.needsRevalidation)
          }
        />
        {task.projectSnapshot?.workflowPolicy === "enforced" && (
          <p className="form-tip">
            Cette mission conserve le workflow imposé par son projet.
          </p>
        )}
        {task.runId && (
          <p className="form-tip">
            La configuration pourra être enregistrée à la fin du passage actif.
          </p>
        )}
        <label>
          Livrables souhaités
          <input
            value={draft.configuration.deliverables.join(", ")}
            onChange={(e) =>
              setDraft({
                ...draft,
                configuration: {
                  ...draft.configuration,
                  deliverables: e.target.value
                    .split(",")
                    .map((s) => s.trim())
                    .filter(Boolean),
                },
              })
            }
          />
        </label>
      </div>
      <div className="modal-footer">
        <button className="button secondary" onClick={onClose}>
          Annuler
        </button>
        <button
          className="button accent"
          disabled={
            !title.trim() ||
            !!task.runId ||
            editableSteps.some((s) => !s.title.trim())
          }
          onClick={() => {
            onUpdate({
              ...task,
              title,
              brief: draft.brief,
              project: draft.project,
              projectSnapshot:
                draft.project !== task.project && task.projectSnapshot
                  ? {
                      ...task.projectSnapshot,
                      directory: draft.project,
                      capturedAt: now(),
                    }
                  : task.projectSnapshot,
              providerSessions:
                draft.project !== task.project ||
                draft.provider !== task.provider ||
                draft.model !== task.model
                  ? undefined
                  : task.providerSessions,
              provider: draft.provider,
              model: draft.model,
              agents: draft.agents.map((agent) => {
                const value = scheduling(agent);
                return {
                  ...agent,
                  ...(value.writeScope !== undefined
                    ? {
                        writeScope: value.writeScope
                          .map((entry) => entry.trim().replaceAll("\\", "/"))
                          .filter(Boolean),
                      }
                    : {}),
                  ...(value.dependsOn !== undefined
                    ? {
                        dependsOn: value.dependsOn
                          .map((entry) => entry.trim())
                          .filter(Boolean),
                      }
                    : {}),
                };
              }),
              configuration: draft.configuration,
              activeStepId: editableSteps.some(
                (s) => s.id === task.activeStepId,
              )
                ? task.activeStepId
                : editableSteps.find((s) => s.status !== "completed")?.id ||
                  editableSteps.at(-1)?.id,
              selectedStepId: editableSteps.some(
                (s) => s.id === task.selectedStepId,
              )
                ? task.selectedStepId
                : editableSteps.find((s) => s.status !== "completed")?.id ||
                  editableSteps.at(-1)?.id,
              steps: editableSteps.map(
                (s) =>
                  task.steps?.find(
                    (live) => live.id === s.id && live.status !== "pending",
                  ) || {
                    ...s,
                    exitCriteria: s.exitCriteria.filter((v) => v.trim()),
                    expectedArtifacts: s.expectedArtifacts.filter(Boolean),
                    skills: s.skills.filter(Boolean),
                  },
              ),
              titleSource:
                titleEdited && title !== task.title
                  ? "human"
                  : task.titleSource,
              titleEditedAt:
                titleEdited && title !== task.title
                  ? now()
                  : task.titleEditedAt,
              reviewApprovedAt: undefined,
              events: [
                ...task.events,
                event("note", "Configuration mise à jour"),
              ],
            });
            onToast("Configuration enregistrée.");
            onClose();
          }}
        >
          Enregistrer
          <Check size={15} />
        </button>
      </div>
    </ModalFrame>
  );
}
function Palette({
  tasks,
  onClose,
  onSelect,
  onAction,
}: {
  tasks: Task[];
  onClose: () => void;
  onSelect: (id: string) => void;
  onAction: (id: string) => void;
}) {
  const [query, setQuery] = useState("");
  const actions = [
    { id: "new", label: "Créer une nouvelle mission", icon: Plus, key: "⌘ N" },
    {
      id: "compose",
      label: "Donner une indication",
      icon: MessageSquare,
      key: "⌘ J",
    },
    {
      id: "feedback",
      label: "Faire un retour",
      icon: MessageSquare,
      key: "⌘ ⇧ J",
    },
    { id: "timeline", label: "Ouvrir la timeline", icon: Activity, key: "⌘ 2" },
    {
      id: "review",
      label: "Ouvrir la review visuelle",
      icon: MessageSquare,
      key: "⌘ 4",
    },
    { id: "export", label: "Partager", icon: Download },
    { id: "settings", label: "Connexions et préférences", icon: Settings2 },
  ];
  const filtered = actions.filter((a) =>
    a.label.toLowerCase().includes(query.toLowerCase()),
  );
  return (
    <ModalFrame title="Rechercher" eyebrow="COMMANDES / ⌘ K" onClose={onClose}>
      <div className="palette-search">
        <Search size={18} />
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Une mission, une action…"
          aria-label="Rechercher une commande"
        />
      </div>
      <div className="palette-section">ACTIONS</div>
      <div className="palette-list">
        {filtered.map((a) => (
          <button key={a.id} onClick={() => onAction(a.id)}>
            <a.icon size={16} />
            {a.label}
            <span>{a.key || "↗"}</span>
          </button>
        ))}
      </div>
      <div className="palette-section">MISSIONS</div>
      <div className="palette-list">
        {tasks
          .filter((t) => t.title.toLowerCase().includes(query.toLowerCase()))
          .map((t) => (
            <button key={t.id} onClick={() => onSelect(t.id)}>
              <Flag size={15} />
              {t.title.replace(/\n/g, " ")}
              <span>↗</span>
            </button>
          ))}
      </div>
      {!filtered.length &&
        !tasks.some((t) =>
          t.title.toLowerCase().includes(query.toLowerCase()),
        ) && <p className="muted-text">Aucun résultat pour cette recherche.</p>}
      <div className="palette-footer">
        <kbd>Tab</kbd> pour naviguer<kbd>Entrée</kbd> pour ouvrir<kbd>Esc</kbd>{" "}
        pour fermer
      </div>
    </ModalFrame>
  );
}
function LegacyHistory({
  task,
  onAnswer,
}: {
  task: Task;
  onAnswer: (id: string, value: string) => void;
}) {
  return (
    <div className="panel-content">
      <div className="panel-heading">
        <h1>Historique v1 — étape inconnue</h1>
      </div>
      <p className="muted-text">
        Ces éléments sont conservés tels qu’ils ont été enregistrés. Leur étape,
        leur démarrage et leur validation ne peuvent pas être prouvés.
      </p>
      <Timeline task={task} onAgent={() => {}} />
      {task.questions.map((q) => (
        <article className="step-result-card" key={q.id}>
          <h2>{q.title}</h2>
          <MarkdownBody text={q.context} />
          {!q.answer && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                const answer = String(
                  new FormData(e.currentTarget).get("answer") || "",
                ).trim();
                if (answer) onAnswer(q.id, answer);
              }}
            >
              <label>
                Réponse conservée dans l’historique
                <textarea name="answer" required />
              </label>
              <button className="button secondary small" type="submit">
                Répondre
              </button>
            </form>
          )}
          <p>{q.answer || "Aucune réponse enregistrée."}</p>
        </article>
      ))}
      {task.artifacts.map((a) => (
        <article className="step-result-card" key={a.id}>
          <h2>{a.title}</h2>
          {a.type === "document" ? (
            <MarkdownBody text={a.content} />
          ) : (
            <pre>{a.content}</pre>
          )}
        </article>
      ))}
      {task.feedback.map((f) => (
        <p key={f.id}>Retour conservé : {f.text}</p>
      ))}
      {task.instructions?.map((i) => (
        <p key={i.id}>Indication conservée : {i.text}</p>
      ))}
    </div>
  );
}
function Delivery({
  task,
  onExport,
  onValidate,
  reviewRequired,
  onToast,
  onReview,
}: {
  task: Task;
  onExport: () => void;
  onValidate: () => void;
  reviewRequired: boolean;
  onToast: (s: string) => void;
  onReview: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const [saving, setSaving] = useState(false);
  const reviewed = !reviewRequired || !!task.reviewApprovedAt;
  const current = task.steps?.find((s) => s.id === task.activeStepId);
  const canValidate =
    current?.type === "delivery" && current.status === "awaiting_human";
  const decisions = task.questions.filter((q) => q.answer);
  const summary = `# ${task.title.replace(/\n/g, " ")}\n\n${task.brief}\n\n## Décisions\n${decisions.map((q) => `- ${q.id} — ${q.title} : ${q.answer}`).join("\n") || "Aucune décision enregistrée."}\n\n## Contributions\n${task.agents.map((a) => `- ${a.name} (${a.role}) — ${a.summary}`).join("\n")}\n\n## Preuves et supports\n${task.artifacts.map((a) => `- ${a.title} (${a.type})`).join("\n")}\n\n## Review\n${task.feedback.map((f) => `- [${f.resolved ? "x" : " "}] ${f.text}`).join("\n") || "Aucun retour visuel."}\n\n## Validation\n${!reviewRequired ? "Mission sans code : validation humaine du résultat." : reviewed ? "Review validée par le pilote." : "Review à valider."}\n${task.demo ? "Mission d’exemple : simulation, aucun code de projet exécuté." : ""}\n\n## Chronologie\n${task.events.map((e) => `- ${e.time} — ${e.title}\n  ${e.detail.slice(0, 3000)}`).join("\n")}\n`;
  const save = async (name: string, content: string) => {
    setSaving(true);
    try {
      if (window.djinn) {
        const result = await window.djinn.saveArtifact({ name, content });
        if (result) onToast("Livrable enregistré.");
      } else {
        download(name, content, "text/markdown");
        onToast("Livrable téléchargé.");
      }
    } catch (e) {
      onToast((e as Error).message);
    } finally {
      setSaving(false);
    }
  };
  const cards = [
    {
      id: "session",
      number: "01",
      title: "Session",
      description: "Décisions, agents, supports et timeline.",
      label: "Exporter .djinn.json",
      icon: PackageCheck,
      action: onExport,
    },
    {
      id: "summary",
      number: "02",
      title: "Synthèse",
      description: "Objectif, choix et preuves.",
      label: "Enregistrer la synthèse",
      icon: FileText,
      action: () => save("djinn-synthese.md", summary),
    },
    {
      id: "mr",
      number: "03",
      title: "Brouillon de MR",
      description: "Changements rapportés et validations.",
      label: "Préparer le brouillon",
      icon: GitBranch,
      action: () =>
        save(
          "djinn-mr-draft.md",
          `# ${task.title.replace(/\n/g, " ")}\n\n## Objectif\n${task.brief}\n\n## Décisions appliquées\n${decisions.map((q) => `- ${q.answer}`).join("\n")}\n\n## Implementation et preuves\n${
            task.events
              .filter((e) => e.type === "tool" || e.type === "review")
              .map((e) => `- ${e.title}\n${e.detail}`)
              .join("\n") || "À compléter avec les preuves de l’agent."
          }\n\n## Validation humaine\n${reviewed ? "Review validée." : "À effectuer."}\n\n${task.demo ? "Brouillon issu d’une démonstration, à ne pas publier comme compte rendu de code réel." : ""}`,
        ),
    },
  ];
  return (
    <div className="panel-content delivery-panel">
      <div className="panel-heading">
        <h1>Livrables</h1>
      </div>
      <div className={`delivery-validation ${reviewed ? "ready" : ""}`}>
        <ShieldCheck size={23} />
        <div>
          <h3>
            {!reviewRequired
              ? "Résultat à valider"
              : reviewed
                ? "Review validée"
                : "Review à valider"}
          </h3>
          <p>
            {reviewed
              ? "Les livrables reprennent les décisions et preuves."
              : "Résolvez les retours avant de terminer."}
          </p>
        </div>
        {!reviewed && (
          <button className="button secondary small" onClick={onReview}>
            Ouvrir la review
            <ArrowUpRight size={14} />
          </button>
        )}
        {reviewed && <CheckCircle2 size={20} />}
      </div>
      <div className="delivery-grid">
        {cards.map((c) => (
          <article className="delivery-card" key={c.id}>
            <div>
              <span className="delivery-number">/{c.number}</span>
              <c.icon size={24} />
            </div>
            <h2>{c.title}</h2>
            <p>{c.description}</p>
            <button
              className="button secondary"
              onClick={c.action}
              disabled={saving}
            >
              {c.label}
              <Download size={14} />
            </button>
          </article>
        ))}
      </div>
      <div className="delivery-footer">
        <span className="muted-text">
          Publication distante et création de MR restent des actions explicites.
        </span>
        <button
          className="button accent"
          disabled={
            !reviewed || !canValidate || task.status === "done" || !!task.runId
          }
          onClick={() => {
            onValidate();
          }}
        >
          {task.status === "done" ? "Mission terminée" : "Terminer la mission"}
          <Check size={15} />
        </button>
      </div>
    </div>
  );
}

function MissionComposer({
  task,
  onSend,
  review,
  consulting = false,
}: {
  task: Task;
  onSend: (value: string) => Promise<boolean>;
  review: boolean;
  consulting?: boolean;
}) {
  // useReducedMotion observes data-motion and prefers-reduced-motion: reduce.
  const reducedMotion = useReducedMotion();
  const [value, setValue] = useState("");
  const [sending, setSending] = useState(false);
  const [history, setHistory] = useState(false);
  const [open, setOpen] = useState(false);
  const [feedback, setFeedback] = useState(review);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const capsuleRef = useRef<HTMLButtonElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);
  const pending = (task.instructions || []).filter(
    (i) => i.status === "queued" || (!i.status && !i.appliedAt),
  ).length;
  const restoreFocus = () => {
    requestAnimationFrame(() => {
      const previous = previousFocus.current;
      const target = previous?.isConnected ? previous : capsuleRef.current;
      target?.focus({ preventScroll: true });
    });
  };
  const expand = (isFeedback = review) => {
    previousFocus.current = document.activeElement as HTMLElement;
    setFeedback(isFeedback);
    setOpen(true);
  };
  const collapse = () => {
    setOpen(false);
    setHistory(false);
    restoreFocus();
  };
  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() =>
      inputRef.current?.focus({ preventScroll: true }),
    );
    return () => cancelAnimationFrame(frame);
  }, [open]);
  useEffect(() => {
    const show = (e: Event) =>
      expand((e as CustomEvent<{ feedback: boolean }>).detail.feedback);
    window.addEventListener("djinn:compose", show);
    return () => window.removeEventListener("djinn:compose", show);
  }, [review]);
  useEffect(() => {
    if (!open) return;
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        collapse();
      }
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [open]);
  const send = async () => {
    if (sending || !value.trim()) return;
    setSending(true);
    try {
      if (await onSend(feedback ? `Retour de review : ${value}` : value)) {
        setValue("");
        setHistory(false);
        collapse();
      }
    } finally {
      setSending(false);
    }
  };
  return (
    <div
      className={`mission-composer ${open ? "is-expanded" : "is-collapsed"}`}
    >
      <motion.div
        layout
        transition={
          reducedMotion
            ? { duration: 0.01 }
            : { type: "spring", stiffness: 360, damping: 30, mass: 0.7 }
        }
      >
        <AnimatePresence initial={false} mode="sync">
          {!open ? (
            <motion.button
              ref={capsuleRef}
              key="collapsed"
              className="composer-capsule"
              onClick={() => expand()}
              layout
              initial={{ opacity: 0, scale: 0.92, y: 8 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              exit={{ opacity: 0, scale: 0.92, y: 6 }}
              transition={
                reducedMotion
                  ? { duration: 0.01 }
                  : { type: "spring", stiffness: 420, damping: 28, mass: 0.7 }
              }
            >
              <MessageSquare size={14} />
              <span>
                {value
                  ? "Reprendre le brouillon"
                  : review
                    ? "Faire un retour"
                    : "Donner une indication"}
              </span>
              {pending > 0 && (
                <span className="composer-pending">{pending}</span>
              )}
              <kbd>{review ? "⌘ ⇧ J" : "⌘ J"}</kbd>
              <Plus size={13} />
            </motion.button>
          ) : (
            <motion.div
              key="expanded"
              layout
              initial={{ opacity: 0, y: 10, scale: 0.96 }}
              animate={{ opacity: 1, y: 0, scale: 1 }}
              exit={{ opacity: 0, y: 8, scale: 0.96 }}
              transition={
                reducedMotion
                  ? { duration: 0.01 }
                  : { type: "spring", stiffness: 350, damping: 26, mass: 0.7 }
              }
            >
              <div className="composer-history">
                {history &&
                  (task.instructions || []).map((i) => (
                    <div key={i.id}>
                      <p>{i.text}</p>
                      <span>
                        {i.status === "prevented"
                          ? `Empêchée : ${i.reason || "reprise nécessaire"}`
                          : i.status === "consumed"
                            ? "Consommée par le destinataire"
                            : i.status === "transmitted"
                              ? "Transmise"
                              : i.appliedAt
                                ? "Incluse dans le contexte du passage"
                                : "En attente de transmission"}
                      </span>
                    </div>
                  ))}
              </div>
              <div className="composer-input">
                <textarea
                  ref={inputRef}
                  aria-label="Indications pour la mission"
                  placeholder={
                    feedback ? "Votre retour…" : "Donner une indication…"
                  }
                  rows={1}
                  maxLength={12000}
                  value={value}
                  onChange={(e) => setValue(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Escape") {
                      e.preventDefault();
                      e.stopPropagation();
                      collapse();
                    }
                    if (
                      e.key === "Enter" &&
                      !e.shiftKey &&
                      !e.nativeEvent.isComposing
                    ) {
                      e.preventDefault();
                      void send();
                    }
                  }}
                />
                <button
                  aria-label="Replier la barre"
                  className="composer-close"
                  onClick={collapse}
                >
                  <ChevronDown size={15} />
                </button>
                <button
                  aria-label="Envoyer l’indication"
                  className="composer-send"
                  disabled={sending || !value.trim()}
                  onClick={() => void send()}
                >
                  <ArrowUp size={16} />
                </button>
              </div>
              <div className="composer-meta">
                <span>
                  {task.runId
                    ? "Transmis immédiatement au chef"
                    : feedback
                      ? "Retour de review"
                      : consulting
                        ? "Indication pour l’étape active"
                        : "Contexte de la mission"}
                </span>
                {!!task.instructions?.length && (
                  <button
                    onClick={() => setHistory(!history)}
                    aria-expanded={history}
                  >
                    {pending
                      ? `${pending} en attente`
                      : `${task.instructions.length} indication${task.instructions.length > 1 ? "s" : ""}`}
                    <ChevronDown size={10} />
                  </button>
                )}
                <kbd>⌘ ↵</kbd>
                <kbd>Esc</kbd>
              </div>
            </motion.div>
          )}
        </AnimatePresence>
      </motion.div>
    </div>
  );
}
