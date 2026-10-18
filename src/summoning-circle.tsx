// The Summoning Circle: choose per project, in one place, which agent and model answer new workers and question
// investigations. Writes the developer's own settings file of the project (ProjectService.SetDefaults), never the
// repository's.
import { useEffect, useState } from "react";

import {
  Change,
  type Project,
  type ProjectServiceShowResponse,
  type ProjectSetting,
  Provider,
  SettingSource,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useStore } from "./data/djinn";
import { defaultModel } from "./data/format";
import { ModalFrame } from "./frame";
import { t } from "./i18n";
import { From } from "./project-panel";
import "./project-panel.css";

const PROVIDERS: readonly {
  id: string;
  enum: Provider;
  label: string;
}[] = [
  { id: "claude", enum: Provider.CLAUDE, label: "Claude Code" },
  { id: "codex", enum: Provider.CODEX, label: "Codex" },
  { id: "antigravity", enum: Provider.ANTIGRAVITY, label: "Antigravity" },
];

export const KNOWN_MODELS: Readonly<Record<string, readonly string[]>> = {
  claude: [
    "claude-sonnet-5-5",
    "claude-opus-5-8",
    "claude-haiku-5-5",
    "sonnet",
    "opus",
    "haiku",
  ],
  codex: ["gpt-5.5", "gpt-5.5-codex", "gpt-5.5-mini"],
  antigravity: [
    "gemini-3.8-flash-high",
    "Gemini 4 Argon (High)",
    "gemini-4-argon-high",
    "gemini-3.8-pro-high",
  ],
};

function settingOf(
  shown: ProjectServiceShowResponse | undefined,
  name: string,
): ProjectSetting | undefined {
  return shown?.settings.find((s) => s.name === name);
}

function providerEnumOf(id: string): Provider {
  return PROVIDERS.find((p) => p.id === id)?.enum ?? Provider.CLAUDE;
}

function modelOptionsFor(provider: string, current: string): string[] {
  const known = KNOWN_MODELS[provider] ?? [];
  if (current && !known.includes(current)) {
    return [current, ...known];
  }
  return [...known];
}

export function SummoningCircle({
  projects,
  onClose,
}: {
  projects: Project[];
  onClose: () => void;
}) {
  return (
    <ModalFrame
      eyebrow={t("summoning.eyebrow")}
      title={t("summoning.title")}
      onClose={onClose}
      wide
    >
      <p className="panel-lead">
        {t("summoning.subtitle")} {t("summoning.detail")}
      </p>
      {projects.length === 0 ? (
        <div className="config-box">
          <p className="muted">{t("summoning.empty")}</p>
        </div>
      ) : (
        <div className="summoning-list">
          {projects.map((project) => (
            <SummoningProject key={project.id} project={project} />
          ))}
        </div>
      )}
    </ModalFrame>
  );
}

export function SummoningProject({
  project,
  initialShown,
}: {
  project: Project;
  initialShown?: ProjectServiceShowResponse;
}) {
  const clients = useClients();
  const store = useStore();
  const [shown, setShown] = useState<ProjectServiceShowResponse | undefined>(
    initialShown,
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (initialShown) return;
    let live = true;
    clients.projects
      .show({ project: project.id })
      .then((res) => live && setShown(res))
      .catch((err) => live && setError(message(err)));
    return () => {
      live = false;
    };
  }, [clients, initialShown, project.id]);

  const providerSetting = settingOf(shown, "provider");
  const modelSetting = settingOf(shown, "model");
  const questionModelSetting = settingOf(shown, "question_model");

  const provider = providerSetting?.value || "claude";
  const providerSource = providerSetting?.source ?? SettingSource.DEFAULT;
  const model = modelSetting?.value ?? "";
  const modelSource = modelSetting?.source ?? SettingSource.DEFAULT;
  const questionModel = questionModelSetting?.value ?? "";
  const questionModelSource =
    questionModelSetting?.source ?? SettingSource.DEFAULT;

  const hasDeveloper =
    providerSource === SettingSource.DEVELOPER ||
    modelSource === SettingSource.DEVELOPER ||
    questionModelSource === SettingSource.DEVELOPER;

  const defModel = defaultModel(providerEnumOf(provider)) || "default";
  const workerModels = modelOptionsFor(provider, model);
  const questionModels = modelOptionsFor(provider, questionModel);

  const apply = (req: {
    provider?: Provider;
    model?: string;
    questionModel?: string;
    unset?: boolean;
  }) => {
    setBusy(true);
    setError("");
    clients.projects
      .setDefaults({ project: project.id, ...req })
      .then((res) => {
        setShown((prev) =>
          prev
            ? {
                ...prev,
                settings: res.settings,
                developerFile: res.developerFile,
              }
            : ({
                settings: res.settings,
                developerFile: res.developerFile,
              } as ProjectServiceShowResponse),
        );
        void store.changed("", [Change.PROJECT]);
      })
      .catch((err) => setError(message(err)))
      .finally(() => setBusy(false));
  };

  return (
    <section className="config-box summoning-project" aria-label={project.name}>
      <div className="summoning-project-head">
        <h3>
          <strong>{project.name}</strong>
          {project.directory && (
            <span className="config-from">{project.directory}</span>
          )}
        </h3>
        {hasDeveloper && (
          <button
            type="button"
            className="button small secondary"
            disabled={busy}
            title={t("summoning.unset_detail")}
            onClick={() => apply({ unset: true })}
          >
            {t("summoning.unset")}
          </button>
        )}
      </div>
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <div className="setting-row summoning-row">
        <div>
          <strong>
            {t("summoning.provider")}
            <From source={providerSource} shown={shown} />
          </strong>
        </div>
        <select
          className="summoning-select"
          aria-label={`${project.name} ${t("summoning.provider")}`}
          value={provider}
          disabled={busy || !shown}
          onChange={(e) => apply({ provider: providerEnumOf(e.target.value) })}
        >
          {PROVIDERS.map((p) => (
            <option key={p.id} value={p.id}>
              {p.label}
            </option>
          ))}
        </select>
      </div>
      <div className="setting-row summoning-row">
        <div>
          <strong>
            {t("summoning.model")}
            <From source={modelSource} shown={shown} />
          </strong>
        </div>
        <select
          className="summoning-select"
          aria-label={`${project.name} ${t("summoning.model")}`}
          value={model}
          disabled={busy || !shown}
          onChange={(e) => apply({ model: e.target.value })}
        >
          <option value="">
            {t("summoning.default_model", { model: defModel })}
          </option>
          {workerModels.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
      </div>
      <div className="setting-row summoning-row">
        <div>
          <strong>
            {t("summoning.question_model")}
            <From source={questionModelSource} shown={shown} />
          </strong>
        </div>
        <select
          className="summoning-select"
          aria-label={`${project.name} ${t("summoning.question_model")}`}
          value={questionModel}
          disabled={busy || !shown}
          onChange={(e) => apply({ questionModel: e.target.value })}
        >
          <option value="">{t("summoning.same_as_worker")}</option>
          {questionModels.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
      </div>
    </section>
  );
}
