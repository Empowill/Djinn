// The flight plan of the active wishes, merged (T13): a bar at the top while something waits for you, then each
// wish at a glance, then what waits for you (the open questions of every wish, the blocking ones first, the workers
// that wait, the wishes Djinn proposes to grant, the proofs you can give of the azimas whose work is done), and the
// questions you asked to investigate. The tasks of every wish
// have a tab of their own (task-tabs.tsx), and so have their decisions (decision-log.tsx). Every line shows the wish
// it comes from, and an answer, a stop or a grant goes back to it. Each wish keeps its own view; an empty section is hidden.
import {
  type CSSProperties,
  useCallback,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  Change,
  type Choice,
  Closer,
  type MarkKind,
  type Question,
  type Task,
  TaskStatus,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import { AttentionBar, attentionOf } from "./attention";
import { type Decision, decisionOf } from "./data/decisions";
import { useData, usePausable, useWishDetails } from "./data/djinn";
import {
  type AzimaGroup,
  flightPlan,
  isAzima,
  spent,
  taskMapOf,
} from "./data/flight";
import {
  investigating,
  noLead,
  runningOf,
  waitsForYou,
  watcherRuns,
  wishTone,
} from "./data/format";
import { DecisionLog } from "./decision-log";
import { t } from "./i18n";
import { Inbox } from "./inbox";
import { useWrites } from "./marks";
import { useKeepPlace } from "./scroll-anchor";
import { CountPill, StatusBadge } from "./status";
import {
  AzimaCard,
  azimaFinished,
  DraftAzimaCard,
  MoveAzimaDialog,
  proofWords,
} from "./azima";
import { MoveQuestionDialog, WithdrawQuestionDialog } from "./wish-dialogs";
import { TaskSections, type View, ViewTabs } from "./task-tabs";
import { OperatingLoad } from "./operating-load";
import { SpentLine } from "./usage";
import { Machine } from "./visuals";
import { WishQuestion } from "./wish-question";
import { WishTask } from "./wish-task";
import {
  GrantCard,
  InvestigatingSection,
  WaitingTasks,
  wishOrigin,
} from "./wish-view";

export function FlightPlan({
  wishes,
  onOpen,
  onToast,
}: {
  // The active wishes, by rank.
  wishes: Wish[];
  onOpen: (wishId: string) => void;
  onToast: (text: string) => void;
}) {
  const projects = useData((s) => s.projects);
  const allWishes = useData((s) => s.wishes);
  const projectById = useMemo(
    () => new Map(projects.map((p) => [p.id, p])),
    [projects],
  );
  const pausable = usePausable();
  const wishIds = useMemo(() => wishes.map((w) => w.id), [wishes]);
  const details = useWishDetails(wishIds);
  const plan = useMemo(() => flightPlan(wishes, details), [wishes, details]);
  const scrollRef = useRef<HTMLDivElement>(null);
  const keepPlace = useKeepPlace(scrollRef);
  const { clients, act, quiet, answer, enlighten, mark, move, withdraw } =
    useWrites(onToast);
  const waits =
    plan.questions.length +
    plan.waiting.length +
    plan.ready.length +
    plan.proofs.length;
  const renderAttentionOrigin = useCallback(
    (wish: Wish) => wishOrigin(wish),
    [],
  );
  const attention = useMemo(
    () =>
      attentionOf(
        plan.questions,
        plan.waiting,
        plan.ready,
        renderAttentionOrigin,
      ),
    [plan.questions, plan.waiting, plan.ready, renderAttentionOrigin],
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
  // What a link between a decision and a task brings into sight in the other tab: its id.
  const [focus, setFocus] = useState("");
  const show = useCallback((to: View, id = "") => {
    setView(to);
    setFocus(id);
  }, []);

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

  const renderTask = useCallback(
    (item: Task) => {
      const wish = wishById.get(item.wishId);
      return (
        <WishTask
          key={item.id}
          task={item}
          origin={wish ? wishOrigin(wish) : undefined}
          decision={wish ? decisionOfTask(item, wish) : undefined}
          focused={focus === item.id}
          onDecision={handleDecision}
          project={projectById.get(item.projectId)}
          onStop={handleStop}
          onSend={handleSend}
          onHold={pausable ? handleHold : undefined}
          onDone={handleDone}
        />
      );
    },
    [
      wishById,
      decisionOfTask,
      focus,
      handleDecision,
      projectById,
      handleStop,
      handleSend,
      pausable,
      handleHold,
      handleDone,
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
        ({ item }) =>
          item.azima.id === focus || item.parts.some((p) => p.id === focus),
      ),
    [doneAzimas, focus],
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
        onValidate={handleValidateAzima}
      />
    ),
    [tasksOf, renderTask, focus, handleValidateAzima],
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
      />
    ),
    [tasksOf, focus, handleOpenDraft, handleMoveDraft],
  );

  const handleQuestionAnswer = useCallback(
    (choice: Choice, note: string, q?: Question) => {
      if (q) return answer(q.wishId, q.id, choice, note);
      return Promise.resolve();
    },
    [answer],
  );

  const handleQuestionMark = useCallback(
    (kind: MarkKind, remove: boolean, q?: Question) => {
      if (q) return mark(q.wishId, q.id, kind, remove);
      return Promise.resolve();
    },
    [mark],
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
  const checkDecisionNoLead = useCallback(
    ({ wish }: { wish: Wish }) => noLead(wish),
    [],
  );
  const handleTaskFocus = useCallback(
    (id: string) => show("tasks", id),
    [show],
  );

  return (
    <div className="wish-view flight-plan review">
      <header className="topbar">
        <div className="breadcrumbs">
          <strong>{t("plan.title")}</strong>
        </div>
        <OperatingLoad />
      </header>
      <div className="mission-scroll" ref={keepPlace}>
        <AttentionBar items={attention} />
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
            />
          </div>
        </div>

        <div className="overview-content">
          <ViewTabs
            view={view}
            main={t("plan.title")}
            tasks={
              plan.moving.length +
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
              azimas={activeAzimas}
              doneAzimas={doneAzimas}
              drafts={plan.drafts}
              fold="plan"
              showDone={showDone}
              showDrafts={showDrafts}
              renderAzima={renderAzimaItem}
              renderDraft={renderDraftItem}
              render={renderMovingItem}
            />
          )}
          {view === "main" && <Inbox onOpen={onOpen} onToast={onToast} />}
          {view === "main" && (
            <>
              <div className="main-columns">
                {wishes.length > 0 && (
                  <div className="main-side">
                    <section
                      className="plan-wishes"
                      aria-label={t("sidebar.wishes")}
                    >
                      {wishes.map((wish) => {
                        const detail = details[wish.id];
                        const open =
                          detail?.questions.filter(waitsForYou).length ?? 0;
                        const digging =
                          detail?.questions.filter(investigating).length ?? 0;
                        const { running, watching } = runningOf(
                          detail?.tasks ?? [],
                        );
                        const done =
                          detail?.tasks.filter(
                            (x) => x.status === TaskStatus.DONE && !isAzima(x),
                          ).length ?? 0;
                        const total =
                          detail?.tasks.filter((x) => !isAzima(x)).length ?? 0;
                        const failed =
                          detail?.tasks.filter(
                            (x) => x.status === TaskStatus.FAILED,
                          ).length ?? 0;
                        return (
                          <button
                            key={wish.id}
                            className={`plan-wish tone-${wishTone(wish, open, running, watching)}`}
                            onClick={() => onOpen(wish.id)}
                            title={t("plan.open_wish")}
                          >
                            <span className="plan-wish-title">
                              <span className="plan-wish-rank">
                                {wish.rank}
                              </span>
                              <strong>{wish.title}</strong>
                            </span>
                            <span className="plan-wish-counts">
                              <StatusBadge
                                tone={wishTone(wish, open, running, watching)}
                                label={
                                  open
                                    ? t("plan.questions", { count: open })
                                    : wish.ready
                                      ? t("wish.ready")
                                      : running
                                        ? t("plan.running", { count: running })
                                        : watching
                                          ? t("plan.watching", {
                                              count: watching,
                                            })
                                          : t("pill.calm")
                                }
                              />
                              {digging > 0 && (
                                <CountPill
                                  tone="investigating"
                                  count={digging}
                                  label={t("pill.investigating_detail", {
                                    count: digging,
                                  })}
                                />
                              )}
                              {open > 0 && running > 0 && (
                                <CountPill
                                  tone="running"
                                  count={running}
                                  label={t("plan.running", { count: running })}
                                />
                              )}
                              {(open > 0 || running > 0) && watching > 0 && (
                                <CountPill
                                  tone="watching"
                                  count={watching}
                                  label={t("plan.watching", {
                                    count: watching,
                                  })}
                                />
                              )}
                              {failed > 0 && (
                                <CountPill
                                  tone="failed"
                                  count={failed}
                                  label={t("pill.failed_detail", {
                                    count: failed,
                                  })}
                                />
                              )}
                              {total > 0 && (
                                <CountPill
                                  tone="done"
                                  count={done}
                                  label={t("pill.done_detail", {
                                    done,
                                    count: total,
                                  })}
                                >
                                  / {total}
                                </CountPill>
                              )}
                            </span>
                            {total > 0 && (
                              <span
                                className="plan-wish-progress"
                                style={
                                  {
                                    "--done": `${(100 * done) / total}%`,
                                  } as CSSProperties
                                }
                                aria-hidden="true"
                              />
                            )}
                            <SpentLine spent={spent(detail?.tasks ?? [])} />
                          </button>
                        );
                      })}
                    </section>
                  </div>
                )}

                {waits > 0 && (
                  <div className="main-moves">
                    <section
                      className="action-center"
                      id="action-center"
                      aria-label={t("panels.your_move")}
                    >
                      <div className="section-title">
                        <h2>{t("panels.your_move")}</h2>
                        <p>
                          {plan.questions.length > 0
                            ? t("wish.questions_wait", {
                                count: plan.questions.length,
                              })
                            : t("panels.your_move_detail")}
                        </p>
                      </div>
                      {plan.questions.length > 0 && (
                        <section className="decisions-section">
                          <div className="section-heading">
                            <h3>
                              {t("panels.decisions")}
                              <span className="count">
                                {plan.questions.length}
                              </span>
                            </h3>
                          </div>
                          {plan.questions.map(({ wish, item, blocking }) => (
                            <WishQuestion
                              key={item.id}
                              question={item}
                              origin={wishOrigin(wish)}
                              blocking={blocking}
                              noLead={noLead(wish)}
                              onAnswer={handleQuestionAnswer}
                              onMark={handleQuestionMark}
                              onEnlighten={handleQuestionEnlighten}
                              onMove={(q) =>
                                setMovingQuestion({
                                  wishId: wish.id,
                                  question: q,
                                })
                              }
                              onWithdraw={(q) =>
                                setWithdrawingQuestion({
                                  wishId: wish.id,
                                  question: q,
                                })
                              }
                            />
                          ))}
                        </section>
                      )}
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
                  </div>
                )}

                {plan.investigating.length > 0 && (
                  <div className="main-side">
                    <InvestigatingSection
                      questions={plan.investigating}
                      origin
                      onAnswer={handleQuestionAnswer}
                      onMark={handleQuestionMark}
                      onMove={(q) =>
                        setMovingQuestion({
                          wishId: q.wishId,
                          question: q,
                        })
                      }
                      onWithdraw={(q) =>
                        setWithdrawingQuestion({
                          wishId: q.wishId,
                          question: q,
                        })
                      }
                    />
                  </div>
                )}
              </div>

              {plan.loaded &&
                wishes.length > 0 &&
                waits + plan.running.length + plan.investigating.length ===
                  0 && <p className="muted-text">{t("plan.calm")}</p>}
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
      {withdrawingQuestion && (
        <WithdrawQuestionDialog
          question={withdrawingQuestion.question}
          currentWishId={withdrawingQuestion.wishId}
          onClose={() => setWithdrawingQuestion(null)}
          onWithdraw={withdraw}
        />
      )}
    </div>
  );
}
