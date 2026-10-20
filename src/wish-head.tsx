// The wish's head, around its title: its description, a few lines the person edits in place (a click, saved when the
// field is left), the Lead button, split: Lead resumes the one lead, focused on the wish, the arrow beside it lists the
// agents found on this machine, to start a lead of another one from the brief, and opens their setup (driven by Djinn,
// a chip of the lead's seat instead, its menu moving it); where its work lands; and where Djinn last pushed its integration branches.
import {
  Check,
  ChevronDown,
  CloudUpload,
  GitBranch,
  GitBranchPlus,
  GitMerge,
  GitPullRequestArrow,
  ScrollText,
  Settings2,
  Terminal,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";

import {
  type Account,
  type IntegrationBranch,
  type Lead,
  type LeadBatch,
  LeadBatchState,
  type LeadExit,
  type LeadSeat,
  ProjectPush,
  Provider,
  PushStrategy,
  type Wish,
  type WishMain,
  type WishPush,
} from "../gen/ts/plan/v1/plan_pb";
import type { Provider as Agent } from "../gen/ts/ui/v1/ui_pb";
import {
  defaultModel,
  effectivePushStrategy,
  gitReason,
  shortModel,
  usd,
  when,
} from "./data/format";
import { estimateCost } from "./data/prices";
import { t } from "./i18n";
import { UpdateModelsSection } from "./summoning-circle";

// The agents a lead runs, in the order the menu lists them, by their identifier in UiService.GetEnvironment.
const agents: [string, Provider][] = [
  ["claude", Provider.CLAUDE],
  ["codex", Provider.CODEX],
  ["antigravity", Provider.ANTIGRAVITY],
];

// The lead Djinn drives, for the lead's menu: where it runs, the accounts of this machine it may move to, its
// running or failed batch, and its journal of past batches.
export interface LeadDrive {
  seat?: LeadSeat;
  accounts: Account[];
  batch?: LeadBatch;
  journal?: LeadBatch[];
}

// A seat the lead may move to: its agent, model and account; an empty model is the agent's default, an empty account
// its own sign-in.
export type SeatChoice = { provider: Provider; model: string; account: string };

// agentNamed is the name of an agent the window shows, by its kind.
const agentNamed: Partial<Record<Provider, string>> = {
  [Provider.UNSPECIFIED]: "Claude Code",
  [Provider.CLAUDE]: "Claude Code",
  [Provider.CODEX]: "Codex",
  [Provider.ANTIGRAVITY]: "Antigravity",
  [Provider.FAKE]: "Fake",
};

// accountLabel is what the window calls an account: its label, else its name.
function accountLabel(name: string, accounts: readonly Account[]): string {
  return accounts.find((a) => a.name === name)?.label || name;
}

// seatName names a seat in a few words: its agent, and its account's label after a dot.
export function seatName(
  seat: { provider: Provider; account: string },
  accounts: readonly Account[],
): string {
  const agent = agentNamed[seat.provider] ?? "Claude Code";
  return seat.account
    ? `${agent} · ${accountLabel(seat.account, accounts)}`
    : agent;
}

// seatLabel names where the lead Djinn drives runs, for its chip: the agent, its account, then the model it was set
// to, else the one its last batch ran on that agent; no model when it runs the agent's default.
export function seatLabel(drive: LeadDrive): string {
  const provider = drive.seat?.provider || Provider.CLAUDE;
  const last = drive.batch;
  const model =
    drive.seat?.model ||
    ((last?.provider || Provider.CLAUDE) === provider ? last?.model : "") ||
    "";
  const seat = seatName(
    { provider, account: drive.seat?.account ?? "" },
    drive.accounts,
  );
  return model ? `${seat} · ${shortModel(model)}` : seat;
}

// recordedAgent is the agent of the wish's recorded lead; none without a session, except Antigravity. A lead stored without one ran Claude.
export function recordedAgent(lead?: Lead): Provider | undefined {
  if (lead?.provider === Provider.ANTIGRAVITY) return Provider.ANTIGRAVITY;
  if (!lead?.sessionId) return undefined;
  return lead.provider === Provider.UNSPECIFIED
    ? Provider.CLAUDE
    : lead.provider;
}

// leadTrouble is what the dot beside Lead says, from LeadService.Get: why the lead could not start, or how its program
// ended, until Lead starts it again. Failed unless it ended with code 0. Undefined while it runs, or never ran.
export function leadTrouble(
  exit?: LeadExit,
  startError?: string,
): { text: string; failed: boolean } | undefined {
  if (startError)
    return { text: t("wish.lead_failed", { error: startError }), failed: true };
  if (exit)
    return {
      text: t("wish.lead_ended", { code: exit.code }),
      failed: exit.code !== 0,
    };
  return undefined;
}

export function WishTitle({
  title,
  editing: startEditing = false,
  onSave,
}: {
  title: string;
  editing?: boolean;
  onSave: (title: string) => void;
}) {
  const [editing, setEditing] = useState(startEditing);
  if (!editing)
    return (
      <h1
        className="wish-title"
        role="button"
        tabIndex={0}
        title={t("wish.rename_detail")}
        onClick={() => setEditing(true)}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            event.preventDefault();
            setEditing(true);
          }
        }}
      >
        {title}
      </h1>
    );
  return (
    <input
      type="text"
      className="wish-title-edit"
      aria-label={t("wish.title")}
      defaultValue={title}
      maxLength={500}
      autoFocus
      onBlur={(event) => {
        setEditing(false);
        const text = event.currentTarget.value.trim();
        if (text && text !== title.trim()) onSave(text);
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter") {
          event.currentTarget.blur();
        } else if (event.key === "Escape") {
          // Left as it was: the blur that follows saves nothing.
          event.currentTarget.value = title;
          event.currentTarget.blur();
        }
      }}
    />
  );
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

// batchCost formats a batch's cost: exact when reported, else estimated from its token usage.
export function batchCost(batch: LeadBatch): string {
  if ((batch.usage?.costUsd ?? 0) > 0) return usd(batch.usage!.costUsd);
  const est = estimateCost(batch.usage, batch.model, batch.provider);
  if (est.costUsd > 0) return `≈ ${usd(est.costUsd)}`;
  return usd(0);
}

// LeadBatchStatus shows the running or failed batch beside Lead: since when, events, retry when any, and cost.
export function LeadBatchStatus({ batch }: { batch: LeadBatch }) {
  const failed = batch.state === LeadBatchState.FAILED;
  const since = when(
    failed ? (batch.endTime ?? batch.startTime) : batch.startTime,
  );
  const title = [failed && batch.error ? batch.error : "", ...batch.events]
    .filter(Boolean)
    .join("\n");
  return (
    <span
      className={failed ? "lead-batch failed" : "lead-batch running"}
      title={title || undefined}
    >
      {since && (
        <time className="lead-batch-when">
          {t("inbox.failing_since", { when: since })}
        </time>
      )}
      <span className="lead-batch-events">
        {t("wish.lead_batch_events", { count: batch.events.length })}
      </span>
      {(batch.attempt > 1 || failed) && (
        <span className="lead-batch-retry">
          {failed
            ? batch.attempt > 1
              ? t("wish.lead_batch_retry_failed", { attempt: batch.attempt })
              : t("wish.lead_batch_failed")
            : t("wish.lead_batch_retry", { attempt: batch.attempt })}
        </span>
      )}
      <span className="lead-batch-cost">{batchCost(batch)}</span>
    </span>
  );
}

// LeadJournal lists past batches: when, model and session (plus retry or failed badge), launches, last sentence, cost.
export function LeadJournal({ batches }: { batches: readonly LeadBatch[] }) {
  if (batches.length === 0) return null;
  return (
    <table className="compact-table lead-journal-list">
      <tbody>
        {batches.map((b) => {
          const failed = b.state === LeadBatchState.FAILED;
          const model =
            b.model ||
            defaultModel(b.provider) ||
            agentNamed[b.provider] ||
            "claude";
          return (
            <tr
              key={b.id}
              className={failed ? "lead-batch-row failed" : "lead-batch-row"}
            >
              <td>
                <time>{when(b.endTime ?? b.startTime)}</time>
              </td>
              <td>
                <span className="lead-batch-model">{model}</span>
                {b.sessionId && (
                  <>
                    {" "}
                    <code className="lead-batch-session" title={b.sessionId}>
                      {b.sessionId.slice(0, 8)}
                    </code>
                  </>
                )}
                {b.attempt > 1 && (
                  <>
                    {" "}
                    <span className="lead-batch-retry">
                      {t("wish.lead_batch_retry", { attempt: b.attempt })}
                    </span>
                  </>
                )}
                {failed && (
                  <>
                    {" "}
                    <span className="lead-batch-failed">
                      {t("wish.lead_batch_failed")}
                    </span>
                  </>
                )}
              </td>
              <td className="lead-batch-launches">
                {b.commands.length > 0 ? (
                  b.commands.map((c, i) => <code key={i}>{c}</code>)
                ) : (
                  <span className="muted-text">—</span>
                )}
              </td>
              <td className="lead-batch-sentence">
                {b.note}
                {b.error && (
                  <span className="lead-batch-error">
                    {b.note ? ` (${b.error})` : b.error}
                  </span>
                )}
              </td>
              <td className="lead-batch-cost">{batchCost(b)}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

// LeadButton is Lead at the top of a wish or of the flying carpet. Lead in its terminal: Talk to the Djinn resumes it,
// the arrow beside lists the agents. Driven by Djinn, there is no terminal to open (Decision Q115 B): a chip names the
// seat and its menu moves the lead; a failed batch is retried from the lead's journal on the side (LeadSideLog).
export function LeadButton({
  recorded,
  agents: given,
  open: startOpen = false,
  journalOpen: startJournalOpen = false,
  focused = true,
  trouble,
  loadAgents,
  onLead,
  onPick,
  onSetup,
  drive,
  onMove,
}: {
  recorded?: Provider;
  agents?: Agent[];
  open?: boolean;
  journalOpen?: boolean;
  // On a wish, the lead resumes focused on it; on the flying carpet, on no wish: the tooltips say which.
  focused?: boolean;
  // The lead could not start, or ended: a dot on Lead, its tooltip saying why (leadTrouble). Not shown driven: a
  // failed batch says why on the side.
  trouble?: { text: string; failed: boolean };
  loadAgents: () => Promise<Agent[]>;
  onLead: () => void;
  onPick: (provider: Provider) => void;
  // Opens the setup of the agents: install one, sign it in.
  onSetup?: () => void;
  // While Djinn drives the lead, the seat's chip replaces Lead, and its menu moves it to another agent, model or
  // account (onMove).
  drive?: LeadDrive;
  onMove?: (seat: SeatChoice) => void;
}) {
  const [open, setOpen] = useState(startOpen);
  const [journalShown, setJournalShown] = useState(startJournalOpen);
  const [found, setFound] = useState<Agent[] | undefined>(given);
  const root = useRef<HTMLDivElement>(null);
  const journalRoot = useRef<HTMLDivElement>(null);
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
  useEffect(() => {
    if (!journalShown) return;
    const away = (event: MouseEvent) => {
      if (!journalRoot.current?.contains(event.target as Node))
        setJournalShown(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setJournalShown(false);
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", escape);
    };
  }, [journalShown]);
  const activeBatch =
    drive?.batch &&
    (drive.batch.state === LeadBatchState.RUNNING ||
      drive.batch.state === LeadBatchState.FAILED)
      ? drive.batch
      : undefined;
  const pastBatches = (drive?.journal ?? []).filter(
    (b) => b.state !== LeadBatchState.RUNNING,
  );
  const menu = open && (
    <LeadMenu
      agents={found}
      recorded={recorded}
      focused={focused}
      onPick={(provider) => {
        setOpen(false);
        onPick(provider);
      }}
      onSetup={
        onSetup &&
        (() => {
          setOpen(false);
          onSetup();
        })
      }
      drive={drive}
      onMove={
        onMove &&
        ((seat) => {
          setOpen(false);
          onMove(seat);
        })
      }
    />
  );
  return (
    <>
      {drive ? (
        // Driven, no terminal to open (Decision Q115 B): the seat alone, its menu moves the lead.
        <div className="lead-seat-wrap" ref={root}>
          <button
            className="button secondary small lead-seat"
            aria-label={t("wish.lead_seat", { seat: seatLabel(drive) })}
            title={t("wish.lead_seats_detail")}
            aria-haspopup="menu"
            aria-expanded={open}
            onClick={() => setOpen(!open)}
          >
            <span>{seatLabel(drive)}</span>
            <ChevronDown size={14} />
          </button>
          {menu}
        </div>
      ) : (
        <div className="split-button" ref={root}>
          <button
            className="button secondary small"
            title={
              trouble?.text ??
              (focused ? t("wish.resume_detail") : t("plan.talk_detail"))
            }
            onClick={onLead}
          >
            <Terminal size={14} />
            <span>{t("wish.resume")}</span>
            {trouble && (
              <span
                className={trouble.failed ? "lead-dot failed" : "lead-dot"}
                aria-hidden="true"
              />
            )}
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
          {menu}
        </div>
      )}
      {activeBatch && <LeadBatchStatus batch={activeBatch} />}
      {pastBatches.length > 0 && (
        <div className="lead-journal-wrap" ref={journalRoot}>
          <button
            className="button secondary small lead-journal-button"
            aria-label={t("wish.lead_journal")}
            title={t("wish.lead_journal_detail")}
            aria-expanded={journalShown}
            onClick={() => setJournalShown(!journalShown)}
          >
            <ScrollText size={14} />
            <span>{pastBatches.length}</span>
          </button>
          {journalShown && (
            <div
              className="lead-journal-popover"
              role="dialog"
              aria-label={t("wish.lead_journal")}
            >
              <LeadJournal batches={pastBatches} />
            </div>
          )}
        </div>
      )}
    </>
  );
}

// LeadMenu lists the agents a lead may run, those missing from this machine disabled, saying why; the recorded
// lead's is marked, and picking it resumes that lead, focused on the wish. While Djinn drives the lead, it lists the
// seats instead (SeatItems): one click moves the lead there. Last, the way to set the agents up.
export function LeadMenu({
  agents: found,
  recorded,
  focused = true,
  onPick,
  onSetup,
  drive,
  onMove,
}: {
  agents?: Agent[];
  recorded?: Provider;
  focused?: boolean;
  onPick: (provider: Provider) => void;
  onSetup?: () => void;
  drive?: LeadDrive;
  onMove?: (seat: SeatChoice) => void;
}) {
  if (!found)
    return (
      <div className="lead-menu" role="menu" aria-label={t("wish.lead_agents")}>
        <p className="muted-text">{t("common.loading")}</p>
      </div>
    );
  if (drive && onMove)
    return (
      <div className="lead-menu" role="menu" aria-label={t("wish.lead_agents")}>
        <SeatItems found={found} drive={drive} onMove={onMove} />
        {onSetup && (
          <button role="menuitem" className="lead-agent" onClick={onSetup}>
            <span className="lead-agent-name">
              <Settings2 size={13} aria-hidden="true" />
              {t("agents.title")}
            </span>
          </button>
        )}
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
                  ? focused
                    ? t("wish.lead_recorded")
                    : t("plan.lead_recorded")
                  : t("wish.lead_new")}
            </span>
          </button>
        );
      })}
      {onSetup && (
        <button role="menuitem" className="lead-agent" onClick={onSetup}>
          <span className="lead-agent-name">
            <Settings2 size={13} aria-hidden="true" />
            {t("agents.title")}
          </span>
        </button>
      )}
    </div>
  );
}

// SeatItems are the seats of the lead Djinn drives: each agent on its own sign-in, then on each of its accounts; the
// one it runs on is marked, Antigravity and a missing agent disabled, saying why. A click moves the lead there, its
// model kept on the same agent. Last, the model, for the agent it runs on: Enter moves it to that model.
function SeatItems({
  found,
  drive,
  onMove,
}: {
  found: Agent[];
  drive: LeadDrive;
  onMove: (seat: SeatChoice) => void;
}) {
  const current = drive.seat?.provider || Provider.CLAUDE;
  const [model, setModel] = useState(drive.seat?.model ?? "");
  return (
    <>
      {agents.map(([id, provider]) => {
        const agent = found.find((a) => a.id === id);
        if (!agent) return null;
        const accounts = drive.accounts.filter((a) => a.provider === provider);
        return ["", ...accounts.map((a) => a.name)].map((account) => {
          const here =
            provider === current && account === (drive.seat?.account ?? "");
          const leads = provider !== Provider.ANTIGRAVITY;
          return (
            <button
              key={`${id}/${account}`}
              role="menuitem"
              className={here ? "lead-agent current" : "lead-agent"}
              disabled={!agent.available || !leads}
              aria-current={here || undefined}
              onClick={() =>
                onMove({
                  provider,
                  account,
                  model: provider === current ? (drive.seat?.model ?? "") : "",
                })
              }
            >
              <span className="lead-agent-name">
                {account
                  ? `${agent.name} · ${accountLabel(account, drive.accounts)}`
                  : agent.name}
                {here && <Check size={13} aria-hidden="true" />}
              </span>
              <span className="lead-agent-detail">
                {!agent.available
                  ? t("wish.lead_missing", { command: agent.command })
                  : !leads
                    ? t("wish.lead_cannot_lead")
                    : here
                      ? drive.seat?.model
                        ? t("wish.lead_seat_model", { model: drive.seat.model })
                        : t("wish.lead_seat_current")
                      : t("wish.lead_seat_move")}
              </span>
            </button>
          );
        });
      })}
      <form
        className="lead-agent"
        onSubmit={(event) => {
          event.preventDefault();
          onMove({
            provider: current,
            account: drive.seat?.account ?? "",
            model: model.trim(),
          });
        }}
      >
        <label className="lead-agent-name">
          {t("wish.lead_model")}
          <input
            value={model}
            placeholder={t("wish.lead_model_default")}
            aria-label={t("wish.lead_model")}
            onChange={(event) => setModel(event.target.value)}
          />
        </label>
        <span className="lead-agent-detail">{t("wish.lead_model_detail")}</span>
      </form>
    </>
  );
}

// LastPushes says, for each project of the wish, when Djinn last pushed its integration branch and how many commits it
// brought, their titles on hover; a push the remote refused, until one passes; and a push its checks hold, why on
// hover. Names the project when the wish has several.
export function LastPushes({
  pushes,
  projects,
  codes,
}: {
  pushes: WishPush[];
  projects: { id: string; name: string; push?: ProjectPush }[];
  codes?: ReadonlyMap<string, string>;
}) {
  const projectIds = Array.from(
    new Set([...projects.map((p) => p.id), ...pushes.map((p) => p.projectId)]),
  );
  return (
    <>
      {projectIds.map((id) => {
        const project = projects.find((p) => p.id === id);
        const push = pushes.find((p) => p.projectId === id);
        const onDemand = project?.push === ProjectPush.ON_DEMAND;
        const last = push?.last;
        // Each branch whose push checks are red keeps its hold (W37), its title naming the branch once there are
        // several.
        const holds = Object.entries(push?.holds ?? {}).sort(([a], [b]) =>
          a.localeCompare(b),
        );
        if (!last && !push?.refused && !holds.length && !onDemand) return null;
        const name =
          projects.length > 1 ? (project?.name ?? push?.projectId) : undefined;
        return (
          <span key={id} className="wish-push">
            <CloudUpload size={13} />
            {name && <b>{name}</b>}
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
            {push?.refused && (
              <span className="wish-push-refused" title={push.refused}>
                {t("wish.push_refused")}
              </span>
            )}
            {holds.map(([branch, hold]) => (
              <span
                key={branch}
                className="wish-push-refused"
                title={[
                  holds.length > 1 ? branch : "",
                  hold.held,
                  hold.failure?.command ? `$ ${hold.failure.command}` : "",
                  hold.failure?.output,
                ]
                  .filter(Boolean)
                  .join("\n\n")}
              >
                {hold.correctedBy
                  ? t("wish.push_held_corrected", {
                      task: codes?.get(hold.correctedBy) ?? hold.correctedBy,
                    })
                  : t("wish.push_held")}
              </span>
            ))}
            {onDemand && (
              <span className="wish-push-on-demand">
                {t("wish.push_on_demand")}
              </span>
            )}
          </span>
        );
      })}
    </>
  );
}

// shortReason returns the first line of s, trimmed of whitespace and trailing colon, for short display in the head.
export function shortReason(s: string): string {
  const line = s.split("\n")[0]?.trim() ?? "";
  return line.replace(/:$/, "");
}

// MainMerges says, for each project of the wish, the last time Djinn merged the project's main branch into the wish's
// integration branch, how many commits and which release it brought, their titles on hover; and why the next merge
// waits, visible without hovering, the full reason on hover. Names the project when the wish has several.
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
              <span className="wish-push-refused" title={gitReason(main.held)}>
                {t("wish.main_held", {
                  reason: shortReason(gitReason(main.held)),
                })}
              </span>
            )}
          </span>
        );
      })}
    </>
  );
}

// PushStrategySelector lets the developer see and change the push strategy of a wish.
export function PushStrategySelector({
  wish,
  projects,
  onChange,
  disabled,
}: {
  wish?: Wish;
  projects?: { pushStrategy?: PushStrategy }[];
  onChange: (strategy: PushStrategy) => void;
  disabled?: boolean;
}) {
  const current = effectivePushStrategy(wish, projects);
  return (
    <span className="wish-push-strategy">
      <GitBranch size={13} />
      <div
        className="segmented-switch"
        role="group"
        aria-label={t("wish.strategy_label")}
      >
        <button
          type="button"
          className={current !== PushStrategy.AZIMA ? "active" : ""}
          title={t("wish.strategy_wish_detail")}
          onClick={() => onChange(PushStrategy.WISH)}
          disabled={disabled}
        >
          {t("wish.strategy_wish")}
        </button>
        <button
          type="button"
          className={current === PushStrategy.AZIMA ? "active" : ""}
          title={t("wish.strategy_azima_detail")}
          onClick={() => onChange(PushStrategy.AZIMA)}
          disabled={disabled}
        >
          {t("wish.strategy_azima")}
        </button>
      </div>
    </span>
  );
}

// The most branches the picker lists at once: typing narrows them.
const shownBranches = 8;

// WhereWorkLands says, in the wish's head, where its work lands. Per wish, a chip for each of its projects in Git: the
// integration branch Djinn merges its finished work into, the project named when the wish has several; a click opens
// the picker that changes it. Per azima, one chip: each azima lands in main from a branch of its own.
export function WhereWorkLands({
  branches,
  projects,
  perAzima,
  open: startOpen = "",
  loadBranches,
  onChange,
}: {
  branches: IntegrationBranch[];
  projects: { id: string; name: string; git?: boolean }[];
  perAzima: boolean;
  // The project whose picker shows at first.
  open?: string;
  loadBranches: (
    projectId: string,
  ) => Promise<{ branches: string[]; reference: string }>;
  onChange: (
    projectId: string,
    branch: string,
    create: boolean,
  ) => Promise<unknown>;
}) {
  const [open, setOpen] = useState(startOpen);
  const branchOf = (id: string) =>
    branches.find((b) => b.projectId.toLowerCase() === id.toLowerCase())
      ?.branch ?? "";
  if (perAzima) {
    const rest = projects.map((p) => branchOf(p.id)).filter(Boolean);
    return (
      <span
        className="wish-lands"
        title={[
          t("wish.lands_per_azima_detail"),
          rest.length > 0 &&
            t("wish.lands_rest", { branches: rest.join(", ") }),
        ]
          .filter(Boolean)
          .join("\n")}
      >
        <GitPullRequestArrow size={13} />
        {t("wish.lands_per_azima")}
      </span>
    );
  }
  return (
    <>
      {projects
        .filter((p) => p.git !== false || branchOf(p.id))
        .map((project) => {
          const branch = branchOf(project.id);
          const name = projects.length > 1 ? project.name : undefined;
          return (
            <span key={project.id} className="wish-lands-chip">
              <button
                type="button"
                className="wish-lands"
                aria-haspopup="dialog"
                aria-expanded={open === project.id}
                title={
                  branch
                    ? t("wish.lands_detail", { branch })
                    : t("wish.lands_unset_detail")
                }
                onClick={() => setOpen(open === project.id ? "" : project.id)}
              >
                <GitPullRequestArrow size={13} />
                {name && <b>{name}</b>}
                {branch ? (
                  <span>
                    {t("wish.lands_in")} <code>{branch}</code>
                  </span>
                ) : (
                  <span>{t("wish.lands_unset")}</span>
                )}
                <ChevronDown size={12} />
              </button>
              {open === project.id && (
                <BranchPicker
                  current={branch}
                  project={name}
                  load={() => loadBranches(project.id)}
                  onClose={() => setOpen("")}
                  onPick={(to, create) =>
                    onChange(project.id, to, create).then(() => setOpen(""))
                  }
                />
              )}
            </span>
          );
        })}
    </>
  );
}

// BranchPicker lists the project's local branches, the latest first, narrowed by what is typed, the current one
// marked; a name no branch has makes a new branch from the current one. A click elsewhere, or Escape, closes it.
export function BranchPicker({
  current,
  project,
  branches: given,
  load,
  onClose,
  onPick,
}: {
  current: string;
  project?: string;
  branches?: string[];
  load: () => Promise<{ branches: string[]; reference: string }>;
  onClose: () => void;
  onPick: (branch: string, create: boolean) => Promise<unknown>;
}) {
  const [found, setFound] = useState<string[] | undefined>(given);
  const [reference, setReference] = useState("");
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let live = true;
    load()
      .then((res) => {
        if (!live) return;
        setFound(res.branches);
        setReference(res.reference);
      })
      .catch(() => live && setFound([]));
    const away = (event: MouseEvent) => {
      if (!root.current?.parentElement?.contains(event.target as Node))
        onClose();
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", escape);
    return () => {
      live = false;
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", escape);
    };
    // Opening the picker loads the branches once; new callbacks, at each render of the head, do not.
    // eslint-disable-next-line react-hooks/exhaustive-deps, @eslint-react/exhaustive-deps -- load and onClose left out, see above
  }, []);
  const name = typed.trim();
  const all = found ?? [];
  const matching = all.filter((b) =>
    b.toLowerCase().includes(name.toLowerCase()),
  );
  const exists = all.some((b) => b.toLowerCase() === name.toLowerCase());
  // A new branch starts from the current one; for a wish that records none, from the project's reference branch, never
  // from the branch the developer's copy has checked out.
  const from = current || reference;
  const pick = (branch: string, create: boolean) => {
    if (busy) return;
    setBusy(true);
    onPick(branch, create)
      .catch(() => undefined)
      .finally(() => setBusy(false));
  };
  return (
    <div
      className="branch-picker"
      role="dialog"
      aria-label={
        project
          ? t("wish.lands_pick_project", { project })
          : t("wish.lands_pick")
      }
      ref={root}
    >
      <input
        type="text"
        aria-label={t("wish.lands_branch")}
        placeholder={t("wish.lands_placeholder")}
        value={typed}
        maxLength={200}
        autoFocus
        onChange={(event) => setTyped(event.currentTarget.value)}
        onKeyDown={(event) => {
          if (event.key !== "Enter" || !name) return;
          event.preventDefault();
          if (exists)
            pick(
              all.find((b) => b.toLowerCase() === name.toLowerCase())!,
              false,
            );
          else if (from) pick(name, true);
        }}
      />
      {!found && <p className="muted-text">{t("common.loading")}</p>}
      {matching.slice(0, shownBranches).map((branch) => {
        const here = branch.toLowerCase() === current.toLowerCase();
        return (
          <button
            key={branch}
            type="button"
            className={here ? "branch-option current" : "branch-option"}
            aria-current={here || undefined}
            disabled={busy}
            onClick={() => !here && pick(branch, false)}
          >
            <code>{branch}</code>
            {here && <Check size={13} aria-hidden="true" />}
          </button>
        );
      })}
      {found && matching.length > shownBranches && (
        <p className="muted-text">
          {t("wish.lands_more", { count: matching.length - shownBranches })}
        </p>
      )}
      {found && name && !exists && from && (
        <button
          type="button"
          className="branch-option new"
          disabled={busy}
          onClick={() => pick(name, true)}
        >
          <GitBranchPlus size={13} aria-hidden="true" />
          <span>{t("wish.lands_new", { branch: name, from })}</span>
        </button>
      )}
    </div>
  );
}

// WishModels lets the developer preview and change provider and model for open tasks in a wish.
export function WishModels({ wishId }: { wishId: string }) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (!open) return;
    const away = (event: MouseEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", escape);
    };
  }, [open]);
  return (
    <span className="wish-lands-chip wish-models-chip" ref={root}>
      <button
        type="button"
        className="wish-models"
        aria-haspopup="dialog"
        aria-expanded={open}
        title={t("wish.models_button_detail")}
        onClick={() => setOpen(!open)}
      >
        <Settings2 size={13} aria-hidden="true" />
        <span>{t("wish.models_button")}</span>
        <ChevronDown size={12} />
      </button>
      {open && (
        <div
          className="branch-picker wish-models-picker"
          role="dialog"
          aria-label={t("summoning.bulk_title")}
        >
          <UpdateModelsSection wishId={wishId} />
        </div>
      )}
    </span>
  );
}
