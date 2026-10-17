// The flying carpet was called the flight plan: this file, FlightPlan and the flight-plan class keep that name.
// The flying carpet of the active wishes, merged (T13): a bar at the top while something waits for you, then two
// columns (mockup L04 v2): on the left, what waits for you (the open questions of every wish, grouped by wish in the
// questions column, question-column.tsx; then the workers that wait, the proofs you can give of the azimas whose work
// is done, the wishes Djinn proposes to grant, and the questions you asked to investigate); on the right, the glance:
// each wish's workers running now and to come (glance.tsx), which opens the wish. The tasks of every wish have a tab
// of their own (task-tabs.tsx), and so have their decisions (decision-log.tsx). Every line shows the wish
// it comes from, and an answer, a stop or a grant goes back to it. Each wish keeps its own view; an empty section is hidden.
// The Djinn, the one lead, is at hand as on a wish: Talk to the Djinn in the header opens its terminal, the pill at the
// bottom writes to it.
import { type ReactNode, useCallback, useMemo, useRef, useState } from "react";

import {
  Change,
  type Choice,
  Closer,
  type Provider,
  type Question,
  type Task,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import { AttentionBar, attentionOf, useReach } from "./attention";
import { type Decision, decisionOf } from "./data/decisions";
import { useData, usePausable, useWishDetails } from "./data/djinn";
import {
  type AzimaGroup,
  flightPlan,
  mergeNest,
  spent,
  taskMapOf,
} from "./data/flight";
import { fixerOf, noLead, watcherRuns } from "./data/format";
import { litIfrits } from "./data/ifrit";
import { DecisionLog } from "./decision-log";
import { t } from "./i18n";
import { Inbox } from "./inbox";
import { LeadMessage } from "./lead-message";
import { useEyeNeeds } from "./djinn-eye";
import { useWrites } from "./marks";
import { useKeepPlace } from "./scroll-anchor";
import { StatusBadge } from "./status";
import {
  AzimaCard,
  azimaFinished,
  DraftAzimaCard,
  MoveAzimaDialog,
  proofWords,
  useStickyRoom,
} from "./azima";
import {
  MoveQuestionDialog,
  SleepQuestionDialog,
  WithdrawQuestionDialog,
} from "./wish-dialogs";
import { TaskSections, type View, ViewTabs } from "./task-tabs";
import { OperatingLoad } from "./operating-load";
import { SpentLine } from "./usage";
import { Machine } from "./visuals";
import { QuestionColumn, questionGroups } from "./question-column";
import { Glance } from "./glance";
import { WishTask } from "./wish-task";
import { HourglassLine } from "./hourglass";
import { PageHeader } from "./page-header";
import {
  LeadButton,
  leadTrouble,
  recordedAgent,
  type SeatChoice,
} from "./wish-head";
import {
  GrantCard,
  InvestigatingSection,
  WaitingTasks,
  wishOrigin,
} from "./wish-view";

const noTasks: readonly Task[] = [];

export function FlightPlan({
  wishes,
  onOpen,
  onOpenProject,
  onToast,
  onSetupAgents,
}: {
  // The active wishes, by rank.
  wishes: Wish[];
  onOpen: (wishId: string) => void;
  // Opens a project's panel, from its line in the inbox.
  onOpenProject?: (projectId: string) => void;
  onToast: (text: string) => void;
  onSetupAgents?: () => void;
}) {
  const projects = useData((s) => s.projects);
  const lead = useData((s) => s.lead);
  const leadExit = useData((s) => s.leadExit);
  const leadStartError = useData((s) => s.leadStartError);
  const leadPrompt = useData((s) => s.leadPrompt);
  const leadDrive = useData((s) => s.leadDrive);
  const allWishes = useData((s) => s.wishes);
  const projectById = useMemo(
    () => new Map(projects.map((p) => [p.id, p])),
    [projects],
  );
  const pausable = usePausable();
  const wishIds = useMemo(() => wishes.map((w) => w.id), [wishes]);
  const details = useWishDetails(wishIds);
  const plan = useMemo(() => flightPlan(wishes, details), [wishes, details]);
  const allTasks = useMemo(
    () => wishes.flatMap((w) => details[w.id]?.tasks ?? []),
    [wishes, details],
  );
  const flightPlanSpent = useMemo(() => spent(allTasks), [allTasks]);
  const scrollRef = useRef<HTMLDivElement>(null);
  const keepPlace = useKeepPlace(scrollRef);
  useStickyRoom(scrollRef, false);
  const {
    clients,
    act,
    quiet,
    answer,
    enlighten,
    move,
    withdraw,
    later,
    sleep,
  } = useWrites(onToast);
  const waits =
    plan.questions.length +
    plan.waitingLead.length +
    plan.waiting.length +
    plan.ready.length +
    plan.proofs.length;
  // Nothing waits and nothing runs: the questions column shows the Djinn asleep, and says so.
  const calm =
    plan.loaded &&
    wishes.length > 0 &&
    waits +
      plan.running.length +
      plan.investigating.length +
      plan.asleep.length ===
      0;
  const renderAttentionOrigin = useCallback(
    (wish: Wish) => wishOrigin(wish),
    [],
  );
  // The lit ifrits of every active wish, the latest activity first: a band each, at the top of what waits for you.
  const ifrits = useMemo(
    () =>
      litIfrits(
        wishes.flatMap((wish) =>
          (details[wish.id]?.tasks ?? []).map((item) => ({ wish, item })),
        ),
      ),
    [wishes, details],
  );
  // A worker waiting or an ifrit lit: the Djinn's eye lets its flame shine.
  useEyeNeeds(plan.waiting.length > 0 || ifrits.length > 0);
  const attention = useMemo(
    () =>
      attentionOf(
        [...plan.questions, ...plan.waitingLead],
        plan.waiting,
        plan.ready,
        renderAttentionOrigin,
        leadPrompt,
        ifrits,
        leadDrive?.limit && {
          limit: leadDrive.limit,
          accounts: leadDrive.accounts,
        },
      ),
    [
      plan.questions,
      plan.waitingLead,
      plan.waiting,
      plan.ready,
      renderAttentionOrigin,
      leadPrompt,
      ifrits,
      leadDrive,
    ],
  );
  const [view, setView] = useState<View>("main");
  const [movingAzima, setMovingAzima] = useState<{
    wishId: string;
    task: Task;
  } | null>(null);
  const [movingQuestion, setMovingQuestion] = useState<{
    wishId: string;
    question: Question;
  } | null>(null);
  const [withdrawingQuestion, setWithdrawingQuestion] = useState<{
    wishId: string;
    question: Question;
  } | null>(null);
  const moveQuestion = useCallback(
    (question: Question) =>
      setMovingQuestion({ wishId: question.wishId, question }),
    [],
  );
  const withdrawQuestion = useCallback(
    (question: Question) =>
      setWithdrawingQuestion({ wishId: question.wishId, question }),
    [],
  );
  const laterQuestion = useCallback(
    (question: Question) =>
      later(question.wishId, question.id, !!question.laterTime),
    [later],
  );
  const [sleepingQuestion, setSleepingQuestion] = useState<Question | null>(
    null,
  );
  const wakeQuestion = useCallback(
    (question: Question) => sleep(question.wishId, question.id, [], true),
    [sleep],
  );
  // The questions column, one group per wish (question-column.tsx).
  const questionsByWish = useMemo(
    () => questionGroups(wishes, plan.questions),
    [wishes, plan.questions],
  );
  // What a link between a decision and a task brings into sight in the other tab: its id.
  const [focus, setFocus] = useState("");
  const show = useCallback((to: View, id = "") => {
    setView(to);
    setFocus(id);
  }, []);
  // What the plan's tabs show of the bar's lines: the open questions, the waiting workers and the grants in the
  // first tab, the answered questions in the decisions.
  const shown = useMemo(
    () =>
      new Set([
        ...plan.questions.map(({ item }) => `question-${item.id}`),
        ...plan.decisions.map(({ item }) => `decision-${item.id}`),
        ...plan.waiting.map(({ item }) => `waiting-${item.id}`),
        ...plan.ready.map((wish) => `grant-${wish.id}`),
        ...ifrits.map(({ item }) => `task-${item.id}`),
      ]),
    [plan.questions, plan.decisions, plan.waiting, plan.ready, ifrits],
  );
  const { reach, to } = useReach(show, shown);
  const openTask = useCallback((task: Task) => to(`task-${task.id}`), [to]);

  const wishById = useMemo(
    () => new Map(wishes.map((w) => [w.id, w])),
    [wishes],
  );

  const decisionsByWishId = useMemo(() => {
    const map = new Map<string, Decision[]>();
    for (const d of plan.decisions) {
      let list = map.get(d.wish.id);
      if (!list) {
        list = [];
        map.set(d.wish.id, list);
      }
      list.push(d.item);
    }
    return map;
  }, [plan.decisions]);

  // A task's decision, among those of its wish: two wishes may each have a Q01.
  const decisionOfTask = useCallback(
    (task: Task, wish: Wish) =>
      decisionOf(task, decisionsByWishId.get(wish.id) ?? []),
    [decisionsByWishId],
  );

  const handleDecision = useCallback(
    (task: Task) => {
      const wish = wishById.get(task.wishId);
      if (!wish) return;
      const d = decisionOfTask(task, wish);
      show("decisions", d?.id);
    },
    [wishById, decisionOfTask, show],
  );

  const handleStop = useCallback(
    (task: Task) => {
      quiet(
        act(task.wishId, () => clients.tasks.stop({ taskId: task.id }), [
          Change.TASK,
        ]),
      );
    },
    [quiet, act, clients.tasks],
  );

  const handleStart = useCallback(
    (task: Task) => {
      quiet(
        act(task.wishId, () => clients.tasks.start({ taskId: task.id }), [
          Change.TASK,
        ]),
      );
    },
    [quiet, act, clients.tasks],
  );

  const handleSend = useCallback(
    (text: string, task: Task) =>
      act(task.wishId, () => clients.tasks.send({ taskId: task.id, text }), []),
    [act, clients.tasks],
  );

  const handleHold = useCallback(
    (pause: boolean, task: Task) => {
      quiet(
        act(
          task.wishId,
          () =>
            pause
              ? clients.tasks.pause({ taskId: task.id })
              : clients.tasks.resume({ taskId: task.id }),
          [Change.TASK],
        ),
      );
    },
    [quiet, act, clients.tasks],
  );

  const handleDone = useCallback(
    (note: string, task: Task) =>
      act(
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
    [act, clients.tasks],
  );

  // An ifrit put out: a message lights it again, in its own session and worktree.
  const handleRelight = useCallback(
    (text: string, task: Task) =>
      act(
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
    [act, clients.tasks],
  );

  // Where the merges of a wish show: under the task they serve.
  const nestOf = useCallback(
    (wishId: string) => mergeNest(details[wishId]?.tasks ?? noTasks),
    [details],
  );

  const handleTurnComments = useCallback(
    (task: Task) =>
      act(
        task.wishId,
        () =>
          clients.tasks.turnComments({
            taskId: task.id,
            by: Closer.DEVELOPER,
          }),
        [Change.TASK],
        t("task.turn_comments_done", { task: task.code }),
      ),
    [act, clients.tasks],
  );

  const renderTask = useCallback(
    function row(item: Task): ReactNode {
      const wish = wishById.get(item.wishId);
      const nest = nestOf(item.wishId);
      const merges = nest.under.get(item.id);
      return (
        <WishTask
          key={item.id}
          task={item}
          origin={wish ? wishOrigin(wish) : undefined}
          fixedBy={fixerOf(item, details[item.wishId]?.tasks ?? noTasks)?.code}
          decision={wish ? decisionOfTask(item, wish) : undefined}
          focused={focus === item.id}
          onDecision={handleDecision}
          project={projectById.get(item.projectId)}
          onStop={handleStop}
          onStart={handleStart}
          onSend={handleSend}
          onHold={pausable ? handleHold : undefined}
          onDone={handleDone}
          onRelight={handleRelight}
          merges={merges}
          nested={merges?.map(row)}
          mergeOpen={nest.host.get(focus) === item.id}
          alsoMerged={nest.links.get(item.id)}
          onTurnComments={handleTurnComments}
        />
      );
    },
    [
      nestOf,
      details,
      wishById,
      decisionOfTask,
      focus,
      handleDecision,
      projectById,
      handleStop,
      handleStart,
      handleSend,
      pausable,
      handleHold,
      handleDone,
      handleRelight,
      handleTurnComments,
    ],
  );

  // The tasks of a wish, by id: what an azima waits for.
  const tasksOf = useCallback(
    (wish: Wish) => taskMapOf(details[wish.id]?.tasks ?? []),
    [details],
  );

  const handleValidateAzima = useCallback(
    (azima: Task) =>
      act(
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
    [act, clients.tasks],
  );

  // An azima renamed from its card: djinn task update --title, written to its plan file too.
  const handleRenameAzima = useCallback(
    (azima: Task, title: string) => {
      quiet(
        act(
          azima.wishId,
          () => clients.tasks.update({ taskId: azima.id, title }),
          [Change.TASK],
        ),
      );
    },
    [quiet, act, clients.tasks],
  );

  const activeAzimas = useMemo(
    () => plan.azimas.filter((x) => !azimaFinished(x.item.azima)),
    [plan.azimas],
  );
  const doneAzimas = useMemo(
    () => plan.azimas.filter((x) => azimaFinished(x.item.azima)),
    [plan.azimas],
  );
  const showDone = useMemo(
    () =>
      doneAzimas.some(
        ({ wish, item }) =>
          item.azima.id === focus ||
          item.parts.some(
            (p) => p.id === (nestOf(wish.id).host.get(focus) ?? focus),
          ),
      ),
    [doneAzimas, focus, nestOf],
  );

  const showFinished = useMemo(
    () =>
      plan.finished.some(
        ({ wish, item }) =>
          item.id === (nestOf(wish.id).host.get(focus) ?? focus),
      ),
    [plan.finished, focus, nestOf],
  );

  const renderAzimaItem = useCallback(
    ({ wish, item }: { wish: Wish; item: AzimaGroup }) => (
      <AzimaCard
        key={item.azima.id}
        azima={item.azima}
        parts={item.parts}
        tasks={tasksOf(wish)}
        origin={wishOrigin(wish)}
        render={renderTask}
        focus={focus}
        nest={nestOf(wish.id)}
        onValidate={handleValidateAzima}
        onRename={handleRenameAzima}
      />
    ),
    [
      tasksOf,
      nestOf,
      renderTask,
      focus,
      handleValidateAzima,
      handleRenameAzima,
    ],
  );

  const renderMovingItem = useCallback(
    ({ item }: { wish: Wish; item: Task }) => renderTask(item),
    [renderTask],
  );

  const showDrafts = useMemo(
    () => plan.drafts.some(({ item }) => item.id === focus),
    [plan.drafts, focus],
  );

  const handleOpenDraft = useCallback(
    (wish: Wish, task: Task) => {
      quiet(
        act(
          wish.id,
          () => clients.tasks.open({ azima: task.id }),
          [Change.TASK],
          t("azima.opened_toast", { code: task.code }),
        ),
      );
    },
    [quiet, act, clients.tasks],
  );

  const handleMoveDraft = useCallback((wish: Wish, task: Task) => {
    setMovingAzima({ wishId: wish.id, task });
  }, []);

  const renderDraftItem = useCallback(
    ({ wish, item }: { wish: Wish; item: Task }) => (
      <DraftAzimaCard
        key={item.id}
        azima={item}
        tasks={tasksOf(wish)}
        origin={wishOrigin(wish)}
        focus={focus}
        onOpen={() => handleOpenDraft(wish, item)}
        onMove={() => handleMoveDraft(wish, item)}
        onRename={handleRenameAzima}
      />
    ),
    [tasksOf, focus, handleOpenDraft, handleMoveDraft, handleRenameAzima],
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

  const handleGrant = useCallback(
    (wish?: Wish) => {
      if (!wish) return;
      quiet(
        act(
          wish.id,
          () => clients.wishes.grant({ wishId: wish.id }),
          [Change.WISH],
          t("wish.granted_toast"),
        ),
      );
    },
    [quiet, act, clients.wishes],
  );

  const renderDecisionOrigin = useCallback(
    ({ wish }: { wish: Wish }) => wishOrigin(wish),
    [],
  );
  const checkDecisionNoLead = useCallback(() => noLead(lead), [lead]);
  // resumeLead resumes the Djinn, or starts one of another agent, on no wish; what Djinn says of it shows as a toast.
  const resumeLead = (provider?: Provider) => {
    let note = "";
    return act(
      "",
      async () => {
        ({ note } = await clients.lead.resume({ provider }));
      },
      [Change.WISH],
      () => note,
    );
  };
  // moveLead moves the lead Djinn drives to another seat; what Djinn says of it shows as a toast.
  const moveLead = (seat: SeatChoice) => {
    let note = "";
    return act(
      "",
      async () => {
        ({ note } = await clients.lead.move(seat));
      },
      [Change.WISH],
      () => note,
    );
  };
  const trouble = leadTrouble(leadExit, leadStartError);
  const handleTaskFocus = useCallback(
    (id: string) => show("tasks", id),
    [show],
  );

  return (
    <div className="wish-view flight-plan review">
      <PageHeader
        middle={<OperatingLoad />}
        actions={
          <LeadButton
            focused={false}
            recorded={recordedAgent(lead)}
            trouble={trouble}
            loadAgents={async () =>
              (await clients.ui.getEnvironment({})).providers
            }
            onLead={() => quiet(resumeLead())}
            onPick={(provider) => quiet(resumeLead(provider))}
            onSetup={onSetupAgents}
            drive={leadDrive}
            onMove={(seat) => quiet(moveLead(seat))}
          />
        }
      >
        <strong>{t("plan.title")}</strong>
      </PageHeader>
      <div className="mission-scroll" ref={keepPlace}>
        <AttentionBar items={attention} reach={reach} />
        <div className="hero mission-header review-head">
          <div className="hero-copy">
            <span className="eyebrow">{t("plan.eyebrow")}</span>
            <h1>{t("plan.title")}</h1>
            <p>
              {wishes.length
                ? t("plan.detail", { count: wishes.length })
                : t("plan.empty")}
            </p>
          </div>
          <div className="hero-visual">
            <Machine
              active={plan.running.some(({ item }) => !watcherRuns(item))}
              pulse={plan.running
                .filter(({ item }) => !watcherRuns(item))
                .map(({ item }) => item.id)
                .sort()
                .join(",")}
            />
          </div>
        </div>

        <div className="overview-content">
          <ViewTabs
            view={view}
            main={t("plan.title")}
            tasks={
              plan.moving.length +
              plan.finished.length +
              plan.azimas.reduce((n, { item }) => n + item.parts.length, 0)
            }
            decisions={plan.decisions.length}
            onView={(to) => show(to)}
          />
          {view === "decisions" && (
            <DecisionLog
              items={plan.decisions}
              origin={renderDecisionOrigin}
              noLead={checkDecisionNoLead}
              focus={focus}
              onTask={handleTaskFocus}
            />
          )}
          {view === "tasks" && (
            <TaskSections
              moving={plan.moving}
              finished={plan.finished}
              showFinished={showFinished}
              azimas={activeAzimas}
              doneAzimas={doneAzimas}
              drafts={plan.drafts}
              fold="plan"
              showDone={showDone}
              showDrafts={showDrafts}
              renderAzima={renderAzimaItem}
              renderDraft={renderDraftItem}
              render={renderMovingItem}
              aside={<SpentLine spent={flightPlanSpent} />}
            />
          )}
          {view === "main" && (
            <Inbox
              onOpen={onOpen}
              onOpenProject={onOpenProject}
              onToast={onToast}
            />
          )}
          {view === "main" && (
            <>
              <div className="main-columns">
                <div className="main-moves">
                  <QuestionColumn
                    groups={questionsByWish}
                    headings
                    onOpenWish={onOpen}
                    noLead={noLead(lead)}
                    onAnswer={handleQuestionAnswer}
                    onEnlighten={handleQuestionEnlighten}
                    onMove={moveQuestion}
                    onWithdraw={withdrawQuestion}
                    onLater={laterQuestion}
                    onSleep={setSleepingQuestion}
                    asleep={plan.asleep}
                    onWake={wakeQuestion}
                    calm={calm}
                  />
                  {plan.waiting.length +
                    plan.proofs.length +
                    plan.ready.length >
                    0 && (
                    <section
                      className="your-move"
                      id="action-center"
                      aria-label={t("panels.your_move")}
                    >
                      {plan.waiting.length > 0 && (
                        <WaitingTasks waiting={plan.waiting} origin />
                      )}
                      {plan.proofs.length > 0 && (
                        <section className="plan-waiting">
                          <div className="section-heading">
                            <h3>
                              {t("plan.proofs")}
                              <span className="count">
                                {plan.proofs.length}
                              </span>
                            </h3>
                          </div>
                          {plan.proofs.map(({ wish, item }, i) => (
                            <p
                              className="plan-line"
                              key={`${item.azima.id}-${i}`}
                            >
                              <StatusBadge
                                tone="proof"
                                label={proofWords([item.need])}
                                title={t("azima.needs", {
                                  needs: item.need.needs,
                                })}
                              />
                              {wishOrigin(wish)}
                              <span>
                                {t("plan.proof_line", {
                                  azima: item.azima.code,
                                  box: item.need.box,
                                })}
                              </span>
                            </p>
                          ))}
                        </section>
                      )}
                      {plan.ready.map((wish) => (
                        <GrantCard
                          key={wish.id}
                          wish={wish}
                          origin={wishOrigin(wish)}
                          onGrant={handleGrant}
                        />
                      ))}
                    </section>
                  )}
                  {plan.investigating.length > 0 && (
                    <InvestigatingSection
                      questions={plan.investigating}
                      origin
                      onAnswer={handleQuestionAnswer}
                      onMove={moveQuestion}
                      onWithdraw={withdrawQuestion}
                    />
                  )}
                </div>
                {wishes.length > 0 && (
                  <Glance
                    wishes={wishes}
                    tasksOf={details}
                    all={allWishes.length > 0 ? allWishes : wishes}
                    onOpenWish={onOpen}
                    onOpenTask={openTask}
                  />
                )}
              </div>

              {/* The machine's live graphs are in the Hourglass (hourglass.tsx): here, its load in a line. */}
              <HourglassLine />
            </>
          )}
        </div>
      </div>
      {movingAzima && (
        <MoveAzimaDialog
          azima={movingAzima.task}
          wishes={wishes}
          currentWishId={movingAzima.wishId}
          onClose={() => setMovingAzima(null)}
          onMove={(targetWishId) => {
            const moving = movingAzima;
            setMovingAzima(null);
            quiet(
              act(
                moving.wishId,
                () =>
                  clients.tasks.move({
                    azima: moving.task.id,
                    wish: targetWishId,
                  }),
                [Change.TASK, Change.WISH],
                t("azima.moved_toast", { code: moving.task.code }),
              ),
            );
          }}
        />
      )}
      {movingQuestion && (
        <MoveQuestionDialog
          question={movingQuestion.question}
          wishes={allWishes.length > 0 ? allWishes : wishes}
          currentWishId={movingQuestion.wishId}
          onClose={() => setMovingQuestion(null)}
          onMove={move}
        />
      )}
      {sleepingQuestion && (
        <SleepQuestionDialog
          question={sleepingQuestion}
          tasks={details[sleepingQuestion.wishId]?.tasks ?? []}
          currentWishId={sleepingQuestion.wishId}
          onClose={() => setSleepingQuestion(null)}
          onSleep={sleep}
        />
      )}
      {withdrawingQuestion && (
        <WithdrawQuestionDialog
          question={withdrawingQuestion.question}
          currentWishId={withdrawingQuestion.wishId}
          onClose={() => setWithdrawingQuestion(null)}
          onWithdraw={withdraw}
        />
      )}
      <LeadMessage stopped={!lead || !!trouble} onResume={() => resumeLead()} />
    </div>
  );
}
