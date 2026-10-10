// A question of a wish, on Clément's question card, laid out to decide at a glance: its title, the recommendation
// boxed first, the options as buttons, then what is at stake. Two gestures, one note field for both: "Enlighten me",
// on the left, asks the lead to find out more first (QuestionService.Enlighten), the note saying what to look into;
// "Rub the lamp", in the lamp's yellow, answers with the option selected, the recommended one until you pick another,
// and the note with it. Open, it says how much it holds up: red when a task waits for it, orange under its before
// words ("before the merge"), grey when it can wait. Being investigated, it folds to one line out of the way, and
// opens on a click; revised, it waits for you again, open. Answered, it is a decision: no mark asks to read or
// approve it again, and the decision log shows it. Djinn types each answer in the lead's terminal; a wish without a
// lead session has no lead to tell, and the card says so. Its rounds, each request and each revision, fold below.
import {
  Check,
  ChevronDown,
  CornerDownRight,
  History,
  Lamp,
  Lightbulb,
} from "lucide-react";
import { motion } from "motion/react";
import { type ReactNode, memo, useState } from "react";

import {
  Choice,
  MarkKind,
  type Question,
  RoundKind,
} from "../gen/ts/plan/v1/plan_pb";
import { EMPTY_BLOCKING } from "./data/flight";
import {
  answerText,
  choiceOf,
  investigating,
  letter,
  recommendedChoice,
  when,
} from "./data/format";
import { t } from "./i18n";
import { MarkButtons } from "./marks";
import { MarkdownBody } from "./markdown-body";
import { StatusBadge } from "./status";

export const WishQuestion = memo(function WishQuestion({
  question: q,
  expanded: open = false,
  origin,
  blocking = EMPTY_BLOCKING,
  noLead = false,
  onAnswer,
  onMark,
  onEnlighten,
}: {
  question: Question;
  // Where the question comes from, in the flight plan of several wishes: its wish.
  origin?: ReactNode;
  // The codes of the tasks that wait for this answer.
  blocking?: readonly string[];
  // A decision opened at first. An open question is always open.
  expanded?: boolean;
  // The wish has no lead session: no lead is told the answer, it waits in the wish's brief.
  noLead?: boolean;
  // Answers the question; resolves once djinn has it. A rejection keeps the card open.
  onAnswer: (
    choice: Choice,
    note: string,
    question?: Question,
  ) => Promise<void>;
  // Marks an open question read.
  onMark?: (
    kind: MarkKind,
    remove: boolean,
    question?: Question,
  ) => Promise<void>;
  // Asks the lead to investigate before deciding, the note saying what to look into.
  onEnlighten?: (note: string, question?: Question) => Promise<void>;
}) {
  const answered = !!q.answer;
  // A message built by hand (a test) may leave the lists out.
  const rounds = q.rounds ?? [];
  const digging = investigating(q);
  // What you asked to look into, while it is being investigated.
  const asked = digging ? rounds.at(-1)?.note : undefined;
  const recommended = answered ? undefined : recommendedChoice(q);
  const [expanded, setExpanded] = useState(open);
  // Being investigated, it is one line until you open it.
  const [peek, setPeek] = useState(false);
  const folded = digging && !peek;
  // The recommended option is selected until you pick one: a revision that recommends another moves it. With no
  // option named, nothing is selected: rubbing the lamp waits for your pick.
  const [picked, setPicked] = useState<Choice>();
  const choice = picked ?? (q.options.length ? recommended : Choice.YES);
  const [note, setNote] = useState("");
  const [sending, setSending] = useState(false);
  const busy = async (run: () => Promise<void>) => {
    setSending(true);
    try {
      await run();
    } catch {
      // The toast says why; the card stays as it was.
    } finally {
      setSending(false);
    }
  };
  const status = answered ? (
    <StatusBadge tone="done" label={t("panels.decision_recorded")} />
  ) : digging ? (
    <StatusBadge tone="investigating" label={t("question.investigating")} />
  ) : blocking.length ? (
    <StatusBadge
      tone="failed"
      label={t("page.blocking", { tasks: blocking.join(", ") })}
    />
  ) : q.before ? (
    <StatusBadge tone="waiting" label={q.before} />
  ) : (
    <StatusBadge tone="later" label={t("question.can_wait")} />
  );
  const level =
    answered || digging
      ? ""
      : blocking.length
        ? "is-blocking"
        : q.before
          ? ""
          : "can-wait";
  const heading = (
    <>
      <span className="question-id">
        {answered ? <Check size={14} aria-label={q.code} /> : q.code}
      </span>
      <div className="question-title">
        <span className="question-meta">
          {origin}
          {status}
          {q.revision > 0 && (
            <span className="revised-badge">
              {t("question.revised", { count: q.revision })}
            </span>
          )}
        </span>
        <h3>{q.text}</h3>
        {answered && <p className="answer-value">{answerText(q)}</p>}
      </div>
    </>
  );
  const body = answered ? expanded : !folded;
  return (
    <motion.article
      id={`question-${q.id}`}
      className={`question-card ${answered ? "answered" : digging ? "investigating" : "open"} ${level}${folded ? " folded" : ""}`}
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, x: 30 }}
      transition={{ duration: 0.25 }}
    >
      <div className="question-top">
        {folded ? (
          <button
            className="question-heading question-fold"
            onClick={() => setPeek(true)}
            aria-expanded={false}
          >
            <Lightbulb size={15} aria-hidden="true" />
            <span className="question-id">{q.code}</span>
            <span className="question-fold-text">{q.text}</span>
            {origin}
            {status}
            {asked && (
              <span className="question-fold-note">
                {t("question.investigating_asked", { note: asked })}
              </span>
            )}
            <ChevronDown size={16} />
          </button>
        ) : answered || digging ? (
          <button
            className="question-heading"
            onClick={() => (answered ? setExpanded(!expanded) : setPeek(false))}
            aria-expanded={answered ? expanded : true}
          >
            {heading}
            <ChevronDown
              size={16}
              className={answered && !expanded ? "" : "rotated"}
            />
          </button>
        ) : (
          <div className="question-heading">{heading}</div>
        )}
        {onMark && !answered && !folded && (
          <MarkButtons item={q} onMark={onMark} />
        )}
      </div>
      {body && (
        <div className="question-inner">
          {digging && (
            <p className="investigating-note">
              <Lightbulb size={15} aria-hidden="true" />
              <span>
                {asked
                  ? t("question.investigating_note", { note: asked })
                  : t("question.investigating_detail")}
              </span>
            </p>
          )}
          {q.recommendation && (
            <section className="recommendation">
              <span className="eyebrow">
                {t("panels.recommendation")}
                {recommended !== undefined &&
                  recommended !== Choice.YES &&
                  ` · ${letter(recommended - Choice.A)}`}
              </span>
              <MarkdownBody text={q.recommendation} />
            </section>
          )}
          {answered ? (
            <div className="answered-info">
              <span>
                {t("question.decided", { when: when(q.answer?.createTime) })}
              </span>
              {q.answer?.note && <p>{q.answer.note}</p>}
              {noLead && (
                <p className="no-lead">{t("question.no_lead_answered")}</p>
              )}
            </div>
          ) : (
            <>
              {q.options.length > 0 && (
                <div
                  className="question-options"
                  role="group"
                  aria-label={t("page.options")}
                >
                  {q.options.map((option, index) => {
                    const selected = choice === choiceOf(index);
                    return (
                      <button
                        key={index}
                        className={`option ${selected ? "selected" : ""}`}
                        onClick={() => setPicked(choiceOf(index))}
                        aria-pressed={selected}
                      >
                        <strong className="option-letter">
                          {letter(index)}
                        </strong>
                        <p>{option}</p>
                        {recommended === choiceOf(index) && (
                          <span className="option-recommended">
                            {t("question.recommended")}
                          </span>
                        )}
                      </button>
                    );
                  })}
                </div>
              )}
              <textarea
                value={note}
                onChange={(e) => setNote(e.target.value)}
                placeholder={
                  q.options.length
                    ? t("question.note_placeholder")
                    : t("question.answer_placeholder")
                }
                rows={2}
                maxLength={2000}
                aria-label={t("question.note_label")}
              />
              <div className="question-actions">
                {onEnlighten && !digging && (
                  <button
                    className="button secondary lamp-enlighten"
                    title={t("question.enlighten_detail")}
                    disabled={sending}
                    onClick={() =>
                      void busy(async () => {
                        await onEnlighten(note.trim(), q);
                        setNote("");
                      })
                    }
                  >
                    <Lightbulb size={14} />
                    {t("question.enlighten")}
                  </button>
                )}
                <span className="spacer" />
                <button
                  className="button accent lamp-rub"
                  title={
                    choice === undefined
                      ? t("question.rub_pick")
                      : t("question.rub_detail")
                  }
                  disabled={sending || choice === undefined}
                  onClick={() => {
                    if (choice !== undefined)
                      void busy(() => onAnswer(choice, note.trim(), q));
                  }}
                >
                  <Lamp size={14} />
                  {t("question.rub")}
                </button>
              </div>
              <p className="question-hint">
                <CornerDownRight size={14} aria-hidden="true" />
                {t(noLead ? "question.no_lead" : "question.goes_to_lead")}
              </p>
            </>
          )}
          {q.context && (
            <section className="question-context">
              <h4>{t("page.context")}</h4>
              <div className="prose">
                <MarkdownBody text={q.context} />
              </div>
            </section>
          )}
          {rounds.length > 0 && <Rounds question={q} />}
        </div>
      )}
    </motion.article>
  );
});

// Rounds is a question's history, folded: each request to investigate and each revision, dated.
function Rounds({ question }: { question: Question }) {
  return (
    <details className="question-rounds">
      <summary>
        <History size={13} aria-hidden="true" />
        {t("question.rounds", { count: question.rounds.length })}
      </summary>
      <table className="compact-table">
        <tbody>
          {question.rounds.map((round, i) => (
            <tr key={i}>
              <td>
                <time>{when(round.createTime)}</time>
              </td>
              <td>
                {round.kind === RoundKind.ENLIGHTEN
                  ? t("page.round_enlighten")
                  : t("page.round_revise")}
              </td>
              <td>
                {round.kind === RoundKind.ENLIGHTEN
                  ? round.note
                  : round.recommendation &&
                    t("question.round_before", {
                      recommendation: round.recommendation,
                    })}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </details>
  );
}
