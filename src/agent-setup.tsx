// The setup of the agents: one line per agent, where it stands, and one button for its next step, which says what it
// does on hover. Install and sign in run the agent's own command in a terminal of the panel, so nobody opens a
// terminal of theirs; once the command ends, Djinn checks again. Shown in the settings, from the lead's menu, and by
// itself when the window opens with no agent ready. Under each agent, its accounts: a sign-in each in a folder of the
// agent's own, signed in once by the agent's own command, for the workers to run on and the lead to move to; Djinn
// keeps no token, and shows none. Each sign-in, the agent's own or an account, pauses and resumes from its line, as
// djinn lead pause-account does. Under each sign-in, its usage as djinn lead accounts says it: active, paused or
// limited, its week's gauge and when the week ends, its 5 hours, and whether the next task starts there.
import {
  Download,
  LogIn,
  Pause,
  Play,
  Plus,
  RefreshCw,
  Square,
  X,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import {
  type AccountUsage,
  Provider,
  UsageSource,
  type UsageWindow,
} from "../gen/ts/plan/v1/plan_pb";
import {
  type AccountSetup,
  type Provider as AgentEnvironment,
  ProviderState,
  type UiServiceGetEnvironmentResponse,
} from "../gen/ts/ui/v1/ui_pb";
import { message } from "./data/client";
import { useDjinn } from "./data/djinn";
import { ModalFrame } from "./frame";
import { type TextKey, language, t } from "./i18n";
import { type TerminalStatus, fitSafely, mountTerminal } from "./lead-terminal";
import { keepLive } from "./stillness";

// USAGE_EVERY is how often the open panel reads the sign-ins' usage again: Djinn's probe reads each every 5 minutes,
// and a worker at each of its turns.
const USAGE_EVERY = 60_000;

// needsSetup tells an agent that cannot run as it is: not installed, or not signed in. One whose sign-in is unknown
// may well be ready: it is not held back.
export function needsSetup(agent: AgentEnvironment): boolean {
  return (
    agent.state === ProviderState.MISSING ||
    agent.state === ProviderState.SIGNED_OUT
  );
}

export interface Agents {
  // What the last check found; undefined until one answers.
  env?: UiServiceGetEnvironmentResponse;
  checking: boolean;
  error: string;
  check: () => void;
  // setPaused shows a pause Djinn kept, without checking the agents again: of the account called account, or with ""
  // of the agent's own sign-in.
  setPaused: (agentId: string, account: string, paused: boolean) => void;
  // The usage of each sign-in Djinn read or tried to (LeadService.Accounts); undefined until it answers.
  usage?: readonly AccountUsage[];
}

// withPaused is env with the pause of a sign-in changed: the account called account of the agent agentId, or with ""
// the agent's own.
export function withPaused(
  env: UiServiceGetEnvironmentResponse,
  agentId: string,
  account: string,
  paused: boolean,
): UiServiceGetEnvironmentResponse {
  return {
    ...env,
    providers: env.providers.map((agent) =>
      agent.id !== agentId
        ? agent
        : account
          ? {
              ...agent,
              accounts: agent.accounts.map((setup) =>
                setup.account?.name === account
                  ? { ...setup, account: { ...setup.account, paused } }
                  : setup,
              ),
            }
          : { ...agent, paused },
    ),
  };
}

// useAgents checks the agents once mounted, and again on check. A check runs each agent's status command: it takes
// up to a few seconds. The sign-ins' usage is read with each check, after each pause, and every USAGE_EVERY while
// mounted: a read of what Djinn keeps, which costs no model turn.
export function useAgents(): Agents {
  const clients = useDjinn()?.clients;
  const [env, setEnv] = useState<UiServiceGetEnvironmentResponse>();
  const [usage, setUsage] = useState<readonly AccountUsage[]>();
  const [checking, setChecking] = useState(true);
  const [error, setError] = useState("");
  const asked = useRef(0);
  const read = useRef(0);
  const readUsage = useCallback(() => {
    if (!clients) return;
    const n = ++read.current;
    clients.lead
      .accounts({})
      .then((res) => n === read.current && setUsage(res.usage))
      .catch(() => undefined); // The gauges keep the last reading; the next one comes within USAGE_EVERY.
  }, [clients]);
  const check = useCallback(() => {
    if (!clients) return;
    const n = ++asked.current;
    setChecking(true);
    setError("");
    clients.ui
      .getEnvironment({ agents: true })
      .then((res) => n === asked.current && setEnv(res))
      .catch((err) => n === asked.current && setError(message(err)))
      .finally(() => n === asked.current && setChecking(false));
    readUsage();
  }, [clients, readUsage]);
  const cancelPending = useCallback(() => {
    asked.current++; // Unmounted: what comes back is dropped.
    read.current++;
  }, []);
  useEffect(() => {
    check();
    return cancelPending;
  }, [cancelPending, check]);
  useEffect(() => keepLive(readUsage, USAGE_EVERY), [readUsage]);
  const setPaused = useCallback(
    (agentId: string, account: string, paused: boolean) => {
      setEnv((env) => env && withPaused(env, agentId, account, paused));
      readUsage(); // The next task's sign-in may change.
    },
    [readUsage],
  );
  return { env, checking, error, check, setPaused, usage };
}

// usageOf is what Djinn last read of a sign-in's usage: the account called account of the agent agentId, or with ""
// the agent's own; undefined when it read nothing there.
export function usageOf(
  usage: readonly AccountUsage[] | undefined,
  agentId: string,
  account: string,
): AccountUsage | undefined {
  const provider = agentProviders[agentId];
  return usage?.find((u) => u.provider === provider && u.account === account);
}

// quotaTold names the agents whose quota Djinn reads: Claude Code's probe (get_usage) and its workers' streams. The
// others' sign-ins show their state alone.
const quotaTold = new Set(["claude"]);

// agentProviders are the providers of the agents, by their identifier.
const agentProviders: Record<string, Provider> = {
  claude: Provider.CLAUDE,
  codex: Provider.CODEX,
  antigravity: Provider.ANTIGRAVITY,
};

// agentStates names where an agent stands, and the dot that shows it.
const agentStates: Record<ProviderState, { label: TextKey; dot: string }> = {
  [ProviderState.UNSPECIFIED]: {
    label: "settings.agent_unknown",
    dot: "neutral",
  },
  [ProviderState.MISSING]: { label: "settings.agent_missing", dot: "neutral" },
  [ProviderState.SIGNED_OUT]: {
    label: "settings.agent_signed_out",
    dot: "blocked",
  },
  [ProviderState.READY]: { label: "settings.agent_ready", dot: "green" },
  [ProviderState.UNKNOWN]: { label: "settings.agent_unknown", dot: "blocked" },
};

// A step that runs: an agent's install or sign-in command, in the panel's terminal.
interface Step {
  agentId: string;
  kind: "install" | "login";
  line: string;
  name: string; // The terminal's: new for each run, so a run never attaches to the one before.
}

// nextStep is the step that takes an agent further, if any: install it, or sign it in. A sign-in Djinn cannot check
// stays offered, never pushed.
function nextStep(
  agent: AgentEnvironment,
): { kind: Step["kind"]; line: string; hint: TextKey } | undefined {
  if (agent.state === ProviderState.MISSING && agent.installCommand)
    return {
      kind: "install",
      line: agent.installCommand,
      hint: "agents.next_install",
    };
  if (agent.state === ProviderState.SIGNED_OUT && agent.loginCommand)
    return {
      kind: "login",
      line: agent.loginCommand,
      hint: "agents.next_login",
    };
  if (agent.state === ProviderState.UNKNOWN && agent.loginCommand)
    return {
      kind: "login",
      line: agent.loginCommand,
      hint: "agents.next_unknown",
    };
  return undefined;
}

// AgentSetupPanel lists the agents, one line each with its next step; that step runs below its line.
export function AgentSetupPanel({ agents }: { agents: Agents }) {
  const { env, checking, error, check, setPaused, usage } = agents;
  const [step, setStep] = useState<Step>();
  const [running, setRunning] = useState(false);
  const run = (agentId: string, kind: Step["kind"], line: string) => {
    setRunning(true);
    setStep({
      agentId,
      kind,
      line,
      name: `setup-${agentId}-${Date.now().toString(36)}`,
    });
  };
  const ended = useCallback(() => {
    setRunning(false);
    check();
  }, [check]);
  const none = env !== undefined && env.providers.every(needsSetup);
  return (
    <div className="agent-setup">
      {none && <p className="agent-setup-intro">{t("agents.intro_none")}</p>}
      {error && <p role="alert">{error}</p>}
      <div className="provider-settings">
        {env === undefined && (
          <div className="provider-setting">
            <span className="muted-text">{t("settings.agent_checking")}</span>
          </div>
        )}
        {env?.providers.map((agent) => (
          <AgentRow
            key={agent.id}
            agent={agent}
            checking={checking}
            busy={running}
            step={step}
            onRun={run}
            onEnded={ended}
            onChanged={check}
            onPaused={setPaused}
            usage={usage}
          />
        ))}
        {env && <AddAccount onAdded={check} />}
      </div>
      <button
        type="button"
        className="button secondary small agent-check"
        title={t("agents.check_detail")}
        disabled={running || checking}
        onClick={check}
      >
        <RefreshCw size={13} />
        {checking ? t("agents.checking") : t("agents.check")}
      </button>
    </div>
  );
}

function AgentRow({
  agent,
  checking,
  busy,
  step,
  onRun,
  onEnded,
  onChanged,
  onPaused,
  usage,
}: {
  agent: AgentEnvironment;
  checking: boolean;
  busy: boolean;
  step?: Step;
  onRun: (agentId: string, kind: Step["kind"], line: string) => void;
  onEnded: () => void;
  onChanged: () => void;
  onPaused: Agents["setPaused"];
  usage: Agents["usage"];
}) {
  const [error, setError] = useState("");
  const state = agentStates[agent.state] ?? agentStates[ProviderState.UNKNOWN];
  const next = nextStep(agent);
  return (
    <>
      <div
        className="provider-setting"
        data-agent={agent.id}
        title={agent.available ? agent.command : undefined}
      >
        <span className="provider-glyph">
          {agent.id === "codex" ? "⬡" : agent.id === "antigravity" ? "◆" : "✳"}
        </span>
        <div>
          <h3>{agent.name}</h3>
          {agent.version && <span className="muted-text">{agent.version}</span>}
        </div>
        <span className="status-text">
          <span className={`status-dot ${state.dot}`} />
          {t(state.label)}
        </span>
        {next && (
          <button
            type="button"
            className={`button small ${agent.state === ProviderState.UNKNOWN ? "secondary" : "accent"}`}
            title={`${t(next.hint)}\n${next.line}`}
            disabled={busy || checking}
            onClick={() => onRun(agent.id, next.kind, next.line)}
          >
            {next.kind === "install" ? (
              <Download size={13} />
            ) : (
              <LogIn size={13} />
            )}
            {next.kind === "install" ? t("agents.install") : t("agents.login")}
          </button>
        )}
        {(agent.available || agent.paused) && (
          <PauseButton
            agentId={agent.id}
            account=""
            paused={agent.paused}
            onPaused={onPaused}
            onError={setError}
          />
        )}
      </div>
      {(agent.available || agent.paused) && (
        <SignInUsage
          id={agent.id}
          paused={agent.paused}
          signedIn={agent.state === ProviderState.READY}
          withAccounts={agent.accounts.length > 0}
          reads={quotaTold.has(agent.id)}
          usage={usageOf(usage, agent.id, "")}
        />
      )}
      {error && <p role="alert">{error}</p>}
      {step?.agentId === agent.id && (
        <StepTerminal key={step.name} step={step} onEnded={onEnded} />
      )}
      {agent.accounts.map((setup) => (
        <AccountRow
          key={setup.account?.name}
          agent={agent}
          setup={setup}
          busy={busy || checking}
          step={step}
          onRun={onRun}
          onEnded={onEnded}
          onChanged={onChanged}
          onPaused={onPaused}
          usage={usageOf(usage, agent.id, setup.account?.name ?? "")}
        />
      ))}
    </>
  );
}

// AccountRow is an account of an agent: its label and folder, where it stands, its sign-in in its own folder, its
// pause, a button that forgets it, its folder left as it is, and below its usage.
function AccountRow({
  agent,
  setup,
  busy,
  step,
  onRun,
  onEnded,
  onChanged,
  onPaused,
  usage,
}: {
  agent: AgentEnvironment;
  setup: AccountSetup;
  busy: boolean;
  step?: Step;
  onRun: (agentId: string, kind: Step["kind"], line: string) => void;
  onEnded: () => void;
  onChanged: () => void;
  onPaused: Agents["setPaused"];
  usage?: AccountUsage;
}) {
  const clients = useDjinn()?.clients;
  const [error, setError] = useState("");
  const account = setup.account;
  if (!account) return null;
  const id = `${agent.id}/${account.name}`;
  const state = agentStates[setup.state] ?? agentStates[ProviderState.UNKNOWN];
  const signIn =
    agent.available &&
    setup.state !== ProviderState.READY &&
    Boolean(setup.loginCommand);
  const forget = () => {
    setError("");
    clients?.lead
      .removeAccount({ name: account.name })
      .then(onChanged)
      .catch((err) => setError(message(err)));
  };
  return (
    <>
      <div
        className="provider-setting provider-account"
        data-account={account.name}
        title={account.configDirectory}
      >
        <span className="provider-glyph">↳</span>
        <div>
          <h3>{account.label || account.name}</h3>
          <code>{account.configDirectory}</code>
        </div>
        <span className="status-text">
          <span className={`status-dot ${state.dot}`} />
          {t(state.label)}
        </span>
        {signIn && (
          <button
            type="button"
            className="button small accent"
            title={`${t("agents.account_login_detail")}\n${setup.loginCommand}`}
            disabled={busy}
            onClick={() => onRun(id, "login", setup.loginCommand)}
          >
            <LogIn size={13} />
            {t("agents.login")}
          </button>
        )}
        <PauseButton
          agentId={agent.id}
          account={account.name}
          paused={account.paused}
          onPaused={onPaused}
          onError={setError}
        />
        <button
          type="button"
          className="button secondary small"
          title={t("agents.account_forget_detail")}
          aria-label={t("agents.account_forget")}
          disabled={busy}
          onClick={forget}
        >
          <X size={13} />
        </button>
      </div>
      <SignInUsage
        id={id}
        paused={account.paused}
        signedIn={setup.state === ProviderState.READY}
        withAccounts
        reads={quotaTold.has(agent.id)}
        usage={usage}
      />
      {error && <p role="alert">{error}</p>}
      {step?.agentId === id && (
        <StepTerminal key={step.name} step={step} onEnded={onEnded} />
      )}
    </>
  );
}

// SignInUsage is the line under a sign-in's: its state, and what Djinn last read of its quota. The state is paused
// (no worker starts there), limited until a reset (a worker hit the limit of the whole account), or active (signed in,
// workers may start there). Then the week's gauge, how much of it is used and when it ends, the 5 hours used, and
// whether the next task of the agent starts there; hovered, when and where Djinn read them. Shown for a sign-in paused
// or with a reading, and for every sign-in of an agent with accounts, where the choice between them shows. reads
// tells an agent whose quota Djinn reads: only its sign-ins say their quota is not read yet.
function SignInUsage({
  id,
  paused,
  signedIn,
  withAccounts,
  reads,
  usage,
}: {
  id: string;
  paused: boolean;
  signedIn: boolean;
  withAccounts: boolean;
  reads: boolean;
  usage?: AccountUsage;
}) {
  if (!paused && !usage && !withAccounts) return null;
  const limited = usage?.limitedUntil && timestampDate(usage.limitedUntil);
  const state = paused
    ? { dot: "neutral", label: t("agents.paused"), state: "paused" }
    : limited
      ? {
          dot: "blocked",
          label: t("agents.state_limited", { time: when(limited) }),
          state: "limited",
        }
      : signedIn
        ? { dot: "green", label: t("agents.state_active"), state: "active" }
        : undefined;
  const week = usage?.sevenDay;
  const fiveHour = usage?.fiveHour;
  const read = usage?.readTime && timestampDate(usage.readTime);
  return (
    <div
      className="sign-in-usage"
      data-usage={id}
      data-state={state?.state}
      title={
        read
          ? t(
              usage?.source === UsageSource.PROBE
                ? "agents.usage_read_probe"
                : "agents.usage_read_worker",
              { time: clockOf(read) },
            )
          : undefined
      }
    >
      {state && (
        <span className="status-text">
          <span className={`status-dot ${state.dot}`} />
          {state.label}
        </span>
      )}
      {week && <WeekGauge week={week} />}
      {fiveHour && (
        <span className="muted-text">
          {t("agents.five_hour", { percent: percent(fiveHour) })}
        </span>
      )}
      {!week && !fiveHour && (reads || usage) && (
        <span className="muted-text" title={usage?.probeError || undefined}>
          {usage?.probeError
            ? t("agents.usage_failed")
            : t("agents.usage_unread")}
        </span>
      )}
      {usage?.next && (
        <span
          className="badge sign-in-next"
          title={t("agents.usage_next_detail")}
        >
          {t("agents.usage_next")}
        </span>
      )}
    </div>
  );
}

// WeekGauge is a sign-in's week: a gauge of the share used, the share in words, and when the week ends. Full past
// 95 %, where Djinn starts no new task there.
function WeekGauge({ week }: { week: UsageWindow }) {
  const used = percent(week);
  const ends = week.resetsTime && timestampDate(week.resetsTime);
  return (
    <span className="usage-week">
      <span className="muted-text">{t("agents.week")}</span>
      <span
        className={`usage-gauge${week.usedPercent >= 95 ? " full" : ""}`}
        role="meter"
        aria-label={t("agents.week_used")}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={used}
      >
        <span style={{ width: `${Math.min(100, Math.max(0, used))}%` }} />
      </span>
      <span className="usage-percent">{used} %</span>
      {ends && (
        <span className="muted-text">
          {t("agents.week_ends", { time: when(ends) })}
        </span>
      )}
    </span>
  );
}

// percent is a window's share used, in whole percent.
function percent(window: UsageWindow): number {
  return Math.round(window.usedPercent);
}

// timestampDate is a proto timestamp as a date.
function timestampDate(ts: { seconds: bigint; nanos: number }): Date {
  return new Date(Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1e6));
}

// when is a date within the week to come, in the window's language: its day, its date and its time.
function when(date: Date): string {
  return date.toLocaleString(language, {
    weekday: "short",
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// clockOf is a date's hour and minute, in the window's language.
function clockOf(date: Date): string {
  return date.toLocaleTimeString(language, {
    hour: "2-digit",
    minute: "2-digit",
  });
}

// PauseButton pauses or resumes a sign-in of the agent agentId: the account called account, or with "" the agent's
// own, the way djinn lead pause-account and resume-account do. Djinn keeps it in its settings; the line shows it at
// once.
function PauseButton({
  agentId,
  account,
  paused,
  onPaused,
  onError,
}: {
  agentId: string;
  account: string;
  paused: boolean;
  onPaused: Agents["setPaused"];
  onError: (error: string) => void;
}) {
  const clients = useDjinn()?.clients;
  const [sending, setSending] = useState(false);
  const toggle = () => {
    if (!clients) return;
    const req = { name: account, provider: agentProviders[agentId] };
    onError("");
    setSending(true);
    (paused ? clients.lead.resumeAccount(req) : clients.lead.pauseAccount(req))
      .then(() => onPaused(agentId, account, !paused))
      .catch((err) => onError(message(err)))
      .finally(() => setSending(false));
  };
  const label = paused
    ? account
      ? t("agents.resume_account")
      : t("agents.resume_own")
    : account
      ? t("agents.pause_account")
      : t("agents.pause_own");
  return (
    <button
      type="button"
      className="button secondary small"
      title={`${label}\n${paused ? t("agents.resume_detail") : t("agents.pause_detail")}`}
      aria-label={label}
      aria-pressed={paused}
      disabled={sending}
      onClick={toggle}
    >
      {paused ? <Play size={13} /> : <Pause size={13} />}
      {paused ? t("agents.resume") : t("agents.pause")}
    </button>
  );
}

// AddAccount names an account of Claude Code or Codex: Djinn makes its folder, beside the agent's own, and the
// account's line offers its sign-in.
function AddAccount({ onAdded }: { onAdded: () => void }) {
  const clients = useDjinn()?.clients;
  const [provider, setProvider] = useState(Provider.CLAUDE);
  const [name, setName] = useState("");
  const [label, setLabel] = useState("");
  const [error, setError] = useState("");
  const add = () => {
    setError("");
    clients?.lead
      .addAccount({ name: name.trim(), provider, label: label.trim() })
      .then(() => {
        setName("");
        setLabel("");
        onAdded();
      })
      .catch((err) => setError(message(err)));
  };
  return (
    <>
      <form
        className="provider-setting account-add"
        title={t("agents.account_detail")}
        onSubmit={(event) => {
          event.preventDefault();
          add();
        }}
      >
        <span className="provider-glyph">+</span>
        <select
          aria-label={t("agents.account_agent")}
          value={provider}
          onChange={(event) => setProvider(Number(event.target.value))}
        >
          <option value={Provider.CLAUDE}>Claude Code</option>
          <option value={Provider.CODEX}>Codex</option>
        </select>
        <input
          aria-label={t("agents.account_name")}
          placeholder={t("agents.account_name")}
          value={name}
          pattern="[a-z0-9][a-z0-9\-]{0,31}"
          required
          onChange={(event) => setName(event.target.value)}
        />
        <input
          aria-label={t("agents.account_label")}
          placeholder={t("agents.account_label")}
          value={label}
          onChange={(event) => setLabel(event.target.value)}
        />
        <button type="submit" className="button secondary small">
          <Plus size={13} />
          {t("agents.account_add")}
        </button>
      </form>
      {error && <p role="alert">{error}</p>}
    </>
  );
}

// StepTerminal runs a step in a terminal of djinn, shown here; the user types in it when the command asks. Leaving
// the panel hangs the command up.
function StepTerminal({ step, onEnded }: { step: Step; onEnded: () => void }) {
  const djinn = useDjinn();
  const host = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<TerminalStatus>({ kind: "connecting" });
  const onEndedRef = useRef(onEnded);
  onEndedRef.current = onEnded;
  const idRef = useRef("");
  useEffect(() => {
    const element = host.current;
    if (!element || !djinn) return;
    let live = true;
    const mounted = mountTerminal(
      element,
      djinn,
      { name: step.name, line: step.line },
      (next) => {
        if (!live) return;
        setStatus(next);
        if (next.kind === "running") idRef.current = next.info.id;
        if (next.kind === "exited" || next.kind === "error") {
          idRef.current = "";
          onEndedRef.current();
        }
      },
    );
    mounted.term.focus();
    const observer = new ResizeObserver(() => fitSafely(mounted.fit));
    observer.observe(element);
    return () => {
      live = false;
      observer.disconnect();
      mounted.dispose();
      if (idRef.current)
        void djinn.terminal.close(idRef.current).catch(() => undefined);
    };
  }, [djinn, step.name, step.line]);
  const stop = () => {
    if (idRef.current && djinn)
      void djinn.terminal.close(idRef.current).catch(() => undefined);
  };
  const title =
    step.kind === "install" ? t("agents.step_install") : t("agents.step_login");
  return (
    <div className="agent-step" role="group" aria-label={title}>
      <header>
        <strong>{title}</strong>
        <code>{step.line}</code>
        {status.kind === "running" && (
          <button
            type="button"
            className="button secondary small"
            onClick={stop}
          >
            <Square size={12} />
            {t("agents.stop")}
          </button>
        )}
      </header>
      <div className="agent-step-screen" ref={host} />
      <p className="agent-step-state" role="status">
        {status.kind === "connecting" || status.kind === "running"
          ? t("agents.step_running")
          : status.kind === "exited"
            ? status.code === 0
              ? t("agents.step_done")
              : t("agents.step_failed", { code: status.code })
            : status.message}
      </p>
    </div>
  );
}

// AgentSetup is the panel on its own, as the lead's menu opens it, and the window when no agent is ready.
export function AgentSetup({ onClose }: { onClose: () => void }) {
  const agents = useAgents();
  return (
    <ModalFrame
      title={t("agents.title")}
      eyebrow={t("settings.eyebrow")}
      onClose={onClose}
      wide
    >
      <AgentSetupPanel agents={agents} />
      <div className="modal-footer">
        <button type="button" className="button secondary" onClick={onClose}>
          {t("common.close")}
        </button>
      </div>
    </ModalFrame>
  );
}
