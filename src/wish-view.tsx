// The screen of one wish, read from the services: what waits for you first (its open questions, the blocking ones
// first, its workers that wait, and "My wish is granted" once Djinn proposes it), its journal and the rights its
// workers have. Its tasks have a workspace panel of their own (task-tabs.tsx), with what they spent, its decisions
// another (decision-log.tsx), its tilasms another (tilasms.tsx), and the blocks its agents wrote for one another the
// last (agent-blocks.tsx): what is for you is a question.
// Djinn proposes; only the user grants.
import {
  Bot,
  ChevronRight,
  Clock3,
  Download,
  FileText,
  Pause,
  Play,
  ScrollText,
  Sparkles,
  Trash2,
} from "lucide-react";
import { AnimatePresence } from "motion/react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";

import {
  Allowance,
  type Block,
  Change,
  type Choice,
  Closer,
  type KeptWorktree,
  type MarkKind,
  type Project,
  type Provider,
  type Task,
  TaskStatus,
  InstructionStatus,
  type Wish,
  type WishExport,
  WishState,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { decisionOf, decisionsOf } from "./data/decisions";
import {
  useClients,
  useData,
  usePausable,
  useStore,
  useWishDetail,
} from "./data/djinn";
import {
  type OpenQuestion,
  azimaGroups,
  forkedAs,
  finishedTasks,
  investigatingQuestions,
  isAzima,
  movingTasks,
  openQuestions,
  spent,
  waitingTasks,
  azimasDone,
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
  wishTone,
} from "./data/format";
import { type Entry, journal } from "./data/journal";
import { t } from "./i18n";
import { AgentBlocks, agentBlocks } from "./agent-blocks";
import { AttentionBar, attentionOf } from "./attention";
import { ModalFrame } from "./frame";
import { useWrites } from "./marks";
import { InstructionList } from "./instruction-list";
import { LeadMessage } from "./lead-message";
import { LeadPromptCard } from "./lead-prompt";
import { leadTerminal } from "./lead-terminal";
import { LeadStatus } from "./lead-status";
import { MarkdownBody } from "./markdown-body";
import { OlderLine, useRecent } from "./older";
import { useKeepPlace } from "./scroll-anchor";
import { DecisionLog } from "./decision-log";
import { CountPill, StatusBadge } from "./status";
import { AzimaCard, azimaFinished } from "./azima";
import { TaskSections, type View } from "./task-tabs";
import { TilasmsTab } from "./tilasms";
import { SpentLine } from "./usage";
import { WishQuestion } from "./wish-question";
import { ChangeProvider } from "./wish-dialogs";
import { WishTask } from "./wish-task";
import { WishWorkspace, type WishWorkspacePanel } from "./wish-workspace";
import { PageHeader, PageHeaderAction } from "./page-header";
import {
  LastPushes,
  LeadButton,
  MainMerges,
  WishDescription,
  recordedAgent,
} from "./wish-head";
import { providerName } from "./provider";

// Opening is a tilasm to show in a wish's Tilasms workspace panel, as a djinn:// link asks: a new object at each request.
export interface Opening {
  wishId: string;
  tilasmId: string;
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
  const done = detail.tasks.filter(
    (task) => task.status === TaskStatus.DONE && !isAzima(task),
  ).length;
  // The azimas' progress, then the work's: the azimas are not counted as tasks.
  const azimaProgress = azimasDone(detail.tasks);
  const work = workCount(detail.tasks);
  const granted = wish.state === WishState.GRANTED;
  const [changing, setChanging] = useState(false);
  const [view, setView] = useState<View>(opening ? "tilasms" : "main");
  const [workspaceRequest, setWorkspaceRequest] = useState(0);
  const [deleting, setDeleting] = useState(false);
  // What a link between a decision and a task brings into sight in the other workspace panel: its id.
  const [focus, setFocus] = useState("");
  const show = (to: View, id = "") => {
    setView(to);
    setFocus(id);
    setWorkspaceRequest((request) => request + 1);
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
    if (view !== "tasks" || !focus) return;
    const timer = setTimeout(() => {
      document
        .querySelector<HTMLButtonElement>(`#task-${focus} .wish-task-heading`)
        ?.focus({ preventScroll: true });
    }, 0);
    return () => clearTimeout(timer);
  }, [view, focus, workspaceRequest]);
  const moving = movingTasks(detail.tasks);
  const finished = finishedTasks(detail.tasks);
  const latestTask = moving[0] ?? finished[0];
  const latestDecision = decisions[0];
  const latestTilasm = detail.tilasms.at(-1);
  const latestAgent = forAgents.at(-1);
  const latestInstruction =
    detail.instructions.find(
      (instruction) => instruction.status !== InstructionStatus.DONE,
    ) ?? detail.instructions.at(-1);
  const latestJournal = journalEntries[0];
  const previewText = (text: string | undefined, fallback: string) => {
    const value = text?.replace(/\s+/g, " ").trim();
    return value ? <span title={value}>{value}</span> : fallback;
  };
  const taskPreview = latestTask
    ? previewText(
        [latestTask.code, latestTask.title, taskStatusText(latestTask)]
          .filter(Boolean)
          .join(" · "),
        t("workspace.tasks", { count: work }),
      )
    : work > 0
      ? t("pill.done_detail", { done, count: work })
      : t("workspace.tasks", { count: 0 });
  const decisionPreview = previewText(
    latestDecision?.question?.text || latestDecision?.block?.title,
    t("workspace.decisions", { count: decisions.length }),
  );
  const tilasmPreview = previewText(
    latestTilasm?.title,
    detail.tilasms.length > 0
      ? t("workspace.tilasms", { count: detail.tilasms.length })
      : t("tilasms.drop"),
  );
  const agentPreview = previewText(
    latestAgent?.title,
    t("workspace.agents", { count: forAgents.length }),
  );
  const instructionPreview = previewText(
    latestInstruction?.text,
    t("workspace.instructions", { count: detail.instructions.length }),
  );
  const journalPreview = previewText(
    latestJournal?.summary || latestJournal?.note,
    t("workspace.journal", { count: journalEntries.length }),
  );
  // The done azimas fold, apart (src/task-tabs.tsx); a link to one, or to one of its parts, shows them.
  const azimas = azimaGroups(detail.tasks);
  const doneAzimas = azimas.filter((x) => azimaFinished(x.azima));
  const showDone = doneAzimas.some(
    (x) => x.azima.id === focus || x.parts.some((p) => p.id === focus),
  );
  const byId = new Map(detail.tasks.map((task) => [task.id, task]));
  // The page keeps your place when something above what you read changes (src/scroll-anchor.ts).
  const scrollRef = useRef<HTMLDivElement>(null);
  const keepPlace = useKeepPlace(scrollRef);
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
      focused={focus === task.id}
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
  // lead resumes the wish's lead, or starts one of another agent; what Djinn says of it shows as a toast: a terminal
  // that runs a program already, a session Djinn does not know.
  const lead = (provider?: Provider) => {
    let note = "";
    quiet(
      act(
        async () => {
          ({ note } = await clients.wishes.resume({
            wishId: wish.id,
            provider,
          }));
        },
        [Change.WISH],
        () => note,
      ),
    );
  };
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
  const prompt = wish.leadRunning ? wish.leadPrompt : undefined;
  const attention = attentionOf(
    open,
    waiting,
    wish.ready ? [wish] : [],
    undefined,
    prompt ? [wish] : [],
  );
  const tone = wishTone(wish, open.length, running, watching);

  const saveDescription = (text: string) =>
    quiet(
      act(
        () => clients.wishes.describe({ wishId: wish.id, text }),
        [Change.WISH],
      ),
    );

  // The wish opens as one concise card. Detail panels are real children of the workspace, so a panel is mounted only
  // once; this matters for task, question, grant and tilasm IDs used by focus links and system notifications.
  const threadHead = (
    <div className="workspace-thread-head">
      <div className="workspace-summary-head">
        <span className="eyebrow">
          {projects.map((p) => p.name).join(" · ") || t("wish.no_project")}
        </span>
        <h1>{wish.title}</h1>
        <details className="workspace-thread-description">
          <summary>{t("wish.description")}</summary>
          <WishDescription
            key={wish.id + wish.description}
            title={wish.title}
            description={wish.description}
            onSave={saveDescription}
          />
        </details>
        <div className="review-facts workspace-facts">
          <div className="review-pills">
            <StatusBadge tone={tone} label={wishStateText(wish)} />
            {open.length > 0 && (
              <CountPill
                tone="waiting"
                count={open.length}
                label={t("wish.questions_wait", { count: open.length })}
              >
                {t("pill.to_decide")}
              </CountPill>
            )}
            {digging.length > 0 && (
              <CountPill
                tone="investigating"
                count={digging.length}
                label={t("pill.investigating_detail", {
                  count: digging.length,
                })}
              >
                {t("pill.investigating")}
              </CountPill>
            )}
            {running > 0 && (
              <CountPill
                tone="running"
                count={running}
                label={t("plan.running", { count: running })}
              >
                {t("pill.running")}
              </CountPill>
            )}
            {watching > 0 && (
              <CountPill
                tone="watching"
                count={watching}
                label={t("plan.watching", { count: watching })}
              >
                {t("pill.watching")}
              </CountPill>
            )}
            {azimaProgress.count > 0 && azimaProgress.proof === 0 && (
              <CountPill
                tone="done"
                count={azimaProgress.done}
                label={t("pill.azimas_detail", {
                  done: azimaProgress.done,
                  count: azimaProgress.count,
                })}
              >
                / {azimaProgress.count} {t("pill.azimas")}
              </CountPill>
            )}
            {azimaProgress.proof > 0 && (
              <CountPill
                tone="done"
                count={azimaProgress.done}
                label={t("pill.azimas_proof_detail", {
                  done: azimaProgress.done,
                  proof: azimaProgress.proof,
                  count: azimaProgress.count,
                })}
              >
                {t("pill.azimas_done")} ·{" "}
                <b className="tone-proof">{azimaProgress.proof}</b>{" "}
                {t("pill.azimas_proof")} / {azimaProgress.count}{" "}
                {t("pill.azimas")}
              </CountPill>
            )}
            {work > 0 && (
              <CountPill
                tone="done"
                count={done}
                label={t("pill.done_detail", { done, count: work })}
              >
                / {work} {t("pill.done")}
              </CountPill>
            )}
          </div>
          <div className="hero-meta workspace-meta">
            <span>
              <Clock3 size={13} aria-hidden="true" />
              {when(wish.createTime)}
            </span>
            {granted ? (
              <button
                className="text-button"
                onClick={() =>
                  quiet(
                    act(
                      () => clients.wishes.activate({ wishId: wish.id }),
                      [Change.WISH],
                    ),
                  )
                }
              >
                <Play size={13} aria-hidden="true" />
                {t("wish.reopen")}
              </button>
            ) : isActive(wish) ? (
              <button
                className="text-button"
                title={t("wish.pause_detail")}
                onClick={() =>
                  quiet(
                    act(
                      () => clients.wishes.pause({ wishId: wish.id }),
                      [Change.WISH],
                    ),
                  )
                }
              >
                <Pause size={13} aria-hidden="true" />
                {t("wish.pause")}
              </button>
            ) : (
              <button
                className="text-button"
                title={t("wish.activate_detail", { max: MAX_ACTIVE })}
                onClick={() =>
                  quiet(
                    act(
                      () => clients.wishes.activate({ wishId: wish.id }),
                      [Change.WISH],
                    ),
                  )
                }
              >
                <Play size={13} aria-hidden="true" />
                {t("wish.activate")}
              </button>
            )}
            <LastPushes pushes={wish.pushes} projects={projects} />
            <MainMerges mains={wish.mains} projects={projects} />
          </div>
        </div>
      </div>
      <LeadStatus
        wish={wish}
        onView={() => void viewLead()}
        onStart={startLead}
      />
    </div>
  );

  const summaryPanel: WishWorkspacePanel = {
    id: "summary",
    title: t("workspace.summary"),
    pinned: true,
    children: null,
  };
  const taskPanel: WishWorkspacePanel = {
    id: "tasks",
    title: t("wish.tasks"),
    count: workCount(detail.tasks),
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
            showDone={showDone}
            renderAzima={({ azima, parts }) => (
              <AzimaCard
                key={azima.id}
                azima={azima}
                parts={parts}
                tasks={byId}
                render={renderTask}
                focus={focus}
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

  const decisionsPanel: WishWorkspacePanel = {
    id: "decisions",
    title: t("tabs.decisions"),
    count: decisions.length,
    preview: decisionPreview,
    icon: <FileText size={14} aria-hidden="true" />,
    hidden: decisions.length === 0,
    children: (
      <>
        <span id="view-tab-decisions" className="workspace-a11y-label">
          {t("tabs.decisions")}
        </span>
        <DecisionLog
          items={decisionItems}
          noLead={() => noLead(wish)}
          focus={focus}
          onTask={(id) => show("tasks", id)}
        />
      </>
    ),
  };

  const tilasmsPanel: WishWorkspacePanel = {
    id: "tilasms",
    title: t("tabs.tilasms"),
    count: detail.tilasms.length,
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
        />
      </>
    ),
  };

  const agentsPanel: WishWorkspacePanel = {
    id: "agents",
    title: t("tabs.agents"),
    count: forAgents.length,
    preview: agentPreview,
    icon: <Bot size={14} aria-hidden="true" />,
    hidden: forAgents.length === 0,
    children: (
      <>
        <span id="view-tab-agents" className="workspace-a11y-label">
          {t("tabs.agents")}
        </span>
        <AgentBlocks blocks={forAgents} codes={codes} />
      </>
    ),
  };

  const journalPanel: WishWorkspacePanel = {
    id: "journal",
    title: t("page.journal"),
    count: journalEntries.length,
    preview: journalPreview,
    icon: <ScrollText size={14} aria-hidden="true" />,
    children: <Journal wish={wish} blocks={detail.blocks} initiallyShown />,
  };

  const rightsPanel: WishWorkspacePanel = {
    id: "rights",
    title: t("rights.title"),
    count: projects.length,
    preview: previewText(
      projects[0]?.name,
      t("workspace.projects", { count: projects.length }),
    ),
    icon: <Pause size={14} aria-hidden="true" />,
    hidden: projects.length === 0,
    children: (
      <Rights
        wish={wish}
        projects={projects}
        onAllow={(projectId, mode) =>
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
          )
        }
      />
    ),
  };

  const instructionsPanel: WishWorkspacePanel = {
    id: "instructions",
    title: t("instruction.title"),
    count: detail.instructions.length,
    preview: instructionPreview,
    icon: <FileText size={14} aria-hidden="true" />,
    hidden: detail.instructions.length === 0,
    children: (
      <InstructionList
        instructions={detail.instructions}
        tasks={detail.tasks}
        onTask={(id) => show("tasks", id)}
      />
    ),
  };

  const globalActions =
    open.length > 0 ||
    waiting.length > 0 ||
    wish.ready ||
    !!prompt ||
    digging.length > 0 ? (
      <div className="workspace-global-actions">
        {(open.length > 0 || waiting.length > 0 || wish.ready || prompt) && (
          <section
            className="action-center"
            id="action-center"
            aria-label={t("panels.your_move")}
          >
            <div className="section-title">
              <h2>{t("panels.your_move")}</h2>
              <p>
                {prompt
                  ? t("lead_prompt.waits")
                  : open.length
                    ? t("wish.questions_wait", { count: open.length })
                    : wish.ready
                      ? t("wish.ready_detail")
                      : t("panels.your_move_detail")}
              </p>
            </div>
            {prompt && (
              <LeadPromptCard
                wishId={wish.id}
                prompt={prompt}
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
            )}
            {open.length > 0 && (
              <section className="decisions-section">
                <div className="section-heading">
                  <h3>
                    {t("panels.decisions")}
                    <span className="count">{open.length}</span>
                  </h3>
                </div>
                <AnimatePresence mode="popLayout">
                  {open.map(({ item: q, blocking }) => (
                    <WishQuestion
                      key={q.id}
                      question={q}
                      blocking={blocking}
                      noLead={noLead(wish)}
                      onAnswer={(choice, note) =>
                        answer(wish.id, q.id, choice, note)
                      }
                      onMark={(kind, remove) =>
                        mark(wish.id, q.id, kind, remove)
                      }
                      onEnlighten={(note) => enlighten(wish.id, q.id, note)}
                    />
                  ))}
                </AnimatePresence>
              </section>
            )}
            {waiting.length > 0 && <WaitingTasks waiting={waiting} />}
            {wish.ready && (
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
            )}
          </section>
        )}
        {digging.length > 0 && (
          <InvestigatingSection
            questions={digging}
            onAnswer={(q, choice, note) => answer(wish.id, q, choice, note)}
            onMark={(q, kind, remove) => mark(wish.id, q, kind, remove)}
          />
        )}
      </div>
    ) : null;

  const panels: WishWorkspacePanel[] = [
    summaryPanel,
    taskPanel,
    decisionsPanel,
    tilasmsPanel,
    agentsPanel,
    journalPanel,
    rightsPanel,
    instructionsPanel,
  ];
  const workspacePanel = view === "main" ? undefined : view;

  return (
    <div className="wish-view review">
      {changing && (
        <ChangeProvider
          wish={wish}
          onClose={() => setChanging(false)}
          onChanged={(provider) => {
            setChanging(false);
            onToast(t("provider.changed", { agent: providerName(provider) }));
          }}
        />
      )}
      <PageHeader
        actions={
          <>
            <PageHeaderAction
              className="wish-provider"
              label={`${t("wish.provider_detail")} (${providerName(wish.provider)})`}
              title={`${t("provider.title")} (${providerName(wish.provider)})`}
              onClick={() => setChanging(true)}
            >
              <Bot size={16} aria-hidden="true" />
              <span>{providerName(wish.provider)}</span>
            </PageHeaderAction>
            <LeadButton
              recorded={recordedAgent(wish.lead)}
              loadAgents={async () =>
                (await clients.ui.getEnvironment({})).providers
              }
              onLead={() => lead()}
              onPick={(provider) => lead(provider)}
            />
            <PageHeaderAction
              label={t("wish.page")}
              title={t("wish.page_detail")}
              onClick={() =>
                quiet(
                  (async () => {
                    const res = await clients.wishes.render({
                      wishId: wish.id,
                    });
                    onToast(t("wish.page_written", { file: res.file }));
                  })().catch((error) => onToast(message(error))),
                )
              }
            >
              <FileText size={16} aria-hidden="true" />
            </PageHeaderAction>
            <PageHeaderAction
              label={t("app.share")}
              onClick={() =>
                quiet(
                  (async () => {
                    const res = await clients.wishes.export({
                      wishId: wish.id,
                    });
                    onToast(t("wish.exported", { file: res.file }));
                  })().catch((error) => onToast(message(error))),
                )
              }
            >
              <Download size={16} aria-hidden="true" />
            </PageHeaderAction>
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
        {projects.map((project) => (
          <span
            className="header-project"
            key={project.id}
            title={project.directory || t("project.no_folder")}
          >
            {project.name}
            <ChevronRight size={12} aria-hidden="true" />
          </span>
        ))}
        <strong>{wish.title}</strong>
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
        <AttentionBar items={attention} />
        {threadHead}
        {globalActions && (
          <div className="workspace-global">{globalActions}</div>
        )}
        <WishWorkspace
          wishId={wish.id}
          panels={panels}
          ready={detail.loaded}
          initialPanel={workspacePanel}
          initialPanelRequest={`${workspacePanel ?? "summary"}:${workspaceRequest}`}
          onPanelChange={(panelId) => {
            if (!panelId) {
              setView("main");
              setFocus("");
              return;
            }
            if (
              panelId === "tasks" ||
              panelId === "decisions" ||
              panelId === "tilasms" ||
              panelId === "agents"
            ) {
              setView(panelId);
            }
          }}
        />
      </div>
      <LeadMessage wish={wish} onResume={startLead} onToast={onToast} />
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
}: {
  wish: Wish;
  blocks: Block[];
  initiallyShown?: boolean;
}) {
  const clients = useClients();
  const [shown, setShown] = useState(initiallyShown);
  const [commands, setCommands] = useState(false);
  const [exp, setExp] = useState<WishExport>();
  const [error, setError] = useState("");
  // What the store read of the wish: a new one means the wish changed.
  const detail = useData((s) => s.details[wish.id]);
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
      className="wish-section wish-journal"
      aria-label={t("page.journal")}
    >
      <div className="section-heading">
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
              <JournalEntry key={entry.id} entry={entry} />
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
    </section>
  );
}

function JournalEntry({ entry }: { entry: Entry }) {
  return (
    <tr className={entry.command ? "command" : "log"}>
      <td>
        <time>{when(entry.at)}</time>
      </td>
      <td>{entry.command && <code>{entry.command}</code>}</td>
      <td>
        {entry.summary && <span>{entry.summary}</span>}
        {entry.note && <MarkdownBody text={entry.note} />}
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
