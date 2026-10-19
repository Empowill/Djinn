// The Summoning Circle (W383, W400, W417): one row per kind of worker, global to Djinn (every project).
// Models come from the server (live agent discovery + shared internal/usage/prices.yaml table), never a hand-written UI list.
// Providers not signed in on this machine show greyed with their sign-in command and are never offered as choices.
import { useEffect, useMemo, useState } from "react";

import {
  Change,
  Provider,
  type TaskServiceUpdateModelsResponse,
  WorkerKind,
  type WorkerSeat,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useDjinn } from "./data/djinn";
import { defaultModel } from "./data/format";
import { catalogFromPriceTable } from "./data/prices";
import { ModalFrame } from "./frame";
import { type TextKey, t } from "./i18n";
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

export function SummoningCircle({
  initialRows,
  initialModels,
  initialProviders,
  onChoose,
  onClose,
}: {
  initialRows?: WorkerSeat[];
  initialModels?: readonly CircleModel[];
  initialProviders?: readonly CircleProvider[];
  onChoose?: (req: {
    kind: WorkerKind;
    provider: Provider;
    model: string;
  }) => Promise<WorkerSeat[]>;
  onClose: () => void;
}) {
  const djinn = useDjinn();
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

  return (
    <ModalFrame
      eyebrow={t("summoning.eyebrow")}
      title={t("summoning.title")}
      onClose={onClose}
      wide
    >
      <p className="panel-lead">
        {t("summoning.subtitle")}. {t("summoning.detail")}
      </p>
      <div className="summoning-providers">
        {providers.map((p) => {
          const name = providerName(p.provider);
          if (p.signedIn) {
            return (
              <span key={p.provider} className="summoning-provider signed-in">
                {name}
              </span>
            );
          }
          return (
            <span
              key={p.provider}
              className="summoning-provider unsigned"
              title={`${t("summoning.sign_in_with")} ${p.loginCommand}`}
            >
              {name} · {t("summoning.sign_in_with")}{" "}
              <code>{p.loginCommand}</code>
            </span>
          );
        })}
      </div>
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
              </div>
              <select
                className="summoning-select"
                aria-label={label}
                value={value}
                disabled={busy || !loaded || (isLead && options.length === 0)}
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
                prev.map((r) => res.circle.find((c) => c.kind === r.kind) ?? r),
              );
            }
          }}
        />
      </section>
    </ModalFrame>
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
