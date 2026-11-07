// The Summoning Circle (W383, W400, W417, W540): a page of the window, like the Grimoire, that says who works, on which
// account, with which model (tilasm L49 v2 mockup A). First the agents, each with its next step (agent-setup.tsx);
// then every sign-in of every agent in the order of priority, ranked by drag and drop or Alt+Arrow, each with its
// gauges, its state, the workers and the tasks waiting there, and where the Djinn runs; then one row per kind of
// worker, global to Djinn (every project); last, the change of the open tasks' models at once. The one place where the
// Djinn's agent, model and account are chosen: the window's top has no selector of its own (W578). The lead in its
// terminal keeps its recorded session; when the Djinn's row names another agent, its line starts the Djinn there.
// Models come from the server (live agent discovery + shared internal/usage/prices.yaml table), never a hand-written UI list.
// Providers not signed in on this machine are never offered as choices: their rows say how to sign them in.
import { useCallback, useEffect, useMemo, useState } from "react";

import {
  Change,
  Provider,
  type Task,
  TaskKind,
  TaskStatus,
  type TaskServiceUpdateModelsResponse,
  WorkerKind,
  type WorkerSeat,
} from "../gen/ts/plan/v1/plan_pb";
import type { UiServiceGetEnvironmentResponse } from "../gen/ts/ui/v1/ui_pb";
import {
  AgentSetupPanel,
  type Agents,
  type SignInLoad,
  useAgents,
} from "./agent-setup";
import { message } from "./data/client";
import { useDjinn, useOptionalData } from "./data/djinn";
import { defaultModel } from "./data/format";
import { catalogFromPriceTable } from "./data/prices";
import { type TextKey, t } from "./i18n";
import { OperatingLoad } from "./operating-load";
import { PageHeader } from "./page-header";
import { recordedAgent } from "./wish-head";
import "./project-panel.css";

export interface CircleModel {
  readonly id: string;
  readonly label: string;
  readonly provider: Provider;
}

export interface CircleProvider {
  readonly provider: Provider;
  readonly signedIn: boolean;
  readonly loginCommand: string;
  readonly leadSupported: boolean;
  readonly leadUnavailableReason?: string;
}

export const DEFAULT_CIRCLE_PROVIDERS: readonly CircleProvider[] = [
  {
    provider: Provider.CLAUDE,
    signedIn: true,
    loginCommand: "claude auth login",
    leadSupported: true,
  },
  {
    provider: Provider.CODEX,
    signedIn: true,
    loginCommand: "codex login",
    leadSupported: true,
  },
  {
    provider: Provider.ANTIGRAVITY,
    signedIn: true,
    loginCommand: "agy",
    leadSupported: true,
  },
];

const MODEL_ALIASES: Readonly<Record<string, string>> = {
  fable: "claude-fable-5-1",
  "claude fable 5.1": "claude-fable-5-1",
  sonnet: "claude-sonnet-5-5",
  "claude sonnet 5.5": "claude-sonnet-5-5",
  "claude sonnet 4.5": "claude-sonnet-4-5",
  opus: "claude-opus-5-5",
  "claude opus 5.5": "claude-opus-5-5",
  "claude opus 5.8": "claude-opus-5-5",
  "claude-opus-5-8": "claude-opus-5-5",
  haiku: "claude-haiku-5-5",
  "claude haiku 5.5": "claude-haiku-5-5",
  "gpt-5.5": "gpt-5.5",
  "gpt-5.3 codex": "gpt-5.3-codex",
  "gpt-5.5 codex": "gpt-5.3-codex",
  "gpt-5.5-codex": "gpt-5.3-codex",
  "gpt-5.5 mini": "gpt-5.3-codex",
  "gpt-5.5-mini": "gpt-5.3-codex",
  "gemini 4 argon (high)": "gemini-4-argon-high",
  "gemini 4 argon": "gemini-4-argon-high",
  argon: "gemini-4-argon-high",
  "gemini 4 argon (medium)": "gemini-4-argon-medium",
  "gemini 3.8 flash (high)": "gemini-3.8-flash-high",
  "gemini 3.8 flash": "gemini-3.8-flash-high",
  flash: "gemini-3.8-flash-high",
  "gemini 3.8 flash (medium)": "gemini-3.8-flash-medium",
  "gemini 3.8 pro (high)": "gemini-4-argon-high",
  "gemini 3.8 pro": "gemini-4-argon-high",
  "gemini-3.8-pro-high": "gemini-4-argon-high",
  pro: "gemini-4-argon-high",
};

export function canonicalModelId(model: string): string {
  const trimmed = model.trim();
  if (!trimmed) return "";
  return MODEL_ALIASES[trimmed.toLowerCase()] ?? trimmed;
}

export function providerName(provider: Provider): string {
  switch (provider) {
    case Provider.CLAUDE:
      return t("summoning.provider_claude");
    case Provider.CODEX:
      return t("summoning.provider_codex");
    case Provider.ANTIGRAVITY:
      return t("summoning.provider_antigravity");
    default:
      return "";
  }
}

export interface CircleRowSpec {
  readonly kind: WorkerKind;
  readonly key:
    | "lead"
    | "investigation"
    | "mockup"
    | "ifrit"
    | "work"
    | "correction"
    | "review"
    | "question";
  readonly labelKey: TextKey;
  readonly detailKey: TextKey;
  readonly providers: readonly Provider[];
}

export const CIRCLE_ROWS: readonly CircleRowSpec[] = [
  {
    kind: WorkerKind.LEAD,
    key: "lead",
    labelKey: "summoning.kind_lead",
    detailKey: "summoning.kind_lead_detail",
    providers: [Provider.CLAUDE, Provider.CODEX, Provider.ANTIGRAVITY],
  },
  {
    kind: WorkerKind.INVESTIGATION,
    key: "investigation",
    labelKey: "summoning.kind_investigation",
    detailKey: "summoning.kind_investigation_detail",
    providers: [Provider.CLAUDE, Provider.CODEX, Provider.ANTIGRAVITY],
  },
  {
    kind: WorkerKind.MOCKUP,
    key: "mockup",
    labelKey: "summoning.kind_mockup",
    detailKey: "summoning.kind_mockup_detail",
    providers: [Provider.CLAUDE, Provider.CODEX, Provider.ANTIGRAVITY],
  },
  {
    kind: WorkerKind.IFRIT,
    key: "ifrit",
    labelKey: "summoning.kind_ifrit",
    detailKey: "summoning.kind_ifrit_detail",
    providers: [Provider.CLAUDE, Provider.ANTIGRAVITY],
  },
  {
    kind: WorkerKind.WORK,
    key: "work",
    labelKey: "summoning.kind_work",
    detailKey: "summoning.kind_work_detail",
    providers: [Provider.CLAUDE, Provider.CODEX, Provider.ANTIGRAVITY],
  },
  {
    kind: WorkerKind.CORRECTION,
    key: "correction",
    labelKey: "summoning.kind_correction",
    detailKey: "summoning.kind_correction_detail",
    providers: [Provider.CLAUDE, Provider.CODEX, Provider.ANTIGRAVITY],
  },
  {
    kind: WorkerKind.REVIEW,
    key: "review",
    labelKey: "summoning.kind_review",
    detailKey: "summoning.kind_review_detail",
    providers: [Provider.CLAUDE, Provider.CODEX, Provider.ANTIGRAVITY],
  },
  {
    kind: WorkerKind.QUESTION,
    key: "question",
    labelKey: "summoning.kind_question",
    detailKey: "summoning.kind_question_detail",
    providers: [Provider.CLAUDE, Provider.CODEX],
  },
];

function isProviderSelectableForRow(
  spec: CircleRowSpec,
  provider: Provider,
  providers: readonly CircleProvider[],
): boolean {
  if (!spec.providers.includes(provider)) return false;
  const info = providers.find((p) => p.provider === provider);
  if (!info) return true;
  if (!info.signedIn) return false;
  if (spec.kind === WorkerKind.LEAD && !info.leadSupported) return false;
  return true;
}

function modelsForRow(
  spec: CircleRowSpec,
  models: readonly CircleModel[],
  providers: readonly CircleProvider[],
): CircleModel[] {
  return models.filter((m) =>
    isProviderSelectableForRow(spec, m.provider, providers),
  );
}

// CIRCLE is the page of the window that shows the Summoning Circle, as the side panel's entry and the lead's menu ask.
// A wish's id is a UUID: never this.
export const CIRCLE = "circle";

// loadOf counts what runs on each sign-in, by its agent and account ("" for the agent's own): the workers running
// there, and the tasks stopped by its limit that wait to start again. A watcher or an azima runs no agent of its own.
export function loadOf(tasks: readonly Task[]): Map<string, SignInLoad> {
  const out = new Map<string, SignInLoad>();
  for (const task of tasks) {
    if (task.provider === Provider.WATCH || task.kind === TaskKind.AZIMA)
      continue;
    const running = task.status === TaskStatus.RUNNING;
    if (!running && task.status !== TaskStatus.RESUMING) continue;
    const provider =
      task.provider === Provider.UNSPECIFIED ? Provider.CLAUDE : task.provider;
    const key = `${provider}/${task.account}`;
    const load = out.get(key) ?? { workers: 0, waiting: 0 };
    if (running) load.workers++;
    else load.waiting++;
    out.set(key, load);
  }
  return out;
}

export function SummoningCircle({
  initialRows,
  initialModels,
  initialProviders,
  initialAgents,
  initialTasks,
  initialLead,
  onChoose,
  onAgents,
}: {
  initialRows?: WorkerSeat[];
  initialModels?: readonly CircleModel[];
  initialProviders?: readonly CircleProvider[];
  onChoose?: (req: {
    kind: WorkerKind;
    provider: Provider;
    model: string;
  }) => Promise<WorkerSeat[]>;
  // The agents and their sign-ins, and the tasks, as a test gives them; else read from Djinn.
  initialAgents?: Agents;
  initialTasks?: readonly Task[];
  // The agent of the lead's recorded session, and whether Djinn drives the lead, as a test gives them; else the store's.
  initialLead?: { recorded?: Provider; driven: boolean };
  // The agents as the Circle reads them, at each check: the window's side panel follows them (its yellow dot, W578).
  onAgents?: (env: UiServiceGetEnvironmentResponse) => void;
}) {
  const djinn = useDjinn();
  const liveAgents = useAgents();
  const agents = initialAgents ?? liveAgents;
  useEffect(() => {
    if (agents.env) onAgents?.(agents.env);
  }, [agents.env, onAgents]);
  const storeRecorded = useOptionalData(
    (s) => recordedAgent(s.lead),
    undefined,
  );
  const storeDriven = useOptionalData((s) => s.leadDrive !== undefined, false);
  const recorded = initialLead ? initialLead.recorded : storeRecorded;
  const driven = initialLead ? initialLead.driven : storeDriven;
  // What Djinn said of the last start of the Djinn on another agent, from the Djinn's row.
  const [leadNote, setLeadNote] = useState("");
  // The tasks of the active wishes, which the window reads while it is open (wish-app.tsx).
  const details = useOptionalData((s) => s.details, undefined);
  const load = useMemo(
    () =>
      loadOf(
        initialTasks ??
          Object.values(details ?? {}).flatMap((d) => d?.tasks ?? []),
      ),
    [details, initialTasks],
  );
  const loadAt = useCallback(
    (provider: Provider, account: string) => load.get(`${provider}/${account}`),
    [load],
  );
  const [rows, setRows] = useState<WorkerSeat[]>(initialRows ?? []);
  const [models, setModels] = useState<readonly CircleModel[]>(
    () => initialModels ?? catalogFromPriceTable(),
  );
  const [providers, setProviders] = useState<readonly CircleProvider[]>(
    () => initialProviders ?? DEFAULT_CIRCLE_PROVIDERS,
  );
  const selectableModels = useMemo(
    () =>
      models.filter(
        (m) =>
          providers.find((p) => p.provider === m.provider)?.signedIn !== false,
      ),
    [models, providers],
  );
  const [loaded, setLoaded] = useState(Boolean(initialRows));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (initialRows || !djinn) return;
    let live = true;
    djinn.clients.lead
      .getCircle({})
      .then((res) => {
        if (!live) return;
        setRows(res.rows);
        if (res.models.length > 0 || res.providers.length > 0) {
          setModels(res.models);
        }
        if (res.providers.length > 0) {
          setProviders(res.providers);
        }
        setLoaded(true);
      })
      .catch((err) => {
        if (!live) return;
        setError(message(err));
        setLoaded(true);
      });
    return () => {
      live = false;
    };
  }, [djinn, initialRows]);

  const apply = (
    spec: CircleRowSpec,
    value: string,
    options: CircleModel[],
  ) => {
    setBusy(true);
    setError("");
    const entry = options.find((m) => m.id === value);
    const provider =
      value === ""
        ? Provider.UNSPECIFIED
        : (entry?.provider ?? spec.providers[0]);
    const model = value === "" ? "" : (entry?.id ?? value);
    const call = onChoose
      ? onChoose({ kind: spec.kind, provider, model })
      : djinn
        ? djinn.clients.lead
            .setCircle({ kind: spec.kind, provider, model })
            .then((res) => {
              if (res.models.length > 0 || res.providers.length > 0) {
                setModels(res.models);
              }
              if (res.providers.length > 0) {
                setProviders(res.providers);
              }
              void djinn.store.changed("", [Change.PROJECT]);
              return res.rows;
            })
        : Promise.resolve(rows);
    call
      .then((nextRows) => setRows(nextRows))
      .catch((err) => setError(message(err)))
      .finally(() => setBusy(false));
  };

  // startLead starts the lead in its terminal on another agent than its recorded session's, from the brief: it becomes
  // the Djinn (LeadService.Resume with a provider), as the window's top offered before W578.
  const startLead = (provider: Provider) => {
    if (!djinn) return;
    setBusy(true);
    setError("");
    setLeadNote("");
    djinn.clients.lead
      .resume({ provider })
      .then((res) => {
        setLeadNote(res.note);
        void djinn.store.changed("", [Change.WISH]);
      })
      .catch((err) => setError(message(err)))
      .finally(() => setBusy(false));
  };

  return (
    <div className="wish-view summoning-page">
      <PageHeader middle={<OperatingLoad />}>
        <strong>{t("summoning.title")}</strong>
      </PageHeader>
      <div className="mission-scroll">
        <div className="summoning-content">
          <header className="summoning-head">
            <h1>{t("summoning.title")}</h1>
            <p>{t("summoning.page_detail")}</p>
          </header>
          <AgentSetupPanel agents={agents} load={loadAt} />
          <h3 className="summoning-section-title">
            {t("summoning.models_title")}
          </h3>
          <p className="agent-setup-intro">{t("summoning.detail")}</p>
          {error && (
            <p className="field-error" role="alert">
              {error}
            </p>
          )}
          <section className="config-box summoning-list">
            {CIRCLE_ROWS.map((spec) => {
              const seat = rows.find((r) => r.kind === spec.kind);
              const isLead = spec.kind === WorkerKind.LEAD;
              const options = modelsForRow(spec, models, providers);
              const rawModel = isLead
                ? seat?.model || defaultModel(seat?.provider || Provider.CLAUDE)
                : seat && seat.provider !== Provider.UNSPECIFIED
                  ? seat.model || defaultModel(seat.provider)
                  : "";
              const canonical = canonicalModelId(rawModel);
              const value = isLead
                ? options.some((m) => m.id === canonical)
                  ? canonical
                  : (options[0]?.id ?? canonical)
                : options.some((m) => m.id === canonical)
                  ? canonical
                  : "";
              const unsignedForRow = providers.filter(
                (p) => spec.providers.includes(p.provider) && !p.signedIn,
              );
              const leadUnavailableForRow = isLead
                ? providers.filter(
                    (p) =>
                      spec.providers.includes(p.provider) &&
                      p.signedIn &&
                      !p.leadSupported,
                  )
                : [];
              const label = t(spec.labelKey);
              const detail = t(spec.detailKey);
              // In its terminal, the lead resumes its recorded session: when this row names another agent, it starts
              // there only on demand.
              const leadAgent = isLead
                ? (options.find((m) => m.id === value)?.provider ??
                  seat?.provider)
                : undefined;
              const otherLead =
                isLead &&
                !driven &&
                recorded !== undefined &&
                leadAgent !== undefined &&
                leadAgent !== Provider.UNSPECIFIED &&
                leadAgent !== recorded
                  ? leadAgent
                  : undefined;
              return (
                <div className="setting-row summoning-row" key={spec.key}>
                  <div>
                    <strong>{label}</strong>
                    <p>{detail}</p>
                    {leadUnavailableForRow.length > 0 && (
                      <div className="summoning-row-notes">
                        {leadUnavailableForRow.map((p) => (
                          <span
                            key={p.provider}
                            className="summoning-provider unsigned lead-unavailable"
                            title={p.leadUnavailableReason}
                          >
                            {providerName(p.provider)}
                          </span>
                        ))}
                      </div>
                    )}
                    {otherLead !== undefined && (
                      <div className="summoning-lead-start">
                        <p>
                          {t("summoning.lead_recorded", {
                            agent: providerName(recorded!),
                          })}
                        </p>
                        <button
                          type="button"
                          className="button secondary small"
                          title={t("summoning.lead_start_detail")}
                          disabled={busy}
                          onClick={() => startLead(otherLead)}
                        >
                          {t("summoning.lead_start", {
                            agent: providerName(otherLead),
                          })}
                        </button>
                      </div>
                    )}
                    {isLead && leadNote && (
                      <p className="summoning-lead-note" role="status">
                        {leadNote}
                      </p>
                    )}
                  </div>
                  <select
                    className="summoning-select"
                    aria-label={label}
                    value={value}
                    disabled={
                      busy || !loaded || (isLead && options.length === 0)
                    }
                    onChange={(e) => apply(spec, e.target.value, options)}
                  >
                    {!isLead && (
                      <option value="">{t("summoning.same_as_lead")}</option>
                    )}
                    {options.map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.label}
                      </option>
                    ))}
                    {leadUnavailableForRow.map((p) => (
                      <option
                        key={`lead-unavail-${p.provider}`}
                        disabled
                        title={p.leadUnavailableReason}
                      >
                        {t("summoning.lead_unavailable_option", {
                          provider: providerName(p.provider),
                          reason: p.leadUnavailableReason || "",
                        })}
                      </option>
                    ))}
                    {unsignedForRow.map((p) => (
                      <option
                        key={`unsigned-${p.provider}`}
                        disabled
                        title={`${t("summoning.sign_in_with")} ${p.loginCommand}`}
                      >
                        {t("summoning.sign_in_option", {
                          provider: providerName(p.provider),
                          command: p.loginCommand,
                        })}
                      </option>
                    ))}
                  </select>
                </div>
              );
            })}
          </section>
          <section className="config-box summoning-bulk">
            <UpdateModelsSection
              models={selectableModels}
              onApplied={(res) => {
                if (res.circle.length > 0) {
                  setRows((prev) =>
                    prev.map(
                      (r) => res.circle.find((c) => c.kind === r.kind) ?? r,
                    ),
                  );
                }
              }}
            />
          </section>
        </div>
      </div>
    </div>
  );
}

export function UpdateModelsSection({
  wishId,
  projectId,
  models: givenModels,
  onApplied,
}: {
  wishId?: string;
  projectId?: string;
  models?: readonly CircleModel[];
  onApplied?: (res: TaskServiceUpdateModelsResponse) => void;
}) {
  const djinn = useDjinn();
  const models = givenModels ?? catalogFromPriceTable();
  const [selectedId, setSelectedId] = useState("");
  const [now, setNow] = useState(false);
  const [preview, setPreview] =
    useState<TaskServiceUpdateModelsResponse | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [appliedSummary, setAppliedSummary] = useState("");

  useEffect(() => {
    if (!djinn || !selectedId) {
      setPreview(null);
      return;
    }
    const entry = models.find((m) => m.id === selectedId);
    if (!entry) return;
    let live = true;
    setError("");
    djinn.clients.tasks
      .updateModels({
        wishId: wishId ?? "",
        projectId: projectId ?? "",
        provider: entry.provider,
        model: entry.id,
        now,
        yes: false,
      })
      .then((res) => {
        if (!live) return;
        setPreview(res);
      })
      .catch((err) => {
        if (!live) return;
        setError(message(err));
      });
    return () => {
      live = false;
    };
  }, [djinn, models, now, projectId, selectedId, wishId]);

  const handleApply = () => {
    if (!djinn || !selectedId || busy) return;
    const entry = models.find((m) => m.id === selectedId);
    if (!entry) return;
    setBusy(true);
    setError("");
    djinn.clients.tasks
      .updateModels({
        wishId: wishId ?? "",
        projectId: projectId ?? "",
        provider: entry.provider,
        model: entry.id,
        now,
        yes: true,
      })
      .then((res) => {
        setPreview(res);
        setAppliedSummary(res.text);
        void djinn.store.changed(wishId ?? "", [Change.TASK, Change.PROJECT]);
        onApplied?.(res);
      })
      .catch((err) => setError(message(err)))
      .finally(() => setBusy(false));
  };

  return (
    <div className="update-models-section">
      <h3>{t("summoning.bulk_title")}</h3>
      <p className="muted-text">{t("summoning.bulk_detail")}</p>
      <div className="branch-choice-row">
        <select
          className="summoning-select"
          aria-label={t("summoning.bulk_model_label")}
          value={selectedId}
          disabled={busy}
          onChange={(e) => {
            setAppliedSummary("");
            setSelectedId(e.target.value);
          }}
        >
          <option value="">{t("summoning.bulk_choose")}</option>
          {models.map((m) => (
            <option key={m.id} value={m.id}>
              {m.label}
            </option>
          ))}
        </select>
        <label className="summoning-bulk-now">
          <input
            type="checkbox"
            checked={now}
            disabled={busy}
            onChange={(e) => {
              setAppliedSummary("");
              setNow(e.target.checked);
            }}
          />
          <span>{t("summoning.bulk_now_label")}</span>
        </label>
        <button
          type="button"
          className="button primary"
          disabled={!selectedId || busy || !preview}
          onClick={handleApply}
        >
          {t("summoning.bulk_apply")}
        </button>
      </div>
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      {preview && (
        <div className="update-models-preview" role="status">
          <p className="notice-detail">{appliedSummary || preview.text}</p>
          {preview.tasks.length > 0 && (
            <ul className="change-lines">
              {preview.tasks.map((task) => (
                <li key={task.id}>
                  <code>{task.code}</code> {task.title} →{" "}
                  {providerName(task.provider)} ({task.model})
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
