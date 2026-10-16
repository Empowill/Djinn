// The bar that stays at the top of the flying carpet and of a wish while something waits for you: one line each, the
// most blocking first, in the colour of how much it holds up; within a level the questions as they were asked, so a
// new line comes after the others of its level. A click takes you there, in whatever tab, wish or fold it is; a line
// the page cannot show any more is not a button. A move's line has its main link beside it, a button that opens where
// the developer acts (a pull request, a CI run). A question is blocking (red), needed before something (orange, under
// its before words) or can wait (grey), as on its card. A choice the lead's agent shows in its terminal (an approval,
// a folder to trust) is blocking too: its options are buttons that answer it there. Each lit ifrit has a band right
// after that choice, the one with the latest activity first: its links, its countdown once Djinn warned it goes out,
// the first line of its last reply, then its own questions and the lead's questions about it, which leave the lines
// below; without a lit ifrit, the order is the one above.
import {
  ArrowDownRight,
  BellRing,
  CircleUserRound,
  TerminalSquare,
} from "lucide-react";
import { type ReactNode, useCallback, useEffect, useState } from "react";

import {
  type LeadPrompt,
  type Task,
  TaskStatus,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useDjinn } from "./data/djinn";
import {
  byAsked,
  type Item,
  type OpenQuestion,
  type Waiting,
} from "./data/flight";
import { firstLine, useNow } from "./data/format";
import { ifritLinks, ifritPhase, ifritWord } from "./data/ifrit";
import { mainLink } from "./data/links";
import { t } from "./i18n";
import { IfritCountdown, IfritLinks, KeepLit } from "./ifrit";
import { MoveLink } from "./link";
import { ToneIcon } from "./status";

// How much a line holds up: a question workers wait on, a move only the developer can make, a question needed
// before something, a worker that asks to edit, a question that can wait, a wish ready to grant.
// A lit ifrit's band holds lines of these levels.
export type Level =
  "blocking" | "move" | "question" | "action" | "later" | "ready" | "ifrit";

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
  // The choice the lead's agent shows, for its line: its options answer it.
  prompt?: LeadPrompt;
  // A move's main link, where the developer acts: a button beside its line.
  link?: string;
  // A lit ifrit's band: the ifrit, and the lines of its questions and of the lead's questions about it.
  band?: { task: Task; items: Attention[] };
  // On a line of a band: who asked, the ifrit or the lead about it.
  asker?: string;
}

const order: Record<Level, number> = {
  ifrit: -1,
  blocking: 0,
  move: 1,
  question: 2,
  action: 3,
  later: 4,
  ready: 5,
};

// attentionOf lists what waits for you, the most blocking first, each level's questions the oldest first; origin marks
// a line with its wish. prompt is the choice the lead's agent shows in its terminal, if any: it holds up the lead, so
// every wish, and comes first. ifrits are the lit ifrits (litIfrits, the latest activity first): a band each follows
// the prompt, holding the ifrit's own questions (Question.task_id), then the lead's questions about it
// (Question.about_task_id), each the most urgent first; they show there only.
export function attentionOf(
  questions: readonly OpenQuestion[],
  waiting: readonly Waiting[],
  ready: readonly Wish[],
  origin?: (wish: Wish) => ReactNode,
  prompt?: LeadPrompt,
  ifrits: readonly Item<Task>[] = [],
): Attention[] {
  const out: Attention[] = [];
  // The band each question goes to, by its id, and who asked it.
  const bandOf = new Map<string, { id: string; own: boolean }>();
  for (const { item: task } of ifrits)
    for (const { item } of questions) {
      if (bandOf.has(item.id)) continue;
      if (item.taskId === task.id)
        bandOf.set(item.id, { id: task.id, own: true });
      else if (item.aboutTaskId === task.id)
        bandOf.set(item.id, { id: task.id, own: false });
    }
  const bands = new Map(
    ifrits.map(({ wish, item: task }) => [
      task.id,
      {
        key: `ifrit-${task.id}`,
        level: "ifrit" as const,
        target: `task-${task.id}`,
        wishId: wish.id,
        code: task.code,
        text: task.title,
        origin: origin?.(wish),
        band: { task, items: [] as Attention[] },
      },
    ]),
  );
  const own: Attention[] = [];
  const about: Attention[] = [];
  if (prompt?.options.length)
    out.push({
      key: "lead-prompt",
      level: "blocking",
      label: t("lead_prompt.label"),
      target: "lead-prompt",
      wishId: "",
      code: "",
      text: prompt.title || t("lead_prompt.title"),
      prompt,
    });
  for (const { wish, item, blocking } of [...questions].sort(byAsked)) {
    const band = bandOf.get(item.id);
    const lines = band ? (band.own ? own : about) : out;
    const level: Level = blocking.length
      ? "blocking"
      : item.move && item.before
        ? "move"
        : item.waitingLead
          ? "question"
          : item.before
            ? "question"
            : "later";
    const host = band && bands.get(band.id)?.band.task;
    lines.push({
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
      origin: band ? undefined : origin?.(wish),
      link: item.move ? mainLink(item) : undefined,
      asker: host
        ? t(band.own ? "attention.asked_by_ifrit" : "attention.about_ifrit", {
            code: host.code,
          })
        : undefined,
    });
  }
  const urgent = (a: Attention, b: Attention) =>
    order[a.level] - order[b.level];
  for (const line of [...own.sort(urgent), ...about.sort(urgent)]) {
    const id = bandOf.get(line.key)?.id;
    if (id) bands.get(id)?.band.items.push(line);
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
  out.sort(urgent);
  // The lead's choice stays first: until it is made, the lead relays nothing to any ifrit.
  const first = out[0]?.prompt ? 1 : 0;
  out.splice(first, 0, ...bands.values());
  return out;
}

// attentionCount counts the lines, a band's included: what the bar's summary says.
export function attentionCount(items: readonly Attention[]): number {
  return items.reduce(
    (sum, item) => sum + (item.band ? 1 + item.band.items.length : 1),
    0,
  );
}

const levelKeys = {
  ifrit: "attention.level_ifrit",
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

// The tab of a page (a wish or the flying carpet) that shows a line's target: the decisions show the answered
// questions, the tasks a task asked for itself (task-<id>, as the resources panel asks), the first tab all the rest.
export type Tab = "main" | "decisions" | "tasks";

export function tabOf(target: string): Tab {
  if (target.startsWith("decision-")) return "decisions";
  return target.startsWith("task-") ? "tasks" : "main";
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
  const bands = items.some((item) => item.band);
  return (
    <nav
      className={bands ? "attention-bar with-ifrit" : "attention-bar"}
      aria-label={t("attention.label")}
    >
      <span className="attention-summary">
        <BellRing size={14} aria-hidden="true" />
        {t("attention.summary", { count: attentionCount(items) })}
      </span>
      <ul>
        {items.map((item) => {
          if (item.prompt)
            return (
              <li key={item.key}>
                <LeadPromptItem item={item} prompt={item.prompt} />
              </li>
            );
          if (item.band)
            return (
              <li key={item.key}>
                <IfritBand
                  item={item}
                  task={item.band.task}
                  lines={item.band.items}
                  reach={reach}
                />
              </li>
            );
          return <AttentionLine key={item.key} item={item} reach={reach} />;
        })}
      </ul>
    </nav>
  );
}

// AttentionLine is a line of the bar, a button that reaches its target, or plain text when the page cannot show it.
function AttentionLine({
  item,
  reach,
}: {
  item: Attention;
  reach: (item: Attention) => (() => void) | undefined;
}) {
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
      {item.asker && <span className="attention-asker">{item.asker}</span>}
      <span className="attention-text">{item.text}</span>
      {item.origin}
    </>
  );
  return (
    <li className={item.link ? "attention-with-link" : undefined}>
      {click ? (
        <button
          className={`attention-item level-${item.level}`}
          onClick={click}
        >
          {line}
          <ArrowDownRight size={14} aria-hidden="true" />
        </button>
      ) : (
        <span className={`attention-item level-${item.level}`}>{line}</span>
      )}
      {item.link && <MoveLink href={item.link} />}
    </li>
  );
}

// IfritBand is a lit ifrit's band: its flame, code, title and the word of where it stands, a button to its card; its
// links; its countdown and Keep lit once Djinn warned it goes out; the first line of its last reply; then its lines.
function IfritBand({
  item,
  task,
  lines,
  reach,
}: {
  item: Attention;
  task: Task;
  lines: readonly Attention[];
  reach: (item: Attention) => (() => void) | undefined;
}) {
  // A minute is the countdown's step.
  const now = useNow(true, 15_000);
  const click = reach(item);
  const head = (
    <>
      <span className="attention-level">
        <ToneIcon tone="ifrit" size={14} />
        {t(levelKeys.ifrit)}
      </span>
      <b className="attention-code">{item.code}</b>
      <span className="attention-text">{item.text}</span>
      <span className="attention-ifrit-state">{ifritWord(task, now)}</span>
      {item.origin}
    </>
  );
  const reply = firstLine(task.ifrit?.reply ?? "");
  return (
    <section
      className="attention-ifrit tone-ifrit"
      aria-label={t("ifrit.band", { code: task.code, title: task.title })}
    >
      <div className="attention-ifrit-head">
        {click ? (
          <button className="attention-ifrit-title" onClick={click}>
            {head}
            <ArrowDownRight size={14} aria-hidden="true" />
          </button>
        ) : (
          <span className="attention-ifrit-title">{head}</span>
        )}
        <IfritLinks links={ifritLinks(task)} />
        {ifritPhase(task, now) === "warned" && (
          <span className="attention-ifrit-warning">
            <IfritCountdown task={task} now={now} />
            <KeepLit />
          </span>
        )}
      </div>
      {reply && (
        <p className="attention-ifrit-reply" title={t("ifrit.reply")}>
          <q>{reply}</q>
        </p>
      )}
      {lines.length > 0 && (
        <ul>
          {lines.map((line) => (
            <AttentionLine key={line.key} item={line} reach={reach} />
          ))}
        </ul>
      )}
    </section>
  );
}

// LeadPromptItem is the line of the choice the lead's agent shows: its question, what it is about, and its options as
// buttons. A click sends the option to the lead's terminal as its keys would (LeadService.Choose), only while the
// terminal shows that very choice; the line goes once the agent took the answer.
function LeadPromptItem({
  item,
  prompt,
}: {
  item: Attention;
  prompt: LeadPrompt;
}) {
  const clients = useClients();
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");
  const choose = async (index: number) => {
    setSending(true);
    setError("");
    try {
      await clients.lead.choose({
        option: index + 1,
        title: prompt.title,
        lines: prompt.lines,
        options: prompt.options,
      });
    } catch (err) {
      setError(message(err));
    } finally {
      setSending(false);
    }
  };
  return (
    <div
      id="lead-prompt"
      className={`attention-item attention-prompt level-${item.level}`}
    >
      <div className="attention-prompt-head">
        <span className="attention-level">
          <TerminalSquare size={14} aria-hidden="true" />
          {item.label}
        </span>
        <span className="attention-text">{item.text}</span>
      </div>
      {prompt.lines.length > 0 && (
        <pre className="attention-prompt-lines">{prompt.lines.join("\n")}</pre>
      )}
      <div
        className="attention-prompt-options"
        role="group"
        aria-label={item.text}
        title={t("lead_prompt.detail")}
      >
        {prompt.options.map((option, index) => (
          <button
            key={index}
            className="button secondary small"
            disabled={sending}
            onClick={() => void choose(index)}
          >
            {option}
          </button>
        ))}
      </div>
      {error && (
        <p className="attention-prompt-error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
