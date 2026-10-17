// The questions column of the flying carpet and of a wish's first page (Decision Q08, mockup L04 v2): the open
// questions on the page's ground, no card around them, grouped by wish on the flying carpet (each group headed by its
// wish's title, which opens it), the wish's own on its page. A question is closed, its two-line row, or open, in full
// (wish-question.tsx): one is open at a time, the first until you go to another, and when the open one leaves (answered,
// withdrawn, moved, put to sleep) the one below it opens, else the one above. The questions asleep (Question.sleep)
// are out of it: a grey count below the groups lists them on demand (AsleepQuestions); one that wakes comes first.
// Nothing moves under the reader (AGENTS.md, Interface): each wish's questions come as they were asked, the oldest
// first, so a new one comes below and a revised one keeps its place; the groups keep the wishes' rank. The question
// open stays open when one comes above it, and stays where it is on screen: the page's scroll anchoring holds it
// (scroll-anchor.ts), and at the very top of the page, where that holds nothing, the column does.
// The keys: ↑ and ↓ (Home, End) go to the question above or below, from one wish to the next, and nothing moves past
// either end; the question they reach opens, and the page scrolls its title to the top of the view, wherever it was:
// a scroll you ask for by moving. The question that has the focus, the one Enter acts on, shows a gold ring. With no
// question focused, anywhere on the page but in a field, the first ↑ or ↓ gives the open question the focus (none
// open, the first for ↓, the last for ↑), and the next one moves. Escape steps back: from the note to the question,
// then your pick is cleared (wish-question.tsx), then the focus leaves the column, where ↓ brings it back. With the
// focus nowhere, the open question takes the other keys as if it had it, and the focus with them. Space and the page
// keys scroll a long question: the arrows stay with the questions.
import { Moon } from "lucide-react";
import {
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import type { Choice, Question, Wish } from "../gen/ts/plan/v1/plan_pb";
import { byAsked, type OpenQuestion } from "./data/flight";
import { sleepTexts } from "./data/format";
import { t } from "./i18n";
import {
  type QuestionKey,
  type QuestionKeys,
  WishQuestion,
} from "./wish-question";
import { placeTaken } from "./scroll-anchor";

export interface QuestionGroup {
  wish: Wish;
  questions: OpenQuestion[];
}

// questionGroups groups the open questions by wish, in the order of the wishes (by rank), each wish's as they were
// asked, the oldest first: one group per wish that has one.
export function questionGroups(
  wishes: readonly Wish[],
  questions: readonly OpenQuestion[],
): QuestionGroup[] {
  const byWish = new Map<string, OpenQuestion[]>();
  for (const question of questions) {
    const list = byWish.get(question.wish.id);
    if (list) list.push(question);
    else byWish.set(question.wish.id, [question]);
  }
  return wishes.flatMap((wish) => {
    const list = byWish.get(wish.id);
    return list ? [{ wish, questions: list.sort(byAsked) }] : [];
  });
}

// stepTo is where ↑ (step -1) or ↓ (step 1) goes from the open question: the one above or below, itself at either end;
// from none open, the first or the last.
export function stepTo(
  order: readonly string[],
  current: string | undefined,
  step: number,
): string | undefined {
  const at = current === undefined ? -1 : order.indexOf(current);
  if (at < 0) return step > 0 ? order[0] : order.at(-1);
  return order[Math.min(Math.max(at + step, 0), order.length - 1)];
}

// arrowTo is where ↑ (step -1) or ↓ (step 1) goes, and whether it moves: with a question focused, to the one above or
// below (stepTo); with none, the open one takes the focus first and nothing moves, none open the first or the last
// opens.
export function arrowTo(
  order: readonly string[],
  current: string | undefined,
  step: number,
  focused: boolean,
): { id: string; move: boolean } | undefined {
  const id =
    focused || current === undefined ? stepTo(order, current, step) : current;
  return id === undefined ? undefined : { id, move: id !== current };
}

// heir is the question that opens when the open one, gone, has left the list: the first below it in the list before
// that is still there, else the nearest above.
export function heir(
  before: readonly string[],
  after: readonly string[],
  gone: string,
): string | undefined {
  const at = before.indexOf(gone);
  if (at < 0) return after[0];
  const still = new Set(after);
  return (
    before.slice(at + 1).find((id) => still.has(id)) ??
    before
      .slice(0, at)
      .reverse()
      .find((id) => still.has(id))
  );
}

// What a key does to the column itself: go to another question, leave the column, or nothing it knows (the open
// question may take it).
export type ColumnMove = "up" | "down" | "first" | "last" | "leave";

export function columnMove(e: {
  key: string;
  ctrlKey: boolean;
  metaKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
}): ColumnMove | undefined {
  if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return undefined;
  return (
    {
      ArrowUp: "up",
      ArrowDown: "down",
      Home: "first",
      End: "last",
      Escape: "leave",
    } as const
  )[e.key as "ArrowUp"];
}

export function QuestionColumn({
  groups,
  headings = false,
  onOpenWish,
  noLead = false,
  onAnswer,
  onEnlighten,
  onMove,
  onWithdraw,
  onLater,
  onSleep,
  asleep = [],
  onWake,
  children,
}: {
  groups: QuestionGroup[];
  // On the flying carpet: each group is headed by its wish's title, which opens the wish.
  headings?: boolean;
  onOpenWish?: (wishId: string) => void;
  noLead?: boolean;
  onAnswer: (
    choice: Choice,
    note: string,
    question?: Question,
  ) => Promise<void>;
  onEnlighten?: (note: string, question?: Question) => Promise<void>;
  onMove?: (question: Question) => void;
  onWithdraw?: (question: Question) => void;
  // Puts a question off for later, or gives it its place back; resolves once djinn has it.
  onLater?: (question: Question) => Promise<unknown>;
  // Puts a question to sleep until conditions hold (SleepQuestionDialog).
  onSleep?: (question: Question) => void;
  // The questions asleep, counted in a line below the others, listed on demand (AsleepQuestions), each woken by hand.
  asleep?: readonly OpenQuestion[];
  onWake?: (question: Question) => Promise<unknown>;
  // What else waits for you, below the questions: the workers that wait, the proofs, the grant.
  children?: ReactNode;
}) {
  const order = useMemo(
    () => groups.flatMap((g) => g.questions.map(({ item }) => item.id)),
    [groups],
  );
  // The question you went to: undefined until you go to one (the first is open), null once you left the column.
  const [chosen, setChosen] = useState<string | null>();
  // The first question, open until you go to another: it stays the one open when another comes above it (a wish
  // ranked higher asks one).
  const first = useRef<string>(undefined);
  // The list as it was at the last render: where the open question was, once it is gone.
  const seen = useRef<readonly string[]>(order);
  const opened = chosen === undefined ? first.current : chosen;
  const current =
    opened === null
      ? undefined
      : opened === undefined
        ? order[0]
        : order.includes(opened)
          ? opened
          : heir(seen.current, order, opened);
  const column = useRef<HTMLElement>(null);
  const keys: QuestionKeys = useRef<(e: QuestionKey) => void>(undefined);
  // Whether the question that opens takes the focus: it was gone to (its title to the top of the view), or the open
  // one left with the focus in it.
  const follow = useRef(false);
  const shown = useRef(current);

  const select = useCallback((id: string) => {
    follow.current = true;
    setChosen(id);
  }, []);

  useLayoutEffect(() => {
    const was = shown.current;
    shown.current = current;
    seen.current = order;
    if (chosen === undefined) first.current = current;
    if (typeof chosen === "string" && chosen !== current)
      setChosen(current ?? undefined);
    const left = was !== undefined && was !== current && !order.includes(was);
    const moved = follow.current;
    if (!moved && !(left && focusHere(column.current))) return;
    follow.current = false;
    if (current === undefined) return;
    const element = document.getElementById(`question-${current}`);
    if (!element) return;
    if (document.activeElement !== element)
      element.focus({ preventScroll: true });
    if (moved) toTitle(element);
    else reveal(element);
  }, [current, order, chosen]);

  // The keys, read on the window: the arrows reach the column from anywhere on the page, the others with the focus
  // nowhere, after a question left it.
  const state = useRef({ order, current });
  state.current = { order, current };

  // Put off, the open question goes to the bottom of its wish's list: the one that came after it opens, as when it
  // leaves (the last stays open). Given its place back, it stays open and the page follows it there.
  const later = useMemo(
    () =>
      onLater &&
      ((question: Question) => {
        const { order } = state.current;
        const next = order[order.indexOf(question.id) + 1];
        const back = !!question.laterTime;
        void onLater(question).then(
          () => {
            if (!back) {
              if (next) select(next);
              return;
            }
            requestAnimationFrame(() => {
              const element = document.getElementById(
                `question-${question.id}`,
              );
              if (element) toTitle(element);
            });
          },
          () => {},
        );
      }),
    [onLater, select],
  );

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing || e.keyCode === 229) return;
      const root = column.current;
      // A dialog, or a tilasm opened over the page (which leaves the column inert), keeps the keys.
      if (
        !root ||
        root.closest("[inert]") ||
        document.querySelector(".modal-backdrop")
      )
        return;
      const target = e.target instanceof Node ? e.target : null;
      const inside = !!target && root.contains(target);
      const nowhere =
        !target ||
        target === document.body ||
        target === document.documentElement;
      if (typing(target)) return;
      const { order, current } = state.current;
      const move = columnMove(e);
      // No question has the focus: from anywhere on the page, the arrows give it to the open one first.
      const focused = inQuestion(root, target);
      if (
        (move === "up" || move === "down") &&
        !focused &&
        (inside || nowhere || onPage(root, target))
      ) {
        const to = arrowTo(order, current, move === "down" ? 1 : -1, false);
        if (!to) return;
        e.preventDefault();
        if (to.move) select(to.id);
        else take(to.id);
        return;
      }
      if (!inside && !nowhere) return;
      if (move && move !== "leave") {
        if (!order.length) return;
        e.preventDefault();
        const next =
          move === "first"
            ? order[0]
            : move === "last"
              ? order.at(-1)
              : stepTo(order, current, move === "down" ? 1 : -1);
        if (next === undefined) return;
        if (next === current) {
          // Nothing past either end: the focus stays where it is.
          if (nowhere) take(next);
          return;
        }
        select(next);
        return;
      }
      if (current === undefined) return;
      // With the focus nowhere, the open question takes the key as if it had it, and the focus if it used it.
      if (nowhere) {
        keys.current?.(e);
        if (
          e.defaultPrevented &&
          move !== "leave" &&
          !inQuestion(root, document.activeElement)
        )
          document
            .getElementById(`question-${current}`)
            ?.focus({ preventScroll: true });
      }
      if (move !== "leave" || e.defaultPrevented) return;
      e.preventDefault();
      follow.current = false;
      setChosen(null);
      if (inside && document.activeElement instanceof HTMLElement)
        document.activeElement.blur();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [select]);

  // Where the open question stood in the page at the last render, scrolling aside. With the page at its very top,
  // where its scroll anchoring keeps nothing, what grew above the question since (the bar of what waits for you, a
  // wish's group) scrolls the page by as much, before it paints, so it stays where you read it. Once scrolled, the
  // anchoring holds it.
  const stood = useRef<{ id: string; at: number }>(undefined);
  useLayoutEffect(() => {
    const was = stood.current;
    stood.current = undefined;
    if (current === undefined || !column.current?.isConnected) return;
    const element = document.getElementById(`question-${current}`);
    const scroller = element && scrollerOf(element, true);
    if (!element || !scroller) return;
    const view = scroller.getBoundingClientRect();
    const at = () =>
      element.getBoundingClientRect().top - view.top + scroller.scrollTop;
    const top = element.getBoundingClientRect().top;
    if (
      was?.id === current &&
      scroller.scrollTop <= 0 &&
      top >= view.top &&
      top < view.bottom &&
      at() - was.at >= 1
    )
      scroller.scrollTop += at() - was.at;
    stood.current = { id: current, at: at() };
  });

  return (
    <section
      className="question-column decisions-section"
      id="questions"
      ref={column}
      aria-label={t("question.column")}
    >
      <h2 className="question-column-label">{t("question.column")}</h2>
      {groups.length === 0 && (
        <p className="question-none">{t("question.none")}</p>
      )}
      {groups.map(({ wish, questions }) => (
        <div className="question-group" key={wish.id}>
          {headings && (
            <button
              type="button"
              className="question-group-head"
              onClick={() => onOpenWish?.(wish.id)}
              title={t("plan.open_wish")}
            >
              <span className="name">{wish.title}</span>
              <span className="question-group-count">{questions.length}</span>
            </button>
          )}
          {questions.map(({ item, blocking, report }) => (
            <WishQuestion
              key={item.id}
              question={item}
              blocking={blocking}
              report={report}
              noLead={noLead}
              current={item.id === current}
              tabbable={current === undefined && item.id === order[0]}
              onSelect={select}
              keys={keys}
              onAnswer={onAnswer}
              onEnlighten={onEnlighten}
              onMove={onMove}
              onWithdraw={onWithdraw}
              onLater={later}
              onSleep={onSleep}
            />
          ))}
        </div>
      ))}
      <AsleepQuestions questions={asleep} headings={headings} onWake={onWake} />
      {children}
    </section>
  );
}

// AsleepQuestions are the questions asleep until their conditions hold, out of the column: a discreet count, which
// opens their list, each with what it sleeps until and a button that wakes it now.
export function AsleepQuestions({
  questions,
  headings = false,
  onWake,
}: {
  questions: readonly OpenQuestion[];
  // On the flying carpet: each says its wish.
  headings?: boolean;
  onWake?: (question: Question) => Promise<unknown>;
}) {
  const [open, setOpen] = useState(false);
  if (questions.length === 0) return null;
  return (
    <div className="question-asleep">
      <button
        type="button"
        className="question-asleep-toggle"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <Moon size={13} aria-hidden="true" />
        {t("question.asleep_count", { count: questions.length })}
      </button>
      {open && (
        <ul className="question-asleep-list">
          {questions.map(({ wish, item }) => (
            <li key={item.id} id={`asleep-${item.id}`}>
              <span className="question-id">{item.code}</span>
              <span className="question-asleep-text">
                {item.text}
                {headings && (
                  <span className="question-asleep-wish">{wish.title}</span>
                )}
                <span className="question-asleep-until">
                  {t("question.asleep_until", { until: sleepTexts(item) })}
                </span>
              </span>
              {onWake && (
                <button
                  type="button"
                  className="button secondary question-wake"
                  onClick={() => void onWake(item).catch(() => {})}
                >
                  {t("question.wake")}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// take gives the focus to the open question, without moving: the page scrolls to its title only when none of it is in
// sight.
function take(id: string) {
  const element = document.getElementById(`question-${id}`);
  if (!element) return;
  element.focus({ preventScroll: true });
  const scroller = scrollerOf(element);
  const view = scroller
    ? scroller.getBoundingClientRect()
    : { top: 0, bottom: window.innerHeight };
  const margin = parseFloat(getComputedStyle(element).scrollMarginTop) || 0;
  const box = element.getBoundingClientRect();
  if (box.bottom <= view.top + margin || box.top >= view.bottom)
    toTitle(element);
}

// typing says whether a key goes to a field: the arrows move its caret.
function typing(target: Node | null): boolean {
  return (
    target instanceof HTMLElement &&
    (target.isContentEditable || target.matches("input, textarea, select"))
  );
}

// inQuestion says whether a question of the column holds the element (the focus, a key's target).
function inQuestion(column: HTMLElement, node: Node | null): boolean {
  return (
    node instanceof Element &&
    column.contains(node) &&
    !!node.closest(".question-row")
  );
}

// What takes the arrows for itself on the page: a menu, a list to pick from, tabs, a slider.
const ARROWS_TAKEN =
  '[role="menu"], [role="listbox"], [role="tablist"], [role="slider"], [role="radiogroup"], [role="tree"], [role="grid"], [role="dialog"]';

// onPage says whether a key's target is on the column's page (the flying carpet, a wish's page), out of what takes the
// arrows for itself.
function onPage(column: HTMLElement, target: Node | null): boolean {
  return (
    target instanceof Element &&
    !!column.closest(".wish-view")?.contains(target) &&
    !target.closest(ARROWS_TAKEN)
  );
}

// focusHere says whether the focus is in the column, or nowhere: the open question that left had it.
function focusHere(column: HTMLElement | null): boolean {
  const active = document.activeElement;
  return (
    !active || active === document.body || (!!column && column.contains(active))
  );
}

// reveal shows a question that opens by its title: the page scrolls only when its top is out of sight, below the bar
// of what waits for you (its scroll margin), or when its bottom is and it fits.
function reveal(element: HTMLElement) {
  const scroller = scrollerOf(element);
  const view = scroller
    ? scroller.getBoundingClientRect()
    : { top: 0, bottom: window.innerHeight };
  const margin = parseFloat(getComputedStyle(element).scrollMarginTop) || 0;
  const box = element.getBoundingClientRect();
  if (
    box.top < view.top + margin ||
    box.height > view.bottom - view.top - margin
  )
    element.scrollIntoView({ block: "start" });
  else if (box.bottom > view.bottom) element.scrollIntoView({ block: "end" });
}

// toTitle scrolls the page so a question gone to reads from its title down: its top at the top of the view, below the
// bar of what waits for you (its scroll margin), wherever the page was. The page keeps its place from there.
function toTitle(element: HTMLElement) {
  element.scrollIntoView({ block: "start" });
  const scroller = scrollerOf(element, true);
  if (scroller) placeTaken(scroller);
}

// scrollerOf is the element that scrolls the question: the first above it that scrolls, or with any, the first that can.
function scrollerOf(element: HTMLElement, any = false): HTMLElement | null {
  for (let node = element.parentElement; node; node = node.parentElement) {
    const { overflowY } = getComputedStyle(node);
    if (
      (overflowY === "auto" || overflowY === "scroll") &&
      (any || node.scrollHeight > node.clientHeight)
    )
      return node;
  }
  return null;
}
