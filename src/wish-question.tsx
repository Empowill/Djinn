// A question of a wish, on Clément's question card: its options by letter, what is at stake, the recommendation, and
// an answer that goes to QuestionService.Answer. Answered, it is a decision.
import {
  ArrowRight,
  Check,
  CheckCircle2,
  ChevronDown,
  CornerDownRight,
} from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useState } from "react";

import { Choice, type Question } from "../gen/ts/plan/v1/plan_pb";
import { answerText, choiceOf, letter, when } from "./data/format";
import { t } from "./i18n";
import { MarkdownBody } from "./markdown-body";

export function WishQuestion({
  question: q,
  expanded: open = false,
  onAnswer,
}: {
  question: Question;
  // Opened at first: the one question waiting, say.
  expanded?: boolean;
  // Answers the question; resolves once djinn has it. A rejection keeps the card open.
  onAnswer: (choice: Choice, note: string) => Promise<void>;
}) {
  const answered = !!q.answer;
  const [expanded, setExpanded] = useState(open && !answered);
  const [choice, setChoice] = useState<Choice>(
    q.options.length ? Choice.A : Choice.YES,
  );
  const [note, setNote] = useState("");
  const [sending, setSending] = useState(false);
  const send = async () => {
    setSending(true);
    try {
      await onAnswer(choice, note.trim());
    } finally {
      setSending(false);
    }
  };
  return (
    <motion.article
      id={`question-${q.id}`}
      className={`question-card ${answered ? "answered" : "blocking"}`}
      initial={{ opacity: 0, y: 14 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, x: 30 }}
      transition={{ duration: 0.35 }}
    >
      <button
        className="question-heading"
        onClick={() => setExpanded(!expanded)}
        aria-expanded={expanded}
      >
        <span className="question-id">
          {answered ? <Check size={14} /> : q.code}
        </span>
        <div>
          <span className="question-meta">
            {answered ? (
              <>
                {q.code} <span>·</span> {t("panels.decision_recorded")}
              </>
            ) : (
              t("question.open")
            )}
          </span>
          <h3>{q.text}</h3>
          {answered && <p className="answer-value">{answerText(q)}</p>}
        </div>
        <ChevronDown size={16} className={expanded ? "rotated" : ""} />
      </button>
      <AnimatePresence initial={false}>
        {expanded && (
          <motion.div
            className="question-content"
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.3 }}
          >
            <div className="question-inner">
              {q.context && (
                <div className="question-context">
                  <MarkdownBody text={q.context} />
                </div>
              )}
              {answered ? (
                <div className="answered-info">
                  <span>
                    <CheckCircle2 size={15} />{" "}
                    {t("question.decided", {
                      when: when(q.answer?.createTime),
                    })}
                  </span>
                  {q.answer?.note && <p>{q.answer.note}</p>}
                </div>
              ) : (
                <>
                  {q.recommendation && (
                    <div className="recommendation">
                      <span className="mini-spark">✳</span>
                      <div>
                        <span>{t("panels.recommendation")}</span>
                        <MarkdownBody text={q.recommendation} />
                      </div>
                    </div>
                  )}
                  {q.options.length > 0 && (
                    <div className="question-options">
                      {q.options.map((option, index) => {
                        const selected = choice === choiceOf(index);
                        return (
                          <button
                            key={index}
                            className={`option ${selected ? "selected" : ""}`}
                            onClick={() => setChoice(choiceOf(index))}
                            aria-pressed={selected}
                          >
                            <span className="option-radio">
                              {selected && <span />}
                            </span>
                            <div>
                              <strong>{letter(index)}</strong>
                              <p>{option}</p>
                            </div>
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
                  <div className="question-footer">
                    <span>
                      <CornerDownRight size={14} />
                      {t("question.goes_to_lead")}
                    </span>
                    <button
                      className="button accent small"
                      disabled={sending || choice === Choice.UNSPECIFIED}
                      onClick={() => void send()}
                    >
                      {q.options.length
                        ? t("panels.confirm_choice")
                        : t("panels.confirm_answer")}
                      <ArrowRight size={14} />
                    </button>
                  </div>
                </>
              )}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </motion.article>
  );
}
