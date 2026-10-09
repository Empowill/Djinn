// A question of a wish, on Clément's question card, laid out to decide at a glance: its title, the recommendation
// boxed first, the options as buttons, then what is at stake. Two lamp buttons: "Rub the lamp" answers with the
// recommended option in one click (a mark the lamp turns into the answer), "Enlighten me" asks the lead to find out
// more first (QuestionService.Enlighten). Answered, it is a decision: no mark asks to read or approve it again, and
// the decision log shows it. Its rounds, each request and each revision, fold below.
import {
  ArrowRight,
  Check,
  ChevronDown,
  CornerDownRight,
  History,
  Lamp,
  Lightbulb,
} from "lucide-react";
import { motion } from "motion/react";
import { type ReactNode, useState } from "react";

import {
  Choice,
  MarkKind,
  type Question,
  RoundKind,
} from "../gen/ts/plan/v1/plan_pb";
import {
  answerText,
  choiceOf,
  investigating,
  letter,
  recommendedChoice,
  when,
} from "./data/format";
import { t } from "./i18n";
import { MarkButtons, type OnMark } from "./marks";
import { MarkdownBody } from "./markdown-body";
import { StatusBadge } from "./status";

export function WishQuestion({
  question: q,
  expanded: open = false,
  origin,
  blocking = [],
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
  // Answers the question; resolves once djinn has it. A rejection keeps the card open.
  onAnswer: (choice: Choice, note: string) => Promise<void>;
  // Marks an open question read, or approves it, which answers it with the recommended option.
  onMark?: OnMark;
  // Asks the lead to investigate before deciding.
  onEnlighten?: (note: string) => Promise<void>;
}) {
  const answered = !!q.answer;
  // A message built by hand (a test) may leave the lists out.
  const rounds = q.rounds ?? [];
  const digging = investigating(q);
  const recommended = answered ? undefined : recommendedChoice(q);
  const rubbable = !!q.recommendation && recommended !== undefined;
  const [expanded, setExpanded] = useState(open);
  // The recommended option is chosen until you pick one: a revision that recommends another moves it.
  const [picked, setPicked] = useState<Choice>();
  const choice =
    picked ?? (q.options.length ? (recommended ?? Choice.A) : Choice.YES);
  const [note, setNote] = useState("");
  const [asking, setAsking] = useState(false);
  const [dig, setDig] = useState("");
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
  ) : (
    <StatusBadge tone="waiting" label={t("question.open")} />
  );
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
  const body = answered ? expanded : true;
  return (
    <motion.article
      id={`question-${q.id}`}
      className={`question-card ${answered ? "answered" : digging ? "investigating" : "open"} ${blocking.length ? "is-blocking" : ""}`}
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, x: 30 }}
      transition={{ duration: 0.25 }}
    >
      <div className="question-top">
        {answered ? (
          <button
            className="question-heading"
            onClick={() => setExpanded(!expanded)}
            aria-expanded={expanded}
          >
            {heading}
            <ChevronDown size={16} className={expanded ? "rotated" : ""} />
          </button>
        ) : (
          <div className="question-heading">{heading}</div>
        )}
        {onMark && !answered && (
          <MarkButtons item={q} approve={false} onMark={onMark} />
        )}
      </div>
      {body && (
        <div className="question-inner">
          {digging && (
            <p className="investigating-note">
              <Lightbulb size={15} aria-hidden="true" />
              <span>
                {rounds.at(-1)?.note
                  ? t("question.investigating_note", {
                      note: rounds.at(-1)!.note,
                    })
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
                {rubbable && onMark && (
                  <button
                    className="button accent lamp-rub"
                    title={t("question.rub_detail")}
                    disabled={sending}
                    onClick={() =>
                      void busy(() => onMark(MarkKind.APPROVED, false))
                    }
                  >
                    <Lamp size={14} />
                    {t("question.rub")}
                  </button>
                )}
                {onEnlighten && !digging && (
                  <button
                    className="button secondary lamp-enlighten"
                    title={t("question.enlighten_detail")}
                    aria-expanded={asking}
                    onClick={() => setAsking(!asking)}
                  >
                    <Lightbulb size={14} />
                    {t("question.enlighten")}
                  </button>
                )}
                <span className="spacer" />
                <button
                  className="button secondary small"
                  disabled={sending || choice === Choice.UNSPECIFIED}
                  onClick={() => void busy(() => onAnswer(choice, note.trim()))}
                >
                  {q.options.length
                    ? t("panels.confirm_choice")
                    : t("panels.confirm_answer")}
                  <ArrowRight size={14} />
                </button>
              </div>
              {asking && onEnlighten && (
                <div className="enlighten-form">
                  <textarea
                    value={dig}
                    onChange={(e) => setDig(e.target.value)}
                    placeholder={t("question.enlighten_placeholder")}
                    rows={2}
                    maxLength={2000}
                    aria-label={t("question.enlighten_label")}
                    autoFocus
                  />
                  <button
                    className="button accent small"
                    disabled={sending}
                    onClick={() =>
                      void busy(async () => {
                        await onEnlighten(dig.trim());
                        setAsking(false);
                        setDig("");
                      })
                    }
                  >
                    {t("question.enlighten_send")}
                  </button>
                </div>
              )}
              <p className="question-hint">
                <CornerDownRight size={14} aria-hidden="true" />
                {t("question.goes_to_lead")}
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
}

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
