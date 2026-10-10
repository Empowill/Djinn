// The bar that stays at the top of the flight plan and of a wish while something waits for you: one line each, the
// most blocking first, in the colour of how much it holds up. A click takes you there. A question is blocking (red),
// needed before something (orange, under its before words) or can wait (grey), as on its card.
import { ArrowDownRight, BellRing, CircleUserRound } from "lucide-react";
import type { ReactNode } from "react";

import { TaskStatus, type Wish } from "../gen/ts/plan/v1/plan_pb";
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
      : item.move
        ? "move"
        : item.before
          ? "question"
          : "later";
    out.push({
      key: item.id,
      level,
      label:
        blocking.length || item.move ? undefined : item.before || undefined,
      target: `question-${item.id}`,
      code: item.code,
      text: blocking.length
        ? `${item.text} · ${t("page.blocking", { tasks: blocking.join(", ") })}`
        : item.text,
      origin: origin?.(wish),
    });
  }
  for (const { wish, item, question } of waiting) {
    // A worker that waits for an open question is already on that question's line.
    if (item.status === TaskStatus.WAITING && question) continue;
    const failed = item.status === TaskStatus.FAILED;
    out.push({
      key: item.id,
      level: "action",
      target: `waiting-${item.id}`,
      code: item.code,
      text: failed
        ? firstLine(item.error) || t("attention.worker_failed")
        : t("attention.worker_waits"),
      origin: origin?.(wish),
    });
  }
  for (const wish of ready)
    out.push({
      key: `ready-${wish.id}`,
      level: "ready",
      target: `grant-${wish.id}`,
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

// jump scrolls to an element, puts the focus on it and makes it glow a moment.
export function jump(id: string) {
  const element =
    document.getElementById(id) ??
    document.getElementById(id.replace(/^waiting-/, "task-"));
  if (!element) return;
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
}

export function AttentionBar({ items }: { items: readonly Attention[] }) {
  if (!items.length) return null;
  return (
    <nav className="attention-bar" aria-label={t("attention.label")}>
      <span className="attention-summary">
        <BellRing size={14} aria-hidden="true" />
        {t("attention.summary", { count: items.length })}
      </span>
      <ul>
        {items.map((item) => (
          <li key={item.key}>
            <button
              className={`attention-item level-${item.level}`}
              onClick={() => jump(item.target)}
            >
              <span className="attention-level">
                {item.level === "move" && (
                  <CircleUserRound size={14} aria-hidden="true" />
                )}
                {item.label ?? t(levelKeys[item.level])}
              </span>
              {item.code && <b className="attention-code">{item.code}</b>}
              <span className="attention-text">{item.text}</span>
              {item.origin}
              <ArrowDownRight size={14} aria-hidden="true" />
            </button>
          </li>
        ))}
      </ul>
    </nav>
  );
}
