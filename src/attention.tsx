// The bar that stays at the top of the flight plan and of a wish while something waits for you: one line each, the
// most blocking first, in the colour of how much it holds up. A click takes you there, in whatever tab, wish or fold
// it is; a line the page cannot show any more is not a button. A question is blocking (red), needed before something
// (orange, under its before words) or can wait (grey), as on its card.
import { ArrowDownRight, BellRing, CircleUserRound } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useState } from "react";

import { TaskStatus, type Wish } from "../gen/ts/plan/v1/plan_pb";
import { useDjinn } from "./data/djinn";
import type { OpenQuestion, Waiting } from "./data/flight";
import { firstLine } from "./data/format";
import { t } from "./i18n";

// How much a line holds up: a question workers wait on, a move only the developer can make, a question needed
// before something, a worker that asks to edit, a question that can wait, a wish ready to grant.
export type Level =
  "blocking" | "move" | "question" | "action" | "later" | "ready";

export interface Attention {
  key: string;
  level: Level;
  // The words of the level, when they are the line's own: a question's before words.
  label?: string;
  // The id of the element to scroll to.
  target: string;
  // The wish that holds it.
  wishId: string;
  code: string;
  text: string;
  origin?: ReactNode;
}

const order: Record<Level, number> = {
  blocking: 0,
  move: 1,
  question: 2,
  action: 3,
  later: 4,
  ready: 5,
};

// attentionOf lists what waits for you, the most blocking first; origin marks a line with its wish.
export function attentionOf(
  questions: readonly OpenQuestion[],
  waiting: readonly Waiting[],
  ready: readonly Wish[],
  origin?: (wish: Wish) => ReactNode,
): Attention[] {
  const out: Attention[] = [];
  for (const { wish, item, blocking } of questions) {
    const level: Level = blocking.length
      ? "blocking"
      : item.move && item.before
        ? "move"
        : item.waitingLead
          ? "question"
          : item.before
            ? "question"
            : "later";
    out.push({
      key: item.id,
      level,
      label: item.waitingLead
        ? t("question.waiting_lead")
        : blocking.length || item.move
          ? undefined
          : item.before || undefined,
      // An answered question waiting for a lead shows in the decisions.
      target: item.waitingLead ? `decision-${item.id}` : `question-${item.id}`,
      wishId: wish.id,
      code: item.code,
      text: blocking.length
        ? `${item.text} · ${t("page.blocking", { tasks: blocking.join(", ") })}`
        : item.text,
      origin: origin?.(wish),
    });
  }
  for (const { wish, item, question, host } of waiting) {
    // A worker that waits for an open question is already on that question's line.
    if (item.status === TaskStatus.WAITING && question) continue;
    const failed = item.status === TaskStatus.FAILED;
    const text = failed
      ? firstLine(item.error) || t("attention.worker_failed")
      : t("attention.worker_waits");
    out.push({
      key: item.id,
      level: "action",
      target: `waiting-${item.id}`,
      wishId: wish.id,
      // A merge counts with the task it serves: its line is that task's.
      code: host?.code ?? item.code,
      text: host ? `${t("task.merge")} ${item.code} · ${text}` : text,
      origin: origin?.(wish),
    });
  }
  for (const wish of ready)
    out.push({
      key: `ready-${wish.id}`,
      level: "ready",
      target: `grant-${wish.id}`,
      wishId: wish.id,
      code: "",
      text: t("wish.ready_title"),
      origin: origin?.(wish),
    });
  return out.sort((a, b) => order[a.level] - order[b.level]);
}

const levelKeys = {
  blocking: "attention.level_blocking",
  move: "attention.level_move",
  question: "attention.level_question",
  action: "attention.level_action",
  later: "attention.level_later",
  ready: "attention.level_ready",
} as const;

// jump scrolls to an element, puts the focus on it and makes it glow a moment; false when the page does not show it.
export function jump(id: string): boolean {
  const element =
    document.getElementById(id) ??
    document.getElementById(id.replace(/^waiting-/, "task-")) ??
    document.getElementById(id.replace(/^question-/, "decision-"));
  if (!element) return false;
  const reduced = document.documentElement.dataset.motion === "reduced";
  element.scrollIntoView({
    behavior: reduced ? "auto" : "smooth",
    block: "start",
  });
  if (!element.hasAttribute("tabindex")) element.setAttribute("tabindex", "-1");
  element.focus({ preventScroll: true });
  element.classList.remove("flash");
  void element.offsetWidth;
  element.classList.add("flash");
  return true;
}

// The tab of a page (a wish or the flight plan) that shows a line's target: the decisions show the answered
// questions, the first tab all the rest.
export type Tab = "main" | "decisions";

export function tabOf(target: string): Tab {
  return target.startsWith("decision-") ? "decisions" : "main";
}

// Way is where a click on a line takes you: a tab of this page, or the wish that holds it.
export type Way = { tab: Tab } | { wishId: string };

// wayTo says where a line's target is: in another wish than here, or in a tab of this page if it shows it (shown
// holds the ids of the targets the page renders, in any tab or fold); undefined when the page cannot show it any
// more: answered, withdrawn or moved meanwhile.
export function wayTo(
  item: Attention,
  shown: ReadonlySet<string>,
  here = "",
): Way | undefined {
  if (here && item.wishId !== here) return { wishId: item.wishId };
  return shown.has(item.target) ? { tab: tabOf(item.target) } : undefined;
}

// How long a jump waits for the page to show its target: a wish that opens reads its questions first.
const SETTLE = 3000;

// useReach brings a line's target into sight the way the page does elsewhere: show switches the page's tab with the
// item in focus, which unfolds the part that holds it; another wish opens through the window's focus (src/data/
// focus.ts). Then it jumps, once the page shows the target. reach gives a line's click, undefined for a line the page
// cannot show; to reaches a target of this page, as the window's focus asks.
export function useReach(
  show: (tab: Tab, id: string) => void,
  shown: ReadonlySet<string>,
  here?: string,
) {
  const djinn = useDjinn();
  const [pending, setPending] = useState<{ id: string; at: number }>();
  // Tried again as what the page shows changes, until the target is there or a while has passed.
  useEffect(() => {
    if (!pending) return;
    const wait = pending.at + SETTLE - Date.now();
    if (jump(pending.id) || wait <= 0) return setPending(undefined);
    const timer = setTimeout(() => setPending(undefined), wait);
    return () => clearTimeout(timer);
  }, [pending, shown]);
  const to = useCallback(
    (target: string) => {
      show(tabOf(target), target.replace(/^[a-z]+-/, ""));
      setPending({ id: target, at: Date.now() });
    },
    [show],
  );
  const reach = useCallback(
    (item: Attention) => {
      const way = wayTo(item, shown, here);
      if (!way) return undefined;
      if ("wishId" in way)
        return () =>
          djinn?.focus.show({ wishId: way.wishId, target: item.target });
      return () => to(item.target);
    },
    [shown, here, djinn, to],
  );
  return { reach, to };
}

// AttentionBar lists the lines; reach gives each line's click (useReach), and a line without one is plain text.
export function AttentionBar({
  items,
  reach = (item) => () => jump(item.target),
}: {
  items: readonly Attention[];
  reach?: (item: Attention) => (() => void) | undefined;
}) {
  if (!items.length) return null;
  return (
    <nav className="attention-bar" aria-label={t("attention.label")}>
      <span className="attention-summary">
        <BellRing size={14} aria-hidden="true" />
        {t("attention.summary", { count: items.length })}
      </span>
      <ul>
        {items.map((item) => {
          const click = reach(item);
          const line = (
            <>
              <span className="attention-level">
                {item.level === "move" && (
                  <CircleUserRound size={14} aria-hidden="true" />
                )}
                {item.label ?? t(levelKeys[item.level])}
              </span>
              {item.code && <b className="attention-code">{item.code}</b>}
              <span className="attention-text">{item.text}</span>
              {item.origin}
            </>
          );
          return (
            <li key={item.key}>
              {click ? (
                <button
                  className={`attention-item level-${item.level}`}
                  onClick={click}
                >
                  {line}
                  <ArrowDownRight size={14} aria-hidden="true" />
                </button>
              ) : (
                <span className={`attention-item level-${item.level}`}>
                  {line}
                </span>
              )}
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
