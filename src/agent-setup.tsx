// The setup of the agents: where each one stands, the next step in plain words, and the buttons that take it. Install
// and sign in run the agent's own command in a terminal of the panel, so nobody opens a terminal of theirs; once
// the command ends, Djinn checks again. Shown in the settings, by itself at the first start when no agent is ready,
// and from a new wish whose agent is not ready.
import { Download, LogIn, RefreshCw, Square } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Provider } from "../gen/ts/plan/v1/plan_pb";
import {
  type Provider as AgentEnvironment,
  ProviderState,
  type UiServiceGetEnvironmentResponse,
} from "../gen/ts/ui/v1/ui_pb";
import { message } from "./data/client";
import { useClients, useDjinn } from "./data/djinn";
import { ModalFrame } from "./frame";
import { type TextKey, t } from "./i18n";
import { type TerminalStatus, fitSafely, mountTerminal } from "./lead-terminal";

// agentOf is the agent command line of a wish's agent.
export const agentOf: Partial<Record<Provider, string>> = {
  [Provider.CLAUDE]: "claude",
  [Provider.CODEX]: "codex",
  [Provider.ANTIGRAVITY]: "agy",
};

// needsSetup tells an agent that cannot run a wish as it is: not installed, or not signed in. One whose sign-in is
// unknown may well be ready: it is not held back.
export function needsSetup(agent: AgentEnvironment | undefined): boolean {
  return (
    agent?.state === ProviderState.MISSING ||
    agent?.state === ProviderState.SIGNED_OUT
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
  const clients = useClients();
  const [env, setEnv] = useState<UiServiceGetEnvironmentResponse>();
  const [checking, setChecking] = useState(true);
  const [error, setError] = useState("");
  const asked = useRef(0);
  const check = useCallback(() => {
    const n = ++asked.current;
    setChecking(true);
    setError("");
    clients.ui
      .getEnvironment({ agents: true })
      .then((res) => n === asked.current && setEnv(res))
      .catch((err) => n === asked.current && setError(message(err)))
      .finally(() => n === asked.current && setChecking(false));
  }, [clients]);
  useEffect(() => {
    check();
    return () => {
      asked.current++; // Unmounted: what comes back is dropped.
    };
  }, [check]);
  return { env, checking, error, check };
}

// agentStates names where an agent stands, the dot that shows it, and the next step.
const agentStates: Record<
  ProviderState,
  { label: TextKey; dot: string; next: TextKey }
> = {
  [ProviderState.UNSPECIFIED]: {
    label: "settings.agent_unknown",
    dot: "neutral",
    next: "agents.next_unknown",
  },
  [ProviderState.MISSING]: {
    label: "settings.agent_missing",
    dot: "neutral",
    next: "agents.next_install",
  },
  [ProviderState.SIGNED_OUT]: {
    label: "settings.agent_signed_out",
    dot: "blocked",
    next: "agents.next_login",
  },
  [ProviderState.READY]: {
    label: "settings.agent_ready",
    dot: "green",
    next: "agents.next_ready",
  },
  [ProviderState.UNKNOWN]: {
    label: "settings.agent_unknown",
    dot: "blocked",
    next: "agents.next_unknown",
  },
};

// A step that runs: an agent's install or sign-in command, in the panel's terminal.
interface Step {
  agentId: string;
  kind: "install" | "login";
  line: string;
  name: string; // The terminal's: new for each run, so a run never attaches to the one before.
}

// AgentSetupPanel lists the agents with their next step; Install and Sign in run it below the agent.
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
  const ready = env?.providers.some((p) => p.state === ProviderState.READY);
  return (
    <div className="agent-setup">
      <p className="agent-setup-intro">
        {env && !ready ? t("agents.intro_none") : t("agents.intro")}
      </p>
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
            step={step?.agentId === agent.id ? step : undefined}
            onRun={run}
            onCheck={check}
            onEnded={ended}
          />
        ))}
      </div>
    </div>
  );
}

function AgentRow({
  agent,
  checking,
  busy,
  step,
  onRun,
  onCheck,
  onEnded,
}: {
  agent: AgentEnvironment;
  checking: boolean;
  busy: boolean;
  step?: Step;
  onRun: (agentId: string, kind: Step["kind"], line: string) => void;
  onCheck: () => void;
  onEnded: () => void;
}) {
  const state = agentStates[agent.state] ?? agentStates[ProviderState.UNKNOWN];
  const install =
    agent.state === ProviderState.MISSING ? agent.installCommand : "";
  const login =
    agent.state === ProviderState.SIGNED_OUT ||
    agent.state === ProviderState.UNKNOWN
      ? agent.loginCommand
      : "";
  return (
    <>
      <div className="provider-setting" data-agent={agent.id}>
        <span className="provider-glyph">
          {agent.id === "codex" ? "⬡" : agent.id === "agy" ? "◆" : "✳"}
        </span>
        <div>
          <h3>{agent.name}</h3>
          <span className="muted-text">
            {agent.available
              ? [agent.version, agent.command].filter(Boolean).join(" · ")
              : t("settings.agent_missing")}
          </span>
          <p className="agent-next">{t(state.next)}</p>
        </div>
        <div className="agent-side">
          <span className="status-text">
            <span className={`status-dot ${state.dot}`} />
            {checking ? t("agents.checking") : t(state.label)}
          </span>
          <div className="notification-actions">
            {install && (
              <button
                type="button"
                className="button accent small"
                disabled={busy || checking}
                onClick={() => onRun(agent.id, "install", install)}
              >
                <Download size={13} />
                {t("agents.install")}
              </button>
            )}
            {login && (
              <button
                type="button"
                className={`button small ${agent.state === ProviderState.SIGNED_OUT ? "accent" : "secondary"}`}
                disabled={busy || checking}
                onClick={() => onRun(agent.id, "login", login)}
              >
                <LogIn size={13} />
                {t("agents.login")}
              </button>
            )}
            <button
              type="button"
              className="button secondary small"
              disabled={busy || checking}
              onClick={onCheck}
            >
              <RefreshCw size={13} />
              {t("agents.check")}
            </button>
          </div>
        </div>
      </div>
      {step && <StepTerminal key={step.name} step={step} onEnded={onEnded} />}
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

// AgentSetup is the panel on its own, as the window opens it at the first start.
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
