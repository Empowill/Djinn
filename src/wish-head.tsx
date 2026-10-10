// The wish's head, around its title: its description, a few lines the person edits in place (a click, saved when the
// field is left), the Lead button, split: Lead resumes the recorded lead, the arrow beside it lists the agents found on
// this machine, to start a lead of another one from the brief; and where Djinn last pushed its integration branches.
import {
  Check,
  ChevronDown,
  CloudUpload,
  GitMerge,
  Terminal,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";

import {
  type Lead,
  Provider,
  type WishMain,
  type WishPush,
} from "../gen/ts/plan/v1/plan_pb";
import type { Provider as Agent } from "../gen/ts/ui/v1/ui_pb";
import { when } from "./data/format";
import { t } from "./i18n";

// The agents a lead runs, in the order the menu lists them, by their identifier in UiService.GetEnvironment.
const agents: [string, Provider][] = [
  ["claude", Provider.CLAUDE],
  ["codex", Provider.CODEX],
  ["antigravity", Provider.ANTIGRAVITY],
];

// recordedAgent is the agent of the wish's recorded lead; none without a session. A lead stored without one ran Claude.
export function recordedAgent(lead?: Lead): Provider | undefined {
  if (!lead?.sessionId) return undefined;
  return lead.provider === Provider.UNSPECIFIED
    ? Provider.CLAUDE
    : lead.provider;
}

export function WishDescription({
  title,
  description,
  editing: startEditing = false,
  onSave,
}: {
  title: string;
  description: string;
  editing?: boolean;
  onSave: (text: string) => void;
}) {
  const [editing, setEditing] = useState(startEditing);
  // Until someone writes one, the title stands for the description.
  const shown = description || title;
  if (!editing)
    return (
      <p
        className="wish-description"
        role="button"
        tabIndex={0}
        title={t("wish.describe_detail")}
        onClick={() => setEditing(true)}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            event.preventDefault();
            setEditing(true);
          }
        }}
      >
        {shown}
      </p>
    );
  return (
    <textarea
      className="wish-description-edit"
      aria-label={t("wish.description")}
      defaultValue={shown}
      rows={Math.max(2, shown.split("\n").length)}
      autoFocus
      onBlur={(event) => {
        setEditing(false);
        const text = event.currentTarget.value.trim();
        if (text !== shown.trim()) onSave(text);
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          // Left as it was: the blur that follows saves nothing.
          event.currentTarget.value = shown;
          event.currentTarget.blur();
        }
      }}
    />
  );
}

export function LeadButton({
  recorded,
  agents: given,
  open: startOpen = false,
  loadAgents,
  onLead,
  onPick,
}: {
  recorded?: Provider;
  agents?: Agent[];
  open?: boolean;
  loadAgents: () => Promise<Agent[]>;
  onLead: () => void;
  onPick: (provider: Provider) => void;
}) {
  const [open, setOpen] = useState(startOpen);
  const [found, setFound] = useState<Agent[] | undefined>(given);
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    let live = true;
    loadAgents()
      .then((list) => live && setFound(list))
      .catch(() => undefined);
    // A click elsewhere, or Escape, closes the menu.
    const away = (event: MouseEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", escape);
    return () => {
      live = false;
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", escape);
    };
    // Opening the menu loads the agents once; a new loadAgents, at each render of the head, does not.
    // eslint-disable-next-line react-hooks/exhaustive-deps, @eslint-react/exhaustive-deps -- loadAgents left out, see above
  }, [open]);
  return (
    <div className="split-button" ref={root}>
      <button
        className="button secondary small"
        title={t("wish.resume_detail")}
        onClick={onLead}
      >
        <Terminal size={14} />
        <span>{t("wish.resume")}</span>
      </button>
      <button
        className="button secondary small split-arrow"
        aria-label={t("wish.lead_agents")}
        title={t("wish.lead_agents_detail")}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <ChevronDown size={14} />
      </button>
      {open && (
        <LeadMenu
          agents={found}
          recorded={recorded}
          onPick={(provider) => {
            setOpen(false);
            onPick(provider);
          }}
        />
      )}
    </div>
  );
}

// LeadMenu lists the agents a lead may run, those missing from this machine disabled, saying why; the recorded
// lead's is marked, and picking it resumes its session.
export function LeadMenu({
  agents: found,
  recorded,
  onPick,
}: {
  agents?: Agent[];
  recorded?: Provider;
  onPick: (provider: Provider) => void;
}) {
  if (!found)
    return (
      <div className="lead-menu" role="menu" aria-label={t("wish.lead_agents")}>
        <p className="muted-text">{t("common.loading")}</p>
      </div>
    );
  return (
    <div className="lead-menu" role="menu" aria-label={t("wish.lead_agents")}>
      {agents.map(([id, provider]) => {
        const agent = found.find((a) => a.id === id);
        if (!agent) return null;
        const current = provider === recorded;
        return (
          <button
            key={id}
            role="menuitem"
            className={current ? "lead-agent current" : "lead-agent"}
            disabled={!agent.available}
            aria-current={current || undefined}
            onClick={() => onPick(provider)}
          >
            <span className="lead-agent-name">
              {agent.name}
              {current && <Check size={13} aria-hidden="true" />}
            </span>
            <span className="lead-agent-detail">
              {!agent.available
                ? t("wish.lead_missing", { command: agent.command })
                : current
                  ? t("wish.lead_recorded")
                  : t("wish.lead_new")}
            </span>
          </button>
        );
      })}
    </div>
  );
}

// LastPushes says, for each project of the wish, when Djinn last pushed its integration branch and how many commits it
// brought, their titles on hover; a push the remote refused, until one passes; and a push its checks hold, why on
// hover. Names the project when the wish has several.
export function LastPushes({
  pushes,
  projects,
}: {
  pushes: WishPush[];
  projects: { id: string; name: string }[];
}) {
  return (
    <>
      {pushes.map((push) => {
        const last = push.last;
        if (!last && !push.refused && !push.held) return null;
        const project =
          projects.length > 1
            ? projects.find((p) => p.id === push.projectId)?.name
            : undefined;
        return (
          <span key={push.projectId} className="wish-push">
            <CloudUpload size={13} />
            {project && <b>{project}</b>}
            {last && (
              <span title={last.commits.join("\n")}>
                {t("wish.pushed", {
                  count: last.count,
                  branch: last.branch,
                  remote: last.remote,
                  when: when(last.pushTime),
                })}
              </span>
            )}
            {push.refused && (
              <span className="wish-push-refused" title={push.refused}>
                {t("wish.push_refused")}
              </span>
            )}
            {push.held && (
              <span className="wish-push-refused" title={push.held}>
                {t("wish.push_held")}
              </span>
            )}
          </span>
        );
      })}
    </>
  );
}

// MainMerges says, for each project of the wish, the last time Djinn merged the project's main branch into the wish's
// integration branch, how many commits and which release it brought, their titles on hover; and why the next merge
// waits, on hover, while it does. Names the project when the wish has several.
export function MainMerges({
  mains,
  projects,
}: {
  mains: WishMain[];
  projects: { id: string; name: string }[];
}) {
  return (
    <>
      {mains.map((main) => {
        const last = main.last;
        if (!last && !main.held) return null;
        const project =
          projects.length > 1
            ? projects.find((p) => p.id === main.projectId)?.name
            : undefined;
        return (
          <span key={main.projectId} className="wish-push">
            <GitMerge size={13} />
            {project && <b>{project}</b>}
            {last && (
              <span title={last.commits.join("\n")}>
                {t(
                  last.release
                    ? "wish.main_merged_release"
                    : "wish.main_merged",
                  {
                    count: last.count,
                    main: last.main,
                    branch: last.branch,
                    release: last.release,
                    when: when(last.mergeTime),
                  },
                )}
              </span>
            )}
            {main.held && (
              <span className="wish-push-refused" title={main.held}>
                {t("wish.main_held")}
              </span>
            )}
          </span>
        );
      })}
    </>
  );
}
