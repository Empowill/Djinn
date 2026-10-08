// The window's dialogs on the services: make a wish, add a project, look at a project and its skills, and the
// settings that stay on this page (the language).
import { ArrowRight, FolderOpen, Terminal } from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";

import type { Project, Skill } from "../gen/ts/plan/v1/plan_pb";
import type { UiServiceGetEnvironmentResponse } from "../gen/ts/ui/v1/ui_pb";
import { message } from "./data/client";
import { useClients } from "./data/djinn";
import { MAX_ACTIVE } from "./data/format";
import { Brand, ModalFrame } from "./frame";
import {
  chosenLanguage,
  languageName,
  languages,
  setLanguage,
  systemLanguage,
  t,
} from "./i18n";
import { type Theme, chosenTheme, setTheme } from "./theme";

// MakeWish makes a wish (WishService.Make): a sentence and the projects it works on. With three wishes active, it
// is made paused, as the lamp would refuse a fourth.
export function MakeWish({
  projects,
  active,
  onMade,
  onClose,
}: {
  projects: Project[];
  active: number;
  onMade: (wishId: string) => void;
  onClose: () => void;
}) {
  const clients = useClients();
  const full = active >= MAX_ACTIVE;
  const [title, setTitle] = useState("");
  const [chosen, setChosen] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    try {
      const res = await clients.wishes.make({
        title: title.trim(),
        projectIds: chosen,
        paused: full,
      });
      onMade(res.wish?.id ?? "");
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <ModalFrame
      title={t("make.title")}
      eyebrow={t("make.eyebrow")}
      onClose={onClose}
    >
      <form className="form-fields" onSubmit={(e) => void submit(e)}>
        <label>
          <span>{t("make.what")}</span>
          <input
            autoFocus
            value={title}
            maxLength={500}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={t("make.what_placeholder")}
          />
        </label>
        {projects.length > 0 && (
          <fieldset className="wish-projects">
            <legend>{t("make.projects")}</legend>
            {projects.map((project) => (
              <label key={project.id} className="wish-project-choice">
                <input
                  type="checkbox"
                  checked={chosen.includes(project.id)}
                  onChange={(e) =>
                    setChosen(
                      e.target.checked
                        ? [...chosen, project.id]
                        : chosen.filter((id) => id !== project.id),
                    )
                  }
                />
                <FolderOpen size={14} />
                <span>{project.name}</span>
              </label>
            ))}
          </fieldset>
        )}
        <p className="form-tip">
          {full
            ? t("make.full", { max: MAX_ACTIVE })
            : t("make.tip", { command: 'djinn wish make "…"' })}
        </p>
        {error && <p className="login-message">{error}</p>}
        <div className="modal-footer">
          <button type="button" className="button secondary" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button
            type="submit"
            className="button accent"
            disabled={busy || !title.trim()}
          >
            {full ? t("make.submit_paused") : t("make.submit")}
            <ArrowRight size={14} />
          </button>
        </div>
      </form>
    </ModalFrame>
  );
}

// AddProject adds a folder as a project (ProjectService.Add): it is indexed in seconds, Git or not.
export function AddProject({
  onAdded,
  onClose,
}: {
  onAdded: (project: Project) => void;
  onClose: () => void;
}) {
  const clients = useClients();
  const [directory, setDirectory] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    try {
      const res = await clients.projects.add({
        directory: directory.trim(),
        name: name.trim(),
      });
      if (res.project) onAdded(res.project);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <ModalFrame
      title={t("project.add_title")}
      eyebrow={t("project.eyebrow")}
      onClose={onClose}
    >
      <form className="form-fields" onSubmit={(e) => void submit(e)}>
        <label>
          <span>{t("project.folder")}</span>
          <input
            autoFocus
            value={directory}
            onChange={(e) => setDirectory(e.target.value)}
            placeholder={t("project.folder_placeholder")}
            spellCheck={false}
          />
        </label>
        <label>
          <span>
            {t("project.name")}{" "}
            <span className="label-optional">{t("project.optional")}</span>
          </span>
          <input
            value={name}
            maxLength={100}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <p className="form-tip">
          {t("project.tip", { command: "djinn project add <folder>" })}
        </p>
        {error && <p className="login-message">{error}</p>}
        <div className="modal-footer">
          <button type="button" className="button secondary" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button
            type="submit"
            className="button accent"
            disabled={busy || !directory.trim()}
          >
            {t("project.add")}
            <ArrowRight size={14} />
          </button>
        </div>
      </form>
    </ModalFrame>
  );
}

// ProjectPanel shows a project as djinn knows it, and its skills (SkillService.List): its own and those it summons.
export function ProjectPanel({
  project,
  onClose,
}: {
  project: Project;
  onClose: () => void;
}) {
  const clients = useClients();
  const [skills, setSkills] = useState<Skill[] | undefined>();
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    clients.skills
      .list({ project: project.id })
      .then((res) => live && setSkills(res.skills))
      .catch((err) => live && setError(message(err)));
    return () => {
      live = false;
    };
  }, [clients, project.id]);
  return (
    <ModalFrame
      title={project.name}
      eyebrow={t("project.eyebrow")}
      onClose={onClose}
      wide
    >
      <div className="setting-row">
        <div>
          <strong>{t("project.folder")}</strong>
          <p>{project.directory || t("project.no_folder_detail")}</p>
        </div>
        <span className="badge muted">
          {project.git ? t("project.git") : t("project.not_git")}
        </span>
      </div>
      {project.remote && (
        <div className="setting-row">
          <div>
            <strong>{t("project.remote")}</strong>
            <p>{project.remote}</p>
          </div>
        </div>
      )}
      <div className="settings-divider" />
      <h3>{t("project.skills")}</h3>
      {error && <p className="login-message">{error}</p>}
      {skills === undefined && !error && (
        <p className="muted-text">{t("common.loading")}</p>
      )}
      {skills?.length === 0 && (
        <p className="muted-text">{t("project.no_skills")}</p>
      )}
      {skills?.map((skill) => (
        <div className="setting-row" key={`${skill.source}/${skill.name}`}>
          <div>
            <strong>{skill.name}</strong>
            <p>{skill.missing || skill.description}</p>
          </div>
          {skill.source && (
            <span className="badge muted">
              {t("project.summoned", { source: skill.source })}
            </span>
          )}
        </div>
      ))}
    </ModalFrame>
  );
}

// Settings: the agents djinn finds on this machine, and the language of the page.
export function Settings({ onClose }: { onClose: () => void }) {
  const clients = useClients();
  const [env, setEnv] = useState<UiServiceGetEnvironmentResponse>();
  const [theme, setThemeState] = useState<Theme>(chosenTheme);
  useEffect(() => {
    let live = true;
    clients.ui
      .getEnvironment({})
      .then((res) => live && setEnv(res))
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [clients]);
  return (
    <ModalFrame
      title={t("app.connections")}
      eyebrow={t("settings.eyebrow")}
      onClose={onClose}
      wide
    >
      <div className="provider-settings">
        {env?.providers.map((p) => (
          <div className="provider-setting" key={p.id}>
            <span className="provider-glyph">
              {p.id === "codex" ? "⬡" : "✳"}
            </span>
            <div>
              <h3>{p.name}</h3>
              <span className="muted-text">
                {p.available ? p.command : t("settings.cli_missing")}
              </span>
            </div>
            <span
              className={`status-dot ${p.available ? "green" : "neutral"}`}
            />
          </div>
        ))}
      </div>
      <div className="cli-help">
        <Terminal size={16} />
        <div>
          <strong>{t("settings.install_cli")}</strong>
          <p>
            {t("settings.cli_codex")} <code>npm install -g @openai/codex</code>
            <br />
            {t("settings.cli_claude")}{" "}
            <code>npm install -g @anthropic-ai/claude-code</code>
          </p>
        </div>
      </div>
      <div className="settings-divider" />
      <div className="setting-row">
        <div>
          <strong>{t("settings.language")}</strong>
          <p>{t("settings.language_detail")}</p>
        </div>
        <select
          value={chosenLanguage()}
          onChange={(e) => setLanguage(e.target.value)}
        >
          <option value="">
            {t("settings.language_system", {
              language: languageName(systemLanguage()),
            })}
          </option>
          {languages.map((code) => (
            <option key={code} value={code}>
              {languageName(code)}
            </option>
          ))}
        </select>
      </div>
      <div className="setting-row">
        <div>
          <strong>{t("settings.theme")}</strong>
          <p>{t("settings.theme_detail")}</p>
        </div>
        <select
          value={theme}
          aria-label={t("settings.theme")}
          onChange={(e) => {
            const next = e.target.value as Theme;
            setThemeState(next);
            setTheme(next);
          }}
        >
          <option value="">{t("settings.theme_dark")}</option>
          <option value="light">{t("settings.theme_light")}</option>
          <option value="system">{t("settings.theme_system")}</option>
        </select>
      </div>
      <div className="settings-foot">
        <Brand small />
        <span>Djinn {env?.version}</span>
      </div>
    </ModalFrame>
  );
}
