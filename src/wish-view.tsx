// The screen of one wish, read from the services: on the left, what waits for you first (its open questions in the
// questions column, question-column.tsx, the blocking ones first; its workers that wait, and "My wish is granted" once
// Djinn proposes it; the questions you asked to investigate), on the right its workers running now and to come
// (glance.tsx), then below both the rights its workers have. Its tasks have a tab of their own (task-tabs.tsx), with what they spent, its decisions another
// (decision-log.tsx), its history (commands and lead batches) another, its tilasms another (tilasms.tsx), and the blocks its agents wrote for one another the last
// (agent-blocks.tsx): what is for you is a question.
// Djinn proposes; only the user grants.
import {
  ChevronRight,
  Clock3,
  Download,
  GitMerge,
  Pause,
  Play,
  Sparkles,
  Trash2,
  Wrench,
} from "lucide-react";
import {
  type ReactNode,
  memo,
  useCallback,
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
  type Project,
  type ProjectSync,
  type Provider,
  type Question,
  type Task,
  TaskStatus,
  type Wish,
  type WishExport,
  WishState,
  PushStrategy,
} from "../gen/ts/plan/v1/plan_pb";
import { type Tilasm } from "../gen/ts/plan/v1/tilasm_pb";
import { message } from "./data/client";
import { decisionOf, decisionsOf, isDecisionBlock } from "./data/decisions";
import {
  type ReadState,
  useClients,
  useData,
  useDjinn,
  usePausable,
  useReadState,
  useRetryRead,
  useWishDetail,
} from "./data/djinn";
import {
  type GrantHold,
  type OpenQuestion,
  azimaGroups,
  canCloseWish,
  codeMapOf,
  compareCodes,
  forkedAs,
  grantHolds,
  asleepQuestions,
  investigatingQuestions,
  isAzima,
  mergeNest,
  movingTasks,
  openQuestions,
  spent,
  taskMapOf,
  waitingLeadQuestions,
  waitingTasks,
  azimasDone,
  draftAzimas,
  finishedTasks,
  workCount,
  workDoneCount,
} from "./data/flight";
import {
  allowanceOf,
  deletedText,
  firstLine,
  fixerOf,
  MAX_ACTIVE,
  isActive,
  noLead,
  projectsOf,
  span,
  taskRunDurations,
  taskStatusText,
  taskTone,
  usd,
  when,
  wishStateText,
  runningOf,
  useNow,
  wishTone,
  effectivePushStrategy,
  isWithdrawn,
} from "./data/format";
import { ifritOut, litIfrits } from "./data/ifrit";
import { type Entry, journal } from "./data/journal";
import { t } from "./i18n";
import { useShownTheme } from "./theme";
import { EmptyState } from "./empty-art";
import { ReadingLine } from "./reading-line";
import { AgentBlocks, agentBlocks } from "./agent-blocks";
import { AttentionBar, attentionOf, useReach } from "./attention";
import { ModalFrame } from "./frame";
import { IfritBadge } from "./ifrit";
import { useWrites } from "./marks";
import { OlderLine, useRecent } from "./older";
import { placeTaken, useKeepPlace } from "./scroll-anchor";
import { DecisionLog } from "./decision-log";
import { OperatingLoad } from "./operating-load";
import { CountPill, StatusBadge } from "./status";
import {
  AzimaCard,
  azimaFinished,
  DraftAzimaCard,
  MoveAzimaDialog,
  useStickyRoom,
} from "./azima";
import { RankedAzimaList, type RankPlace } from "./azima-rank";
import {
  MoveQuestionDialog,
  SleepQuestionDialog,
  WithdrawQuestionDialog,
} from "./wish-dialogs";
import {
  kindsForView,
  LayoutToggle,
  TaskSections,
  useTaskLayout,
  type View,
  ViewTabs,
} from "./task-tabs";
import { TaskTree } from "./task-tree";
import { reviewLeft, reviewRows } from "./data/review";
import { ReviewTab } from "./review-tab";
import { TilasmsTab } from "./tilasms";
import { SpentLine } from "./usage";
import { Machine } from "./visuals";
import { WishQuestion } from "./wish-question";
import { QuestionColumn } from "./question-column";
import { Glance } from "./glance";
import { WishTask } from "./wish-task";
import {
  LastPushes,
  LeadButton,
  LeadJournal,
  MainMerges,
  PushStrategySelector,
  WhereWorkLands,
  WishDescription,
  WishModels,
  WishTitle,
  leadTrouble,
  recordedAgent,
  type SeatChoice,
} from "./wish-head";
import { LeadMessage } from "./lead-message";
import { useEyeNeeds } from "./djinn-eye";
import { PageHeader, PageHeaderAction } from "./page-header";
import { TooltipButton } from "./tooltip";

// Opening is a tilasm to show in a wish's Tilasms tab, as a djinn:// link asks: a new object at each request.
export interface Opening {
  wishId: string;
  tilasmId: string;
}

// Reaching is an element of a wish to bring into sight, as a line of the attention bar of another page asks: a new
// object at each request.
export interface Reaching {
  wishId: string;
  target: string;
}

// Landing is a click on the wish in the side panel: it brings back its first tab, the questions, from wherever the wish
// was; a new object at each click.
export interface Landing {
  wishId: string;
}

export function WishView({
  wish,
  opening,
  reaching,
  landing,
  onToast,
  onSetupAgents,
}: {
  wish: Wish;
  // A tilasm of the wish to show now, in its Tilasms tab.
  opening?: Opening;
  // An element of the wish to bring into sight now, in its tab.
  reaching?: Reaching;
  // The wish clicked in the side panel: its first tab now.
  landing?: Landing;
  onToast: (text: string) => void;
  // Opens the setup of the agents, from the lead's menu.
  onSetupAgents?: () => void;
}) {
  const djinn = useDjinn();
  const store = djinn?.store;
  const allProjects = useData((s) => s.projects);
  const projectById = useMemo(
    () => new Map(allProjects.map((p) => [p.id, p])),
    [allProjects],
  );
  const allWishes = useData((s) => s.wishes);
  const leadRecord = useData((s) => s.lead);
  const leadPrompt = useData((s) => s.leadPrompt);
  const leadDrive = useData((s) => s.leadDrive);
  const leadExit = useData((s) => s.leadExit);
  const leadStartError = useData((s) => s.leadStartError);
  const pausable = usePausable();
  const initialKinds = useMemo(
    () => kindsForView(opening ? "tilasms" : "main"),
    [opening],
  );
  const detail = useWishDetail(wish.id, initialKinds);
  const questionsState = useReadState(Change.QUESTION, wish.id);
  const tasksState = useReadState(Change.TASK, wish.id);
  const blocksState = useReadState(Change.BLOCK, wish.id);
  const tilasmsState = useReadState(Change.TILASM, wish.id);
  const retryQuestions = useRetryRead(Change.QUESTION, wish.id);
  const retryTasks = useRetryRead(Change.TASK, wish.id);
  const retryBlocks = useRetryRead(Change.BLOCK, wish.id);
  const retryTilasms = useRetryRead(Change.TILASM, wish.id);
  const decisionsState: ReadState =
    questionsState === "failed" ||
    blocksState === "failed" ||
    tasksState === "failed"
      ? "failed"
      : questionsState === "slow" ||
          blocksState === "slow" ||
          tasksState === "slow"
        ? "slow"
        : questionsState === "reading" ||
            blocksState === "reading" ||
            tasksState === "reading"
          ? "reading"
          : "ready";
  const projects = useMemo(
    () => projectsOf(wish, allProjects),
    [wish, allProjects],
  );
  const open = openQuestions(wish, detail);
  // Its questions column: one group, its own (question-column.tsx).
  const questionGroup = useMemo(
    () => (open.length ? [{ wish, questions: open }] : []),
    [wish, open],
  );
  // The glance on the right: the wish alone; the tasks of the other active wishes, read for the window's side panel,
  // name what one of its tasks waits on across wishes (glance.tsx).
  const glanceWish = useMemo(() => [wish], [wish]);
  const allDetails = useData((s) => s.details);
  const waitingLead = waitingLeadQuestions(wish, detail);
  const digging = investigatingQuestions(wish, detail);
  const sleeping = asleepQuestions(wish, detail);
  const waiting = waitingTasks(wish, detail);
  const codes = codeMapOf(detail.tasks);
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
  const decisionsTotal = useMemo(() => {
    if (detail.questionsTotal === undefined) return undefined;
    const withdrawnCount = detail.questions.filter(isWithdrawn).length;
    const answeredCount = Math.max(
      0,
      detail.questionsTotal - open.length - sleeping.length - withdrawnCount,
    );
    const decisionBlocksCount = detail.blocks.filter(isDecisionBlock).length;
    return Math.max(decisionItems.length, answeredCount + decisionBlocksCount);
  }, [
    detail.questionsTotal,
    open.length,
    sleeping.length,
    detail.questions,
    detail.blocks,
    decisionItems.length,
  ]);
  const { running, investigating, mockup, watching, merging, fixing } =
    runningOf(detail.tasks);
  // Its lit ifrits, the latest activity first, and those put out: each shows by the wish's title, flame and word.
  const ifrits = useMemo(
    () => litIfrits(detail.tasks.map((item) => ({ wish, item }))),
    [detail.tasks, wish],
  );
  const outIfrits = useMemo(
    () => detail.tasks.filter(ifritOut),
    [detail.tasks],
  );
  const ifritNow = useNow(ifrits.length > 0, 15_000);
  const canClose = useMemo(
    () => canCloseWish(wish, detail.tasks),
    [wish, detail.tasks],
  );
  const holds = useMemo(
    () =>
      canClose && !wish.ready ? grantHolds(detail.tasks, detail.questions) : [],
    [canClose, wish.ready, detail.tasks, detail.questions],
  );
  const granted = wish.state === WishState.GRANTED;
  const [grantingId, setGrantingId] = useState("");
  const folding = grantingId === wish.id;
  // Nothing waits for you and nothing runs in the wish: its questions column shows the Djinn asleep (empty-art.tsx).
  const calm =
    detail.loaded &&
    questionsState === "ready" &&
    tasksState === "ready" &&
    open.length +
      waitingLead.length +
      digging.length +
      sleeping.length +
      waiting.length +
      ifrits.length +
      running +
      investigating +
      mockup +
      watching +
      merging +
      fixing ===
      0 &&
    !canClose &&
    !granted &&
    !folding;
  // A worker waiting or an ifrit lit: the Djinn's eye lets its flame shine.
  useEyeNeeds(waiting.length > 0 || ifrits.length > 0);
  const done = workDoneCount(detail.tasks);
  // The azimas' progress, then the work's: the azimas are not counted as tasks.
  const azimaProgress = azimasDone(detail.tasks);
  const work = workCount(detail.tasks);
  const [view, setView] = useState<View>(opening ? "tilasms" : "main");
  const [historyCount, setHistoryCount] = useState<{
    wishId: string;
    count: number;
  }>();
  const [deleting, setDeleting] = useState(false);
  const [movingAzima, setMovingAzima] = useState<Task | null>(null);
  const [movingQuestion, setMovingQuestion] = useState<Question | null>(null);
  const [withdrawingQuestion, setWithdrawingQuestion] =
    useState<Question | null>(null);
  // What a link between a decision and a task brings into sight in the other tab: its id.
  const [focus, setFocus] = useState("");
  const show = useCallback(
    (to: View, id = "") => {
      setView(to);
      setFocus(id);
      if (store) void store.loadKinds(wish.id, kindsForView(to));
    },
    [store, wish.id],
  );
  // The tilasm a link opened: the Tilasms tab shows it, again at each new request.
  const [opened, setOpened] = useState(opening);
  useEffect(() => {
    if (!opening) return;
    setView("tilasms");
    setOpened(opening);
    if (store) void store.loadKinds(wish.id, kindsForView("tilasms"));
  }, [opening, store, wish.id]);
  const moving = useMemo(() => movingTasks(detail.tasks), [detail.tasks]);
  // The finished work part of no azima folds, apart; a link to one of them shows it.
  const finished = useMemo(() => finishedTasks(detail.tasks), [detail.tasks]);
  // Where the merges show: under the task they serve.
  const nest = useMemo(() => mergeNest(detail.tasks), [detail.tasks]);
  // The done azimas fold, apart (src/task-tabs.tsx); a link to one, or to one of its parts, shows them.
  const azimas = useMemo(() => azimaGroups(detail.tasks), [detail.tasks]);
  const activeAzimas = useMemo(
    () => azimas.filter((x) => !azimaFinished(x.azima)),
    [azimas],
  );
  const doneAzimas = useMemo(
    () => azimas.filter((x) => azimaFinished(x.azima)),
    [azimas],
  );
  const showDone = useMemo(
    () =>
      doneAzimas.some(
        (x) =>
          x.azima.id === focus ||
          x.parts.some((p) => p.id === (nest.host.get(focus) ?? focus)),
      ),
    [doneAzimas, focus, nest],
  );
  const showFinished = useMemo(
    () => finished.some((x) => x.id === (nest.host.get(focus) ?? focus)),
    [finished, focus, nest],
  );
  const drafts = useMemo(() => draftAzimas(detail.tasks), [detail.tasks]);
  const showDrafts = useMemo(
    () => drafts.some((x) => x.id === focus),
    [drafts, focus],
  );
  const spentDetail = useMemo(() => spent(detail.tasks), [detail.tasks]);
  // The Tasks tab as a list or as the tree of every task; the tree reads them all, page after page, of the most tasks a
  // page holds.
  const [layout] = useTaskLayout();
  const tree = view === "tasks" && layout === "tree";
  // The Review tab reads every task too: what is left to review is counted on all of them.
  const reviewing = view === "review";
  useEffect(() => {
    if ((tree || reviewing) && store && detail.tasksNextToken)
      void store.loadMore(wish.id, Change.TASK, 200);
  }, [tree, reviewing, store, wish.id, detail.tasksNextToken]);
  const review = useMemo(() => reviewRows(detail.tasks), [detail.tasks]);
  const byId = taskMapOf(detail.tasks);
  // The page keeps your place when something above what you read changes (src/scroll-anchor.ts).
  const scrollRef = useRef<HTMLDivElement>(null);
  const keepPlace = useKeepPlace(scrollRef);
  // The wish clicked in the side panel: its first tab, a tilasm the Tilasms tab showed closed, the page at the top, and
  // its first question open with the focus, once its questions are read (question-column.tsx); the Djinn's eye goes
  // with the focus. After the commit that drops a full-size tilasm, whose page under it is inert until then.
  const land = useRef<() => boolean>(undefined);
  // The last landing, and whether its first question had the focus: a tab chosen meanwhile drops it.
  const landed = useRef<{ landing?: Landing; done: boolean }>({ done: true });
  const questionsRead = detail.loadedKinds.has(Change.QUESTION);
  useEffect(() => {
    if (!landing) return;
    if (landed.current.landing !== landing) {
      landed.current = { landing, done: false };
      if (view !== "main") {
        setOpened(undefined);
        show("main");
        return;
      }
    }
    if (landed.current.done || view !== "main") return;
    setOpened(undefined);
    setFocus("");
    const scroller = scrollRef.current;
    if (scroller) {
      scroller.scrollTop = 0;
      placeTaken(scroller);
    }
    if (!questionsRead) return;
    landed.current.done = true;
    land.current?.();
  }, [landing, view, show, questionsRead]);
  useEffect(() => {
    const el = scrollRef.current;
    if (!el || !store) return;
    const onScroll = () => {
      if (el.scrollHeight - el.scrollTop - el.clientHeight < 300) {
        if (view === "tasks") {
          void store.loadMore(wish.id, Change.TASK);
        } else if (view === "decisions") {
          void store.loadMore(wish.id, Change.QUESTION);
          void store.loadMore(wish.id, Change.BLOCK);
        } else if (view === "agents") {
          void store.loadMore(wish.id, Change.BLOCK);
        } else if (view === "main") {
          void store.loadMore(wish.id, Change.BLOCK);
        }
      }
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, [store, wish.id, view]);
  const {
    clients,
    act: write,
    quiet,
    answer,
    enlighten,
    review: markReviewed,
    urge,
    move,
    withdraw,
    later,
    sleep,
  } = useWrites(onToast);
  const [sleepingQuestion, setSleepingQuestion] = useState<Question | null>(
    null,
  );
  const wakeQuestion = useCallback(
    (q: Question) => sleep(q.wishId, q.id, [], true),
    [sleep],
  );
  const act = useCallback(
    (
      run: () => Promise<unknown>,
      changes: Change[],
      done?: string | (() => string),
    ) => write(wish.id, run, changes, done),
    [write, wish.id],
  );

  const handleStop = useCallback(
    (task: Task) => {
      quiet(
        write(task.wishId, () => clients.tasks.stop({ taskId: task.id }), [
          Change.TASK,
        ]),
      );
    },
    [quiet, write, clients.tasks],
  );

  const handleStart = useCallback(
    (task: Task) => {
      quiet(
        write(task.wishId, () => clients.tasks.start({ taskId: task.id }), [
          Change.TASK,
        ]),
      );
    },
    [quiet, write, clients.tasks],
  );

  const handleSend = useCallback(
    (text: string, task: Task) =>
      write(
        task.wishId,
        () => clients.tasks.send({ taskId: task.id, text }),
        [],
      ),
    [write, clients.tasks],
  );

  const handleHold = useCallback(
    (pause: boolean, task: Task) => {
      quiet(
        write(
          task.wishId,
          () =>
            pause
              ? clients.tasks.pause({ taskId: task.id })
              : clients.tasks.resume({ taskId: task.id }),
          [Change.TASK],
        ),
      );
    },
    [quiet, write, clients.tasks],
  );

  const handleUrge = useCallback(
    (task: Task, urgent: boolean) => quiet(urge(task, urgent)),
    [quiet, urge],
  );

  const handleDone = useCallback(
    (note: string, task: Task) =>
      write(
        task.wishId,
        () =>
          clients.tasks.done({
            taskId: task.id,
            note,
            by: Closer.DEVELOPER,
          }),
        [Change.TASK],
        t("task.marked_done", { task: task.code }),
      ),
    [write, clients.tasks],
  );

  // An ifrit put out: a message lights it again, in its own session and worktree.
  const handleRelight = useCallback(
    (text: string, task: Task) =>
      write(
        task.wishId,
        () =>
          clients.tasks.continue({
            taskId: task.id,
            prompt: text,
            by: Closer.DEVELOPER,
          }),
        [Change.TASK],
        t("ifrit.relit", { code: task.code }),
      ),
    [write, clients.tasks],
  );

  const handleTurnComments = useCallback(
    (task: Task) =>
      write(
        task.wishId,
        () =>
          clients.tasks.turnComments({
            taskId: task.id,
            by: Closer.DEVELOPER,
          }),
        [Change.TASK],
        t("task.turn_comments_done", { task: task.code }),
      ),
    [write, clients.tasks],
  );

  const handleReview = useCallback(
    (task: Task, remove: boolean) => markReviewed(task.wishId, task.id, remove),
    [markReviewed],
  );

  const handleDecision = useCallback(
    (task: Task) => {
      const d = decisionOf(task, decisions);
      show("decisions", d?.id);
    },
    [decisions, show],
  );

  const renderTask = useCallback(
    function row(task: Task): ReactNode {
      const merges = nest.under.get(task.id);
      return (
        <WishTask
          key={task.id}
          task={task}
          codes={codes}
          forkedAs={forkedAs(task, detail.tasks)}
          fixedBy={fixerOf(task, detail.tasks)?.code}
          decision={decisionOf(task, decisions)}
          focused={focus === task.id}
          onDecision={handleDecision}
          project={projectById.get(task.projectId)}
          onStop={handleStop}
          onStart={handleStart}
          onSend={handleSend}
          onHold={pausable ? handleHold : undefined}
          onUrge={handleUrge}
          onDone={handleDone}
          onRelight={handleRelight}
          onTurnComments={handleTurnComments}
          merges={merges}
          nested={merges?.map(row)}
          mergeOpen={nest.host.get(focus) === task.id}
          alsoMerged={nest.links.get(task.id)}
        />
      );
    },
    [
      nest,
      codes,
      detail.tasks,
      decisions,
      focus,
      handleDecision,
      projectById,
      handleStop,
      handleStart,
      handleSend,
      pausable,
      handleHold,
      handleUrge,
      handleDone,
      handleRelight,
      handleTurnComments,
    ],
  );

  const effectiveStrategy = useMemo(
    () => effectivePushStrategy(wish, projects),
    [wish, projects],
  );
  const isPerAzima = effectiveStrategy === PushStrategy.AZIMA;

  const handlePushStrategy = useCallback(
    (strategy: PushStrategy) => {
      quiet(
        act(
          () => clients.wishes.pushStrategy({ wishId: wish.id, strategy }),
          [Change.WISH, Change.TASK],
        ),
      );
    },
    [quiet, act, clients.wishes, wish.id],
  );

  const loadBranches = useCallback(
    (projectId: string) => clients.projects.branches({ project: projectId }),
    [clients.projects],
  );

  const handleLands = useCallback(
    (projectId: string, branch: string, create: boolean) =>
      act(
        () =>
          clients.wishes.setIntegration({
            wishId: wish.id,
            projectId,
            branch,
            createBranch: create,
          }),
        [Change.WISH],
        t("wish.lands_changed", { branch }),
      ),
    [act, clients.wishes, wish.id],
  );

  // The own branch of each azima, by azima id, as its project's syncs say it stands against its remote.
  const azimaSyncs = useMemo(
    () =>
      new Map(
        projects.flatMap((p) =>
          (p.syncs ?? [])
            .filter((x) => x.azimaId)
            .map((x) => [x.azimaId, x] as const),
        ),
      ),
    [projects],
  );

  // A push says how it went on its sync line, never in a toast: the line reads its answer, or its error.
  const handlePushAzima = useCallback(
    (azima: Task) =>
      clients.projects
        .push({
          project: azima.projectId || projects[0]?.id || "",
          wish: wish.id,
          branch: azimaSyncs.get(azima.id)?.branch ?? azima.azima?.branch,
        })
        .finally(
          () =>
            void store?.changed(wish.id, [
              Change.WISH,
              Change.TASK,
              Change.PROJECT,
            ]),
        ),
    [azimaSyncs, clients.projects, projects, store, wish.id],
  );

  // Forgetting a landed branch's sync takes its line off the card; an error says why in a toast.
  const handleForgetSync = useCallback(
    (sync: ProjectSync) =>
      void act(
        () =>
          clients.projects.forgetSync({
            project:
              projects.find((p) =>
                p.syncs?.some((x) => x.branch === sync.branch),
              )?.id ?? "",
            branch: sync.branch,
          }),
        [Change.PROJECT],
        t("azima.sync_forgotten", { branch: sync.branch }),
      ),
    [act, clients.projects, projects],
  );

  const handleOpenPr = useCallback(
    (url: string) => {
      void clients.ui
        .openExternal({ url })
        .catch(() => window.open(url, "_blank", "noopener"));
    },
    [clients.ui],
  );

  const handleValidateAzima = useCallback(
    (azima: Task) => {
      quiet(
        write(
          azima.wishId,
          () =>
            clients.tasks.done({
              taskId: azima.id,
              note: t("azima.validated_note"),
              by: Closer.DEVELOPER,
            }),
          [Change.TASK],
          t("azima.validated", { code: azima.code }),
        ),
      );
    },
    [quiet, write, clients.tasks],
  );

  // An azima renamed from its card: djinn task update --title, written to its plan file too.
  const handleRenameAzima = useCallback(
    (azima: Task, title: string) => {
      quiet(
        write(
          azima.wishId,
          () => clients.tasks.update({ taskId: azima.id, title }),
          [Change.TASK],
        ),
      );
    },
    [quiet, write, clients.tasks],
  );

  // A drag, or Alt+Arrow, ranks an azima just before or after another (azima-rank.tsx).
  const handleRankAzima = useCallback(
    (azima: Task, place: RankPlace) => {
      quiet(
        write(
          azima.wishId,
          () =>
            clients.tasks.rank({
              azima: azima.id,
              ...("before" in place
                ? { before: place.before.id }
                : { after: place.after.id }),
            }),
          [Change.TASK],
        ),
      );
    },
    [quiet, write, clients.tasks],
  );

  const renderAzima = useCallback(
    (
      { azima, parts }: { azima: Task; parts: readonly Task[] },
      grip?: ReactNode,
    ) => (
      <AzimaCard
        key={azima.id}
        grip={grip}
        azima={azima}
        parts={parts}
        tasks={byId}
        render={renderTask}
        focus={focus}
        nest={nest}
        onValidate={handleValidateAzima}
        onRename={handleRenameAzima}
        isPerAzima={isPerAzima}
        sync={azimaSyncs.get(azima.id)}
        onPush={handlePushAzima}
        onForgetSync={handleForgetSync}
        onOpenPr={handleOpenPr}
      />
    ),
    [
      byId,
      nest,
      renderTask,
      focus,
      handleValidateAzima,
      handleRenameAzima,
      isPerAzima,
      azimaSyncs,
      handlePushAzima,
      handleForgetSync,
      handleOpenPr,
    ],
  );

  const handleOpenDraft = useCallback(
    (draft: Task) => {
      quiet(
        act(
          () => clients.tasks.open({ azima: draft.id }),
          [Change.TASK],
          t("azima.opened_toast", { code: draft.code }),
        ),
      );
    },
    [quiet, act, clients.tasks],
  );

  const handleMoveDraft = useCallback((draft: Task) => {
    setMovingAzima(draft);
  }, []);

  const renderDraft = useCallback(
    (draft: Task) => (
      <DraftAzimaCard
        key={draft.id}
        azima={draft}
        tasks={byId}
        focus={focus}
        onOpen={() => handleOpenDraft(draft)}
        onMove={() => handleMoveDraft(draft)}
        onRename={handleRenameAzima}
      />
    ),
    [byId, focus, handleOpenDraft, handleMoveDraft, handleRenameAzima],
  );

  const handleQuestionAnswer = useCallback(
    (choice: Choice, note: string, q?: Question) => {
      if (q) return answer(q.wishId, q.id, choice, note);
      return Promise.resolve();
    },
    [answer],
  );

  const handleQuestionEnlighten = useCallback(
    (note: string, q?: Question) => {
      if (q) return enlighten(q.wishId, q.id, note);
      return Promise.resolve();
    },
    [enlighten],
  );

  const handleQuestionLater = useCallback(
    (q: Question) => later(q.wishId, q.id, !!q.laterTime),
    [later],
  );

  const handleTaskFocus = useCallback(
    (id: string) => show("tasks", id),
    [show],
  );
  const checkNoLead = useCallback(
    () => noLead(leadRecord, leadDrive),
    [leadRecord, leadDrive],
  );
  const handleDecisionsMore = useCallback(() => {
    if (store) {
      void store.loadMore(wish.id, Change.QUESTION);
      void store.loadMore(wish.id, Change.BLOCK);
    }
  }, [store, wish.id]);
  const handleGrant = useCallback(() => {
    setGrantingId(wish.id);
    quiet(
      act(
        () => clients.wishes.grant({ wishId: wish.id }),
        [Change.WISH],
        t("wish.granted_toast"),
      ),
    );
  }, [quiet, act, clients.wishes, wish.id]);
  const handleReopen = useCallback(() => {
    setGrantingId("");
    quiet(
      act(() => clients.wishes.activate({ wishId: wish.id }), [Change.WISH]),
    );
  }, [quiet, act, clients.wishes, wish.id]);
  const handleClearHold = useCallback(
    (hold: GrantHold) => {
      if (hold.kind === "question") {
        quiet(withdraw(hold.question.wishId, hold.question.id, ""));
        return;
      }
      quiet(
        act(
          () =>
            clients.tasks.done({
              taskId: hold.task.id,
              note: "",
              by: Closer.DEVELOPER,
            }),
          [Change.TASK, Change.WISH],
        ),
      );
    },
    [quiet, withdraw, act, clients.tasks],
  );
  const handleClearAllHolds = useCallback(
    (items: GrantHold[]) => {
      quiet(
        act(async () => {
          for (const hold of items) {
            if (hold.kind === "question") {
              await clients.questions.withdraw({
                question: { ref: { case: "id", value: hold.question.id } },
                note: "",
                wishId: hold.question.wishId,
                by: Closer.DEVELOPER,
              });
            } else {
              await clients.tasks.done({
                taskId: hold.task.id,
                note: "",
                by: Closer.DEVELOPER,
              });
            }
          }
        }, [Change.TASK, Change.QUESTION, Change.WISH]),
      );
    },
    [quiet, act, clients.questions, clients.tasks],
  );

  // lead resumes the lead, focusing on this wish, or starts one of another agent; what Djinn says of it shows as a toast: a terminal
  // that runs a program already, a session Djinn does not know.
  const resumeLead = (provider?: Provider) => {
    let note = "";
    return act(
      async () => {
        ({ note } = await clients.lead.resume({
          wishId: wish.id,
          provider,
        }));
      },
      [Change.WISH],
      () => note,
    );
  };
  const lead = (provider?: Provider) => quiet(resumeLead(provider));
  // moveLead moves the lead Djinn drives to another seat; what Djinn says of it shows as a toast.
  const moveLead = (seat: SeatChoice) => {
    let note = "";
    return act(
      async () => {
        ({ note } = await clients.lead.move(seat));
      },
      [Change.WISH],
      () => note,
    );
  };
  const attention = attentionOf(
    [...open, ...waitingLead],
    waiting,
    wish.ready ? [wish] : [],
    undefined,
    leadPrompt,
    ifrits,
    leadDrive?.limit && {
      limit: leadDrive.limit,
      accounts: leadDrive.accounts,
    },
  );
  // What the wish's tabs show of the bar's lines: the open questions, the waiting workers and the grant in the
  // first tab, the answered questions in the decisions.
  const shown = new Set([
    ...open.map(({ item }) => `question-${item.id}`),
    ...decisions.map((d) => `decision-${d.id}`),
    ...waiting.map(({ item }) => `waiting-${item.id}`),
    ...(canClose ? [`grant-${wish.id}`] : []),
    ...ifrits.map(({ item }) => `task-${item.id}`),
  ]);
  const { reach, to } = useReach(show, shown, wish.id);
  useEffect(() => {
    if (reaching) to(reaching.target);
  }, [reaching, to]);
  useStickyRoom(scrollRef, attention.length > 0);
  const tone = wishTone(
    wish,
    open.length,
    running + merging,
    watching,
    investigating,
    ifrits.length,
    mockup,
  );

  return (
    <div className="wish-view review">
      <PageHeader
        middle={<OperatingLoad />}
        actions={
          <>
            {granted ? (
              <button className="button secondary small" onClick={handleReopen}>
                <Play size={14} />
                <span>{t("wish.reopen")}</span>
              </button>
            ) : isActive(wish) ? (
              <TooltipButton
                className="button secondary small"
                label={t("wish.pause")}
                tooltip={t("wish.pause_detail")}
                onClick={() =>
                  quiet(
                    act(
                      () => clients.wishes.pause({ wishId: wish.id }),
                      [Change.WISH],
                    ),
                  )
                }
              >
                <Pause size={14} />
                <span>{t("wish.pause")}</span>
              </TooltipButton>
            ) : (
              <TooltipButton
                className="button accent small"
                label={t("wish.activate")}
                tooltip={t("wish.activate_detail", { max: MAX_ACTIVE })}
                onClick={() =>
                  quiet(
                    act(
                      () => clients.wishes.activate({ wishId: wish.id }),
                      [Change.WISH],
                    ),
                  )
                }
              >
                <Play size={14} />
                <span>{t("wish.activate")}</span>
              </TooltipButton>
            )}
            <LeadButton
              recorded={recordedAgent(leadRecord)}
              trouble={leadTrouble(leadExit, leadStartError)}
              loadAgents={async () =>
                (await clients.ui.getEnvironment({})).providers
              }
              onLead={() => lead()}
              onPick={(provider) => lead(provider)}
              onSetup={onSetupAgents}
              drive={leadDrive}
              onMove={(seat) => quiet(moveLead(seat))}
            />
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
          running={running + investigating + mockup + watching + ifrits.length}
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
      {movingAzima && (
        <MoveAzimaDialog
          azima={movingAzima}
          wishes={allWishes}
          currentWishId={wish.id}
          onClose={() => setMovingAzima(null)}
          onMove={(targetWishId) => {
            const target = movingAzima;
            setMovingAzima(null);
            quiet(
              act(
                () =>
                  clients.tasks.move({
                    azima: target.id,
                    wish: targetWishId,
                  }),
                [Change.TASK, Change.WISH],
                t("azima.moved_toast", { code: target.code }),
              ),
            );
          }}
        />
      )}
      {movingQuestion && (
        <MoveQuestionDialog
          question={movingQuestion}
          wishes={allWishes}
          currentWishId={wish.id}
          onClose={() => setMovingQuestion(null)}
          onMove={move}
        />
      )}
      {sleepingQuestion && (
        <SleepQuestionDialog
          question={sleepingQuestion}
          tasks={detail.tasks}
          currentWishId={wish.id}
          onClose={() => setSleepingQuestion(null)}
          onSleep={sleep}
        />
      )}
      {withdrawingQuestion && (
        <WithdrawQuestionDialog
          question={withdrawingQuestion}
          currentWishId={wish.id}
          onClose={() => setWithdrawingQuestion(null)}
          onWithdraw={withdraw}
        />
      )}
      <div className="mission-scroll" ref={keepPlace}>
        <AttentionBar items={attention} reach={reach} />
        <div className="hero mission-header review-head">
          <div className="hero-copy">
            <span className="eyebrow">
              {projects.map((p) => p.name).join(" · ") || t("wish.no_project")}
            </span>
            <WishTitle
              key={wish.id + wish.title}
              title={wish.title}
              onSave={(title) =>
                quiet(
                  act(
                    () => clients.wishes.rename({ wishId: wish.id, title }),
                    [Change.WISH],
                  ),
                )
              }
            />
            <WishDescription
              key={wish.id + wish.description}
              title={wish.title}
              description={wish.description}
              onSave={(text) =>
                quiet(
                  act(
                    () => clients.wishes.describe({ wishId: wish.id, text }),
                    [Change.WISH],
                  ),
                )
              }
            />
            <div className="review-facts">
              <div className="review-pills">
                <StatusBadge tone={tone} label={wishStateText(wish)} />
                {ifrits.map(({ item }) => (
                  <IfritBadge key={item.id} task={item} now={ifritNow} />
                ))}
                {!ifrits.length &&
                  outIfrits.map((item) => (
                    <IfritBadge key={item.id} task={item} now={ifritNow} />
                  ))}
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
                {mockup > 0 && (
                  <CountPill
                    tone="mockup"
                    count={mockup}
                    label={t("plan.mockup_detail", { count: mockup })}
                  >
                    {t("pill.mockup")}
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
                {merging > 0 && (
                  <CountPill
                    tone="running"
                    icon={GitMerge}
                    count={merging}
                    label={t("plan.merging_detail", { count: merging })}
                  >
                    {t("pill.merging")}
                  </CountPill>
                )}
                {fixing > 0 && (
                  <CountPill
                    tone="fixing"
                    icon={Wrench}
                    count={fixing}
                    label={t("plan.fixing_detail", { count: fixing })}
                  >
                    {t("pill.fixing")}
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
                  // The azimas awaiting their proof, apart from the done ones: their work is done, not them.
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
              <div className="hero-meta">
                <span>
                  <Clock3 size={13} />
                  {when(wish.createTime)}
                </span>
                <PushStrategySelector
                  wish={wish}
                  projects={projects}
                  onChange={handlePushStrategy}
                />
                <WhereWorkLands
                  branches={wish.integrationBranches}
                  projects={projects}
                  perAzima={isPerAzima}
                  loadBranches={loadBranches}
                  onChange={handleLands}
                />
                <WishModels wishId={wish.id} />
                <LastPushes
                  pushes={wish.pushes}
                  projects={projects}
                  codes={codes}
                />
                <MainMerges mains={wish.mains} projects={projects} />
              </div>
            </div>
          </div>
          <div className="hero-visual">
            <Machine active={running > 0} />
          </div>
        </div>

        <div className="overview-content">
          <ViewTabs
            view={view}
            main={t("tabs.wish")}
            tasks={
              tasksState === "ready" || detail.tasks.length > 0
                ? workCount(detail.tasks)
                : null
            }
            review={
              tasksState === "ready" || detail.tasks.length > 0
                ? reviewLeft(review)
                : null
            }
            decisions={
              decisionsState === "ready" ||
              (decisionsTotal ?? decisions.length) > 0
                ? (decisionsTotal ?? decisions.length)
                : null
            }
            history={historyCount?.wishId === wish.id ? historyCount.count : -1}
            tilasms={
              tilasmsState === "ready" || detail.tilasms.length > 0
                ? detail.tilasms.length
                : null
            }
            drafts={detail.drafts.length}
            agents={
              blocksState === "ready" || forAgents.length > 0
                ? forAgents.length
                : null
            }
            onView={(to) => show(to)}
          />
          {view === "tilasms" && (
            <TilasmsTab
              key={wish.id}
              wishId={wish.id}
              opening={opened}
              tilasms={detail.tilasms}
              drafts={detail.drafts}
              holds={detail.holds}
              sent={detail.sent}
              reads={detail.reads}
              sends={detail.sends}
              tasks={detail.tasks}
              readState={tilasmsState}
              onRetry={retryTilasms}
              onToast={onToast}
            />
          )}
          {view === "agents" && (
            <AgentBlocks
              blocks={forAgents}
              codes={codes}
              readState={blocksState}
              onRetry={retryBlocks}
            />
          )}
          {view === "review" && (
            <ReviewTab
              rows={review}
              fold={wish.id}
              codes={codes}
              readState={tasksState}
              onRetry={retryTasks}
              onReview={handleReview}
            />
          )}
          {view === "decisions" && (
            <DecisionLog
              items={decisionItems}
              total={decisionsTotal}
              noLead={checkNoLead}
              focus={focus}
              readState={decisionsState}
              onRetry={() => {
                retryQuestions();
                retryBlocks();
                retryTasks();
              }}
              onTask={handleTaskFocus}
              onMore={handleDecisionsMore}
            />
          )}
          {view === "history" && (
            <Journal
              key={wish.id}
              wish={wish}
              onCount={(count) => setHistoryCount({ wishId: wish.id, count })}
            />
          )}
          {view === "tasks" &&
            (detail.tasks.length === 0 ? (
              tasksState === "ready" ? (
                <div role="tabpanel">
                  <EmptyState art="tasks">{t("wish.no_tasks")}</EmptyState>
                </div>
              ) : (
                <div role="tabpanel">
                  <ReadingLine
                    kind={layout === "tree" ? "graph" : "tasks"}
                    state={tasksState}
                    size="card-r"
                    onRetry={retryTasks}
                  />
                </div>
              )
            ) : tree ? (
              <TaskTree
                tasks={detail.tasks}
                projects={projectById}
                codes={codes}
                focus={focus}
                card={(() => {
                  const shown = byId.get(nest.host.get(focus) ?? focus);
                  return shown && !isAzima(shown) ? renderTask(shown) : null;
                })()}
                onOpen={handleTaskFocus}
                onClose={() => show("tasks")}
                aside={
                  <>
                    <LayoutToggle />
                    <SpentLine spent={spentDetail} />
                  </>
                }
              />
            ) : (
              <TaskSections
                moving={moving}
                finished={finished}
                showFinished={showFinished}
                azimas={activeAzimas}
                doneAzimas={doneAzimas}
                drafts={drafts}
                fold={wish.id}
                showDone={showDone}
                showDrafts={showDrafts}
                readState={tasksState}
                onRetry={retryTasks}
                renderAzima={renderAzima}
                renderAzimas={(groups) => (
                  <RankedAzimaList
                    groups={groups}
                    render={renderAzima}
                    onRank={handleRankAzima}
                  />
                )}
                renderDraft={renderDraft}
                aside={
                  <>
                    <LayoutToggle />
                    <SpentLine spent={spentDetail} />
                  </>
                }
                render={renderTask}
              />
            ))}
          {view === "main" && (
            <>
              {(granted || folding) && (
                <WishBilanCard
                  wish={wish}
                  projects={projects}
                  tasks={detail.tasks}
                  questions={detail.questions}
                  blocks={detail.blocks}
                  tilasms={detail.tilasms}
                  folding={folding}
                  onReopen={handleReopen}
                />
              )}
              <div className="main-columns">
                <div className="main-moves">
                  <QuestionColumn
                    groups={questionGroup}
                    land={land}
                    noLead={noLead(leadRecord, leadDrive)}
                    readState={questionsState}
                    wishCode={wish.title}
                    eyePerch
                    onRetry={retryQuestions}
                    onAnswer={handleQuestionAnswer}
                    onEnlighten={handleQuestionEnlighten}
                    onMove={setMovingQuestion}
                    onWithdraw={setWithdrawingQuestion}
                    onLater={handleQuestionLater}
                    onSleep={setSleepingQuestion}
                    asleep={sleeping}
                    onWake={wakeQuestion}
                    calm={calm}
                  />
                  {(waiting.length > 0 ||
                    (canClose && !granted && !folding)) && (
                    <section
                      className="your-move"
                      id="action-center"
                      aria-label={t("panels.your_move")}
                    >
                      {waiting.length > 0 && <WaitingTasks waiting={waiting} />}
                      {canClose && !granted && !folding && (
                        <GrantCard
                          wish={wish}
                          holds={holds}
                          onClearHold={handleClearHold}
                          onClearAllHolds={handleClearAllHolds}
                          onGrant={handleGrant}
                        />
                      )}
                    </section>
                  )}
                  {digging.length > 0 && (
                    <InvestigatingSection
                      questions={digging}
                      onAnswer={handleQuestionAnswer}
                      onMove={setMovingQuestion}
                      onWithdraw={setWithdrawingQuestion}
                    />
                  )}
                </div>

                <Glance
                  wishes={glanceWish}
                  tasksOf={allDetails}
                  all={allWishes}
                  readState={tasksState}
                  onRetry={retryTasks}
                  onRetryLead={() => lead()}
                  onOpenTask={(task) => to(`task-${task.id}`)}
                />

                <div className="main-after">
                  {projects.length > 0 && (
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
                  )}
                </div>
              </div>
            </>
          )}
        </div>
      </div>
      <LeadMessage
        wishId={wish.id}
        stopped={
          !leadDrive && (!leadRecord || !!leadTrouble(leadExit, leadStartError))
        }
        onResume={() => resumeLead()}
      />
    </div>
  );
}

// GrantCard proposes to grant a wish once its work is done: Djinn proposes, only you grant. When unacknowledged
// refused calls, a watcher, an azima awaiting validation, or an open question still hold back wish.ready, each obstacle
// is named and clearable in one click alongside the grant button (L49).
export const GrantCard = memo(function GrantCard({
  wish,
  origin,
  holds = [],
  onClearHold,
  onClearAllHolds,
  onGrant,
}: {
  wish: Wish;
  origin?: ReactNode;
  holds?: GrantHold[];
  onClearHold?: (hold: GrantHold) => void;
  onClearAllHolds?: (holds: GrantHold[]) => void;
  onGrant: (wish?: Wish) => void;
}) {
  const held = holds.length > 0;
  return (
    <article
      className={`step-result-action wish-grant${held ? " state-held" : ""}`}
      id={`grant-${wish.id}`}
    >
      <div className="wish-grant-top">
        <div>
          <span className="step-result-kicker">
            <StatusBadge
              tone={held ? "waiting" : "done"}
              label={
                held
                  ? t("wish.held_kicker", { count: holds.length })
                  : t("wish.ready")
              }
            />
            {origin}
          </span>
          <h3>
            {held
              ? t("wish.held_title", { count: holds.length })
              : t("wish.ready_title")}
          </h3>
          <p className="wish-grant-sub">
            {held ? t("wish.held_subtitle") : t("wish.ready_subtitle")}
          </p>
        </div>
        <div className="wish-grant-actions">
          {held && holds.length > 1 && onClearAllHolds && (
            <button
              type="button"
              className="button secondary small"
              onClick={() => onClearAllHolds(holds)}
            >
              {t("wish.clear_all_holds", { count: holds.length })}
            </button>
          )}
          <button
            type="button"
            className="button accent"
            onClick={() => onGrant(wish)}
          >
            <Sparkles size={14} />
            {t("wish.grant")}
          </button>
        </div>
      </div>
      {held && (
        <div className="wish-hold-list">
          {holds.map((hold) => {
            if (hold.kind === "question") {
              return (
                <div className="wish-hold-row" key={hold.id}>
                  <div className="wish-hold-main">
                    <div className="wish-hold-title">
                      <span className="wish-task-code">
                        {hold.question.code}
                      </span>
                      <span>{firstLine(hold.question.text)}</span>
                      <StatusBadge
                        tone="waiting"
                        label={t("wish.hold_question_badge")}
                      />
                    </div>
                    <p className="wish-hold-why">
                      {t("wish.hold_question_why")}
                    </p>
                  </div>
                  {onClearHold && (
                    <button
                      type="button"
                      className="button secondary small"
                      onClick={() => onClearHold(hold)}
                    >
                      {t("wish.hold_withdraw_question")}
                    </button>
                  )}
                </div>
              );
            }
            const count = hold.task.refused?.count ?? 0;
            const names = (hold.task.refused?.names ?? []).join(", ");
            const badge =
              hold.kind === "refused" ? (
                <StatusBadge
                  tone="waiting"
                  label={t("wish.hold_refused_badge", { count })}
                />
              ) : hold.kind === "watcher" ? (
                <StatusBadge
                  tone="watching"
                  label={t("wish.hold_watcher_badge")}
                />
              ) : (
                <StatusBadge
                  tone="waiting"
                  label={t("wish.hold_azima_badge")}
                />
              );
            const why =
              hold.kind === "refused"
                ? t("wish.hold_refused_why", { count, names })
                : hold.kind === "watcher"
                  ? t("wish.hold_watcher_why")
                  : t("wish.hold_azima_why");
            const actionLabel =
              hold.kind === "refused"
                ? t("wish.hold_acknowledge")
                : hold.kind === "watcher"
                  ? t("wish.hold_close_watcher")
                  : t("wish.hold_validate_azima");
            return (
              <div className="wish-hold-row" key={hold.id}>
                <div className="wish-hold-main">
                  <div className="wish-hold-title">
                    <span className="wish-task-code">{hold.task.code}</span>
                    <span>{hold.task.title}</span>
                    {badge}
                  </div>
                  <p className="wish-hold-why">{why}</p>
                </div>
                {onClearHold && (
                  <button
                    type="button"
                    className="button secondary small"
                    onClick={() => onClearHold(hold)}
                  >
                    {actionLabel}
                  </button>
                )}
              </div>
            );
          })}
        </div>
      )}
    </article>
  );
});

// WishBilanCard is the summary of a granted wish (L49): the Djinn folds back into the lamp (transform/opacity), the
// Khatam seal rises over the spout, and the wish's tasks, tilasms, decisions, duration, cost, and theme-aware voice
// stay at the top of the archive.
export const WishBilanCard = memo(function WishBilanCard({
  wish,
  projects,
  tasks,
  questions,
  blocks,
  tilasms,
  folding = false,
  onReopen,
}: {
  wish: Wish;
  projects: Project[];
  tasks: readonly Task[];
  questions: readonly Question[];
  blocks: readonly Block[];
  tilasms: readonly Tilasm[];
  folding?: boolean;
  onReopen: () => void;
}) {
  const shownTheme = useShownTheme();
  const [sealed, setSealed] = useState(() => !folding);
  useEffect(() => {
    if (sealed) return;
    if (typeof requestAnimationFrame !== "function") {
      setSealed(true);
      return;
    }
    let frame2 = 0;
    const frame1 = requestAnimationFrame(() => {
      frame2 = requestAnimationFrame(() => setSealed(true));
    });
    return () => {
      cancelAnimationFrame(frame1);
      cancelAnimationFrame(frame2);
    };
  }, [sealed]);

  const workTasks = useMemo(
    () => tasks.filter((item) => !isAzima(item)),
    [tasks],
  );
  const doneCount = useMemo(
    () => workTasks.filter((item) => item.status === TaskStatus.DONE).length,
    [workTasks],
  );
  const cancelledCount = useMemo(
    () =>
      workTasks.filter((item) => item.status === TaskStatus.CANCELLED).length,
    [workTasks],
  );
  const openCount = Math.max(0, workTasks.length - doneCount - cancelledCount);

  const sortedTilasms = useMemo(
    () => [...tilasms].sort((a, b) => compareCodes(a.code, b.code)),
    [tilasms],
  );
  const tilasmsSub =
    sortedTilasms.length === 0
      ? t("wish.bilan_tilasms_none")
      : sortedTilasms.length === 1
        ? sortedTilasms[0].code
        : t("wish.bilan_tilasms_range", {
            first: sortedTilasms[0].code,
            last: sortedTilasms[sortedTilasms.length - 1].code,
          });

  const decisionsList = useMemo(
    () => decisionsOf(questions, [...blocks], tasks),
    [questions, blocks, tasks],
  );
  const withdrawnCount = useMemo(
    () => questions.filter(isWithdrawn).length,
    [questions],
  );
  const answeredCount = decisionsList.length;
  const totalDecisions = answeredCount + withdrawnCount;
  const decisionCodes = useMemo(
    () =>
      Array.from(
        new Set([
          ...questions.map((q) => q.code),
          ...decisionsList.map((d) => d.ref),
        ]),
      )
        .filter(Boolean)
        .sort(compareCodes),
    [questions, decisionsList],
  );
  const decisionsSub =
    totalDecisions === 0
      ? t("wish.bilan_decisions_none")
      : decisionCodes.length > 1
        ? t("wish.bilan_decisions_range", {
            first: decisionCodes[0],
            last: decisionCodes[decisionCodes.length - 1],
            answered: answeredCount,
            withdrawn: withdrawnCount,
          })
        : t("wish.bilan_decisions_sub", {
            answered: answeredCount,
            withdrawn: withdrawnCount,
          });

  const { wallText, runText, stampedAt } = useMemo(() => {
    const msOf = (ts?: { seconds: bigint; nanos: number }) =>
      ts ? Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1_000_000) : 0;
    const createdMs = msOf(wish.createTime);
    const grantedMs = msOf(wish.grantTime);
    let firstMs = createdMs;
    let lastMs = grantedMs;
    let runMs = 0;
    for (const task of workTasks) {
      const startMs = msOf(task.firstStartTime) || msOf(task.startTime);
      const endMs = msOf(task.endTime);
      for (const d of taskRunDurations(task, endMs || grantedMs || createdMs)) {
        runMs += d;
      }
      if (startMs > 0 && (firstMs === 0 || startMs < firstMs)) {
        firstMs = startMs;
      }
      if (endMs > lastMs) {
        lastMs = endMs;
      }
    }
    const wallMs = firstMs > 0 && lastMs > firstMs ? lastMs - firstMs : 0;
    return {
      wallText: wallMs > 0 ? span(wallMs) : "0s",
      runText: runMs > 0 ? span(runMs) : "0s",
      stampedAt: when(wish.grantTime ?? wish.createTime),
    };
  }, [wish.createTime, wish.grantTime, workTasks]);

  const spentSummary = useMemo(() => spent(tasks), [tasks]);
  const totalUsd = spentSummary.costUsd + spentSummary.estimatedCostUsd;
  const runsCount = useMemo(
    () =>
      workTasks.reduce(
        (n, task) => n + Math.max(task.runs.length, task.usage ? 1 : 0),
        0,
      ),
    [workTasks],
  );

  const projectNames =
    projects.map((p) => p.name).join(" · ") || t("wish.no_project");
  const summaryLine = firstLine(wish.description) || projectNames;
  const quote =
    shownTheme === "light"
      ? t("wish.bilan_quote_light")
      : t("wish.bilan_quote_dark");

  return (
    <section
      className={`wish-bilan${sealed ? " is-granted" : ""}`}
      aria-label={t("wish.bilan_kicker", { title: wish.title })}
    >
      <div className="wish-bilan-art" aria-hidden="true">
        <svg className="wish-bilan-svg" viewBox="0 0 220 155" fill="none">
          <g className="wish-bilan-djinn">
            <path
              d="M135 72 C133 56, 118 44, 108 32 C100 22, 110 12, 122 16 C132 20, 136 32, 130 44"
              stroke="var(--accent)"
              strokeDasharray="3 3"
            />
            <path
              d="M96 26 C104 16, 120 16, 128 26 C122 34, 102 34, 96 26 Z"
              fill="var(--surface)"
              stroke="var(--accent)"
            />
            <circle cx="112" cy="26" r="3.2" fill="var(--sand)" stroke="none" />
          </g>
          <g className="wish-bilan-khatam">
            <polygon
              points="135,32 145,50 125,50"
              stroke="var(--sand)"
              fill="none"
            />
            <polygon
              points="135,56 145,38 125,38"
              stroke="var(--sand)"
              fill="none"
            />
            <circle
              cx="135"
              cy="44"
              r="2.5"
              fill="var(--st-done)"
              stroke="none"
            />
          </g>
          <g stroke="var(--sand)">
            <ellipse cx="92" cy="120" rx="22" ry="4" />
            <path d="M84 116 L88 106 L96 106 L100 116" />
            <path
              d="M58 94 C58 80, 74 74, 92 74 C112 74, 124 82, 138 74 L148 68 C144 84, 130 104, 106 106 L78 106 C64 106, 58 100, 58 94 Z"
              fill="var(--surface)"
            />
            <path d="M58 90 C44 86, 42 102, 56 102" />
            <path d="M82 74 C86 66, 98 66, 102 74" />
            <circle cx="92" cy="65" r="2.5" fill="var(--sand)" />
          </g>
        </svg>
        <div className="wish-bilan-eye-badge">
          <svg className="wish-bilan-eye-svg" viewBox="0 0 28 18" fill="none">
            <path
              d="M2 9 C7 2.5, 21 2.5, 26 9 C21 15.5, 7 15.5, 2 9 Z"
              fill="var(--surface)"
              stroke="var(--sand)"
              strokeWidth="1.4"
            />
            <circle cx="14" cy="9" r="4" fill="var(--sand)" />
            <ellipse cx="14" cy="9" rx="1.2" ry="3.2" fill="var(--bg)" />
          </svg>
          <span>{t("wish.bilan_sealed")}</span>
        </div>
      </div>
      <div className="wish-bilan-body">
        <div className="wish-bilan-head">
          <span className="wish-bilan-kicker">
            ✦ {t("wish.bilan_kicker", { title: wish.title })}
          </span>
          {stampedAt && <span className="muted-text">{stampedAt}</span>}
        </div>
        <h3 className="wish-bilan-title">{wish.title}</h3>
        <p className="wish-bilan-desc">{summaryLine}</p>
        <div className="wish-bilan-grid">
          <div className="wish-bilan-stat">
            <span className="wish-bilan-stat-val">
              {t("wish.bilan_tasks", { count: workTasks.length })}
            </span>
            <span className="wish-bilan-stat-sub">
              {t("wish.bilan_tasks_sub", {
                done: doneCount,
                cancelled: cancelledCount,
                open: openCount,
              })}
            </span>
          </div>
          <div className="wish-bilan-stat">
            <span className="wish-bilan-stat-val">
              {t("wish.bilan_tilasms", { count: sortedTilasms.length })}
            </span>
            <span className="wish-bilan-stat-sub">{tilasmsSub}</span>
          </div>
          <div className="wish-bilan-stat">
            <span className="wish-bilan-stat-val">
              {t("wish.bilan_decisions", { count: totalDecisions })}
            </span>
            <span className="wish-bilan-stat-sub">{decisionsSub}</span>
          </div>
          <div className="wish-bilan-stat">
            <span className="wish-bilan-stat-val">{wallText}</span>
            <span className="wish-bilan-stat-sub">
              {t("wish.bilan_duration_sub", { run: runText })}
            </span>
          </div>
          <div className="wish-bilan-stat">
            <span className="wish-bilan-stat-val">
              {usd(totalUsd) || "$0.00"}
            </span>
            <span className="wish-bilan-stat-sub">
              {t("wish.bilan_cost_sub", { count: runsCount })}
            </span>
          </div>
        </div>
        <div className="wish-bilan-quote">
          <span>{quote}</span>
          <button
            type="button"
            className="button secondary small"
            onClick={onReopen}
          >
            <Play size={14} />
            <span>{t("wish.reopen")}</span>
          </button>
        </div>
      </div>
    </section>
  );
});

// InvestigatingSection holds the questions you asked to investigate: they wait for the lead's revision, and can still
// be answered.
export const InvestigatingSection = memo(function InvestigatingSection({
  questions,
  origin = false,
  onAnswer,
  onMove,
  onWithdraw,
}: {
  questions: OpenQuestion[];
  origin?: boolean;
  onAnswer: (
    choice: Choice,
    note: string,
    question?: Question,
  ) => Promise<void>;
  onMove?: (question: Question) => void;
  onWithdraw?: (question: Question) => void;
}) {
  const lead = useData((s) => s.lead);
  const leadDrive = useData((s) => s.leadDrive);
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
      {questions.map(({ wish, item, blocking, report }) => (
        <WishQuestion
          key={item.id}
          question={item}
          blocking={blocking}
          report={report}
          noLead={noLead(lead, leadDrive)}
          origin={origin ? wishOrigin(wish) : undefined}
          onAnswer={onAnswer}
          onMove={onMove}
          onWithdraw={onWithdraw}
        />
      ))}
    </section>
  );
});

const originCache = new WeakMap<Wish, ReactNode>();

export function wishOrigin(wish: Wish): ReactNode {
  let node = originCache.get(wish);
  if (!node) {
    node = <WishOrigin wish={wish} />;
    originCache.set(wish, node);
  }
  return node;
}

// WishOrigin marks a line with its wish, in the flying carpet of several wishes: its rank and its title.
export const WishOrigin = memo(function WishOrigin({ wish }: { wish: Wish }) {
  return (
    <span className="wish-origin" title={wish.title}>
      {wish.rank > 0 && <b>{wish.rank}</b>}
      {wish.title}
    </span>
  );
});

// WaitingTasks are the workers that wait for the user: for an answer before they edit, or to the blocking question
// they asked, whose answer continues them.
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
          {origin && wishOrigin(wish)}
          <span>
            {question && item.waitQuestionId
              ? t("page.action_waiting_question", { task: item.code, question })
              : question
                ? t("page.action_waiting", { task: item.code, question })
                : t("page.action_waiting_unknown", { task: item.code })}
          </span>
        </p>
      ))}
    </section>
  );
}

// Journal is the History tab of the wish: the lead journal for that wish (past lead batches that touched it) and the
// commands that changed it. The latest command entries show, the older ones fold behind a line (older.tsx).
// WishService.Snapshot reads the whole wish, events and lead batches included: it is read when the tab opens, then
// again as the wish changes.
function Journal({
  wish,
  onCount,
}: {
  wish: Wish;
  onCount?: (count: number) => void;
}) {
  const clients = useClients();
  const [exp, setExp] = useState<WishExport>();
  const [error, setError] = useState("");
  // What the store read of the wish: a new one means the wish changed.
  const detail = useData((s) => s.details[wish.id]);
  useEffect(() => {
    let current = true;
    const ctrl = new AbortController();
    clients.wishes
      .snapshot({ wishId: wish.id }, { signal: ctrl.signal })
      .then((res) => {
        if (!current) return;
        setExp(res.export);
        setError("");
        const cmds = journal(res.export).length;
        const batches = res.export?.leadBatches?.length ?? 0;
        onCount?.(cmds + batches);
      })
      .catch((err) => {
        if (current && !ctrl.signal.aborted) setError(message(err));
      });
    return () => {
      current = false;
      ctrl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps, @eslint-react/exhaustive-deps -- onCount is a stable callback from WishView
  }, [clients, wish, detail]);
  const entries = useMemo(() => journal(exp), [exp]);
  const batches = useMemo(() => exp?.leadBatches ?? [], [exp]);
  const totalCount = entries.length + batches.length;
  const { shown: recent, more } = useRecent(entries.length);
  return (
    <section
      role="tabpanel"
      aria-labelledby="view-tab-history"
      className="wish-section wish-journal"
      aria-label={t("page.journal")}
    >
      {error && <p className="muted-text">{error}</p>}
      {!exp && !error && (
        <ReadingLine kind="commands" state="reading" size="card-r" />
      )}
      {exp && totalCount === 0 && (
        <EmptyState art="decisions">{t("wish.no_history")}</EmptyState>
      )}
      {batches.length > 0 && <LeadJournal batches={batches} />}
      {entries.length > 0 && (
        <table className="compact-table journal-list">
          <tbody>
            {entries.slice(0, recent).map((entry) => (
              <JournalEntry key={entry.id} entry={entry} />
            ))}
          </tbody>
        </table>
      )}
      <OlderLine
        hidden={entries.length - recent}
        label={(count, hidden) =>
          count < hidden
            ? t("wish.journal_older_some", { count, hidden })
            : t("wish.journal_older", { count })
        }
        onShow={more}
      />
    </section>
  );
}

export const JournalEntry = memo(function JournalEntry({
  entry,
}: {
  entry: Entry;
}) {
  return (
    <tr className="command">
      <td>
        <time>{when(entry.at)}</time>
      </td>
      <td>{entry.command && <code>{entry.command}</code>}</td>
      <td>{entry.summary && <span>{entry.summary}</span>}</td>
    </tr>
  );
});

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
// (WishService.Delete). Its workers stop first; a worktree with no work of its own goes, the others and every branch
// stay, as the toast says after the delete.
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
