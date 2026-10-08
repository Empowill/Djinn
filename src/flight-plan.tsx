// The flight plan of the active wishes, merged (T13): what waits for you first (the open questions of every wish,
// the blocking ones first, the workers that wait, the wishes Djinn proposes to grant), then what runs, then the
// latest decisions. Every line shows the wish it comes from, and an answer, a stop or a grant goes back to it. Each
// wish keeps its own view; an empty section is hidden.
import { CheckCircle2, Sparkles } from "lucide-react";
import { useRef } from "react";

import {
  Change,
  type Choice,
  TaskStatus,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useData, useStore, useWishDetails } from "./data/djinn";
import { flightPlan, spent } from "./data/flight";
import { isOpen } from "./data/format";
import { t } from "./i18n";
import { useKeepPlace } from "./scroll-anchor";
import { SpentLine } from "./usage";
import { Machine } from "./visuals";
import { WishQuestion } from "./wish-question";
import { WishTask } from "./wish-task";
import { WaitingTasks, WishOrigin } from "./wish-view";

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
  const clients = useClients();
  const store = useStore();
  const projects = useData((s) => s.projects);
  const details = useWishDetails(wishes.map((w) => w.id));
  const plan = flightPlan(wishes, details);
  const scrollRef = useRef<HTMLDivElement>(null);
  const keepPlace = useKeepPlace(scrollRef);
  const waits = plan.questions.length + plan.waiting.length + plan.ready.length;

  // act runs a write for a wish; djinn's refusal shows as a toast, and what changed in that wish is read again.
  const act = async (
    wishId: string,
    run: () => Promise<unknown>,
    changes: Change[],
    done?: string,
  ) => {
    try {
      await run();
      if (done) onToast(done);
    } catch (error) {
      onToast(message(error));
      throw error;
    } finally {
      void store.changed(wishId, changes);
    }
  };
  const quiet = (promise: Promise<unknown>) => void promise.catch(() => {});
  const answer = (
    wishId: string,
    questionId: string,
    choice: Choice,
    note: string,
  ) =>
    act(
      wishId,
      () =>
        clients.questions.answer({
          question: { ref: { case: "id", value: questionId } },
          choice,
          note,
          wishId,
        }),
      [Change.QUESTION, Change.WISH],
    );

  return (
    <div className="wish-view flight-plan">
      <header className="topbar">
        <div className="breadcrumbs">
          <strong>{t("plan.title")}</strong>
        </div>
      </header>
      <div className="mission-scroll" ref={keepPlace}>
        <div className="hero mission-header">
          <div className="hero-copy">
            <h1>{t("plan.title")}</h1>
            <p>
              {wishes.length
                ? t("plan.detail", { count: wishes.length })
                : t("plan.empty")}
            </p>
          </div>
          <div className="hero-visual">
            <Machine active={plan.running.length > 0} />
          </div>
        </div>

        <div className="overview-content">
          {wishes.length > 0 && (
            <section className="plan-wishes" aria-label={t("sidebar.wishes")}>
              {wishes.map((wish) => {
                const detail = details[wish.id];
                const open = detail?.questions.filter(isOpen).length ?? 0;
                const running =
                  detail?.tasks.filter((x) => x.status === TaskStatus.RUNNING)
                    .length ?? 0;
                return (
                  <button
                    key={wish.id}
                    className="plan-wish"
                    onClick={() => onOpen(wish.id)}
                    title={t("plan.open_wish")}
                  >
                    <span className="plan-wish-rank">{wish.rank}</span>
                    <strong>{wish.title}</strong>
                    <span className="plan-wish-counts">
                      {open > 0 && (
                        <span className="waiting">
                          {t("plan.questions", { count: open })}
                        </span>
                      )}
                      {running > 0 && (
                        <span>{t("plan.running", { count: running })}</span>
                      )}
                      {detail && (
                        <span>
                          {t("plan.tasks", { count: detail.tasks.length })}
                        </span>
                      )}
                      {wish.ready && (
                        <span className="waiting">{t("wish.ready")}</span>
                      )}
                    </span>
                    <SpentLine spent={spent(detail?.tasks ?? [])} />
                  </button>
                );
              })}
            </section>
          )}

          {waits > 0 && (
            <section
              className="action-center"
              id="action-center"
              aria-label={t("panels.your_move")}
            >
              <div className="action-center-heading">
                <div>
                  <span className="eyebrow">{t("panels.next_action")}</span>
                  <h2>{t("panels.your_move")}</h2>
                  {plan.questions.length > 0 && (
                    <p>
                      {t("wish.questions_wait", {
                        count: plan.questions.length,
                      })}
                    </p>
                  )}
                </div>
              </div>
              {plan.questions.length > 0 && (
                <section className="decisions-section">
                  <div className="section-heading">
                    <h3>
                      {t("panels.decisions")}
                      <span className="count">{plan.questions.length}</span>
                    </h3>
                  </div>
                  {plan.questions.map(({ wish, item, blocking }) => (
                    <WishQuestion
                      key={item.id}
                      question={item}
                      origin={<WishOrigin wish={wish} />}
                      blocking={blocking}
                      expanded={plan.questions.length === 1}
                      onAnswer={(choice, note) =>
                        answer(wish.id, item.id, choice, note)
                      }
                    />
                  ))}
                </section>
              )}
              {plan.waiting.length > 0 && (
                <WaitingTasks waiting={plan.waiting} origin />
              )}
              {plan.ready.map((wish) => (
                <article
                  className="step-result-action wish-grant"
                  key={wish.id}
                >
                  <div>
                    <span className="step-result-kicker">
                      {t("wish.ready")} · <WishOrigin wish={wish} />
                    </span>
                    <h3>{t("wish.ready_title")}</h3>
                  </div>
                  <button
                    type="button"
                    className="button accent"
                    onClick={() =>
                      quiet(
                        act(
                          wish.id,
                          () => clients.wishes.grant({ wishId: wish.id }),
                          [Change.WISH],
                          t("wish.granted_toast"),
                        ),
                      )
                    }
                  >
                    <Sparkles size={14} />
                    {t("wish.grant")}
                  </button>
                </article>
              ))}
            </section>
          )}

          {plan.running.length > 0 && (
            <section className="wish-section" aria-label={t("page.running")}>
              <div className="section-heading">
                <h3>
                  {t("page.running")}
                  <span className="count">{plan.running.length}</span>
                </h3>
              </div>
              {plan.running.map(({ wish, item }) => (
                <WishTask
                  key={item.id}
                  task={item}
                  origin={<WishOrigin wish={wish} />}
                  project={projects.find((p) => p.id === item.projectId)}
                  onStop={() =>
                    quiet(
                      act(
                        wish.id,
                        () => clients.tasks.stop({ taskId: item.id }),
                        [Change.TASK],
                      ),
                    )
                  }
                />
              ))}
            </section>
          )}

          {plan.decisions.length > 0 && (
            <section
              className="wish-section plan-decisions"
              aria-label={t("plan.recent_decisions")}
            >
              <div className="section-heading">
                <h3>
                  <CheckCircle2 size={14} />
                  {t("plan.recent_decisions")}
                </h3>
              </div>
              {plan.decisions.map(({ wish, item }) => (
                <WishQuestion
                  key={item.id}
                  question={item}
                  origin={<WishOrigin wish={wish} />}
                  onAnswer={async () => {}}
                />
              ))}
            </section>
          )}

          {plan.loaded &&
            wishes.length > 0 &&
            waits + plan.running.length + plan.decisions.length === 0 && (
              <p className="muted-text">{t("plan.calm")}</p>
            )}
        </div>
      </div>
    </div>
  );
}
