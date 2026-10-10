// The flight plan of the active wishes, merged (T13): a bar at the top while something waits for you, then each
// wish at a glance, then what waits for you (the open questions of every wish, the blocking ones first, the workers
// that wait, the wishes Djinn proposes to grant, the proofs you can give of the azimas whose work is done), and the
// questions you asked to investigate. The tasks of every wish
// have a tab of their own (task-tabs.tsx), and so have their decisions (decision-log.tsx). Every line shows the wish
// it comes from, and an answer, a stop or a grant goes back to it. Each wish keeps its own view; an empty section is hidden.
import { type CSSProperties, useRef, useState } from "react";

import {
  Change,
  Closer,
  type Task,
  TaskStatus,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import { AttentionBar, attentionOf } from "./attention";
import { decisionOf } from "./data/decisions";
import { useData, usePausable, useWishDetails } from "./data/djinn";
import { flightPlan, spent } from "./data/flight";
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
import { AzimaCard, azimaFinished, proofWords } from "./azima";
import { TaskSections, type View, ViewTabs } from "./task-tabs";
import { SpentLine } from "./usage";
import { Machine } from "./visuals";
import { WishQuestion } from "./wish-question";
import { WishTask } from "./wish-task";
import {
  GrantCard,
  InvestigatingSection,
  WaitingTasks,
  WishOrigin,
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
  const pausable = usePausable();
  const details = useWishDetails(wishes.map((w) => w.id));
  const plan = flightPlan(wishes, details);
  const scrollRef = useRef<HTMLDivElement>(null);
  const keepPlace = useKeepPlace(scrollRef);
  const { clients, act, quiet, answer, enlighten, mark } = useWrites(onToast);
  const waits =
    plan.questions.length +
    plan.waiting.length +
    plan.ready.length +
    plan.proofs.length;
  const attention = attentionOf(
    plan.questions,
    plan.waiting,
    plan.ready,
    (wish) => <WishOrigin wish={wish} />,
  );
  const [view, setView] = useState<View>("main");
  // What a link between a decision and a task brings into sight in the other tab: its id.
  const [focus, setFocus] = useState("");
  const show = (to: View, id = "") => (setView(to), setFocus(id));
  const wishOf = new Map(plan.investigating.map((x) => [x.item.id, x.wish]));
  // A task's decision, among those of its wish: two wishes may each have a Q01.
  const decisionOfTask = (task: Task, wish: Wish) =>
    decisionOf(
      task,
      plan.decisions.filter((d) => d.wish.id === wish.id).map((d) => d.item),
    );

  const renderTask = (wish: Wish, item: Task) => (
    <WishTask
      key={item.id}
      task={item}
      origin={<WishOrigin wish={wish} />}
      decision={decisionOfTask(item, wish)}
      focused={focus === item.id}
      onDecision={() => show("decisions", decisionOfTask(item, wish)?.id)}
      project={projects.find((p) => p.id === item.projectId)}
      onStop={() =>
        quiet(
          act(wish.id, () => clients.tasks.stop({ taskId: item.id }), [
            Change.TASK,
          ]),
        )
      }
      onSend={(text) =>
        act(wish.id, () => clients.tasks.send({ taskId: item.id, text }), [])
      }
      onHold={
        pausable
          ? (pause) =>
              quiet(
                act(
                  wish.id,
                  () =>
                    pause
                      ? clients.tasks.pause({ taskId: item.id })
                      : clients.tasks.resume({ taskId: item.id }),
                  [Change.TASK],
                ),
              )
          : undefined
      }
      onDone={(note) =>
        act(
          wish.id,
          () =>
            clients.tasks.done({
              taskId: item.id,
              note,
              by: Closer.DEVELOPER,
            }),
          [Change.TASK],
          t("task.marked_done", { task: item.code }),
        )
      }
    />
  );
  // The tasks of a wish, by id: what an azima waits for.
  const tasksOf = (wish: Wish) =>
    new Map((details[wish.id]?.tasks ?? []).map((task) => [task.id, task]));

  return (
    <div className="wish-view flight-plan review">
      <header className="topbar">
        <div className="breadcrumbs">
          <strong>{t("plan.title")}</strong>
        </div>
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
              origin={({ wish }) => <WishOrigin wish={wish} />}
              noLead={({ wish }) => noLead(wish)}
              focus={focus}
              onTask={(id) => show("tasks", id)}
            />
          )}
          {view === "tasks" && (
            <TaskSections
              moving={plan.moving}
              azimas={plan.azimas.filter((x) => !azimaFinished(x.item.azima))}
              doneAzimas={plan.azimas.filter((x) =>
                azimaFinished(x.item.azima),
              )}
              fold="plan"
              showDone={plan.azimas.some(
                ({ item }) =>
                  azimaFinished(item.azima) &&
                  (item.azima.id === focus ||
                    item.parts.some((p) => p.id === focus)),
              )}
              renderAzima={({ wish, item }) => (
                <AzimaCard
                  key={item.azima.id}
                  azima={item.azima}
                  parts={item.parts}
                  tasks={tasksOf(wish)}
                  origin={<WishOrigin wish={wish} />}
                  render={(task) => renderTask(wish, task)}
                  focus={focus}
                  onValidate={() =>
                    act(
                      wish.id,
                      () =>
                        clients.tasks.done({
                          taskId: item.azima.id,
                          note: t("azima.validated_note"),
                          by: Closer.DEVELOPER,
                        }),
                      [Change.TASK],
                      t("azima.validated", { code: item.azima.code }),
                    )
                  }
                />
              )}
              render={({ wish, item }) => renderTask(wish, item)}
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
                            (x) => x.status === TaskStatus.DONE,
                          ).length ?? 0;
                        const total = detail?.tasks.length ?? 0;
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
                              origin={<WishOrigin wish={wish} />}
                              blocking={blocking}
                              noLead={noLead(wish)}
                              onAnswer={(choice, note) =>
                                answer(wish.id, item.id, choice, note)
                              }
                              onMark={(kind, remove) =>
                                mark(wish.id, item.id, kind, remove)
                              }
                              onEnlighten={(note) =>
                                enlighten(wish.id, item.id, note)
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
                              <WishOrigin wish={wish} />
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
                          origin={<WishOrigin wish={wish} />}
                          onGrant={() =>
                            quiet(
                              act(
                                wish.id,
                                () => clients.wishes.grant({ wishId: wish.id }),
                                [Change.WISH],
                                t("wish.granted_toast"),
                              ),
                            )
                          }
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
                      onAnswer={(id, choice, note) =>
                        answer(wishOf.get(id)!.id, id, choice, note)
                      }
                      onMark={(id, kind, remove) =>
                        mark(wishOf.get(id)!.id, id, kind, remove)
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
    </div>
  );
}
