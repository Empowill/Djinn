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
import { type ReactNode, useState } from "react";

import { Choice, type Question, RoundKind } from "../gen/ts/plan/v1/plan_pb";
import {
  answerText,
  choiceOf,
  investigating,
  letter,
  recommendedChoice,
  when,
} from "./data/format";
import { ActionDockHeader } from "./action-dock";
import { t } from "./i18n";
import { MarkButtons, type OnMark } from "./marks";
import { MarkdownBody } from "./markdown-body";
import { StatusBadge } from "./status";
import { TooltipButton } from "./tooltip";

export function WishQuestion({
  question: q,
  domId,
  expanded: open = false,
  origin,
  blocking = [],
  noLead = false,
  compact = false,
  dockMode = false,
  onAnswer,
  onMark,
  onEnlighten,
}: {
  question: Question;
  // A stable unique id for mounted dock revisions; the regular page keeps the historical fallback.
  domId?: string;
  // Where the question comes from, in the flight plan of several wishes: its wish.
  origin?: ReactNode;
  // The codes of the tasks that wait for this answer.
  blocking?: readonly string[];
  // A decision opened at first. An open question is always open.
  expanded?: boolean;
  // The wish has no lead session: no lead is told the answer, it waits in the wish's brief.
  noLead?: boolean;
  // Compact cards keep secondary context and the optional note under an inline details disclosure.
  compact?: boolean;
  // Dock cards keep the response action visible above a bounded, scrollable decision body.
  dockMode?: boolean;
  // Answers the question; resolves once djinn has it. A rejection keeps the card open.
  onAnswer: (choice: Choice, note: string) => Promise<void>;
  // Marks an open question read.
  onMark?: OnMark;
  // Asks the lead to investigate before deciding, the note saying what to look into.
  onEnlighten?: (note: string) => Promise<void>;
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
  const [note, setNote] = useState("");
  // A nonempty note without an explicit option is a custom response, not the recommendation.
  const customResponse =
    q.options.length > 0 && picked === undefined && note.trim() !== "";
  const choice = customResponse
    ? Choice.UNSPECIFIED
    : (picked ?? (q.options.length ? recommended : Choice.YES));
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
  const status = dockMode ? null : answered ? (
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
        {(origin || status || q.revision > 0) && (
          <span className="question-meta">
            {origin}
            {status}
            {q.revision > 0 && (
              <span className="revised-badge">
                {t("question.revised", { count: q.revision })}
              </span>
            )}
          </span>
        )}
        {!dockMode && <h3>{q.text}</h3>}
        {answered && <p className="answer-value">{answerText(q)}</p>}
      </div>
    </>
  );
  const body = answered ? expanded : !folded;
  const recommendation = q.recommendation ? (
    <section className="recommendation">
      <span className="eyebrow">
        {t("panels.recommendation")}
        {recommended !== undefined &&
          recommended !== Choice.YES &&
          ` · ${letter(recommended - Choice.A)}`}
      </span>
      <MarkdownBody text={q.recommendation} />
    </section>
  ) : null;
  const options =
    !answered && q.options.length > 0 ? (
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
              <strong className="option-letter">{letter(index)}</strong>
              <p>{option}</p>
              {dockMode && selected && (
                <Check
                  className="dock-choice-check"
                  size={14}
                  aria-hidden="true"
                />
              )}
              {recommended === choiceOf(index) && (
                <span className="option-recommended">
                  {t("question.recommended")}
                </span>
              )}
            </button>
          );
        })}
      </div>
    ) : null;
  const noteField = !answered ? (
    <textarea
      value={note}
      onChange={(e) => {
        setNote(e.target.value);
        setPicked(undefined);
      }}
      placeholder={
        q.options.length
          ? t(
              onEnlighten
                ? "question.note_placeholder_enlighten"
                : "question.note_placeholder",
            )
          : t(
              onEnlighten
                ? "question.answer_placeholder_enlighten"
                : "question.answer_placeholder",
            )
      }
      rows={2}
      maxLength={2000}
      aria-label={t("question.note_label")}
    />
  ) : null;
  const actions =
    !answered && !dockMode ? (
      <div className="question-actions">
        {onEnlighten && !digging && (
          <button
            className="button secondary lamp-enlighten"
            title={t("question.enlighten_detail")}
            disabled={sending}
            onClick={() =>
              void busy(async () => {
                await onEnlighten(note.trim());
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
              : customResponse
                ? t("question.rub_aria")
                : t("question.rub_detail")
          }
          disabled={sending || choice === undefined}
          aria-busy={sending}
          onClick={() => {
            if (choice !== undefined)
              void busy(() => onAnswer(choice, note.trim()));
          }}
        >
          <Lamp size={14} />
          {dockMode &&
          !customResponse &&
          choice !== undefined &&
          q.options.length > 0
            ? t("question.rub_choice", {
                choice: letter(choice - Choice.A),
              })
            : t("question.rub")}
        </button>
      </div>
    ) : null;
  const responseLabel =
    !customResponse && choice !== undefined && q.options.length > 0
      ? t("question.rub_choice_aria", {
          choice: letter(choice - Choice.A),
        })
      : t("question.rub_aria");
  const responseTooltip = customResponse
    ? t("question.rub_aria")
    : choice !== undefined && q.options.length > 0
      ? t("question.rub_choice", {
          choice: letter(choice - Choice.A),
        })
      : t("question.rub_detail");
  const dockHeaderActions = dockMode ? (
    <ActionDockHeader>
      {onMark && !answered && !folded && (
        <MarkButtons item={q} onMark={onMark} iconOnly />
      )}
      {onEnlighten && !digging && (
        <TooltipButton
          className="dock-header-icon dock-header-enlighten"
          label={t("question.enlighten")}
          tooltip={t("question.enlighten_detail")}
          disabled={sending}
          onClick={() =>
            void busy(async () => {
              await onEnlighten(note.trim());
              setNote("");
            })
          }
        >
          <Lightbulb size={14} aria-hidden="true" />
        </TooltipButton>
      )}
      {!answered && (
        <TooltipButton
          className="dock-header-submit button accent"
          label={responseLabel}
          tooltip={
            choice === undefined ? t("question.rub_pick") : responseTooltip
          }
          disabled={sending || choice === undefined}
          aria-busy={sending}
          onClick={() => {
            if (choice !== undefined)
              void busy(() => onAnswer(choice, note.trim()));
          }}
        >
          <Lamp size={14} aria-hidden="true" />
        </TooltipButton>
      )}
    </ActionDockHeader>
  ) : null;
  const hint = !answered ? (
    <p className="question-hint">
      <CornerDownRight size={14} aria-hidden="true" />
      {t(noLead ? "question.no_lead" : "question.goes_to_lead")}
    </p>
  ) : null;
  const context = q.context ? (
    <section
      className={`question-context${dockMode ? " dock-question-context" : ""}`}
    >
      {!dockMode && <h4>{t("page.context")}</h4>}
      <div className="prose">
        <MarkdownBody text={q.context} />
      </div>
    </section>
  ) : null;
  const roundsHistory = rounds.length > 0 ? <Rounds question={q} /> : null;
  const answerInfo = answered ? (
    <div className="answered-info">
      <span>{t("question.decided", { when: when(q.answer?.createTime) })}</span>
      {q.answer?.note && <p>{q.answer.note}</p>}
      {noLead && <p className="no-lead">{t("question.no_lead_answered")}</p>}
    </div>
  ) : null;
  return (
    <motion.article
      id={domId ?? `question-${q.id}`}
      className={`question-card ${answered ? "answered" : digging ? "investigating" : "open"} ${level}${folded ? " folded" : ""}${compact ? " compact" : ""}`}
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
        {onMark && !answered && !folded && !dockMode && (
          <MarkButtons item={q} onMark={onMark} />
        )}
      </div>
      {dockHeaderActions}
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
          {compact ? (
            answered ? (
              <>
                {answerInfo}
                <details className="question-compact-details">
                  <summary>{t("page.details")}</summary>
                  {recommendation}
                  {context}
                  {roundsHistory}
                </details>
              </>
            ) : (
              <>
                {dockMode ? (
                  <>
                    <div className="dock-question-body">
                      {context}
                      {(blocking.length > 0 || q.before) && (
                        <p className="dock-question-impact">
                          <span>{t("question.dock_impact")}</span>
                          <span>
                            {blocking.length
                              ? t("page.blocking", {
                                  tasks: blocking.join(", "),
                                })
                              : q.before}
                          </span>
                        </p>
                      )}
                      {options}
                      {noteField && (
                        <div className="dock-question-note">{noteField}</div>
                      )}
                      {roundsHistory}
                    </div>
                  </>
                ) : (
                  <>
                    {options}
                    <details className="question-compact-details">
                      <summary>{t("page.details")}</summary>
                      {recommendation}
                      {noteField}
                      {context}
                      {roundsHistory}
                    </details>
                    {actions}
                    {hint}
                  </>
                )}
              </>
            )
          ) : (
            <>
              {recommendation}
              {answered ? (
                answerInfo
              ) : (
                <>
                  {options}
                  {noteField}
                  {actions}
                  {hint}
                </>
              )}
              {context}
              {roundsHistory}
            </>
          )}
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
