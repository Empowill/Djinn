// The screen of one wish, read from the services: what waits for you first (its open questions, the blocking ones
// first, its workers that wait, and "My wish is granted" once Djinn proposes it), its journal and the rights its
// workers have. Its tasks have a workspace panel of their own (task-tabs.tsx), with what they spent, its decisions
// another (decision-log.tsx), its tilasms another (tilasms.tsx), and the blocks its agents wrote for one another the
// last (agent-blocks.tsx): what is for you is a question.
// Djinn proposes; only the user grants.
import {
  Activity,
  Bot,
  Check,
  ChevronDown,
  ChevronRight,
  Clock3,
  LoaderCircle,
  FileText,
  MessageSquare,
  Pause,
  Play,
  RotateCcw,
  Share2,
  ScrollText,
  Sparkles,
  Terminal,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import {
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  Allowance,
  type Block,
  Change,
  type Choice,
  Closer,
  type KeptWorktree,
  type MarkKind,
  type Project,
  Provider,
  type Task,
  TaskKind,
  TaskStatus,
  type Wish,
  type WishExport,
  WishState,
} from "../gen/ts/plan/v1/plan_pb";
import { type Provider as AgentProvider } from "../gen/ts/ui/v1/ui_pb";
import { message } from "./data/client";
import { decisionOf, decisionsOf } from "./data/decisions";
import {
  useClients,
  useData,
  useDjinn,
  usePausable,
  useStore,
  useWishDetail,
} from "./data/djinn";
import {
  type OpenQuestion,
  azimaGroups,
  awaitedProofs,
  forkedAs,
  finishedTasks,
  investigatingQuestions,
  movingTasks,
  openQuestions,
  spent,
  waitingTasks,
  workCount,
} from "./data/flight";
import {
  allowanceOf,
  deletedText,
  MAX_ACTIVE,
  isActive,
  noLead,
  projectsOf,
  taskStatusText,
  taskTone,
  when,
  wishStateText,
  runningOf,
} from "./data/format";
import { type Entry, journal } from "./data/journal";
import { t } from "./i18n";
import { AgentBlocks, agentBlocks } from "./agent-blocks";
import { ModalFrame } from "./frame";
import { useWrites } from "./marks";
import { InstructionList } from "./instruction-list";
import { LeadMessage } from "./lead-message";
import { LeadPromptCard } from "./lead-prompt";
import { leadTerminal } from "./lead-terminal";
import { MarkdownBody } from "./markdown-body";
import { OlderLine, useRecent } from "./older";
import { useKeepPlace } from "./scroll-anchor";
import { DecisionLog } from "./decision-log";
import { StatusBadge } from "./status";
import { AzimaCard, azimaFinished } from "./azima";
import { TaskSections } from "./task-tabs";
import { TilasmsTab } from "./tilasms";
import { SpentLine } from "./usage";
import { WishQuestion } from "./wish-question";
import { WishTask } from "./wish-task";
import { WishWorkspace, type WishWorkspacePanel } from "./wish-workspace";
import {
  WishActivity,
  WishActivityTabs,
  type ActivityTab,
  type WishActivityTab,
  visibleActivityTabs,
} from "./wish-activity";
import { SettingsDrawer, WishSettingsMenu } from "./wish-settings";
import { PageHeader, PageHeaderAction } from "./page-header";
import { WishDescription } from "./wish-head";
import { providerName } from "./provider";
import { ActionDock, type ActionDockItem } from "./action-dock";

import "./wish-prototype-header.css";

// Opening is a tilasm to show in a wish's Tilasms workspace panel, as a djinn:// link asks: a new object at each request.
export interface Opening {
  wishId: string;
  tilasmId: string;
}

function contentKey(value: string): string {
  let hash = 2166136261;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return `${value.length}-${(hash >>> 0).toString(36)}`;
}

export function WishView({
  wish,
  opening,
  onToast,
}: {
  wish: Wish;
  // A tilasm of the wish to show now, in its Tilasms workspace panel.
  opening?: Opening;
  onToast: (text: string) => void;
}) {
  const allProjects = useData((s) => s.projects);
  const djinn = useDjinn();
  const data = useStore();
  const pausable = usePausable();
  const detail = useWishDetail(wish.id);
  const projects = projectsOf(wish, allProjects);
  const open = openQuestions(wish, detail);
  const digging = investigatingQuestions(wish, detail);
  const waiting = waitingTasks(wish, detail);
  const codes = new Map(detail.tasks.map((task) => [task.id, task.code]));
  // Read again only when the wish's questions, blocks or tasks change, not at every render of the screen.
  const forAgents = useMemo(() => agentBlocks(detail.blocks), [detail.blocks]);
  const decisions = useMemo(
    () => decisionsOf(detail.questions, detail.blocks, detail.tasks),
    [detail.questions, detail.blocks, detail.tasks],
  );
  const decisionItems = useMemo(
    () => decisions.map((item) => ({ item })),
    [decisions],
  );
  const journalEntries = useMemo(
    () => journal(undefined, detail.blocks),
    [detail.blocks],
  );
  const { running, watching } = runningOf(detail.tasks);
  const granted = wish.state === WishState.GRANTED;
  type WorkspaceView =
    | "main"
    | "tasks"
    | "tilasms"
    | "activity"
    | ActivityTab
    | "rights"
    | "agents";
  const [view, setView] = useState<WorkspaceView>(opening ? "tilasms" : "main");
  const [activityTab, setActivityTab] = useState<ActivityTab>("journal");
  const [settingsDrawer, setSettingsDrawer] = useState<"rights" | "agents">();
  const [workspaceRequest, setWorkspaceRequest] = useState(0);
  const [workspaceVisiblePanel, setWorkspaceVisiblePanel] = useState("");
  const [topbarWishVisible, setTopbarWishVisible] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [attentionCollapsed, setAttentionCollapsed] = useState(false);
  const [attentionFocusRequest, setAttentionFocusRequest] = useState(0);
  const [attentionFocusTarget, setAttentionFocusTarget] = useState("");
  // What a link between a decision and a task brings into sight in the other workspace panel: its id.
  const [focus, setFocus] = useState("");
  useEffect(() => {
    setWorkspaceVisiblePanel("");
  }, [wish.id]);
  const show = (to: WorkspaceView, id = "") => {
    if (to === "rights" || to === "agents") {
      setSettingsDrawer(to);
      return;
    }
    setSettingsDrawer(undefined);
    if (to === "decisions" || to === "journal" || to === "instructions") {
      setActivityTab(to);
      setView("activity");
    } else {
      setView(to);
    }
    setFocus(id);
    setWorkspaceRequest((request) => request + 1);
  };
  const focusAttention = (target = "") => {
    setAttentionFocusTarget(target);
    if (!target && !attentionRef.current) {
      show("tasks");
      return;
    }
    setAttentionCollapsed(false);
    setAttentionFocusRequest((request) => request + 1);
  };
  // The tilasm a link opened: the Tilasms workspace panel shows it, again at each new request.
  const [opened, setOpened] = useState(opening);
  useEffect(() => {
    if (!opening) return;
    setView("tilasms");
    setOpened(opening);
    setWorkspaceRequest((request) => request + 1);
  }, [opening]);
  useEffect(() => {
    if (!djinn) return;
    const off = djinn.focus.subscribe((request) => {
      if (request.wishId && request.wishId !== wish.id) return;
      const target = request.target;
      if (!target) return;
      if (
        target.startsWith("question-") ||
        target.startsWith("grant-") ||
        target.startsWith("lead-prompt-")
      ) {
        setAttentionFocusTarget(target);
        setAttentionCollapsed(false);
        setAttentionFocusRequest((value) => value + 1);
        return;
      }
      if (target.startsWith("task-") || target.startsWith("waiting-")) {
        setSettingsDrawer(undefined);
        setView("tasks");
        setFocus(
          target.startsWith("waiting-")
            ? target.slice("waiting-".length)
            : target.slice("task-".length),
        );
        setWorkspaceRequest((value) => value + 1);
      }
    });
    return off;
  }, [djinn, wish.id]);
  useEffect(() => {
    if (
      view !== "tasks" ||
      !focus ||
      workspaceVisiblePanel !== "tasks" ||
      !detail.loaded
    )
      return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let stopped = false;
    const deadline = Date.now() + 5_000;
    const reveal = () => {
      if (stopped) return;
      const row = document.getElementById(`task-${focus}`);
      const heading =
        row?.querySelector<HTMLButtonElement>(".wish-task-heading");
      if (heading) {
        heading.focus({ preventScroll: true });
        return;
      }
      if (Date.now() < deadline) {
        timer = setTimeout(reveal, 50);
      } else {
        setView((current) => (current === "tasks" ? "main" : current));
        setFocus((current) => (current === focus ? "" : current));
      }
    };
    timer = setTimeout(reveal, 0);
    return () => {
      stopped = true;
      if (timer !== undefined) clearTimeout(timer);
    };
  }, [
    detail.loaded,
    detail.tasks,
    focus,
    view,
    workspaceRequest,
    workspaceVisiblePanel,
  ]);
  const moving = movingTasks(detail.tasks);
  const finished = finishedTasks(detail.tasks);
  // Done azima parts remain individually collapsible; links focus the matching card.
  const azimas = azimaGroups(detail.tasks);
  const doneAzimas = azimas.filter((x) => azimaFinished(x.azima));
  const latestTask = moving[0] ?? finished[0];
  const latestDecision = decisions[0];
  const latestTilasm = detail.tilasms.at(-1);
  const latestInstruction = detail.instructions.at(-1);
  const latestJournal = journalEntries[0];
  const previewText = (text: string | undefined) => {
    const value = text?.replace(/\s+/g, " ").trim();
    return value ? <span title={value}>{value}</span> : undefined;
  };
  const taskPreview = latestTask
    ? previewText(
        [latestTask.code, latestTask.title, taskStatusText(latestTask)]
          .filter(Boolean)
          .join(" · "),
      )
    : undefined;
  const decisionPreview = previewText(
    latestDecision?.question?.text || latestDecision?.block?.title,
  );
  const tilasmPreview = previewText(latestTilasm?.title);
  const instructionPreview = previewText(latestInstruction?.text);
  const journalPreview = previewText(
    latestJournal?.summary || latestJournal?.note,
  );
  const byId = new Map(detail.tasks.map((task) => [task.id, task]));
  // The page keeps your place when something above what you read changes (src/scroll-anchor.ts).
  const scrollRef = useRef<HTMLDivElement>(null);
  const threadHeadRef = useRef<HTMLDivElement>(null);
  const attentionRef = useRef<HTMLElement>(null);
  const keepPlace = useKeepPlace(scrollRef);

  useEffect(() => {
    setTopbarWishVisible(false);
    const header = threadHeadRef.current;
    const title = header?.querySelector("h1");
    const scroller = header?.closest(".mission-scroll");
    if (!title || !(scroller instanceof HTMLElement)) return;

    const update = () => {
      const currentHeader = threadHeadRef.current;
      const currentTitle = currentHeader?.querySelector("h1");
      const currentScroller = currentHeader?.closest(".mission-scroll");
      if (!currentTitle || !(currentScroller instanceof HTMLElement)) return;
      const rootTop = currentScroller.getBoundingClientRect().top;
      const titleBottom = currentTitle.getBoundingClientRect().bottom;
      const next = titleBottom <= rootTop;
      setTopbarWishVisible((current) => (current === next ? current : next));
    };

    let frame = 0;
    const onScroll = () => {
      if (frame) return;
      frame = window.requestAnimationFrame(() => {
        frame = 0;
        update();
      });
    };
    scroller.addEventListener("scroll", onScroll, { passive: true });
    const observer = new IntersectionObserver(() => update(), {
      root: scroller,
      threshold: [0, 1],
    });
    observer.observe(title);
    update();
    return () => {
      scroller.removeEventListener("scroll", onScroll);
      if (frame) window.cancelAnimationFrame(frame);
      observer.disconnect();
    };
  }, [wish.id]);
  const {
    clients,
    act: write,
    quiet,
    answer,
    enlighten,
    mark,
  } = useWrites(onToast);
  const act = (
    run: () => Promise<unknown>,
    changes: Change[],
    done?: string | (() => string),
  ) => write(wish.id, run, changes, done);
  const renderTask = (task: Task) => (
    <WishTask
      key={task.id}
      task={task}
      codes={codes}
      forkedAs={forkedAs(task, detail.tasks)}
      decision={decisionOf(task, decisions)}
      focused={focus === task.id && workspaceVisiblePanel === "tasks"}
      compact
      onDecision={() => show("decisions", decisionOf(task, decisions)?.id)}
      project={allProjects.find((p) => p.id === task.projectId)}
      onStop={() =>
        quiet(act(() => clients.tasks.stop({ taskId: task.id }), [Change.TASK]))
      }
      onStart={() =>
        quiet(
          act(() => clients.tasks.start({ taskId: task.id }), [Change.TASK]),
        )
      }
      onSend={(text) =>
        act(() => clients.tasks.send({ taskId: task.id, text }), [])
      }
      onHold={
        pausable
          ? (pause) =>
              quiet(
                act(
                  () =>
                    pause
                      ? clients.tasks.pause({ taskId: task.id })
                      : clients.tasks.resume({ taskId: task.id }),
                  [Change.TASK],
                ),
              )
          : undefined
      }
      onDone={(note) =>
        act(
          () =>
            clients.tasks.done({
              taskId: task.id,
              note,
              by: Closer.DEVELOPER,
            }),
          [Change.TASK],
          t("task.marked_done", { task: task.code }),
        )
      }
    />
  );
  const viewLead = async () => {
    await clients.ui.show({
      wishId: wish.id,
      terminal: leadTerminal(wish.id),
    });
  };
  const startLead = async () => {
    try {
      const res = await clients.wishes.resume({
        wishId: wish.id,
        provider: wish.provider,
      });
      await data.changed(wish.id, [Change.WISH]);
      await clients.ui.show({ wishId: wish.id, terminal: res.terminal });
    } catch (error) {
      await data.changed(wish.id, [Change.WISH]).catch(() => undefined);
      throw error;
    }
  };
  const speakLead = async () => {
    if (!wish.leadRunning) {
      await startLead();
      return;
    }
    await viewLead();
  };
  const changeProvider = (provider: Provider) =>
    act(
      () => clients.wishes.setProvider({ wishId: wish.id, provider }),
      [Change.WISH],
      () => t("provider.changed", { agent: providerName(provider) }),
    );
  const prompt = wish.leadRunning ? wish.leadPrompt : undefined;
  const workTasks = detail.tasks.filter((task) => task.kind !== TaskKind.AZIMA);
  const doneWork = workTasks.filter(
    (task) => task.status === TaskStatus.DONE,
  ).length;
  const failedWork = workTasks.filter(
    (task) => task.status === TaskStatus.FAILED,
  ).length;
  const proofCount = awaitedProofs(detail.tasks).length;
  const totalWork = workCount(detail.tasks);
  const waitingWithoutQuestion = waiting.filter((item) => !item.question);
  const contextParts = [
    open.length > 0 && t("wish.context_decisions", { count: open.length }),
    digging.length > 0 &&
      t("wish.context_investigating", { count: digging.length }),
    waitingWithoutQuestion.length > 0 &&
      t("wish.context_waiting", { count: waitingWithoutQuestion.length }),
    failedWork > 0 && t("wish.context_failures", { count: failedWork }),
    proofCount > 0 && t("wish.context_proofs", { count: proofCount }),
    wish.ready && t("wish.context_ready"),
    prompt && t("wish.context_lead"),
  ].filter((part): part is string => !!part);
  const contextSummary = contextParts.length
    ? contextParts.join(" · ")
    : t("wish.context_none");
  const globalActionCount =
    open.length +
    waitingWithoutQuestion.length +
    (wish.ready ? 1 : 0) +
    (prompt ? 1 : 0) +
    digging.length;
  const activeWorkCount = running + watching;

  const saveDescription = (text: string) =>
    quiet(
      act(
        () => clients.wishes.describe({ wishId: wish.id, text }),
        [Change.WISH],
      ),
    );
  const lifecycleLabel = isActive(wish)
    ? t("page.state_active")
    : wishStateText(wish);
  const renderWishPage = () =>
    quiet(
      (async () => {
        const res = await clients.wishes.render({ wishId: wish.id });
        onToast(t("wish.page_written", { file: res.file }));
      })().catch((error) => onToast(message(error))),
    );
  const exportWish = () =>
    quiet(
      (async () => {
        const res = await clients.wishes.export({ wishId: wish.id });
        onToast(t("wish.exported", { file: res.file }));
      })().catch((error) => onToast(message(error))),
    );

  // The wish opens as one concise card. Detail panels are real children of the workspace, so a panel is mounted only
  // once; this matters for task, question, grant and tilasm IDs used by focus links and system notifications.
  const threadHead = (
    <div className="workspace-thread-head" ref={threadHeadRef}>
      <div className="workspace-summary-head">
        <h1>{wish.title}</h1>
        <div className="workspace-thread-description">
          <WishDescription
            key={wish.id + wish.description}
            title={wish.title}
            description={wish.description}
            onSave={saveDescription}
          />
        </div>
        <div className="review-facts workspace-facts">
          <div className="review-pills">
            <LeadLifecycleMenu
              wish={wish}
              lifecycleLabel={lifecycleLabel}
              onToggle={() =>
                quiet(
                  act(
                    () =>
                      granted || !isActive(wish)
                        ? clients.wishes.activate({ wishId: wish.id })
                        : clients.wishes.pause({ wishId: wish.id }),
                    [Change.WISH],
                  ),
                )
              }
              onSpeak={speakLead}
              onResume={startLead}
            />
            <button
              type="button"
              className={`wish-work-summary${activeWorkCount > 0 ? " is-running" : ""}`}
              onClick={() => show("tasks")}
              title={t("wish.work_progress", {
                done: doneWork,
                total: totalWork,
              })}
              aria-label={t("wish.work_summary", {
                running: running + watching,
                done: doneWork,
                total: totalWork,
              })}
            >
              <span>
                {t("wish.work_summary", {
                  running: running + watching,
                  done: doneWork,
                  total: totalWork,
                })}
              </span>
            </button>
            <button
              type="button"
              className={`wish-context-indicator${globalActionCount > 0 ? " is-actionable" : ""}`}
              onClick={() => focusAttention()}
              title={contextSummary}
            >
              <span>{contextSummary}</span>
            </button>
          </div>
          <div className="hero-meta workspace-meta">
            <span>
              <Clock3 size={13} aria-hidden="true" />
              {when(wish.createTime)}
            </span>
            <WishSettingsMenu
              rightsCount={projects.length}
              agentsCount={forAgents.length}
              onOpenRights={() => show("rights")}
              onOpenAgents={() => show("agents")}
            />
          </div>
        </div>
      </div>
    </div>
  );

  const pendingTaskFocus = view === "tasks" && !!focus;
  const summaryPanel: WishWorkspacePanel = {
    id: "summary",
    title: t("workspace.sections"),
    pinned: true,
    children: null,
  };
  const taskPanel: WishWorkspacePanel = {
    id: "tasks",
    title: t("wish.tasks"),
    count: detail.loaded ? workCount(detail.tasks) : undefined,
    hidden: detail.loaded && detail.tasks.length === 0 && !pendingTaskFocus,
    preview: taskPreview,
    icon: <Play size={14} aria-hidden="true" />,
    children: (
      <>
        <span id="view-tab-tasks" className="workspace-a11y-label">
          {t("wish.tasks")}
        </span>
        {detail.tasks.length === 0 ? (
          <p className="muted-text" role="tabpanel">
            {detail.loaded ? t("wish.no_tasks") : t("common.loading")}
          </p>
        ) : (
          <TaskSections
            moving={moving}
            finished={finished}
            azimas={azimas.filter((x) => !azimaFinished(x.azima))}
            doneAzimas={doneAzimas}
            fold={wish.id}
            workspaceHierarchy
            renderAzima={({ azima, parts }) => (
              <AzimaCard
                key={azima.id}
                azima={azima}
                parts={parts}
                tasks={byId}
                render={renderTask}
                focus={focus}
                compact
                workspace
                onValidate={() =>
                  quiet(
                    act(
                      () =>
                        clients.tasks.done({
                          taskId: azima.id,
                          note: t("azima.validated_note"),
                          by: Closer.DEVELOPER,
                        }),
                      [Change.TASK],
                      t("azima.validated", { code: azima.code }),
                    ),
                  )
                }
              />
            )}
            aside={<SpentLine spent={spent(detail.tasks)} />}
            render={renderTask}
          />
        )}
      </>
    ),
  };

  const tilasmsPanel: WishWorkspacePanel = {
    id: "tilasms",
    title: t("tabs.tilasms"),
    count: detail.loaded ? detail.tilasms.length : undefined,
    hidden: detail.loaded && detail.tilasms.length === 0,
    preview: tilasmPreview,
    icon: <Sparkles size={14} aria-hidden="true" />,
    children: (
      <>
        <span id="view-tab-tilasms" className="workspace-a11y-label">
          {t("tabs.tilasms")}
        </span>
        <TilasmsTab
          key={wish.id}
          wishId={wish.id}
          opening={opened}
          tilasms={detail.tilasms}
          tasks={detail.tasks}
          onToast={onToast}
          workspace
          active={workspaceVisiblePanel === "tilasms"}
        />
      </>
    ),
  };

  const allowProject = (projectId: string, mode: Allowance) =>
    quiet(
      act(
        () =>
          clients.wishes.allow({
            wishId: wish.id,
            projectId,
            mode,
          }),
        [Change.WISH],
      ),
    );

  const onActivityTabChange = (tab: ActivityTab) => {
    setActivityTab(tab);
    setFocus("");
  };
  const activityTabVisible: Record<ActivityTab, boolean> = {
    journal: !detail.loaded || journalEntries.length > 0,
    decisions: !detail.loaded || decisions.length > 0,
    instructions: !detail.loaded || detail.instructions.length > 0,
  };
  const firstActivityTab = (
    ["journal", "decisions", "instructions"] as const
  ).find((tab) => activityTabVisible[tab]);
  const effectiveActivityTab = activityTabVisible[activityTab]
    ? activityTab
    : (firstActivityTab ?? activityTab);
  const activityTabs: WishActivityTab[] = [
    {
      id: "journal",
      label: t("page.journal"),
      ariaLabel: t("page.journal"),
      icon: <ScrollText size={14} aria-hidden="true" />,
      count: detail.loaded ? journalEntries.length : undefined,
      content: (
        <Journal
          wish={wish}
          blocks={detail.blocks}
          initiallyShown
          embedded
          workspace
          compact
          visible={
            workspaceVisiblePanel === "activity" &&
            effectiveActivityTab === "journal"
          }
        />
      ),
    },
    {
      id: "decisions",
      label: t("tabs.decisions"),
      ariaLabel: t("tabs.decisions"),
      icon: <FileText size={14} aria-hidden="true" />,
      count: detail.loaded ? decisions.length : undefined,
      content: (
        <>
          <span id="view-tab-decisions" className="workspace-a11y-label">
            {t("tabs.decisions")}
          </span>
          <DecisionLog
            items={decisionItems}
            noLead={() => noLead(wish)}
            focus={
              workspaceVisiblePanel === "activity" &&
              effectiveActivityTab === "decisions"
                ? focus
                : ""
            }
            workspace
            compact
            visible={
              workspaceVisiblePanel === "activity" &&
              effectiveActivityTab === "decisions"
            }
            onTask={(id) => show("tasks", id)}
          />
        </>
      ),
    },
    {
      id: "instructions",
      label: t("instruction.title"),
      ariaLabel: t("instruction.title"),
      icon: <MessageSquare size={14} aria-hidden="true" />,
      count: detail.loaded ? detail.instructions.length : undefined,
      content: (
        <InstructionList
          instructions={detail.instructions}
          tasks={detail.tasks}
          onTask={(id) => show("tasks", id)}
          workspace
          compact
          visible={
            workspaceVisiblePanel === "activity" &&
            effectiveActivityTab === "instructions"
          }
        />
      ),
    },
  ];

  const availableActivityTabs = visibleActivityTabs(activityTabs);
  const activityCount =
    decisions.length + journalEntries.length + detail.instructions.length;
  useEffect(() => {
    if (
      availableActivityTabs.length > 0 &&
      !availableActivityTabs.some((tab) => tab.id === activityTab)
    ) {
      setActivityTab(availableActivityTabs[0].id);
    }
  }, [activityTab, availableActivityTabs]);

  const activityPanel: WishWorkspacePanel = {
    id: "activity",
    title: t("workspace.activity"),
    count: detail.loaded ? activityCount : undefined,
    hidden: detail.loaded && activityCount === 0,
    preview: decisionPreview ?? instructionPreview ?? journalPreview,
    icon: <Activity size={14} aria-hidden="true" />,
    headerActions: (
      <WishActivityTabs
        label={t("workspace.activity")}
        tabs={activityTabs}
        activeTab={effectiveActivityTab}
        onTabChange={onActivityTabChange}
        iconsOnly
      />
    ),
    children: (
      <WishActivity
        label={t("workspace.activity")}
        activeTab={effectiveActivityTab}
        onTabChange={onActivityTabChange}
        tabs={activityTabs}
        hideTabs
      />
    ),
  };

  const promptSignature = prompt
    ? [prompt.title, ...prompt.lines, ...prompt.options].join("\u0001")
    : "";
  const promptActionId = prompt
    ? `lead-prompt-${wish.id}:${contentKey(promptSignature)}`
    : "";
  const promptDomId = prompt
    ? `lead-prompt-${wish.id}-${contentKey(promptSignature)}`
    : "";
  const actionDockItems: ActionDockItem[] = [
    ...(prompt
      ? [
          {
            id: promptActionId,
            targetId: `lead-prompt-${wish.id}`,
            summary: prompt.title || t("lead_prompt.title"),
            content: (
              <LeadPromptCard
                wishId={wish.id}
                domId={promptDomId}
                prompt={prompt}
                compact
                dockMode
                onChoose={(index) =>
                  act(
                    () =>
                      clients.wishes.choose({
                        wishId: wish.id,
                        option: index + 1,
                        title: prompt.title,
                        lines: prompt.lines,
                        options: prompt.options,
                      }),
                    [Change.WISH],
                  )
                }
              />
            ),
          },
        ]
      : []),
    ...open.map(({ item: q, blocking }) => ({
      id: `question-${q.id}:${q.revision}`,
      targetId: `question-${q.id}`,
      summary: `${q.code} · ${q.text}`,
      content: (
        <WishQuestion
          question={q}
          domId={`question-${q.id}-${q.revision}`}
          compact
          dockMode
          blocking={blocking}
          noLead={noLead(wish)}
          onAnswer={(choice, note) => answer(wish.id, q.id, choice, note)}
          onMark={(kind, remove) => mark(wish.id, q.id, kind, remove)}
          onEnlighten={(note) => enlighten(wish.id, q.id, note)}
        />
      ),
    })),
    ...digging.map(({ item: q, blocking }) => ({
      id: `question-${q.id}:${q.revision}:investigating`,
      targetId: `question-${q.id}`,
      summary: `${q.code} · ${q.text}`,
      content: (
        <WishQuestion
          question={q}
          domId={`question-${q.id}-${q.revision}-investigating`}
          compact
          dockMode
          blocking={blocking}
          noLead={noLead(wish)}
          onAnswer={(choice, note) => answer(wish.id, q.id, choice, note)}
          onMark={(kind, remove) => mark(wish.id, q.id, kind, remove)}
        />
      ),
    })),
    ...(wish.ready
      ? [
          {
            id: `grant-${wish.id}`,
            targetId: `grant-${wish.id}`,
            summary: t("wish.ready_title"),
            content: (
              <GrantCard
                wish={wish}
                onGrant={() =>
                  quiet(
                    act(
                      () => clients.wishes.grant({ wishId: wish.id }),
                      [Change.WISH],
                      t("wish.granted_toast"),
                    ),
                  )
                }
              />
            ),
          },
        ]
      : []),
  ];
  const actionDock =
    actionDockItems.length > 0 ? (
      <ActionDock
        ref={attentionRef}
        items={actionDockItems}
        collapsed={attentionCollapsed}
        onCollapsedChange={setAttentionCollapsed}
        focusRequest={attentionFocusRequest}
        focusTarget={attentionFocusTarget}
      />
    ) : undefined;

  const panels: WishWorkspacePanel[] = [
    summaryPanel,
    taskPanel,
    tilasmsPanel,
    activityPanel,
  ];
  useEffect(() => {
    if (!detail.loaded) return;
    const panelVisible = {
      tasks: detail.tasks.length > 0,
      tilasms: detail.tilasms.length > 0,
      activity: activityCount > 0,
    };
    const requestedPanel =
      view === "tasks" || view === "tilasms" || view === "activity"
        ? view
        : undefined;
    if (requestedPanel === "tasks" && focus && detail.tasks.length === 0) {
      const timer = setTimeout(() => {
        setView((current) => (current === "tasks" ? "main" : current));
        setFocus((current) => (current === focus ? "" : current));
      }, 5_000);
      return () => clearTimeout(timer);
    }
    if (requestedPanel && !panelVisible[requestedPanel]) {
      setView("main");
      setFocus("");
    }
    if (
      workspaceVisiblePanel &&
      (workspaceVisiblePanel === "tasks" ||
        workspaceVisiblePanel === "tilasms" ||
        workspaceVisiblePanel === "activity") &&
      !panelVisible[workspaceVisiblePanel]
    ) {
      setWorkspaceVisiblePanel("");
    }
  }, [
    activityCount,
    detail.loaded,
    detail.tasks.length,
    detail.tilasms.length,
    focus,
    view,
    workspaceVisiblePanel,
  ]);
  const workspacePanel =
    view === "main" || view === "rights" || view === "agents"
      ? undefined
      : view === "decisions" || view === "journal" || view === "instructions"
        ? "activity"
        : view;

  return (
    <div className="wish-view review">
      <PageHeader
        actions={
          <>
            <HeaderAgentMenu wish={wish} onPick={changeProvider} />
            <HeaderShareMenu onRender={renderWishPage} onExport={exportWish} />
            <PageHeaderAction
              className="danger"
              label={t("wish.delete")}
              onClick={() => setDeleting(true)}
            >
              <Trash2 size={16} aria-hidden="true" />
            </PageHeaderAction>
          </>
        }
      >
        <strong
          className={`topbar-wish-title${topbarWishVisible ? " is-visible" : ""}`}
          aria-hidden={!topbarWishVisible}
        >
          {wish.title}
        </strong>
      </PageHeader>
      {deleting && (
        <DeleteWish
          wish={wish}
          tasks={detail.tasks.length}
          running={running + watching}
          onClose={() => setDeleting(false)}
          onDelete={() => {
            setDeleting(false);
            let kept: KeptWorktree[] = [];
            quiet(
              act(
                async () => {
                  ({ kept } = await clients.wishes.delete({ wishId: wish.id }));
                },
                [Change.WISH],
                () => deletedText(wish.title, kept),
              ),
            );
          }}
        />
      )}
      <div className="mission-scroll" ref={keepPlace}>
        <WishWorkspace
          wishId={wish.id}
          head={threadHead}
          panels={panels}
          ready={detail.loaded}
          initialPanel={workspacePanel}
          initialPanelRequest={`${workspacePanel ?? "summary"}:${workspaceRequest}`}
          onPanelChange={(panelId) => {
            setWorkspaceVisiblePanel(panelId ?? "");
            if (!panelId) {
              setView("main");
              setFocus("");
              return;
            }
            if (panelId === "activity") setView("activity");
            else if (panelId !== "summary")
              setView(panelId as "tasks" | "tilasms");
          }}
        />
      </div>
      {settingsDrawer === "rights" && (
        <SettingsDrawer
          title={t("rights.title")}
          onClose={() => setSettingsDrawer(undefined)}
        >
          <Rights wish={wish} projects={projects} onAllow={allowProject} />
        </SettingsDrawer>
      )}
      {settingsDrawer === "agents" && (
        <SettingsDrawer
          title={t("tabs.agents")}
          onClose={() => setSettingsDrawer(undefined)}
        >
          <>
            <span id="view-tab-agents" className="workspace-a11y-label">
              {t("tabs.agents")}
            </span>
            <AgentBlocks blocks={forAgents} codes={codes} />
          </>
        </SettingsDrawer>
      )}
      <LeadMessage
        wish={wish}
        attention={actionDock}
        attentionCount={actionDockItems.length}
        attentionCollapsed={attentionCollapsed}
        attentionRequest={attentionFocusRequest}
        onAttentionOpen={focusAttention}
        onResume={startLead}
        onToast={onToast}
      />
    </div>
  );
}

function LeadLifecycleMenu({
  wish,
  lifecycleLabel,
  onToggle,
  onSpeak,
  onResume,
}: {
  wish: Wish;
  lifecycleLabel: string;
  onToggle: () => void;
  onSpeak: () => Promise<void>;
  onResume: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const running = wish.leadRunning;
  const ended = !running && !!wish.leadExit;
  const error = actionError || wish.leadError;
  const code = wish.leadExit?.code ?? 0;
  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePointer = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
      triggerRef.current?.focus();
    };
    document.addEventListener("mousedown", closeOnOutsidePointer);
    document.addEventListener("keydown", closeOnEscape);
    const firstItem =
      rootRef.current?.querySelector<HTMLButtonElement>('[role="menuitem"]');
    firstItem?.focus();
    return () => {
      document.removeEventListener("mousedown", closeOnOutsidePointer);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  const run = async (action: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    setActionError("");
    try {
      await action();
      setOpen(false);
    } catch (startFailure) {
      setActionError(message(startFailure));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="wish-lifecycle-split" ref={rootRef}>
      <button
        type="button"
        className="wish-lifecycle-badge"
        onClick={onToggle}
        title={
          wish.state === WishState.GRANTED
            ? t("wish.reopen")
            : isActive(wish)
              ? t("wish.pause_detail")
              : t("wish.activate_detail", { max: MAX_ACTIVE })
        }
        aria-label={`${lifecycleLabel} · ${isActive(wish) ? t("wish.pause") : t("wish.activate")}`}
      >
        {isActive(wish) ? (
          <Pause size={12} aria-hidden="true" />
        ) : (
          <Play size={12} aria-hidden="true" />
        )}
        <span>{lifecycleLabel}</span>
      </button>
      <button
        ref={triggerRef}
        type="button"
        className="wish-lifecycle-menu-trigger"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t("lead_menu.open")}
        title={t("lead_menu.open")}
        onClick={() => setOpen((isOpen) => !isOpen)}
      >
        <ChevronDown size={11} aria-hidden="true" />
      </button>
      {open && (
        <div
          className="wish-lead-menu"
          role="menu"
          aria-label={t("lead_menu.open")}
        >
          <button
            type="button"
            role="menuitem"
            disabled={busy}
            onClick={() => void run(onSpeak)}
          >
            {busy ? (
              <LoaderCircle
                size={14}
                className="wish-lead-spinner"
                aria-hidden="true"
              />
            ) : (
              <MessageSquare size={14} aria-hidden="true" />
            )}
            {t("lead_menu.speak")}
          </button>
          {!running && (ended || !!error) && (
            <button
              type="button"
              role="menuitem"
              disabled={busy}
              onClick={() => void run(onResume)}
            >
              <RotateCcw size={14} aria-hidden="true" />
              {t("lead_menu.resume")}
            </button>
          )}
          {error && (
            <p className="wish-lead-menu-error" role="alert">
              <TriangleAlert size={13} aria-hidden="true" />
              {error}
            </p>
          )}
          {ended && !error && (
            <p className="wish-lead-menu-status" role="status">
              {t("lead_menu.ended", { code })}
            </p>
          )}
          {running && (
            <p className="wish-lead-menu-status" role="status">
              <Terminal size={13} aria-hidden="true" />
              {t("lead_menu.terminal_hint")}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

function providerForAgent(id: string): Provider | undefined {
  if (id === "claude") return Provider.CLAUDE;
  if (id === "codex") return Provider.CODEX;
  if (id === "antigravity" || id === "agy") return Provider.ANTIGRAVITY;
  return undefined;
}

function agentIdForProvider(provider: Provider): string {
  if (provider === Provider.CODEX) return "codex";
  if (provider === Provider.ANTIGRAVITY) return "antigravity";
  return "claude";
}

function HeaderAgentMenu({
  wish,
  onPick,
}: {
  wish: Wish;
  onPick: (provider: Provider) => Promise<unknown>;
}) {
  const clients = useClients();
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const [providers, setProviders] = useState<AgentProvider[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const currentId = agentIdForProvider(wish.provider);

  useEffect(() => {
    if (!open) return;
    let live = true;
    setLoading(true);
    setError("");
    clients.ui
      .getEnvironment({ agents: true })
      .then((response) => {
        if (live) setProviders(response.providers);
      })
      .catch((failure) => {
        if (live) setError(message(failure));
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [clients, open]);

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePointer = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
      triggerRef.current?.focus();
    };
    document.addEventListener("mousedown", closeOnOutsidePointer);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("mousedown", closeOnOutsidePointer);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  useEffect(() => {
    if (!open || providers.length === 0) return;
    rootRef.current
      ?.querySelector<HTMLButtonElement>('[role="menuitemradio"]')
      ?.focus();
  }, [open, providers.length]);

  const choose = async (agent: AgentProvider) => {
    const provider = providerForAgent(agent.id);
    if (!provider || !agent.available || busy) return;
    setBusy(true);
    setError("");
    try {
      await onPick(provider);
      setOpen(false);
    } catch (failure) {
      setError(message(failure));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="wish-agent-menu" ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className="button secondary page-header-action wish-provider wish-agent-trigger"
        aria-label={t("wish.change_provider")}
        aria-haspopup="menu"
        aria-expanded={open}
        title={t("wish.change_provider")}
        onClick={() => setOpen((isOpen) => !isOpen)}
      >
        <Bot size={15} aria-hidden="true" />
        <span>{providerName(wish.provider)}</span>
        <ChevronDown size={12} aria-hidden="true" />
      </button>
      {open && (
        <div
          className="wish-agent-options"
          role="menu"
          aria-label={t("wish.change_provider")}
        >
          {loading && (
            <span className="wish-agent-loading">
              {t("wish.agent_loading")}
            </span>
          )}
          {!loading && providers.length === 0 && (
            <span className="wish-agent-loading">{t("wish.agent_none")}</span>
          )}
          {!loading &&
            providers.map((agent) => {
              const provider = providerForAgent(agent.id);
              const current =
                agent.id === currentId ||
                (currentId === "antigravity" && agent.id === "agy");
              return (
                <button
                  key={agent.id}
                  type="button"
                  role="menuitemradio"
                  aria-checked={current}
                  className={`wish-agent-option${current ? " is-current" : ""}`}
                  disabled={busy || !agent.available || !provider}
                  onClick={() => void choose(agent)}
                >
                  <span className="wish-agent-option-mark">
                    {current && <Check size={13} aria-hidden="true" />}
                  </span>
                  <span className="wish-agent-option-copy">
                    <strong>
                      {agent.name || providerName(provider ?? Provider.CLAUDE)}
                    </strong>
                    <small>
                      {current
                        ? t("wish.agent_current")
                        : agent.available
                          ? agent.version || t("wish.agent_available")
                          : t("wish.agent_unavailable")}
                    </small>
                  </span>
                </button>
              );
            })}
          {error && (
            <p className="wish-agent-error" role="alert">
              {error}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

function HeaderShareMenu({
  onRender,
  onExport,
}: {
  onRender: () => void;
  onExport: () => void;
}) {
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePointer = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
      triggerRef.current?.focus();
    };
    document.addEventListener("mousedown", closeOnOutsidePointer);
    document.addEventListener("keydown", closeOnEscape);
    const firstItem =
      rootRef.current?.querySelector<HTMLButtonElement>('[role="menuitem"]');
    firstItem?.focus();
    return () => {
      document.removeEventListener("mousedown", closeOnOutsidePointer);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  const choose = (action: () => void) => {
    setOpen(false);
    action();
  };

  return (
    <div className="wish-share-menu" ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className="button secondary page-header-action wish-share-trigger"
        aria-label={t("app.share")}
        aria-haspopup="menu"
        aria-expanded={open}
        title={t("wish.share_menu")}
        onClick={() => setOpen((isOpen) => !isOpen)}
      >
        <Share2 size={16} aria-hidden="true" />
      </button>
      {open && (
        <div
          className="wish-share-options"
          role="menu"
          aria-label={t("wish.share_menu")}
        >
          <button
            type="button"
            role="menuitem"
            onClick={() => choose(onRender)}
          >
            <FileText size={14} aria-hidden="true" />
            {t("wish.share_page")}
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => choose(onExport)}
          >
            <Share2 size={14} aria-hidden="true" />
            {t("wish.share_export")}
          </button>
        </div>
      )}
    </div>
  );
}

// GrantCard proposes to grant a wish: Djinn proposes, only you grant.
export function GrantCard({
  wish,
  origin,
  onGrant,
}: {
  wish: Wish;
  origin?: ReactNode;
  onGrant: () => void;
}) {
  return (
    <article className="step-result-action wish-grant" id={`grant-${wish.id}`}>
      <div>
        <span className="step-result-kicker">
          <StatusBadge tone="done" label={t("wish.ready")} />
          {origin}
        </span>
        <h3>{t("wish.ready_title")}</h3>
      </div>
      <button type="button" className="button accent" onClick={onGrant}>
        <Sparkles size={14} />
        {t("wish.grant")}
      </button>
    </article>
  );
}

// InvestigatingSection holds the questions you asked to investigate: they wait for the lead's revision, and can still
// be answered.
export function InvestigatingSection({
  questions,
  origin = false,
  onAnswer,
  onMark,
}: {
  questions: OpenQuestion[];
  origin?: boolean;
  onAnswer: (id: string, choice: Choice, note: string) => Promise<void>;
  onMark: (id: string, kind: MarkKind, remove: boolean) => Promise<void>;
}) {
  return (
    <section
      className="wish-section investigating-section"
      aria-label={t("question.investigating_title")}
    >
      <div className="section-title">
        <h2>
          {t("question.investigating_title")}
          <span className="count">{questions.length}</span>
        </h2>
        <p>{t("question.investigating_section")}</p>
      </div>
      {questions.map(({ wish, item, blocking }) => (
        <WishQuestion
          key={item.id}
          question={item}
          blocking={blocking}
          noLead={noLead(wish)}
          origin={origin ? <WishOrigin wish={wish} /> : undefined}
          onAnswer={(choice, note) => onAnswer(item.id, choice, note)}
          onMark={(kind, remove) => onMark(item.id, kind, remove)}
        />
      ))}
    </section>
  );
}

// WishOrigin marks a line with its wish, in the flight plan of several wishes: its rank and its title.
export function WishOrigin({ wish }: { wish: Wish }) {
  return (
    <span className="wish-origin" title={wish.title}>
      {wish.rank > 0 && <b>{wish.rank}</b>}
      {wish.title}
    </span>
  );
}

// WaitingTasks are the workers that wait for the user: for an answer before they edit.
export function WaitingTasks({
  waiting,
  origin = false,
}: {
  waiting: { item: Task; question: string; wish: Wish }[];
  // Each line shows its wish.
  origin?: boolean;
}) {
  return (
    <section className="plan-waiting">
      <div className="section-heading">
        <h3>
          {t("page.actions")}
          <span className="count">{waiting.length}</span>
        </h3>
      </div>
      {waiting.map(({ item, question, wish }) => (
        <p className="plan-line" key={item.id} id={`waiting-${item.id}`}>
          <StatusBadge tone={taskTone(item)} label={taskStatusText(item)} />
          {origin && <WishOrigin wish={wish} />}
          <span>
            {question
              ? t("page.action_waiting", { task: item.code, question })
              : t("page.action_waiting_unknown", { task: item.code })}
          </span>
        </p>
      ))}
    </section>
  );
}

// Journal is the story of the wish: its log blocks at once, and the commands that changed it once asked for. The
// latest entries show, the older ones fold behind a line (older.tsx).
// WishService.Snapshot reads the whole wish, events included: it is read on a click, then again as the wish changes.
function Journal({
  wish,
  blocks,
  initiallyShown = false,
  embedded = false,
  workspace = false,
  compact = false,
  visible = true,
}: {
  wish: Wish;
  blocks: Block[];
  initiallyShown?: boolean;
  embedded?: boolean;
  workspace?: boolean;
  compact?: boolean;
  visible?: boolean;
}) {
  const clients = useClients();
  const [shown, setShown] = useState(embedded || initiallyShown);
  const [commands, setCommands] = useState(false);
  const [exp, setExp] = useState<WishExport>();
  const [error, setError] = useState("");
  const [journalDetail, setJournalDetail] = useState<Entry>();
  // What the store read of the wish: a new one means the wish changed.
  const detail = useData((s) => s.details[wish.id]);
  useEffect(() => {
    if (workspace && !visible) setJournalDetail(undefined);
  }, [visible, workspace]);
  useEffect(() => {
    if (!commands || !shown) return;
    let current = true;
    clients.wishes
      .snapshot({ wishId: wish.id })
      .then((res) => {
        if (!current) return;
        setExp(res.export);
        setError("");
      })
      .catch((err) => {
        if (current) setError(message(err));
      });
    return () => {
      current = false;
    };
  }, [clients, wish, commands, shown, detail]);
  const entries = useMemo(
    () => journal(commands ? exp : undefined, blocks),
    [commands, exp, blocks],
  );
  const { shown: recent, more } = useRecent(entries.length);
  return (
    <section
      className={`wish-section wish-journal${embedded ? " is-embedded" : ""}${workspace ? " is-workspace" : ""}${compact ? " is-compact" : ""}`}
      aria-label={t("page.journal")}
    >
      <div className="section-heading">
        {!embedded && (
          <button
            className="fold-heading"
            onClick={() => setShown(!shown)}
            aria-expanded={shown}
          >
            <ChevronRight size={14} className={shown ? "rotated-90" : ""} />
            <h3>
              {t("page.journal")}
              {entries.length > 0 && (
                <span className="count">{entries.length}</span>
              )}
            </h3>
          </button>
        )}
        {shown && (
          <button
            className="text-button"
            onClick={() => setCommands(!commands)}
            aria-pressed={commands}
          >
            <ScrollText size={13} />
            {commands ? t("wish.journal_hide") : t("wish.journal_show")}
          </button>
        )}
      </div>
      {shown && error && <p className="muted-text">{error}</p>}
      {shown && entries.length > 0 && (
        <table className="compact-table journal-list">
          <tbody>
            {entries.slice(0, recent).map((entry) => (
              <JournalEntry
                key={entry.id}
                entry={entry}
                workspace={workspace}
                onOpen={workspace ? () => setJournalDetail(entry) : undefined}
              />
            ))}
          </tbody>
        </table>
      )}
      {shown && (
        <OlderLine
          hidden={entries.length - recent}
          label={(count, hidden) =>
            count < hidden
              ? t("wish.journal_older_some", { count, hidden })
              : t("wish.journal_older", { count })
          }
          onShow={more}
        />
      )}
      {journalDetail && workspace && visible && (
        <SettingsDrawer
          title={
            journalDetail.command || journalDetail.summary || t("page.journal")
          }
          onClose={() => setJournalDetail(undefined)}
        >
          <div className="workspace-activity-detail journal-detail">
            <time>{when(journalDetail.at)}</time>
            {journalDetail.command && <code>{journalDetail.command}</code>}
            {journalDetail.summary && <p>{journalDetail.summary}</p>}
            {journalDetail.note && (
              <div className="prose">
                <MarkdownBody text={journalDetail.note} />
              </div>
            )}
          </div>
        </SettingsDrawer>
      )}
    </section>
  );
}

function JournalEntry({
  entry,
  workspace = false,
  onOpen,
}: {
  entry: Entry;
  workspace?: boolean;
  onOpen?: () => void;
}) {
  const openFromKeyboard = (event: ReactKeyboardEvent<HTMLTableRowElement>) => {
    if (!workspace || !onOpen || (event.key !== "Enter" && event.key !== " "))
      return;
    event.preventDefault();
    onOpen();
  };
  const preview = entry.note
    ?.split(/\r?\n/)
    .map((line) => line.trim())
    .find(Boolean);
  return (
    <tr
      className={`${entry.command ? "command" : "log"}${workspace ? " workspace-journal-entry" : ""}`}
      role={workspace ? "button" : undefined}
      tabIndex={workspace ? 0 : undefined}
      aria-label={workspace ? t("page.details") : undefined}
      onClick={workspace ? onOpen : undefined}
      onKeyDown={openFromKeyboard}
    >
      <td>
        <time>{when(entry.at)}</time>
      </td>
      <td>{entry.command && <code>{entry.command}</code>}</td>
      <td>
        {entry.summary && <span>{entry.summary}</span>}
        {entry.note &&
          (workspace ? (
            <span className="journal-preview">{preview}</span>
          ) : (
            <MarkdownBody text={entry.note} />
          ))}
      </td>
    </tr>
  );
}

const modes = [Allowance.NONE, Allowance.EDIT, Allowance.AUTO] as const;
const modeKeys = {
  [Allowance.NONE]: "rights.none",
  [Allowance.EDIT]: "rights.edit",
  [Allowance.AUTO]: "rights.auto",
} as const;

// Rights shows, per project, what the wish allows its workers, and changes it (WishService.Allow).
function Rights({
  wish,
  projects,
  onAllow,
}: {
  wish: Wish;
  projects: Project[];
  onAllow: (projectId: string, mode: Allowance) => void;
}) {
  return (
    <section
      className="wish-section wish-rights"
      aria-label={t("rights.title")}
    >
      <div className="section-heading">
        <h3>{t("rights.title")}</h3>
      </div>
      <p className="muted-text">{t("rights.detail")}</p>
      {projects.map((project) => (
        <div className="setting-row" key={project.id}>
          <div>
            <strong>{project.name}</strong>
            <p>{project.directory || t("project.no_folder")}</p>
          </div>
          <select
            aria-label={t("rights.for", { project: project.name })}
            value={allowanceOf(wish, project.id)}
            onChange={(e) => onAllow(project.id, Number(e.target.value))}
          >
            {modes.map((mode) => (
              <option key={mode} value={mode}>
                {t(modeKeys[mode])}
              </option>
            ))}
          </select>
        </div>
      ))}
    </section>
  );
}

// DeleteWish asks before a wish goes for good, with its tasks, their events, its questions and its blocks
// (WishService.Delete). Its workers stop first and its lead's terminal closes; a worktree with no work of its own
// goes, the others and every branch stay, as the toast says after the delete.
function DeleteWish({
  wish,
  tasks,
  running,
  onDelete,
  onClose,
}: {
  wish: Wish;
  tasks: number;
  running: number;
  onDelete: () => void;
  onClose: () => void;
}) {
  return (
    <ModalFrame
      title={t("wish.delete_title", { title: wish.title })}
      eyebrow={t("wish.delete_eyebrow")}
      onClose={onClose}
    >
      <div className="form-fields">
        <p>{t("wish.delete_what", { count: tasks })}</p>
        {running > 0 && (
          <p className="login-message">
            {t("wish.delete_running", { count: running })}
          </p>
        )}
        <p className="form-tip">{t("wish.delete_kept")}</p>
        <div className="modal-footer">
          <button type="button" className="button secondary" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button
            type="button"
            className="button danger-button"
            onClick={onDelete}
          >
            <Trash2 size={14} />
            {t("wish.delete_confirm")}
          </button>
        </div>
      </div>
    </ModalFrame>
  );
}
