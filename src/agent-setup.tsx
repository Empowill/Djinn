// The setup of the agents, and the accounts screen: one line per agent, where it stands, and one button for its next
// step, which says what it does on hover. Install and sign in run the agent's own command in a terminal of the panel,
// so nobody opens a terminal of theirs; once the command ends, Djinn checks again. Shown at the top of the Summoning
// Circle's page (summoning-circle.tsx), in the settings, and by itself when the window opens with no agent ready. Then every sign-in of every agent in one list,
// the developer's order of priority (decision Q295 B, tilasm L49 v2 mockup A), ranked by drag and drop or Alt+Arrow
// (sign-in-order.tsx, djinn lead order): the agent's own and its accounts, a sign-in each in a folder of the agent's
// own, signed in once by the agent's own command; Djinn keeps no token, and shows none. Each pauses and resumes from
// its line, as djinn lead pause-account does; under each, its usage as djinn lead accounts says it: active, paused or
// limited, its week's and its 5 hours' gauges and when each ends, whether the order skips it, whether the next task
// starts there, whether the Djinn runs there, the workers and waiting tasks there (in the Circle), and whether a
// session runs there in overflow. Above the list, the Djinn's account: following the order,
// or pinned on one sign-in (djinn lead pin, --unpin). At the right of the list's title, the mode that picks a sign-in
// (decision Q339 C, djinn lead account-mode): dynamic, by default, the week that ends soonest first and the
// pseudo-unlimited ones last; manual, the order alone. On each line, its switch Unlimited (djinn lead unlimited), and
// when it is on, whether Djinn detected it (auto) or the developer forced it.
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
import { getOption } from "@bufbuild/protobuf";
import {
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import { field as validation } from "../gen/ts/buf/validate/validate_pb";
import {
  AccountMode,
  type AccountUsage,
  LeadServiceAddAccountRequestSchema,
  type LeadSeat,
  Provider,
  UnlimitedSetting,
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
import { useDjinn, useOptionalData } from "./data/djinn";
import { ModalFrame } from "./frame";
import { type TextKey, language, t } from "./i18n";
import { type TerminalStatus, fitSafely, mountTerminal } from "./lead-terminal";
import { type Ranked, RankedSignIns, reordered } from "./sign-in-order";
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
  // Every sign-in in the developer's order, as djinn lead order takes them (claude, claude/work); undefined until
  // LeadService.Accounts answers.
  order?: readonly string[];
  // setOrder writes the whole order (LeadService.Order), shown at once.
  setOrder?: (order: string[]) => void;
  // The Djinn's seat, and whether it is pinned on its account (LeadService.Accounts).
  seat?: LeadSeat;
  pinned?: boolean;
  // pin pins the Djinn on an account of its agent, "" for its own sign-in; undefined takes the pin off (LeadService.Pin).
  pin?: (account: string | undefined) => void;
  // How a sign-in is picked (LeadService.Accounts): dynamic, by default, or manual; setMode writes it
  // (LeadService.AccountMode), shown at once.
  mode?: AccountMode;
  setMode?: (mode: AccountMode) => void;
  // setUnlimited writes the developer's word on whether a sign-in (claude, claude/work) is pseudo-unlimited
  // (LeadService.Unlimited), shown at once.
  setUnlimited?: (signIn: string, unlimited: UnlimitedSetting) => void;
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
  const [shownOrder, setShownOrder] = useState<readonly string[]>();
  const [lead, setLead] = useState<{ seat?: LeadSeat; pinned: boolean }>();
  const [shownMode, setShownMode] = useState<AccountMode>();
  const [checking, setChecking] = useState(true);
  const [error, setError] = useState("");
  const asked = useRef(0);
  const read = useRef(0);
  const readUsage = useCallback(() => {
    if (!clients) return;
    const n = ++read.current;
    clients.lead
      .accounts({})
      .then((res) => {
        if (n !== read.current) return;
        setUsage(res.usage);
        setShownOrder(res.order);
        setLead({ seat: res.seat, pinned: res.pinned });
        setShownMode(res.mode);
      })
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
  const setOrder = useCallback(
    (next: string[]) => {
      if (!clients) return;
      setShownOrder(next); // Shown at once; what Djinn wrote comes back with the usage.
      setError("");
      clients.lead
        .order({ signIn: next })
        .catch((err) => setError(message(err)))
        .finally(readUsage); // The next task's sign-in may change.
    },
    [clients, readUsage],
  );
  const pin = useCallback(
    (account: string | undefined) => {
      if (!clients) return;
      setLead((lead) => lead && { ...lead, pinned: account !== undefined });
      setError("");
      clients.lead
        .pin({ account: account ?? "", unpin: account === undefined })
        .catch((err) => setError(message(err)))
        .finally(readUsage);
    },
    [clients, readUsage],
  );
  const setMode = useCallback(
    (next: AccountMode) => {
      if (!clients) return;
      setShownMode(next);
      setError("");
      clients.lead
        .accountMode({ mode: next })
        .catch((err) => setError(message(err)))
        .finally(readUsage); // The next task's sign-in may change.
    },
    [clients, readUsage],
  );
  const setUnlimited = useCallback(
    (signIn: string, unlimited: UnlimitedSetting) => {
      if (!clients) return;
      setUsage((usage) => withUnlimited(usage, signIn, unlimited));
      setError("");
      clients.lead
        .unlimited({ signIn, unlimited })
        .catch((err) => setError(message(err)))
        .finally(readUsage);
    },
    [clients, readUsage],
  );
  return {
    env,
    checking,
    error,
    check,
    setPaused,
    usage,
    order: shownOrder,
    setOrder,
    seat: lead?.seat,
    pinned: lead?.pinned,
    pin,
    mode: shownMode,
    setMode,
    setUnlimited,
  };
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

// withUnlimited is usage with the developer's word on a sign-in (claude, claude/work) changed, as Djinn will say it
// back: forced on or off, or auto, where Djinn's detection decides. A sign-in Djinn said nothing of gets a line.
export function withUnlimited(
  usage: readonly AccountUsage[] | undefined,
  signIn: string,
  set: UnlimitedSetting,
): readonly AccountUsage[] {
  const [agentId, account = ""] = signIn.split("/");
  const provider = agentProviders[agentId];
  const old = usageOf(usage, agentId, account);
  const detected = old?.unlimitedDetected ?? false;
  const changed = {
    ...(old ?? { provider, account }),
    unlimitedSetting: set,
    unlimitedDetected: detected,
    unlimited:
      set === UnlimitedSetting.FORCED ||
      (set !== UnlimitedSetting.LIMITED && detected),
  } as AccountUsage;
  return old
    ? (usage ?? []).map((u) => (u === old ? changed : u))
    : [...(usage ?? []), changed];
}

// unlimitedFor is the word that turns a sign-in's Unlimited on or off: auto when Djinn detects it so already, else
// forced or limited.
export function unlimitedFor(
  usage: AccountUsage | undefined,
  on: boolean,
): UnlimitedSetting {
  if (on === (usage?.unlimitedDetected ?? false)) return UnlimitedSetting.AUTO;
  return on ? UnlimitedSetting.FORCED : UnlimitedSetting.LIMITED;
}

// SignInLoad is what runs on a sign-in now: the workers running there, and the tasks waiting there for its limit to
// lift before they start again.
export interface SignInLoad {
  workers: number;
  waiting: number;
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

// agentGlyph is the sign an agent's lines carry.
function agentGlyph(id: string): string {
  return id === "codex" ? "⬡" : id === "antigravity" ? "◆" : "✳";
}

// A sign-in on the accounts screen: the agent's own (no setup), or one of its accounts.
interface SignInLine extends Ranked {
  agent: AgentEnvironment;
  setup?: AccountSetup;
}

// signInLines are the sign-ins the screen shows, in the developer's order (Agents.order), the ones it does not name
// after, in the order by default: each agent's own, then its accounts as added. An agent's own sign-in shows once the
// agent is installed, or while it is paused. whole is every sign-in of the order, shown or not, for a move to write.
export function signInLines(
  env: UiServiceGetEnvironmentResponse,
  order: readonly string[] | undefined,
): { lines: SignInLine[]; whole: string[] } {
  const known: SignInLine[] = [];
  for (const agent of env.providers) {
    if (agent.available || agent.paused)
      known.push({
        key: agent.id,
        name: `${agent.name} · ${t("agents.own_sign_in")}`,
        agent,
      });
    for (const setup of agent.accounts)
      if (setup.account)
        known.push({
          key: `${agent.id}/${setup.account.name}`,
          name: setup.account.label || setup.account.name,
          agent,
          setup,
        });
  }
  const whole = [...(order ?? [])];
  for (const line of known) if (!whole.includes(line.key)) whole.push(line.key);
  const lines = whole
    .map((key) => known.find((line) => line.key === key))
    .filter((line): line is SignInLine => line !== undefined);
  return { lines, whole };
}

// AgentSetupPanel lists the agents, one line each with its next step, which runs below its line; then every sign-in of
// every agent in the order of priority, ranked by drag and drop or Alt+Arrow, each with its pause, its usage, where the
// Djinn runs and whether a session runs there in overflow; the Djinn's account, following the order or pinned; and the
// line that adds an account, at the bottom of the order.
export function AgentSetupPanel({
  agents,
  load,
}: {
  agents: Agents;
  // What runs on each sign-in now, from the tasks the window reads: its workers and the tasks waiting to start there.
  load?: (provider: Provider, account: string) => SignInLoad | undefined;
}) {
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
  const drive = useOptionalData((s) => s.leadDrive, undefined);
  const none = env !== undefined && env.providers.every(needsSetup);
  const { lines, whole } = env
    ? signInLines(env, agents.order)
    : { lines: [], whole: [] };
  // Where the Djinn runs: its batch's sign-in, the running one or else the last; with none yet, where it is pinned.
  const lead = drive?.batch
    ? {
        provider: drive.batch.provider || Provider.CLAUDE,
        account: drive.batch.account,
      }
    : agents.pinned && agents.seat
      ? {
          provider: agents.seat.provider || Provider.CLAUDE,
          account: agents.seat.account,
        }
      : undefined;
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
          />
        ))}
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
      {env && (
        <section
          className="sign-in-section"
          aria-label={t("agents.order_title")}
        >
          <div className="sign-in-head">
            <h3 className="sign-in-title">{t("agents.order_title")}</h3>
            {agents.setMode && (
              <ModeSwitch
                mode={agents.mode ?? AccountMode.DYNAMIC}
                onMode={agents.setMode}
              />
            )}
          </div>
          <p className="agent-setup-intro">
            {t(
              agents.mode === AccountMode.MANUAL
                ? "agents.order_detail"
                : "agents.order_detail_dynamic",
            )}
          </p>
          {agents.seat && agents.pin && (
            <LeadPin
              seat={agents.seat}
              pinned={agents.pinned ?? false}
              lines={lines}
              onPin={agents.pin}
            />
          )}
          <div className="provider-settings">
            <RankedSignIns
              items={lines}
              onOrder={(shown) => agents.setOrder?.(reordered(whole, shown))}
              render={(item, grip) => {
                const line = item as SignInLine;
                const account = line.setup?.account?.name ?? "";
                return (
                  <SignInRow
                    line={line}
                    grip={grip}
                    busy={running || checking}
                    step={step}
                    onRun={run}
                    onEnded={ended}
                    onChanged={check}
                    onPaused={setPaused}
                    onUnlimited={agents.setUnlimited}
                    usage={usageOf(usage, line.agent.id, account)}
                    busyHere={load?.(agentProviders[line.agent.id], account)}
                    lead={
                      lead?.provider === agentProviders[line.agent.id] &&
                      lead.account === account
                    }
                  />
                );
              }}
            />
            <AddAccount onAdded={check} />
          </div>
        </section>
      )}
    </div>
  );
}

// ModeSwitch is how a sign-in is picked, as djinn lead account-mode sets it: dynamic, the week that ends soonest first
// and the pseudo-unlimited ones last; manual, the order alone.
function ModeSwitch({
  mode,
  onMode,
}: {
  mode: AccountMode;
  onMode: (mode: AccountMode) => void;
}) {
  const manual = mode === AccountMode.MANUAL;
  return (
    <div
      className="segmented-switch"
      role="group"
      aria-label={t("agents.mode_label")}
    >
      <button
        type="button"
        className={manual ? "" : "active"}
        aria-pressed={!manual}
        title={t("agents.mode_dynamic_detail")}
        onClick={() => onMode(AccountMode.DYNAMIC)}
      >
        {t("agents.mode_dynamic")}
      </button>
      <button
        type="button"
        className={manual ? "active" : ""}
        aria-pressed={manual}
        title={t("agents.mode_manual_detail")}
        onClick={() => onMode(AccountMode.MANUAL)}
      >
        {t("agents.mode_manual")}
      </button>
    </div>
  );
}

// LeadPin is the Djinn's account: following the order, or pinned on one sign-in of its agent, as djinn lead pin and
// djinn lead pin --unpin set it. Choosing Follows the order takes the pin off.
function LeadPin({
  seat,
  pinned,
  lines,
  onPin,
}: {
  seat: LeadSeat;
  pinned: boolean;
  lines: readonly SignInLine[];
  onPin: NonNullable<Agents["pin"]>;
}) {
  const provider = seat.provider || Provider.CLAUDE;
  const own = lines.filter(
    (line) => agentProviders[line.agent.id] === provider,
  );
  const value = pinned ? `pin:${seat.account}` : "follow";
  return (
    <label className="lead-pin" title={t("agents.lead_account_detail")}>
      <span>♛ {t("agents.lead_account")}</span>
      <select
        aria-label={t("agents.lead_account")}
        value={value}
        onChange={(event) => {
          const next = event.target.value;
          onPin(next === "follow" ? undefined : next.slice("pin:".length));
        }}
      >
        <option value="follow">{t("agents.lead_follows")}</option>
        {own.map((line) => (
          <option
            key={line.key}
            value={`pin:${line.setup?.account?.name ?? ""}`}
          >
            {t("agents.lead_pinned_on", { sign_in: line.name })}
          </option>
        ))}
      </select>
    </label>
  );
}

// AgentRow is an agent: its name and version, where it stands, and the button of its next step, which runs below.
function AgentRow({
  agent,
  checking,
  busy,
  step,
  onRun,
  onEnded,
}: {
  agent: AgentEnvironment;
  checking: boolean;
  busy: boolean;
  step?: Step;
  onRun: (agentId: string, kind: Step["kind"], line: string) => void;
  onEnded: () => void;
}) {
  const state = agentStates[agent.state] ?? agentStates[ProviderState.UNKNOWN];
  const next = nextStep(agent);
  return (
    <>
      <div
        className="provider-setting"
        data-agent={agent.id}
        title={agent.available ? agent.command : undefined}
      >
        <span className="provider-glyph">{agentGlyph(agent.id)}</span>
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
      </div>
      {step?.agentId === agent.id && (
        <StepTerminal key={step.name} step={step} onEnded={onEnded} />
      )}
    </>
  );
}

// SignInRow is a sign-in in the order: its grip and rank, its agent, its name, and for an account its folder, where it
// stands, its sign-in in its own folder and a button that forgets it, its folder left as it is; for each, its pause,
// whether the Djinn runs there, whether a session runs there in overflow, and below its usage.
function SignInRow({
  line,
  grip,
  busy,
  step,
  onRun,
  onEnded,
  onChanged,
  onPaused,
  onUnlimited,
  usage,
  lead,
  busyHere,
}: {
  line: SignInLine;
  grip: ReactNode;
  busy: boolean;
  step?: Step;
  onRun: (agentId: string, kind: Step["kind"], line: string) => void;
  onEnded: () => void;
  onChanged: () => void;
  onPaused: Agents["setPaused"];
  onUnlimited?: Agents["setUnlimited"];
  usage?: AccountUsage;
  lead: boolean;
  busyHere?: SignInLoad;
}) {
  const clients = useDjinn()?.clients;
  const [error, setError] = useState("");
  const { agent, setup } = line;
  const account = setup?.account;
  const id = account ? `${agent.id}/${account.name}` : agent.id;
  const state =
    agentStates[setup ? setup.state : agent.state] ??
    agentStates[ProviderState.UNKNOWN];
  const signIn =
    setup !== undefined &&
    agent.available &&
    setup.state !== ProviderState.READY &&
    Boolean(setup.loginCommand);
  const paused = account ? account.paused : agent.paused;
  const forget = () => {
    if (!account) return;
    setError("");
    clients?.lead
      .removeAccount({ name: account.name })
      .then(onChanged)
      .catch((err) => setError(message(err)));
  };
  return (
    <>
      <div
        className="provider-setting sign-in-line"
        {...(account
          ? { "data-account": account.name }
          : { "data-own": agent.id })}
        title={account?.configDirectory}
      >
        {grip}
        <span className="provider-glyph" title={agent.name}>
          {agentGlyph(agent.id)}
        </span>
        <div>
          <h3>{account ? account.label || account.name : agent.name}</h3>
          {account ? (
            <code>{account.configDirectory}</code>
          ) : (
            <span className="muted-text">{t("agents.own_sign_in")}</span>
          )}
        </div>
        {lead && (
          <span
            className="badge sign-in-lead"
            title={t("agents.lead_here_detail")}
          >
            ♛ {t("agents.lead_here")}
          </span>
        )}
        {usage?.next && (
          <span
            className="badge sign-in-next"
            title={t("agents.usage_next_detail")}
          >
            {t("agents.usage_next", { agent: agent.name })}
          </span>
        )}
        {busyHere?.workers ? (
          <span className="badge sign-in-workers">
            {t("agents.workers_here", { count: busyHere.workers })}
          </span>
        ) : null}
        {busyHere?.waiting ? (
          <span
            className="badge sign-in-waiting"
            title={t("agents.waiting_here_detail")}
          >
            {t("agents.waiting_here", { count: busyHere.waiting })}
          </span>
        ) : null}
        {usage?.unlimited && (
          <span
            className="badge sign-in-unlimited"
            title={t("agents.unlimited_detail")}
          >
            {t(
              usage.unlimitedSetting === UnlimitedSetting.FORCED
                ? "agents.unlimited_forced"
                : "agents.unlimited_auto",
            )}
          </span>
        )}
        {usage?.overflowing && (
          <span
            className="badge sign-in-overflow"
            title={t("agents.overflowing_detail")}
          >
            {t("agents.overflowing")}
          </span>
        )}
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
        {onUnlimited && (
          <button
            type="button"
            role="switch"
            className="sign-in-unlimited-switch"
            aria-checked={usage?.unlimited ?? false}
            aria-label={t("agents.unlimited_switch", {
              name: account ? account.label || account.name : agent.name,
            })}
            title={t("agents.unlimited_switch_detail")}
            onClick={() =>
              onUnlimited(id, unlimitedFor(usage, !(usage?.unlimited ?? false)))
            }
          >
            <span className={`toggle${usage?.unlimited ? " on" : ""}`}>
              <span />
            </span>
            {t("agents.unlimited")}
          </button>
        )}
        <PauseButton
          agentId={agent.id}
          account={account?.name ?? ""}
          paused={paused}
          onPaused={onPaused}
          onError={setError}
        />
        {account && (
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
        )}
      </div>
      <SignInUsage
        id={id}
        paused={paused}
        signedIn={(setup ? setup.state : agent.state) === ProviderState.READY}
        withAccounts={account !== undefined || agent.accounts.length > 0}
        reads={quotaTold.has(agent.id)}
        usage={usage}
      />
      {error && <p role="alert">{error}</p>}
      {account && step?.agentId === id && (
        <StepTerminal key={step.name} step={step} onEnded={onEnded} />
      )}
    </>
  );
}

// SignInUsage is the line under a sign-in's: its state, and what Djinn last read of its quota. The state is paused
// (no worker starts there), limited until a reset (a worker hit the limit of the whole account), or active (signed in,
// workers may start there). Then the week's gauge, how much of it is used and when it ends, the 5 hours' gauge and
// when the session ends, and what the order does with a paused or limited one: skipped; hovered, when and where
// Djinn read them. Shown for a sign-in paused
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
  // What the order does with it: a paused or limited sign-in is skipped, the next free one of its agent taken instead.
  const skipped = paused
    ? t("agents.skipped_paused")
    : limited
      ? t("agents.skipped_until", { time: clockOf(limited) })
      : "";
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
      {fiveHour && <FiveHourGauge window={fiveHour} />}
      {!week && !fiveHour && usage?.unlimited && (
        <span className="muted-text">{t("agents.usage_unlimited")}</span>
      )}
      {!week && !fiveHour && !usage?.unlimited && (reads || usage) && (
        <span className="muted-text" title={usage?.probeError || undefined}>
          {usage?.probeError
            ? t("agents.usage_failed")
            : t("agents.usage_unread")}
        </span>
      )}
      {skipped && <span className="muted-text sign-in-skipped">{skipped}</span>}
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

// FiveHourGauge is a sign-in's 5-hour session: a gauge of the share used against its limit, the share in words, and
// when the session ends, its hour and the time left until then. Full past 95 %, where Djinn starts no new task there.
// A session not started yet tells no end. The time left counts down each minute while it shows.
function FiveHourGauge({ window: session }: { window: UsageWindow }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => keepLive(() => setNow(Date.now()), 60_000), []);
  const used = percent(session);
  const ends = session.resetsTime && timestampDate(session.resetsTime);
  return (
    <span className="usage-week usage-five-hour">
      <span className="muted-text">{t("agents.five_hour_label")}</span>
      <span
        className={`usage-gauge${session.usedPercent >= 95 ? " full" : ""}`}
        role="meter"
        aria-label={t("agents.five_hour_used")}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={used}
      >
        <span style={{ width: `${Math.min(100, Math.max(0, used))}%` }} />
      </span>
      <span className="usage-percent">{used} %</span>
      {ends && (
        <span className="muted-text">
          {t("agents.five_hour_ends", {
            time: clockOf(ends),
            left: timeLeft(ends.getTime() - now),
          })}
        </span>
      )}
    </span>
  );
}

// timeLeft is a span of time to come, for a countdown: its hours and minutes, or its minutes under an hour.
export function timeLeft(ms: number): string {
  const minutes = Math.max(0, Math.ceil(ms / 60_000));
  const hours = Math.floor(minutes / 60);
  return hours > 0
    ? `${hours} h ${String(minutes % 60).padStart(2, "0")}`
    : `${minutes} min`;
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

// accountNamePattern is the rule of an account's name, read from the proto's validation of
// LeadServiceAddAccountRequest.name: the window checks the name the server checks, by the same expression.
export const accountNamePattern = new RegExp(
  (() => {
    const rules = getOption(
      LeadServiceAddAccountRequestSchema.field.name,
      validation,
    );
    return rules.type.case === "string" ? (rules.type.value.pattern ?? "") : "";
  })(),
);

// accountNameValid says whether name, trimmed as it is sent, passes that rule.
export function accountNameValid(name: string): boolean {
  return accountNamePattern.test(name.trim());
}

// accountNameExample is the name the window offers as an example, in the reader's language: a name the rule accepts.
export function accountNameExample(): string {
  return t("agents.account_name_example");
}

// AddAccount names an account of Claude Code or Codex: Djinn makes its folder, beside the agent's own, and the
// account's line offers its sign-in. A name the rule refuses says the rule under the line, with an example that passes
// it, and the browser's own bubble says the same instead of its bare "match the requested format".
function AddAccount({ onAdded }: { onAdded: () => void }) {
  const clients = useDjinn()?.clients;
  const [provider, setProvider] = useState(Provider.CLAUDE);
  const [name, setName] = useState("");
  const [label, setLabel] = useState("");
  const [error, setError] = useState("");
  const nameInput = useRef<HTMLInputElement>(null);
  const example = accountNameExample();
  const format = t("agents.account_name_format", { example });
  const invalid = name.trim() !== "" && !accountNameValid(name);
  useEffect(() => {
    nameInput.current?.setCustomValidity?.(invalid ? format : "");
  }, [invalid, format]);
  const add = () => {
    setError("");
    if (!accountNameValid(name)) return;
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
          ref={nameInput}
          aria-label={t("agents.account_name", { example })}
          placeholder={t("agents.account_name", { example })}
          title={format}
          aria-invalid={invalid}
          value={name}
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
      {(invalid || error) && <p role="alert">{invalid ? format : error}</p>}
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

// AgentSetup is the panel on its own, as the window opens it when no agent is ready.
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
