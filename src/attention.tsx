// The bar that stays at the top of the flight plan and of a wish while something waits for you: one line each, the
// most blocking first, in the colour of how much it holds up. A click takes you there.
import { ArrowDownRight, BellRing } from "lucide-react";
import type { ReactNode } from "react";

import { TaskStatus, type Wish } from "../gen/ts/plan/v1/plan_pb";
import type { OpenQuestion, Waiting } from "./data/flight";
import { t } from "./i18n";

// How much a line holds up: a question workers wait on, a question, a worker cut short, a wish ready to grant.
export type Level = "blocking" | "question" | "action" | "ready";

export interface Attention {
  key: string;
  level: Level;
  // The id of the element to scroll to.
  target: string;
  code: string;
  text: string;
  origin?: ReactNode;
}

const order: Record<Level, number> = {
  blocking: 0,
  question: 1,
  action: 2,
  ready: 3,
};

// attentionOf lists what waits for you, the most blocking first; origin marks a line with its wish. prompted are the
// wishes whose lead shows a choice in its terminal: it holds the lead up.
export function attentionOf(
  questions: readonly OpenQuestion[],
  waiting: readonly Waiting[],
  ready: readonly Wish[],
  origin?: (wish: Wish) => ReactNode,
  prompted: readonly Wish[] = [],
): Attention[] {
  const out: Attention[] = [];
  for (const wish of prompted)
    out.push({
      key: `lead-prompt-${wish.id}`,
      level: "blocking",
      target: `lead-prompt-${wish.id}`,
      code: "",
      text: wish.leadPrompt?.title || t("lead_prompt.title"),
      origin: origin?.(wish),
    });
  for (const { wish, item, blocking } of questions)
    out.push({
      key: item.id,
      level: blocking.length ? "blocking" : "question",
      target: `question-${item.id}`,
      code: item.code,
      text: blocking.length
        ? `${item.text} · ${t("page.blocking", { tasks: blocking.join(", ") })}`
        : item.text,
      origin: origin?.(wish),
    });
  for (const { wish, item, question } of waiting) {
    // A worker that waits for an open question is already on that question's line.
    if (item.status === TaskStatus.WAITING && question) continue;
    out.push({
      key: item.id,
      level: "action",
      target: `waiting-${item.id}`,
      code: item.code,
      text:
        item.status === TaskStatus.INTERRUPTED
          ? t("attention.interrupted")
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
  question: "attention.level_question",
  action: "attention.level_action",
  ready: "attention.level_ready",
} as const;

// jump scrolls to an element, puts the focus on it and makes it glow a moment.
export function jump(id: string) {
  const element = document.getElementById(id);
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
                {t(levelKeys[item.level])}
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
