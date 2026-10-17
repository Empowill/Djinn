// A question of a wish, read top-down: its title, then what happened and what is at stake (its context), the report it
// names, the recommendation boxed, then the options, the note and the actions at the bottom. The explanation always
// comes before the call to act. A command in its title, an option, the context or the recommendation is selectable,
// with a copy button that never answers; a link in them opens where it points, and an open move shows its first link
// as a button.
// In the questions column (question-column.tsx, mockup L04 v2), a question has two states: closed, its two-line row
// (its code, its title, how much it holds up); open, in full, its context never cut, its options lettered A, B, C,
// picked by their letter or digit, the note and the gestures always in sight. Enter on it puts the caret in the note,
// Escape in the note gives the focus back to the question, then clears your pick. Elsewhere (the questions being
// investigated) it is Clément's question card, its context in a first view that unfolds in place.
// One note field for the gestures at the bottom: "Withdraw", on the left, closes the question without an answer;
// "Enlighten me", in the middle, asks the lead to find out more first (QuestionService.Enlighten), the note saying
// what to look into; "Rub the lamp", on the right in the lamp's yellow, answers with the option selected, the
// recommended one until you pick another, and the note with it. An option only picks: nothing but the lamp's button,
// Enter in the note or Ctrl+Enter on the card answers (W574). Open, it says how much it holds up: red when a task
// waits for it, orange under its before words ("before the merge"), grey when it can wait. Being investigated, it
// folds to one line out of the way, and opens on a click; revised, it waits for you again, in its place. Answered, it
// is a decision: no mark asks to read or approve it again, and the decision log shows it. Djinn types each answer in
// the lead's terminal; a wish without a lead session has no lead to tell, and the card says so. A grey line says
// when it was asked, and revised. Small buttons sit in its top right corner: Versions (3), once revised, opens its
// earlier versions below it, to read, the current one staying what the card shows first (W114); Later sends it to the
// bottom of its list, where it stays (Djinn keeps when, Question.laterTime), and, pressed, gives it its place back;
// Sleep puts it out of the list until conditions hold (Question.sleep), and Djinn wakes it to the top; Move takes it
// to another wish.
// An option that launches a task once picked (Question.plans, decision Q25) says so on one grey line under its text:
// Launches: its title, and the azima it belongs to. Always one line, cut short, it never moves the card.
// A gesture shows, by key or by click: its button stays pressed a moment and the answer leaves as it comes back up,
// while the card glows in the colour of the gesture, the lamp's brass or a cool light, and a wisp of smoke rises
// from the button (review.css). Reduced motion keeps the colour, without the movement.
import {
  ArrowRightLeft,
  Check,
  ChevronDown,
  CircleOff,
  ClockArrowDown,
  ClockArrowUp,
  CornerDownRight,
  FileText,
  History,
  Lamp,
  Lightbulb,
  Moon,
} from "lucide-react";
import { timestampMs } from "@bufbuild/protobuf/wkt";
import { motion } from "motion/react";
import {
  type RefObject,
  type ReactNode,
  memo,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  type Block,
  Choice,
  type PlannedTask,
  type Question,
} from "../gen/ts/plan/v1/plan_pb";
import { useOptionalData } from "./data/djinn";
import { EMPTY_BLOCKING } from "./data/flight";
import {
  answerText,
  askedWhen,
  choiceOf,
  firstLine,
  investigating,
  isWithdrawn,
  letter,
  planOf,
  questionVersions,
  type QuestionVersion,
  recommendedChoice,
  span,
  useNow,
  when,
} from "./data/format";
import { mainLink } from "./data/links";
import { placeTaken } from "./scroll-anchor";
import { questionPerch } from "./data/eye";
import { EyePerch } from "./djinn-eye";
import { t } from "./i18n";
import { CommandText } from "./copy-command";
import { MoveLink } from "./link";
import { FoldedMarkdown, MarkdownBody } from "./markdown-body";
import { StatusBadge } from "./status";
import { isMac, TooltipButton } from "./tooltip";

// A key on an open question: React's on the card, or the window's when the focus is nowhere (question-column.tsx).
export type QuestionKey = Pick<
  KeyboardEvent,
  "key" | "ctrlKey" | "metaKey" | "shiftKey" | "altKey" | "preventDefault"
> & {
  keyCode?: number;
  isComposing?: boolean;
  nativeEvent?: { isComposing?: boolean };
  target: EventTarget | null;
  currentTarget?: EventTarget | null;
};

// Where the open question of the column takes the keys pressed with the focus nowhere.
export type QuestionKeys = RefObject<((e: QuestionKey) => void) | undefined>;

export const WishQuestion = memo(function WishQuestion({
  question: q,
  expanded: open = false,
  current = false,
  tabbable = false,
  onSelect,
  keys,
  origin,
  blocking = EMPTY_BLOCKING,
  report,
  noLead = false,
  onAnswer,
  onEnlighten,
  onMove,
  onWithdraw,
  onLater,
  onSleep,
}: {
  question: Question;
  // Where the question comes from, in the flying carpet of several wishes: its wish.
  origin?: ReactNode;
  // The codes of the tasks that wait for this answer.
  blocking?: readonly string[];
  // The report of its wish it names (src/data/reports.ts): the card links it.
  report?: Block;
  // A decision opened at first. An open question is always open, but in the questions column.
  expanded?: boolean;
  // In the questions column: whether it is the one open, whether Tab reaches it closed (none is open), what a click
  // or the focus on it closed does (opens it), and where it takes the keys open.
  current?: boolean;
  tabbable?: boolean;
  onSelect?: (id: string) => void;
  keys?: QuestionKeys;
  // The wish has no lead session: no lead is told the answer, it waits in the wish's brief.
  noLead?: boolean;
  // Answers the question; resolves once djinn has it. A rejection keeps the card open.
  onAnswer: (
    choice: Choice,
    note: string,
    question?: Question,
  ) => Promise<void>;
  // Asks the lead to investigate before deciding, the note saying what to look into.
  onEnlighten?: (note: string, question?: Question) => Promise<void>;
  // Moves an open question to another wish.
  onMove?: (question: Question) => void;
  // Withdraws an open question without an answer.
  onWithdraw?: (question: Question) => void;
  // Puts an open question off for later, to the bottom of its list, or gives it its place back when it is.
  onLater?: (question: Question) => void;
  // Puts an open question to sleep until conditions hold: out of the list until then.
  onSleep?: (question: Question) => void;
}) {
  const answered = !!q.answer;
  const withdrawn = isWithdrawn(q);
  // A message built by hand (a test) may leave the lists out.
  const rounds = q.rounds ?? [];
  const digging = investigating(q);
  // What you asked to look into, while it is being investigated.
  const asked = digging ? rounds.at(-1)?.note : undefined;
  // How long it has waited for its revision, to the minute: a request no one took on shows as old (W557).
  const since = digging ? rounds.at(-1)?.createTime : undefined;
  const now = useNow(!!since, 30_000);
  const waited = since
    ? Math.floor((now - timestampMs(since)) / 60_000) * 60_000
    : 0;
  const recommended = answered || withdrawn ? undefined : recommendedChoice(q);
  const [expanded, setExpanded] = useState(open);
  // The earlier versions, which a small button in the card's corner opens below it, to read (W114).
  const versions = useMemo(() => questionVersions(q), [q]);
  const [showVersions, setShowVersions] = useState(false);
  // Being investigated, it is one line until you open it.
  const [peek, setPeek] = useState(false);
  const folded = digging && !peek;
  // The recommended option is selected until you pick one: a revision that recommends another moves it. With no
  // option named, nothing is selected: rubbing the lamp waits for your pick.
  const [picked, setPicked] = useState<Choice>();
  const choice = picked ?? (q.options.length ? recommended : Choice.YES);
  const [note, setNote] = useState("");
  const [sending, setSending] = useState(false);
  const [unpicked, setUnpicked] = useState(false);
  // The gesture the card plays, and whether its button is still down. It lasts while its answer leaves, the chosen
  // button bright beside the other one disabled, and its glow ends after.
  const [gesture, setGesture] = useState<Gesture>();
  const [pressed, setPressed] = useState(false);
  useEffect(() => {
    if (!gesture || sending) return;
    const timer = setTimeout(() => setGesture(undefined), GESTURE_MS);
    return () => clearTimeout(timer);
  }, [gesture, sending]);
  const play = async (kind: Gesture, run: () => Promise<void>) => {
    setGesture(kind);
    setPressed(true);
    setSending(true);
    try {
      await press();
      setPressed(false);
      await run();
    } catch {
      // The toast says why; the card stays as it was.
      setGesture(undefined);
    } finally {
      setPressed(false);
      setSending(false);
    }
  };
  const mod = isMac() ? "⌘" : "⌃";
  // A row of the questions column, closed or open.
  const row = !!onSelect && !answered && !withdrawn;
  const card = useRef<HTMLElement>(null);
  const noteField = useRef<HTMLTextAreaElement>(null);
  const focusNote = () => {
    const field = noteField.current;
    if (!field) return;
    field.focus({ preventScroll: true });
    field.setSelectionRange(field.value.length, field.value.length);
  };

  const isComposing = (e: QuestionKey): boolean => {
    return (
      Boolean(e.nativeEvent?.isComposing) ||
      Boolean((e as unknown as { isComposing?: boolean }).isComposing) ||
      e.keyCode === 229
    );
  };

  const triggerRub = () => {
    if (sending) return;
    if (choice === undefined) {
      setUnpicked(true);
      return;
    }
    void play("rub", () => onAnswer(choice, note.trim(), q));
  };

  const triggerEnlighten = () => {
    if (sending || !onEnlighten || digging) return;
    void play("enlighten", async () => {
      await onEnlighten(note.trim(), q);
      setNote("");
    });
  };

  const triggerWithdraw = () => {
    if (sending || !onWithdraw) return;
    onWithdraw(q);
  };

  const handleTextareaKeyDown = (
    e: React.KeyboardEvent<HTMLTextAreaElement>,
  ) => {
    if (isComposing(e)) return;
    // In the column, Escape gives the focus back to the question.
    if (row && e.key === "Escape" && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      card.current?.focus({ preventScroll: true });
      return;
    }
    if (
      e.key === "Backspace" &&
      (e.ctrlKey || e.metaKey) &&
      e.shiftKey &&
      !e.altKey
    ) {
      if (onWithdraw && !sending) {
        e.preventDefault();
        triggerWithdraw();
      }
      return;
    }
    if (e.key === "Enter") {
      if ((e.ctrlKey || e.metaKey) && e.shiftKey) {
        e.preventDefault();
        triggerEnlighten();
      } else if (e.ctrlKey || e.metaKey) {
        e.preventDefault();
        triggerRub();
      } else if (e.shiftKey) {
        return;
      } else {
        e.preventDefault();
        triggerRub();
      }
    }
  };

  const handleCardKeyDown = (e: QuestionKey) => {
    const inTextarea = (e.target as HTMLElement)?.tagName === "TEXTAREA";
    if (inTextarea) return;
    if (isComposing(e)) return;

    if (
      e.key === "Backspace" &&
      (e.ctrlKey || e.metaKey) &&
      e.shiftKey &&
      !e.altKey
    ) {
      if (onWithdraw && !sending) {
        e.preventDefault();
        triggerWithdraw();
      }
      return;
    }

    if (e.key === "Enter") {
      // Enter alone answers nothing on the card: a click on an option or on the card's text leaves the focus there,
      // and a stray Enter sent the option just looked at (W574). A focused control (an option, a gesture's button,
      // a copy button, Read more) presses itself. Ctrl+Enter rubs the lamp, Ctrl+Shift+Enter enlightens.
      if (!e.ctrlKey && !e.metaKey) {
        // In the column, Enter on the question itself (or with the focus nowhere) puts the caret in the note.
        const onQuestion =
          e.target === e.currentTarget || !holds(card.current, e.target);
        if (row && onQuestion && !e.shiftKey && !e.altKey) {
          e.preventDefault();
          focusNote();
        }
        return;
      }
      e.preventDefault();
      if (e.shiftKey) triggerEnlighten();
      else triggerRub();
      return;
    }

    if (e.ctrlKey || e.metaKey || e.altKey) return;

    // In the column, the arrows go to the other questions: a letter or a digit picks an option, Escape clears your
    // pick, and once none is left the column takes it (the focus leaves it).
    if (row) {
      if (e.key === "Escape") {
        if (picked === undefined) return;
        e.preventDefault();
        setPicked(undefined);
        setUnpicked(false);
        return;
      }
      if (e.shiftKey || [...e.key].length !== 1) return;
      const index = /^[1-9]$/.test(e.key)
        ? Number(e.key) - 1
        : q.options.findIndex((_, i) => letter(i) === e.key.toUpperCase());
      if (index >= 0 && index < q.options.length) {
        e.preventDefault();
        setPicked(choiceOf(index));
        setUnpicked(false);
      }
      return;
    }

    if (q.options.length > 0) {
      if (e.key === "ArrowDown" || e.key === "ArrowRight") {
        e.preventDefault();
        const current = choice !== undefined ? choice - Choice.A : -1;
        const next = current < 0 ? 0 : (current + 1) % q.options.length;
        setPicked(choiceOf(next));
        setUnpicked(false);
        return;
      }
      if (e.key === "ArrowUp" || e.key === "ArrowLeft") {
        e.preventDefault();
        const current = choice !== undefined ? choice - Choice.A : 0;
        const prev = (current - 1 + q.options.length) % q.options.length;
        setPicked(choiceOf(prev));
        setUnpicked(false);
        return;
      }
      if (/^[1-9]$/.test(e.key)) {
        const num = parseInt(e.key, 10);
        if (num >= 1 && num <= q.options.length) {
          e.preventDefault();
          setPicked(choiceOf(num - 1));
          setUnpicked(false);
          return;
        }
      }
    }
  };

  // The open question of the column takes the keys pressed with the focus nowhere.
  useEffect(() => {
    if (!keys || !current) return;
    keys.current = handleCardKeyDown;
    return () => {
      if (keys.current === handleCardKeyDown) keys.current = undefined;
    };
  });

  const status = q.waitingLead ? (
    <StatusBadge tone="waiting" label={t("question.waiting_lead")} />
  ) : answered ? (
    <StatusBadge tone="done" label={t("panels.decision_recorded")} />
  ) : withdrawn ? (
    <StatusBadge tone="withdrawn" label={t("question.withdrawn")} />
  ) : digging ? (
    <StatusBadge
      tone="investigating"
      label={
        waited > 0
          ? t("question.investigating_for", { time: span(waited) })
          : t("question.investigating")
      }
    />
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
  // When it was asked, and revised, in grey: a question keeps its place by it (question-column.tsx).
  const askedAt = askedWhen(q);
  const askedLine = askedAt && (
    <time className="question-asked">{askedAt}</time>
  );
  const level =
    answered || withdrawn || digging
      ? ""
      : blocking.length
        ? "is-blocking"
        : q.before
          ? ""
          : "can-wait";
  const heading = (
    <>
      <span className="question-id">
        {answered ? (
          <Check size={14} aria-label={q.code} />
        ) : withdrawn ? (
          <CircleOff size={14} aria-label={q.code} />
        ) : (
          q.code
        )}
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
          {!answered && !withdrawn && askedLine}
        </span>
        {/* Inside the heading's button when it folds, its links and commands are text. */}
        <h3>
          {answered || withdrawn || digging ? (
            q.text
          ) : (
            <CommandText text={q.text} />
          )}
        </h3>
        {answered && <p className="answer-value">{answerText(q)}</p>}
      </div>
    </>
  );
  const recommendation = q.recommendation && (
    <section className="recommendation">
      <span className="eyebrow">
        {t("panels.recommendation")}
        {recommended !== undefined &&
          recommended !== Choice.YES &&
          ` · ${letter(recommended - Choice.A)}`}
      </span>
      <MarkdownBody text={q.recommendation} copy />
    </section>
  );
  // The options, the note and the gestures, for an open question.
  const options = q.options.length > 0 && (
    <div
      className="question-options"
      role="group"
      aria-label={t("page.options")}
    >
      {q.options.map((option, index) => {
        const selected = choice === choiceOf(index);
        const pick = () => {
          setPicked(choiceOf(index));
          setUnpicked(false);
        };
        // Not a <button>: its text is selectable, and a command in it has a copy button of its own. A
        // click, Enter or Space picks it, never answers.
        return (
          <div
            key={index}
            className={`option ${selected ? "selected" : ""}`}
            role="button"
            tabIndex={0}
            aria-pressed={selected}
            onClick={() => {
              // Selecting words in it picks nothing.
              if (globalThis.getSelection?.()?.toString()) return;
              pick();
            }}
            onKeyDown={(e) => {
              if (
                (e.key !== " " && e.key !== "Enter") ||
                e.ctrlKey ||
                e.metaKey ||
                e.shiftKey ||
                e.altKey ||
                e.target !== e.currentTarget
              )
                return;
              e.preventDefault();
              pick();
            }}
          >
            <strong className="option-letter">{letter(index)}</strong>
            <p>
              <CommandText text={option} />
            </p>
            {recommended === choiceOf(index) && (
              <span className="option-recommended">
                {t("question.recommended")}
              </span>
            )}
            <PlanLine question={q} plan={planOf(q, choiceOf(index))} />
          </div>
        );
      })}
    </div>
  );
  const gestures = (
    <>
      {q.options.length === 0 && (
        <PlanLine question={q} plan={planOf(q, Choice.YES)} />
      )}
      <textarea
        ref={noteField}
        value={note}
        onChange={(e) => setNote(e.target.value)}
        onKeyDown={handleTextareaKeyDown}
        placeholder={
          q.options.length
            ? t("question.note_placeholder")
            : t("question.answer_placeholder")
        }
        rows={2}
        maxLength={2000}
        aria-label={t("question.note_label")}
      />
      {(unpicked || noLead) && (
        <p
          className={`question-hint${unpicked ? " unpicked" : ""}`}
          role={unpicked ? "alert" : undefined}
        >
          <CornerDownRight size={14} aria-hidden="true" />
          {t(unpicked ? "question.rub_pick" : "question.no_lead")}
        </p>
      )}
      <div className="question-actions">
        {onWithdraw && (
          <>
            <button
              type="button"
              className="button secondary lamp-withdraw"
              title={`${t("question.withdraw_detail")} (${mod}⇧⌫)`}
              disabled={sending}
              onClick={triggerWithdraw}
            >
              <CircleOff size={14} aria-hidden="true" />
              {t("question.withdraw")}
              <kbd className="key-hint" aria-hidden="true">
                {mod}⇧⌫
              </kbd>
            </button>
            <span className="spacer" />
          </>
        )}
        {onEnlighten && !digging && (
          <button
            className={`button secondary lamp-enlighten${gesture === "enlighten" && pressed ? " is-pressed" : ""}`}
            title={`${t("question.enlighten_detail")} (${mod}⇧↵)`}
            disabled={sending}
            onClick={triggerEnlighten}
          >
            <Lightbulb size={14} />
            {t("question.enlighten")}
            <kbd className="key-hint" aria-hidden="true">
              {mod}⇧↵
            </kbd>
            {gesture === "enlighten" && <Wisp />}
          </button>
        )}
        <span className="spacer" />
        <button
          className={`button accent lamp-rub${gesture === "rub" && pressed ? " is-pressed" : ""}`}
          title={
            choice === undefined
              ? t("question.rub_pick")
              : `${t("question.rub_detail")} (↵)`
          }
          disabled={sending || choice === undefined}
          onClick={triggerRub}
        >
          <Lamp size={14} />
          {t("question.rub")}
          <kbd className="key-hint" aria-hidden="true">
            ↵
          </kbd>
          {gesture === "rub" && <Wisp />}
        </button>
      </div>
    </>
  );
  // Moving to another wish is rare: an icon in the card's top right corner, on its title's line, named by its tooltip.
  const move = onMove && (
    <TooltipButton
      label={t("question.move")}
      className="question-corner-button question-move"
      onClick={() => onMove(q)}
    >
      <ArrowRightLeft size={14} aria-hidden="true" />
    </TooltipButton>
  );
  // Later, beside it: pressed while the question is put off, its tooltip says what a click does.
  const putOff = !!q.laterTime;
  const later = onLater && (
    <TooltipButton
      label={t("question.later")}
      tooltip={t(putOff ? "question.later_undo" : "question.later_detail")}
      className={`question-corner-button question-later${putOff ? " is-pressed" : ""}`}
      aria-pressed={putOff}
      onClick={() => onLater(q)}
    >
      {putOff ? (
        <ClockArrowUp size={14} aria-hidden="true" />
      ) : (
        <ClockArrowDown size={14} aria-hidden="true" />
      )}
    </TooltipButton>
  );
  // Sleep, beside it: until tasks are over, requests merged or a time comes (SleepQuestionDialog).
  const sleep = onSleep && (
    <TooltipButton
      label={t("question.sleep")}
      tooltip={t("question.sleep_detail")}
      className="question-corner-button question-sleep"
      onClick={() => onSleep(q)}
    >
      <Moon size={14} aria-hidden="true" />
    </TooltipButton>
  );
  // Versions (3): the current version and the two before it, which the button opens below the question.
  const versionsButton = versions.length > 0 && (
    <button
      type="button"
      className={`question-corner-button question-versions${showVersions ? " is-pressed" : ""}`}
      title={t("question.versions_detail")}
      aria-expanded={showVersions}
      onClick={() => setShowVersions(!showVersions)}
    >
      <History size={13} aria-hidden="true" />
      {t("question.versions", { count: versions.length + 1 })}
    </button>
  );
  const earlier = showVersions && versions.length > 0 && (
    <Versions versions={versions} />
  );
  const corner = (later || sleep || move || versionsButton) && (
    <div className="question-corner">
      {versionsButton}
      {later}
      {sleep}
      {move}
    </div>
  );
  const body = answered || withdrawn ? expanded : !folded;
  // An open move shows where to act first, as a button.
  const link = q.move && !answered && !withdrawn ? mainLink(q) : undefined;
  if (row) {
    // On the page's ground: closed, its row, which a click or the focus opens; open, today's card in full.
    return (
      <motion.article
        ref={card}
        id={`question-${q.id}`}
        className={`question-card question-row ${current ? "open current" : "closed"} ${level}${gesture === "rub" ? " gesture-rub" : gesture === "enlighten" ? " gesture-enlighten" : ""}`}
        tabIndex={current || tabbable ? 0 : -1}
        aria-current={current || undefined}
        onKeyDown={current ? handleCardKeyDown : undefined}
        onClick={current ? undefined : () => onSelect?.(q.id)}
        onFocus={
          current
            ? undefined
            : (e) => {
                if (e.target === e.currentTarget) onSelect?.(q.id);
              }
        }
        initial={{ opacity: 0, y: 10 }}
        animate={{ opacity: 1, y: 0 }}
        exit={{ opacity: 0, x: 30 }}
        transition={{ duration: 0.25 }}
      >
        <EyePerch name={questionPerch(q.id)} />
        <span className="question-id">{q.code}</span>
        <div className="question-row-head">
          <h3 className="question-row-title">
            {current ? <CommandText text={q.text} /> : q.text}
          </h3>
          <p className="question-row-meta">
            {status}
            {q.options.length > 0 && (
              <span>
                {t("question.options_count", { count: q.options.length })}
              </span>
            )}
            {!current && picked !== undefined && (
              <span className="question-row-picked">
                {t("question.picked", { letter: letter(picked - Choice.A) })}
              </span>
            )}
            {q.revision > 0 && (
              <span className="revised-badge">
                {t("question.revised", { count: q.revision })}
              </span>
            )}
            {askedLine}
          </p>
        </div>
        {current && corner}
        {current && (
          <div className="question-open">
            {link && (
              <div className="question-open-top">
                <MoveLink href={link} />
              </div>
            )}
            {q.context && <QuestionContext context={q.context} />}
            {report && <QuestionReport report={report} />}
            {recommendation}
            {options}
            {gestures}
            {earlier}
          </div>
        )}
      </motion.article>
    );
  }
  return (
    <motion.article
      id={`question-${q.id}`}
      className={`question-card ${answered ? "answered" : withdrawn ? "withdrawn" : digging ? "investigating" : "open"} ${level}${folded ? " folded" : ""}${gesture === "rub" ? " gesture-rub" : gesture === "enlighten" ? " gesture-enlighten" : ""}`}
      tabIndex={answered || withdrawn || folded ? undefined : 0}
      onKeyDown={
        answered || withdrawn || folded ? undefined : handleCardKeyDown
      }
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
        ) : answered || withdrawn || digging ? (
          <button
            className="question-heading"
            onClick={() =>
              answered || withdrawn ? setExpanded(!expanded) : setPeek(false)
            }
            aria-expanded={answered || withdrawn ? expanded : true}
          >
            {heading}
            <ChevronDown
              size={16}
              className={(answered || withdrawn) && !expanded ? "" : "rotated"}
            />
          </button>
        ) : (
          <div className="question-heading">{heading}</div>
        )}
        {!folded && (answered || withdrawn ? body && versionsButton : corner)}
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
          {link && (
            <p className="question-move-link">
              <MoveLink href={link} />
            </p>
          )}
          {q.context && <QuestionContext context={q.context} folded />}
          {report && <QuestionReport report={report} />}
          {recommendation}
          {answered ? (
            <div className="answered-info">
              <span>
                {t("question.decided", { when: when(q.answer?.createTime) })}
              </span>
              {q.answer?.note && <p>{q.answer.note}</p>}
              {q.waitingLead ? (
                <p className="no-lead">{t("question.waiting_lead_detail")}</p>
              ) : noLead ? (
                <p className="no-lead">{t("question.no_lead_answered")}</p>
              ) : null}
            </div>
          ) : withdrawn ? (
            <div className="answered-info">
              <span>
                {t("question.withdrawn_at", {
                  when: when(q.withdrawal?.createTime),
                })}
              </span>
              {q.withdrawal?.note && <p>{q.withdrawal.note}</p>}
            </div>
          ) : (
            <>
              {options}
              {gestures}
            </>
          )}
          {earlier}
        </div>
      )}
    </motion.article>
  );
});

// PlanLine is the task an option launches once picked, on one grey line: its title, cut short when long, then the
// azima it belongs to, whole, by its code and title once the wish's tasks are read. Nothing for an option without a
// plan.
function PlanLine({
  question,
  plan,
}: {
  question: Question;
  plan?: PlannedTask;
}) {
  const id = plan?.partOf ?? "";
  const azima = useOptionalData(
    (s) =>
      id
        ? s.details[question.wishId]?.tasks.find((task) => task.id === id)
        : undefined,
    undefined,
  );
  if (!plan) return null;
  const title = plan.title || firstLine(plan.prompt);
  const part = azima ? `${azima.code} ${azima.title}`.trim() : "";
  const text = t("question.plan_launches", { title });
  return (
    <span className="option-plan" title={part ? `${text} · ${part}` : text}>
      <CornerDownRight size={12} aria-hidden="true" />
      <span className="option-plan-text">{text}</span>
      {part && <span className="option-plan-azima">· {part}</span>}
    </span>
  );
}

// holds says whether an element holds a key's target; with no document (the screens tests), none.
function holds(element: HTMLElement | null, target: EventTarget | null) {
  return (
    typeof Node !== "undefined" &&
    target instanceof Node &&
    !!element?.contains(target)
  );
}

// A gesture on a question: rub the lamp, or enlighten me.
type Gesture = "rub" | "enlighten";

// How long a gesture's button stays down before its answer leaves, and how long the card plays it: under 600 ms.
const PRESS_MS = 150;
const GESTURE_MS = 560;

// press waits while the button is down. Rendered with no document (the screens tests), there is nothing to see: the
// answer leaves at once.
function press(): Promise<void> {
  if (typeof document === "undefined") return Promise.resolve();
  return new Promise((resolve) => setTimeout(resolve, PRESS_MS));
}

// Wisp is the puff of smoke that rises from a gesture's button.
function Wisp() {
  return <span className="question-wisp" aria-hidden="true" />;
}

// QuestionReport is the report a question names, as a line that unfolds it in place: what was found, beside the
// question it leads to.
function QuestionReport({ report }: { report: Block }) {
  const [open, setOpen] = useState(false);
  const title = report.title || t("agents.untitled");
  return (
    <section className="question-report">
      <button
        type="button"
        className="question-report-link"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <FileText size={14} aria-hidden="true" />
        <span>{t("question.report", { title })}</span>
        <ChevronDown
          size={14}
          aria-hidden="true"
          className={open ? "rotated" : ""}
        />
      </button>
      {open && (
        <div className="question-report-body prose">
          {report.updateTime && (
            <span className="eyebrow">{when(report.updateTime)}</span>
          )}
          <MarkdownBody text={report.content} copy />
        </div>
      )}
    </section>
  );
}

// QuestionContext is what happened and what is at stake, whole or in its first view (folded): the current version
// only, the earlier ones are behind the card's Versions button.
function QuestionContext({
  context,
  folded = false,
}: {
  context: string;
  folded?: boolean;
}) {
  return (
    <section className="question-context">
      <h4>{t("page.context")}</h4>
      {folded ? (
        <FoldedMarkdown
          text={context}
          more={t("question.read_more")}
          less={t("question.read_less")}
          copy
        />
      ) : (
        <div className="prose">
          <MarkdownBody text={context} copy />
        </div>
      )}
    </section>
  );
}

// Versions are a question's earlier versions, to read, the last one first, as a tilasm's: when each was asked or
// revised into, what you asked to investigate about it, and what it said, its context folded to its first view.
function Versions({ versions }: { versions: QuestionVersion[] }) {
  const list = useRef<HTMLElement>(null);
  // Opened from the corner, it may start below the view: it comes into view, and the place is the reader's.
  useEffect(() => {
    const element = list.current;
    if (!element) return;
    element.scrollIntoView({ block: "nearest" });
    for (let node = element.parentElement; node; node = node.parentElement) {
      if (/auto|scroll/.test(getComputedStyle(node).overflowY)) {
        placeTaken(node);
        break;
      }
    }
  }, []);
  return (
    <section ref={list} className="question-versions-list">
      <h4>{t("question.versions_earlier")}</h4>
      <ol reversed>
        {[...versions].reverse().map((v) => (
          <li key={v.number}>
            <p className="question-version-head">
              <span className="question-version-number">
                {t("question.version", { number: v.number })}
              </span>
              <time>
                {t(
                  v.number === 1
                    ? "question.version_asked"
                    : "question.version_revised",
                  {
                    when: when(v.time),
                  },
                )}
              </time>
            </p>
            {v.context && (
              <FoldedMarkdown
                text={v.context}
                more={t("question.read_more")}
                less={t("question.read_less")}
              />
            )}
            {v.options.length > 0 && (
              <ol className="question-version-options" type="A">
                {v.options.map((option, i) => (
                  <li key={i}>{option}</li>
                ))}
              </ol>
            )}
            {v.recommendation && (
              <p className="question-version-recommendation">
                {t("question.version_recommendation", {
                  recommendation: v.recommendation,
                })}
              </p>
            )}
            {v.asked.map((note, i) => (
              <p key={i} className="question-version-asked">
                <Lightbulb size={13} aria-hidden="true" />
                {t("question.version_enlighten", { note })}
              </p>
            ))}
          </li>
        ))}
      </ol>
    </section>
  );
}
