// The window's dialogs on the services: make a wish, add a project, look at a project and its skills, and the
// settings that stay on this page (the language).
import { ArrowRight, FolderOpen } from "lucide-react";
import { type FormEvent, useEffect, useId, useState } from "react";

import { type Project, Provider, type Skill } from "../gen/ts/plan/v1/plan_pb";
import {
  type Provider as AgentEnvironment,
  NotificationAccess,
  ProviderState,
  type UiServiceGetEnvironmentResponse,
} from "../gen/ts/ui/v1/ui_pb";
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
  type TextKey,
  t,
} from "./i18n";
import { defaultProvider, setDefaultProvider, wishProviders } from "./provider";
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
  const [provider, setProvider] = useState<Provider>(defaultProvider);
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
        provider,
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
        <label>
          <span>{t("make.provider")}</span>
          <select
            value={provider}
            onChange={(e) => setProvider(Number(e.target.value) as Provider)}
          >
            {wishProviders.map((p) => (
              <option key={p.provider} value={p.provider}>
                {p.name}
              </option>
            ))}
          </select>
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
  // Only the native window has a folder dialog; the browser types the path.
  const [folderDialog, setFolderDialog] = useState(false);
  useEffect(() => {
    let live = true;
    clients.ui
      .getEnvironment({})
      .then((res) => live && setFolderDialog(res.folderDialog))
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [clients]);
  const choose = async () => {
    try {
      const res = await clients.ui.chooseDirectory({
        title: t("project.choose_title"),
        directory: directory.trim(),
      });
      if (res.directory) setDirectory(res.directory);
    } catch (err) {
      setError(message(err));
    }
  };
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
        <FolderField
          value={directory}
          onChange={setDirectory}
          onChoose={folderDialog ? () => void choose() : undefined}
        />
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

// FolderField is the folder of a new project, typed in; with onChoose, a button next to it opens the system's
// folder dialog (the native window only).
export function FolderField({
  value,
  onChange,
  onChoose,
}: {
  value: string;
  onChange: (directory: string) => void;
  onChoose?: () => void;
}) {
  // The button stays out of the label: the field's name is the folder's alone.
  const id = useId();
  return (
    <div>
      <label htmlFor={id}>
        <span>{t("project.folder")}</span>
      </label>
      <div className="folder-field">
        <input
          id={id}
          autoFocus
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={t("project.folder_placeholder")}
          spellCheck={false}
        />
        {onChoose && (
          <button type="button" className="button secondary" onClick={onChoose}>
            <FolderOpen size={14} />
            {t("project.choose")}
          </button>
        )}
      </div>
    </div>
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

// AgentSetting is one agent: installed or not, its version, signed in or not, and the command that takes it further.
function AgentSetting({ agent }: { agent: AgentEnvironment }) {
  const state = agentStates[agent.state] ?? agentStates[ProviderState.UNKNOWN];
  const next =
    agent.state === ProviderState.MISSING
      ? { label: t("settings.agent_install"), command: agent.installCommand }
      : agent.state === ProviderState.SIGNED_OUT
        ? { label: t("settings.agent_login"), command: agent.loginCommand }
        : agent.state === ProviderState.UNKNOWN && agent.loginCommand
          ? {
              label: t("settings.agent_login_unknown"),
              command: agent.loginCommand,
            }
          : undefined;
  return (
    <div className="provider-setting">
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
        {next?.command && (
          <small>
            {next.label} <code>{next.command}</code>
          </small>
        )}
      </div>
      <span className="status-text">
        <span className={`status-dot ${state.dot}`} />
        {t(state.label)}
      </span>
    </div>
  );
}

// NotificationSetting: whether the system shows Djinn's notifications, and the way to allow them. macOS asks once;
// after a refusal only its settings change it.
function NotificationSetting() {
  const clients = useClients();
  const [state, setState] = useState<{
    access: NotificationAccess;
    settings: boolean;
  }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    clients.ui
      .getNotifications({})
      .then((res) => live && setState(res))
      .catch((err) => live && setError(message(err)));
    return () => {
      live = false;
    };
  }, [clients]);
  const openSettings = () =>
    void clients.ui
      .openNotificationSettings({})
      .catch((err) => setError(message(err)));
  const request = async () => {
    setBusy(true);
    setError("");
    try {
      const res = await clients.ui.requestNotifications({});
      setState(res);
      if (res.access === NotificationAccess.DENIED && res.settings)
        openSettings();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };
  const access = state?.access ?? NotificationAccess.UNSPECIFIED;
  const status =
    access === NotificationAccess.ALLOWED
      ? t("settings.notifications_allowed")
      : access === NotificationAccess.DENIED
        ? t("settings.notifications_denied")
        : access === NotificationAccess.UNAVAILABLE
          ? t("settings.notifications_unavailable")
          : "";
  return (
    <div className="setting-row notification-setting">
      <div>
        <strong>{t("settings.notifications")}</strong>
        <p>{t("settings.notifications_detail")}</p>
        {status && (
          <p>
            <span
              className={`status-dot ${access === NotificationAccess.ALLOWED ? "green" : "neutral"}`}
            />{" "}
            {status}
          </p>
        )}
        {error && <p role="alert">{error}</p>}
      </div>
      <div className="notification-actions">
        {access === NotificationAccess.DENIED && (
          <button
            type="button"
            className="button accent"
            disabled={busy}
            onClick={() => void request()}
          >
            {t("settings.notifications_request")}
          </button>
        )}
        {state?.settings && (
          <button
            type="button"
            className="button secondary"
            onClick={openSettings}
          >
            {t("settings.notifications_open")}
          </button>
        )}
      </div>
    </div>
  );
}

// Settings: the agents djinn finds on this machine, and the language of the page.
export function Settings({ onClose }: { onClose: () => void }) {
  const clients = useClients();
  const [env, setEnv] = useState<UiServiceGetEnvironmentResponse>();
  const [theme, setThemeState] = useState<Theme>(chosenTheme);
  useEffect(() => {
    let live = true;
    // Checking the agents runs each one's status command: it takes up to a few seconds.
    clients.ui
      .getEnvironment({ agents: true })
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
        {env === undefined && (
          <div className="provider-setting">
            <span className="muted-text">{t("settings.agent_checking")}</span>
          </div>
        )}
        {env?.providers.map((p) => (
          <AgentSetting agent={p} key={p.id} />
        ))}
      </div>
      <div className="settings-divider" />
      <NotificationSetting />
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
      <DefaultProviderSetting />
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

// DefaultProviderSetting chooses the agent a new wish is made with, ahead in MakeWish.
function DefaultProviderSetting() {
  const [provider, setProvider] = useState<Provider>(defaultProvider);
  return (
    <div className="setting-row">
      <div>
        <strong>{t("settings.provider")}</strong>
        <p>{t("settings.provider_detail")}</p>
      </div>
      <select
        value={provider}
        aria-label={t("settings.provider")}
        onChange={(e) => {
          const next = Number(e.target.value) as Provider;
          setProvider(next);
          setDefaultProvider(next);
        }}
      >
        {wishProviders.map((p) => (
          <option key={p.provider} value={p.provider}>
            {p.name}
          </option>
        ))}
      </select>
    </div>
  );
}
