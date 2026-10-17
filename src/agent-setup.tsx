// The setup of the agents: one line per agent, where it stands, and one button for its next step, which says what it
// does on hover. Install and sign in run the agent's own command in a terminal of the panel, so nobody opens a
// terminal of theirs; once the command ends, Djinn checks again. Shown in the settings, from the lead's menu, and by
// itself when the window opens with no agent ready. Under each agent, its accounts: a sign-in each in a folder of the
// agent's own, signed in once by the agent's own command, for the lead to move to; Djinn keeps no token.
import { Download, LogIn, Plus, RefreshCw, Square, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Provider } from "../gen/ts/plan/v1/plan_pb";
import {
  type AccountSetup,
  type Provider as AgentEnvironment,
  ProviderState,
  type UiServiceGetEnvironmentResponse,
} from "../gen/ts/ui/v1/ui_pb";
import { message } from "./data/client";
import { useDjinn } from "./data/djinn";
import { ModalFrame } from "./frame";
import { type TextKey, t } from "./i18n";
import { type TerminalStatus, fitSafely, mountTerminal } from "./lead-terminal";

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
}

// useAgents checks the agents once mounted, and again on check. A check runs each agent's status command: it takes
// up to a few seconds.
export function useAgents(): Agents {
  const clients = useDjinn()?.clients;
  const [env, setEnv] = useState<UiServiceGetEnvironmentResponse>();
  const [checking, setChecking] = useState(true);
  const [error, setError] = useState("");
  const asked = useRef(0);
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
  }, [clients]);
  const cancelPending = useCallback(() => {
    asked.current++; // Unmounted: what comes back is dropped.
  }, []);
  useEffect(() => {
    check();
    return cancelPending;
  }, [cancelPending, check]);
  return { env, checking, error, check };
}

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
  const { env, checking, error, check } = agents;
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
}: {
  agent: AgentEnvironment;
  checking: boolean;
  busy: boolean;
  step?: Step;
  onRun: (agentId: string, kind: Step["kind"], line: string) => void;
  onEnded: () => void;
  onChanged: () => void;
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
      </div>
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
        />
      ))}
    </>
  );
}

// AccountRow is an account of an agent: its label and folder, where it stands, its sign-in in its own folder, and a
// button that forgets it, its folder left as it is.
function AccountRow({
  agent,
  setup,
  busy,
  step,
  onRun,
  onEnded,
  onChanged,
}: {
  agent: AgentEnvironment;
  setup: AccountSetup;
  busy: boolean;
  step?: Step;
  onRun: (agentId: string, kind: Step["kind"], line: string) => void;
  onEnded: () => void;
  onChanged: () => void;
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
      {error && <p role="alert">{error}</p>}
      {step?.agentId === id && (
        <StepTerminal key={step.name} step={step} onEnded={onEnded} />
      )}
    </>
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
