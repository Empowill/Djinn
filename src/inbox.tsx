// The inbox: what waits for you outside any wish. Each card says three things, in this order: its kind and the fact;
// what Djinn will do once you hand it over; under each choice, what that choice changes. Two kinds of card: a request
// a source printed (a merge request assigned to you, a mention), with the route Djinn proposes, the recommended
// destination first, and "Dismiss" last; and a source plugged in on this machine that fails, with how to fix it or
// unplug it. Nothing is made until you click, and Djinn never answers a source. Under the cards, one line per project
// whose configuration has something for you to fix (Project.to_fix): its gear in the menu turns red, and the line opens
// the same panel; the detail stays there, never twice. Empty, the inbox is hidden: the sources themselves live in their
// project's panel (InboxSources), plugged in there. The cards come as they arrived, the oldest first: a new one comes
// after the others and moves none of them while you read (AGENTS.md, Interface).
import { timestampMs } from "@bufbuild/protobuf/wkt";
import { Inbox as InboxIcon, Lamp, Plug, Settings, Unplug } from "lucide-react";
import { memo, useEffect, useMemo, useState } from "react";

import {
  Change,
  type InboxItem,
  type InboxSource,
  type Project,
  ProjectNoticeKind,
  type RouteOption,
  RouteKind,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useData, useStore } from "./data/djinn";
import { choiceOf, letter, when } from "./data/format";
import { t } from "./i18n";
import { ProjectFlame } from "./wish-sidebar";
import "./inbox.css";

// byArrival orders inbox items as they arrived, the oldest first.
function byArrival(a: InboxItem, b: InboxItem): number {
  const at = (item: InboxItem) =>
    item.createTime ? timestampMs(item.createTime) : 0;
  return at(a) - at(b) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
}

// failing tells whether a source plugged in on this machine waits for you: it cannot run, or its last run failed.
export function failing(src: InboxSource): boolean {
  return src.plugged && (!!src.error || (src.lastRun?.failures ?? 0) > 0);
}

// useInboxCount is how many cards the inbox shows: the items that wait, the sources that fail, and the projects with
// something to fix.
export function useInboxCount(): number {
  return useData(
    (s) =>
      s.inbox.length +
      s.sources.reduce((n, x) => n + +failing(x), 0) +
      s.projects.reduce((n, p) => n + +(p.toFix > 0), 0),
  );
}

export function Inbox({
  onOpen,
  onOpenProject,
  onToast,
}: {
  // Shows the wish an item went to.
  onOpen: (wishId: string) => void;
  // Opens a project's panel, as its gear does.
  onOpenProject?: (projectId: string) => void;
  onToast: (text: string) => void;
}) {
  const newest = useData((s) => s.inbox);
  const items = useMemo(() => [...newest].sort(byArrival), [newest]);
  const sources = useData((s) => s.sources);
  const broken = useMemo(() => sources.filter(failing), [sources]);
  const projects = useData((s) => s.projects);
  const red = useMemo(() => projects.filter((p) => p.toFix > 0), [projects]);
  const count = items.length + broken.length + red.length;
  if (!count) return null;
  return (
    <section className="inbox" aria-label={t("inbox.title")}>
      <div className="section-title">
        <h2>
          <InboxIcon size={17} aria-hidden="true" />
          {t("inbox.title")}
          <span className="count">{count}</span>
        </h2>
        <p>{t("inbox.detail")}</p>
      </div>
      <div className="card-grid">
        {items.map((item) => (
          <InboxCard
            key={item.id}
            item={item}
            onOpen={onOpen}
            onToast={onToast}
          />
        ))}
        {broken.map((src) => (
          <SourceCard key={src.name} src={src} onToast={onToast} />
        ))}
        {red.map((project) => (
          <ProjectLine
            key={project.id}
            project={project}
            onOpenProject={onOpenProject}
          />
        ))}
      </div>
    </section>
  );
}

// every is how often a source runs, as Go writes a duration, made short: 5m0s is 5m, 1h0m0s is 1h.
function every(duration: string): string {
  return duration.replace(/(\D)0s$/, "$1").replace(/(\D)0m$/, "$1");
}

// useToggle plugs a source in or unplugs it, on this machine, and says so in a toast.
function useToggle(onToast: (text: string) => void) {
  const clients = useClients();
  const store = useStore();
  const [busy, setBusy] = useState("");
  const toggle = async (src: InboxSource) => {
    setBusy(src.name);
    try {
      if (src.plugged) {
        await clients.inbox.unplug({ source: src.name });
        onToast(t("inbox.unplugged_toast", { source: src.name }));
      } else {
        await clients.inbox.plug({ source: src.name });
        onToast(t("inbox.plugged_toast", { source: src.name }));
      }
    } catch (error) {
      onToast(message(error));
    } finally {
      setBusy("");
      void store.changed("", [Change.INBOX]);
    }
  };
  return { busy, toggle };
}

// sourceState says how a source stands on this machine: unplugged, why it cannot run, its last run's failure, or
// how its last run went.
function sourceState(src: InboxSource): string {
  if (src.error) return src.error;
  if (!src.plugged) return t("inbox.unplugged");
  const run = src.lastRun;
  const plugged = t("inbox.plugged", { every: every(src.every) });
  if (!run?.endTime) return plugged;
  if (run.failures > 0)
    return t("inbox.state_failing", {
      when: when(run.failingSince ?? run.endTime),
      count: run.failures,
    });
  return `${plugged} · ${t("inbox.state_ran", { when: when(run.endTime), count: run.items })}`;
}

// InboxSources lists the sources a project's skills declare, each with what it reads, how it stands on this machine,
// and a button that plugs it in or unplugs it, what that does said under it. The project's panel shows it.
export function InboxSources({
  sources,
  onToast,
}: {
  sources: InboxSource[];
  onToast: (text: string) => void;
}) {
  const { busy, toggle } = useToggle(onToast);
  if (!sources.length)
    return <p className="muted-text">{t("project.no_sources")}</p>;
  return (
    <ul className="inbox-sources" aria-label={t("inbox.sources_label")}>
      {sources.map((src) => (
        <li key={src.name} className="inbox-source-row">
          <div className="inbox-source-text">
            <strong>{src.skill}</strong>
            {src.summary && <p>{src.summary}</p>}
            <span
              className={
                src.error || failing(src)
                  ? "inbox-source-error"
                  : `inbox-source-state ${src.plugged ? "plugged" : ""}`
              }
            >
              {sourceState(src)}
            </span>
            {src.watch && <code>{src.watch}</code>}
          </div>
          {(src.plugged || !src.error) && (
            <div className="inbox-source-action">
              <button
                className="button small secondary"
                disabled={busy === src.name}
                onClick={() => void toggle(src)}
              >
                {src.plugged ? <Unplug size={14} /> : <Plug size={14} />}
                {src.plugged ? t("inbox.unplug") : t("inbox.plug")}
              </button>
              <p>
                {src.plugged
                  ? t("inbox.unplug_detail")
                  : t("inbox.plug_detail", { every: every(src.every) })}
              </p>
            </div>
          )}
        </li>
      ))}
    </ul>
  );
}

// optionText is how a destination reads, as on the card of a routed request.
function optionText(
  opt: RouteOption,
  names: ReadonlyMap<string, string>,
): string {
  const skill = opt.template
    ? t("route.with_skill", { skill: opt.template.skill })
    : "";
  const params = {
    wish: opt.title,
    title: opt.title,
    pause: names.get(opt.pauseWishId) ?? "",
  };
  switch (opt.kind) {
    case RouteKind.FILE:
      return t("route.file", params);
    case RouteKind.QUEUE:
      return t("route.queue", params) + skill;
    case RouteKind.SWAP:
      return t("route.swap", params) + skill;
  }
  const projects = opt.projectIds
    .map((id) => names.get(id))
    .filter(Boolean)
    .join(", ");
  return (
    (projects
      ? t("route.new", { ...params, projects })
      : t("route.new_bare", params)) + skill
  );
}

// willText says what Djinn will do once the item goes where it recommends: the second sentence of a card.
function willText(opt: RouteOption | undefined): string {
  if (!opt) return t("inbox.will_none");
  if (opt.kind === RouteKind.FILE)
    return t("inbox.will_file", { wish: opt.title });
  if (opt.template?.watch)
    return t("inbox.will_watch", { skill: opt.template.skill });
  if (opt.template) return t("inbox.will_skill", { skill: opt.template.skill });
  return t("inbox.will_new");
}

// effectText says what a destination changes, under its name.
function effectText(
  opt: RouteOption,
  names: ReadonlyMap<string, string>,
): string {
  switch (opt.kind) {
    case RouteKind.FILE:
      return t("inbox.effect_file");
    case RouteKind.QUEUE:
      return t("inbox.effect_queue");
    case RouteKind.SWAP:
      return t("inbox.effect_swap", {
        pause: names.get(opt.pauseWishId) ?? "",
      });
  }
  if (opt.template?.watch) return t("inbox.effect_watch");
  if (opt.template) return t("inbox.effect_skill");
  return t("inbox.effect_new");
}

const InboxCard = memo(function InboxCard({
  item,
  onOpen,
  onToast,
}: {
  item: InboxItem;
  onOpen: (wishId: string) => void;
  onToast: (text: string) => void;
}) {
  const clients = useClients();
  const store = useStore();
  const projects = useData((s) => s.projects);
  const wishes = useData((s) => s.wishes);
  const names = useMemo(
    () =>
      new Map<string, string>([
        ...projects.map((p) => [p.id, p.name] as [string, string]),
        ...wishes.map((w) => [w.id, w.title] as [string, string]),
      ]),
    [projects, wishes],
  );
  const [sending, setSending] = useState(false);
  const [first, ...rest] = item.text.split("\n");
  const options = item.route?.options ?? [];

  const send = async (run: () => Promise<void>) => {
    setSending(true);
    try {
      await run();
    } catch (error) {
      onToast(message(error));
    } finally {
      setSending(false);
      void store.changed("", [Change.INBOX, Change.WISH]);
    }
  };
  const route = (index: number) =>
    send(async () => {
      const res = await clients.inbox.route({
        itemId: item.id,
        choice: choiceOf(index),
      });
      const wishId = res.item?.wishId;
      if (wishId) {
        await store.changed(wishId, [Change.WISH, Change.BLOCK]);
        onOpen(wishId);
      }
    });
  const dismiss = () =>
    send(async () => {
      await clients.inbox.dismiss({ itemId: item.id });
      onToast(t("inbox.dismissed"));
    });

  return (
    <article className="question-card open inbox-card">
      <div className="question-top">
        <div className="question-heading">
          <div className="question-title">
            <span className="question-meta">
              <span className="inbox-kind">{t("inbox.kind_request")}</span>
              <span className="inbox-source">
                {t("inbox.read_by", { source: item.source })}
              </span>
              <span className="inbox-when">{when(item.createTime)}</span>
            </span>
            <h3>{first}</h3>
            {rest.length > 0 && <p className="inbox-rest">{rest.join("\n")}</p>}
            <p className="inbox-will">{willText(options[0])}</p>
          </div>
        </div>
      </div>
      <div className="question-inner">
        <div
          className="question-options"
          role="group"
          aria-label={t("inbox.routes")}
        >
          {options.map((opt, index) => (
            <button
              key={index}
              className={`option ${index === 0 ? "selected" : ""}`}
              disabled={sending}
              title={opt.reason}
              onClick={() => void route(index)}
            >
              <strong className="option-letter">{letter(index)}</strong>
              <p>{optionText(opt, names)}</p>
              <p className="inbox-effect">{effectText(opt, names)}</p>
              {index === 0 && (
                <span className="option-recommended">
                  {t("question.recommended")}
                </span>
              )}
            </button>
          ))}
          <button
            className="option"
            disabled={sending}
            onClick={() => void dismiss()}
          >
            <strong className="option-letter">{letter(options.length)}</strong>
            <p>{t("inbox.dismiss")}</p>
            <p className="inbox-effect">{t("inbox.effect_dismiss")}</p>
          </button>
        </div>
        {options.length > 0 && (
          <div className="question-actions">
            <button
              className="button accent lamp-rub"
              title={t("inbox.rub_detail")}
              disabled={sending}
              onClick={() => void route(0)}
            >
              <Lamp size={14} />
              {t("question.rub")} · {letter(0)}
            </button>
          </div>
        )}
      </div>
    </article>
  );
});

// SourceCard is a source plugged in on this machine that fails: why, what that stops, and the two ways out, fixing
// the cause (recommended: the next run that goes well takes the card away) or unplugging it.
const SourceCard = memo(function SourceCard({
  src,
  onToast,
}: {
  src: InboxSource;
  onToast: (text: string) => void;
}) {
  const { busy, toggle } = useToggle(onToast);
  const run = src.lastRun;
  const failures = src.error ? 0 : (run?.failures ?? 0);
  const since = failures ? when(run?.failingSince ?? run?.endTime) : "";
  return (
    <article className="question-card open inbox-card inbox-source-card">
      <div className="question-top">
        <div className="question-heading">
          <div className="question-title">
            <span className="question-meta">
              <span className="inbox-kind">{t("inbox.kind_config")}</span>
              {since && (
                <span className="inbox-when">
                  {t("inbox.failing_since", { when: since })}
                </span>
              )}
              {failures > 0 && (
                <span className="inbox-when">
                  {t("inbox.failures", { count: failures })}
                </span>
              )}
            </span>
            <h3>
              {t(src.error ? "inbox.cannot_title" : "inbox.failing_title", {
                source: src.name,
              })}
            </h3>
            <pre className="inbox-error">{src.error || run?.error}</pre>
            {src.summary && (
              <p className="inbox-rest">
                {t("inbox.failing_reads", { summary: src.summary })}
              </p>
            )}
            <p className="inbox-will">
              {src.error
                ? t("inbox.cannot_will")
                : t("inbox.failing_will", { every: every(src.every) })}
            </p>
          </div>
        </div>
      </div>
      <div className="question-inner">
        <div
          className="question-options"
          role="group"
          aria-label={t("inbox.ways_out")}
        >
          <div className="option selected inbox-fix">
            <strong className="option-letter">{letter(0)}</strong>
            <p>{t(src.error ? "inbox.fix_skill" : "inbox.fix")}</p>
            <p className="inbox-effect">
              {t(src.error ? "inbox.fix_skill_effect" : "inbox.fix_effect")}
            </p>
            <span className="option-recommended">
              {t("question.recommended")}
            </span>
          </div>
          <button
            className="option"
            disabled={busy === src.name}
            onClick={() => void toggle(src)}
          >
            <strong className="option-letter">{letter(1)}</strong>
            <p>{t("inbox.unplug_choice")}</p>
            <p className="inbox-effect">{t("inbox.unplug_effect")}</p>
          </button>
        </div>
      </div>
    </article>
  );
});

// ProjectLine is a project whose configuration has something for you to fix, in one line: its red gear, its name and
// how many things, the first of them in one sentence as Djinn finds it (ProjectService.Show), and the button that opens
// its panel, where each one is said with what settles it.
const ProjectLine = memo(function ProjectLine({
  project,
  onOpenProject,
}: {
  project: Project;
  onOpenProject?: (projectId: string) => void;
}) {
  const clients = useClients();
  const [first, setFirst] = useState("");
  useEffect(() => {
    let live = true;
    clients.projects
      .show({ project: project.id })
      .then(
        (res) =>
          live &&
          setFirst(
            res.notices.find((n) => n.kind === ProjectNoticeKind.TO_FIX)
              ?.text ?? "",
          ),
      )
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [clients, project.id, project.toFix]);
  return (
    <article className="question-card inbox-card inbox-project">
      <span className="inbox-project-gear" aria-hidden="true">
        <Settings size={16} />
        <ProjectFlame />
      </span>
      <div className="inbox-project-text">
        <span className="question-meta">
          <span className="inbox-kind">
            {t("inbox.kind_project", { project: project.name })}
          </span>
          <span className="inbox-project-count">
            {t("project.to_fix", { count: project.toFix })}
          </span>
        </span>
        <p>{first || t("inbox.project_detail")}</p>
      </div>
      {onOpenProject && (
        <button
          className="button small secondary"
          onClick={() => onOpenProject(project.id)}
        >
          {t("inbox.project_open")}
        </button>
      )}
    </article>
  );
});
