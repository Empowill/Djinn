// The Summoning Circle (W383, W400): one row per kind of worker, global to Djinn (every project).
// Each model is listed once by its human-readable label while Djinn stores and sends only its canonical model ID.
// Every row except the lead offers "Same as lead" as the default and follows the lead's choice when it changes.
import { useEffect, useState } from "react";

import {
  Change,
  Provider,
  WorkerKind,
  type WorkerSeat,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useDjinn } from "./data/djinn";
import { defaultModel } from "./data/format";
import { ModalFrame } from "./frame";
import { type TextKey, t } from "./i18n";
import "./project-panel.css";

export interface CircleModel {
  readonly id: string;
  readonly label: string;
  readonly provider: Provider;
}

export const CIRCLE_MODELS: readonly CircleModel[] = [
  {
    id: "claude-sonnet-5-5",
    label: "Claude Sonnet 5.5",
    provider: Provider.CLAUDE,
  },
  {
    id: "claude-opus-5-8",
    label: "Claude Opus 5.8",
    provider: Provider.CLAUDE,
  },
  {
    id: "claude-haiku-5-5",
    label: "Claude Haiku 5.5",
    provider: Provider.CLAUDE,
  },
  {
    id: "gpt-5.5",
    label: "GPT-5.5",
    provider: Provider.CODEX,
  },
  {
    id: "gpt-5.5-codex",
    label: "GPT-5.5 Codex",
    provider: Provider.CODEX,
  },
  {
    id: "gpt-5.5-mini",
    label: "GPT-5.5 Mini",
    provider: Provider.CODEX,
  },
  {
    id: "gemini-4-argon-high",
    label: "Gemini 4 Argon (High)",
    provider: Provider.ANTIGRAVITY,
  },
  {
    id: "gemini-3.8-flash-high",
    label: "Gemini 3.8 Flash (High)",
    provider: Provider.ANTIGRAVITY,
  },
  {
    id: "gemini-3.8-pro-high",
    label: "Gemini 3.8 Pro (High)",
    provider: Provider.ANTIGRAVITY,
  },
];

const MODEL_ALIASES: Readonly<Record<string, string>> = {
  sonnet: "claude-sonnet-5-5",
  "claude sonnet 5.5": "claude-sonnet-5-5",
  opus: "claude-opus-5-8",
  "claude opus 5.8": "claude-opus-5-8",
  haiku: "claude-haiku-5-5",
  "claude haiku 5.5": "claude-haiku-5-5",
  "gpt-5.5": "gpt-5.5",
  "gpt-5.5 codex": "gpt-5.5-codex",
  "gpt-5.5 mini": "gpt-5.5-mini",
  "gemini 4 argon (high)": "gemini-4-argon-high",
  "gemini 4 argon": "gemini-4-argon-high",
  argon: "gemini-4-argon-high",
  "gemini 3.8 flash (high)": "gemini-3.8-flash-high",
  "gemini 3.8 flash": "gemini-3.8-flash-high",
  flash: "gemini-3.8-flash-high",
  "gemini 3.8 pro (high)": "gemini-3.8-pro-high",
  "gemini 3.8 pro": "gemini-3.8-pro-high",
  pro: "gemini-3.8-pro-high",
};

export function canonicalModelId(model: string): string {
  const trimmed = model.trim();
  if (!trimmed) return "";
  return MODEL_ALIASES[trimmed.toLowerCase()] ?? trimmed;
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
    providers: [Provider.CLAUDE, Provider.CODEX],
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

function modelsForRow(
  spec: CircleRowSpec,
  currentId: string,
  currentProvider: Provider,
): CircleModel[] {
  const allowed = CIRCLE_MODELS.filter((m) =>
    spec.providers.includes(m.provider),
  );
  if (currentId && !allowed.some((m) => m.id === currentId)) {
    const known = CIRCLE_MODELS.find((m) => m.id === currentId);
    return [
      known ?? {
        id: currentId,
        label: currentId,
        provider: currentProvider || spec.providers[0],
      },
      ...allowed,
    ];
  }
  return allowed;
}

export function SummoningCircle({
  initialRows,
  onChoose,
  onClose,
}: {
  initialRows?: WorkerSeat[];
  onChoose?: (req: {
    kind: WorkerKind;
    provider: Provider;
    model: string;
  }) => Promise<WorkerSeat[]>;
  onClose: () => void;
}) {
  const djinn = useDjinn();
  const [rows, setRows] = useState<WorkerSeat[]>(initialRows ?? []);
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
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <section className="config-box summoning-list">
        {CIRCLE_ROWS.map((spec) => {
          const seat = rows.find((r) => r.kind === spec.kind);
          const isLead = spec.kind === WorkerKind.LEAD;
          const rawModel = isLead
            ? seat?.model || defaultModel(seat?.provider || Provider.CLAUDE)
            : seat && seat.provider !== Provider.UNSPECIFIED
              ? seat.model || defaultModel(seat.provider)
              : "";
          const value = canonicalModelId(rawModel);
          const options = modelsForRow(
            spec,
            value,
            seat?.provider ?? Provider.UNSPECIFIED,
          );
          const label = t(spec.labelKey);
          const detail = t(spec.detailKey);
          return (
            <div className="setting-row summoning-row" key={spec.key}>
              <div>
                <strong>{label}</strong>
                <p>{detail}</p>
              </div>
              <select
                className="summoning-select"
                aria-label={label}
                value={value}
                disabled={busy || !loaded}
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
              </select>
            </div>
          );
        })}
      </section>
    </ModalFrame>
  );
}
