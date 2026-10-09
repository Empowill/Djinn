// The window's dialogs on the services: make a wish, add a project, look at a project and its skills, and the
// settings that stay on this page (the language).
import {
  ArrowLeft,
  ArrowRight,
  Check,
  FolderOpen,
  LoaderCircle,
  Search,
} from "lucide-react";
import {
  type FormEvent,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  Allowance,
  type Project,
  Provider,
  type Skill,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import { NotificationAccess, ProviderState } from "../gen/ts/ui/v1/ui_pb";
import {
  AgentSetupPanel,
  agentOf,
  agentStateText,
  needsSetup,
  useAgents,
} from "./agent-setup";
import { message } from "./data/client";
import { useClients } from "./data/djinn";
import { MAX_ACTIVE } from "./data/format";
import { Brand, ModalFrame } from "./frame";
import {
  chosenLanguage,
  language,
  languageName,
  languages,
  setLanguage,
  systemLanguage,
  t,
} from "./i18n";
import {
  defaultProvider,
  providerName,
  setDefaultProvider,
  wishProviders,
} from "./provider";
import { type Theme, chosenTheme, setTheme } from "./theme";
import { WishComposer } from "./wish-composer";
import { MAX_PROMPT_CHARACTERS, promptTooLong } from "./wish-composer-markdown";

// MakeWish sends the complete Markdown request. Naming the wish belongs to the server and its lead. With three
// wishes active, it is made paused, as the lamp would refuse a fourth.
export function MakeWish({
  projects,
  active,
  onMade,
  onClose,
  onBusyChange,
}: {
  projects: Project[];
  active: number;
  onMade: (wish: Wish) => void;
  onClose: () => void;
  onBusyChange?: (busy: boolean) => void;
}) {
  const clients = useClients();
  const full = active >= MAX_ACTIVE;
  const [prompt, setPrompt] = useState("");
  const [hasText, setHasText] = useState(false);
  const overLimit = useMemo(() => promptTooLong(prompt), [prompt]);
  const [allowance, setAllowance] = useState(Allowance.NONE);
  const [chosen, setChosen] = useState<string[]>([]);
  const [provider, setProvider] = useState<Provider>(defaultProvider);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submitting = useRef(false);
  const submissionFocus = useRef<HTMLElement | null>(null);
  const [search, setSearch] = useState("");
  const form = useRef<HTMLFormElement>(null);
  const page = useRef<HTMLElement>(null);
  const promptId = useId();
  const providerId = useId();
  const searchId = useId();
  const allowanceId = useId();
  // Whether the chosen agent can run the wish; the setup of the agents shows in place of the form, which keeps what
  // was typed.
  const agents = useAgents();
  const [setup, setSetup] = useState(false);
  const agent = agents.env?.providers.find((p) => p.id === agentOf[provider]);
  const previousFocus = useRef<HTMLElement | null>(
    document.activeElement as HTMLElement | null,
  );
  useEffect(() => {
    return () => {
      if (previousFocus.current?.isConnected)
        previousFocus.current.focus({ preventScroll: true });
    };
  }, []);
  useEffect(() => {
    if (setup)
      page.current?.querySelector<HTMLElement>("[data-setup-back]")?.focus();
    else document.getElementById(promptId)?.focus({ preventScroll: true });
  }, [setup, promptId]);
  useEffect(() => {
    if (busy) page.current?.focus({ preventScroll: true });
    else if (submissionFocus.current?.isConnected) {
      submissionFocus.current.focus({ preventScroll: true });
      submissionFocus.current = null;
    }
  }, [busy]);
  const filtered = projects.filter((project) =>
    `${project.name} ${project.directory}`
      .toLocaleLowerCase()
      .includes(search.trim().toLocaleLowerCase()),
  );
  const selected = chosen.filter((id) =>
    projects.some((project) => project.id === id),
  );
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (submitting.current || !hasText || !prompt.trim() || overLimit) return;
    submitting.current = true;
    submissionFocus.current = document.activeElement as HTMLElement | null;
    setBusy(true);
    onBusyChange?.(true);
    setError("");
    try {
      const res = await clients.wishes.make(
        {
          prompt: prompt.trim(),
          projectIds: selected,
          paused: full,
          provider,
          allowance: selected.length ? allowance : Allowance.NONE,
        },
        { headers: { "Accept-Language": language } },
      );
      if (res.wish) onMade(res.wish);
    } catch (err) {
      setError(message(err));
    } finally {
      submitting.current = false;
      setBusy(false);
      onBusyChange?.(false);
    }
  };
  return (
    <section
      ref={page}
      className="wish-creation"
      aria-labelledby={`${promptId}-heading`}
      aria-busy={busy}
      tabIndex={-1}
      onKeyDown={(event) => {
        // The agent setup terminal owns Escape and its other shell shortcuts.
        if (
          event.target instanceof HTMLElement &&
          event.target.closest(".xterm")
        )
          return;
        if (busy && event.key === "Tab") {
          event.preventDefault();
          event.currentTarget.focus();
        }
        if (event.defaultPrevented || event.nativeEvent.isComposing || busy)
          return;
        if (event.key === "Escape") {
          event.preventDefault();
          if (setup) setSetup(false);
          else onClose();
        }
        if (
          !setup &&
          event.key === "Enter" &&
          (event.metaKey || event.ctrlKey)
        ) {
          event.preventDefault();
          form.current?.requestSubmit();
        }
      }}
    >
      <header className="wish-creation-header">
        <button
          type="button"
          className="wish-creation-back"
          disabled={busy}
          onClick={() => (setup ? setSetup(false) : onClose())}
          data-setup-back={setup || undefined}
        >
          <ArrowLeft size={16} />
          {setup ? t("agents.back") : t("make.back")}
        </button>
        <span className="eyebrow">{t("make.eyebrow")}</span>
      </header>
      <div className="wish-creation-scroll">
        <div className="wish-creation-content">
          <div className="wish-creation-intro">
            <h1 id={`${promptId}-heading`}>
              {setup ? t("agents.title") : t("make.title")}
            </h1>
            <p>{setup ? t("make.draft_kept") : t("make.intro")}</p>
          </div>
          {setup && <AgentSetupPanel agents={agents} />}
          {/* Keep the editor mounted during setup: text, formatting, selection and undo history survive. */}
          <form
            ref={form}
            hidden={setup}
            className="wish-creation-form"
            onSubmit={(event) => void submit(event)}
            aria-busy={busy}
          >
            <div className="wish-creation-layout">
              <div className="wish-creation-request">
                <label
                  id={`${promptId}-label`}
                  htmlFor={promptId}
                  className="wish-creation-label"
                >
                  {t("make.what")}
                </label>
                <WishComposer
                  id={promptId}
                  describedBy={`${promptId}-help${overLimit ? ` ${promptId}-limit` : ""}`}
                  disabled={busy}
                  invalid={overLimit}
                  onChange={(markdown, hasText) => {
                    setPrompt(markdown);
                    setHasText(hasText);
                  }}
                />
                <p
                  id={`${promptId}-help`}
                  className="form-tip wish-composer-help"
                >
                  {t("make.editor_help")}
                </p>
                {overLimit && (
                  <p
                    id={`${promptId}-limit`}
                    className="wish-creation-error"
                    role="alert"
                  >
                    {t("make.prompt_too_long", {
                      max: MAX_PROMPT_CHARACTERS.toLocaleString(language),
                    })}
                  </p>
                )}
                <fieldset className="wish-creation-projects" disabled={busy}>
                  <legend>{t("make.projects")}</legend>
                  <p className="form-tip">{t("make.projects_detail")}</p>
                  {projects.length > 0 && (
                    <div className="wish-project-search">
                      <Search size={15} aria-hidden="true" />
                      <label
                        className="wish-creation-sr-only"
                        htmlFor={searchId}
                      >
                        {t("make.search_projects")}
                      </label>
                      <input
                        id={searchId}
                        type="search"
                        value={search}
                        onChange={(event) => setSearch(event.target.value)}
                        placeholder={t("make.search_projects")}
                      />
                    </div>
                  )}
                  <div className="wish-project-cards">
                    {filtered.map((project) => (
                      <label
                        key={project.id}
                        className={`wish-project-card ${selected.includes(project.id) ? "is-selected" : ""}`}
                      >
                        <input
                          type="checkbox"
                          aria-label={project.name}
                          aria-describedby={`${promptId}-project-${project.id}-folder`}
                          checked={selected.includes(project.id)}
                          onChange={(event) =>
                            setChosen(
                              event.target.checked
                                ? [...chosen, project.id]
                                : chosen.filter((id) => id !== project.id),
                            )
                          }
                        />
                        <FolderOpen size={20} aria-hidden="true" />
                        <span className="wish-project-card-copy">
                          <strong>{project.name}</strong>
                          <small
                            id={`${promptId}-project-${project.id}-folder`}
                            title={project.directory || undefined}
                          >
                            {project.directory || t("make.folder_missing")}
                          </small>
                        </span>
                        <span
                          className="wish-project-card-check"
                          aria-hidden="true"
                        >
                          <Check size={13} />
                        </span>
                      </label>
                    ))}
                  </div>
                  {filtered.length === 0 && (
                    <div className="wish-project-empty" role="status">
                      <FolderOpen size={22} />
                      <p>
                        {projects.length
                          ? t("make.no_matches")
                          : t("make.no_projects")}
                      </p>
                    </div>
                  )}
                  {selected.length > 0 && (
                    <p className="form-tip wish-project-count" role="status">
                      {t("make.selected_projects", { count: selected.length })}
                    </p>
                  )}
                </fieldset>
              </div>
              <aside
                className="wish-creation-options"
                aria-label={t("make.options")}
              >
                <div className="wish-creation-option">
                  <label htmlFor={providerId}>{t("make.provider")}</label>
                  <select
                    id={providerId}
                    disabled={busy}
                    value={provider}
                    onChange={(e) =>
                      setProvider(Number(e.target.value) as Provider)
                    }
                  >
                    {wishProviders.map((p) => (
                      <option key={p.provider} value={p.provider}>
                        {p.name}
                      </option>
                    ))}
                  </select>
                  <p className="form-tip">{t("make.agent_detail")}</p>
                  {agent && needsSetup(agent) && (
                    <div className="agent-warning" role="status">
                      <span>
                        {agent.state === ProviderState.MISSING
                          ? t("agents.make_missing", { agent: agent.name })
                          : t("agents.make_signed_out", { agent: agent.name })}
                      </span>
                      <button
                        type="button"
                        className="button secondary small"
                        disabled={busy}
                        onClick={() => setSetup(true)}
                      >
                        {t("agents.set_up")}
                      </button>
                    </div>
                  )}
                  {agents.checking && !agents.env && (
                    <p className="form-tip" role="status">
                      {t("settings.agent_checking")}
                    </p>
                  )}
                  {agents.error && (
                    <p className="form-tip" role="alert">
                      {agents.error}
                    </p>
                  )}
                </div>
                {selected.length > 0 && (
                  <div className="wish-creation-option">
                    <label htmlFor={allowanceId}>{t("make.allowance")}</label>
                    <select
                      id={allowanceId}
                      disabled={busy}
                      value={allowance}
                      onChange={(e) =>
                        setAllowance(Number(e.target.value) as Allowance)
                      }
                    >
                      <option value={Allowance.NONE}>{t("rights.none")}</option>
                      <option value={Allowance.EDIT}>{t("rights.edit")}</option>
                      <option value={Allowance.AUTO}>{t("rights.auto")}</option>
                    </select>
                    <small className="form-tip">
                      {t("make.allowance_detail")}
                    </small>
                  </div>
                )}
                <div
                  className={`wish-creation-status ${full ? "is-full" : ""}`}
                  role="status"
                >
                  <span
                    className={`status-dot ${full ? "neutral" : "green"}`}
                  />
                  <p>
                    {full
                      ? t("make.full", { max: MAX_ACTIVE })
                      : t("make.ready")}
                  </p>
                </div>
              </aside>
            </div>
            {error && (
              <p className="wish-creation-error" role="alert">
                {error}
              </p>
            )}
            <div className="wish-creation-footer">
              <p className="form-tip">
                {t("make.tip", { command: 'djinn wish make "…"' })}
              </p>
              <div className="wish-creation-actions">
                <button
                  type="button"
                  className="button secondary"
                  disabled={busy}
                  onClick={onClose}
                >
                  {t("common.cancel")}
                </button>
                <button
                  type="submit"
                  className="button accent"
                  disabled={busy || !hasText || !prompt.trim() || overLimit}
                >
                  {busy
                    ? t("make.submitting")
                    : full
                      ? t("make.submit_paused")
                      : t("make.submit")}
                  {busy ? (
                    <LoaderCircle className="wish-creation-spinner" size={14} />
                  ) : (
                    <ArrowRight size={14} />
                  )}
                </button>
              </div>
            </div>
          </form>
        </div>
      </div>
    </section>
  );
}

// ChangeProvider changes the agent of a wish (WishService.SetProvider): the lead that runs stops, and one of the new
// agent starts from the wish's brief. The conversation of the old lead cannot follow: the dialog says so before.
export function ChangeProvider({
  wish,
  onChanged,
  onClose,
}: {
  wish: Wish;
  onChanged: (provider: Provider) => void;
  onClose: () => void;
}) {
  const clients = useClients();
  const current =
    wish.provider === Provider.UNSPECIFIED ? Provider.CLAUDE : wish.provider;
  const [provider, setProvider] = useState<Provider>(current);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const agents = useAgents();
  const agentFor = (p: Provider) =>
    agents.env?.providers.find((a) => a.id === agentOf[p]);
  const agent = agentFor(provider);
  const name = providerName(provider);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    try {
      await clients.wishes.setProvider({ wishId: wish.id, provider });
      onChanged(provider);
    } catch (err) {
      setError(message(err));
      setBusy(false);
    }
  };
  return (
    <ModalFrame
      title={t("provider.title")}
      eyebrow={wish.title}
      onClose={onClose}
    >
      <form className="form-fields" onSubmit={(e) => void submit(e)}>
        <label>
          <span>{t("make.provider")}</span>
          <select
            autoFocus
            value={provider}
            onChange={(e) => setProvider(Number(e.target.value) as Provider)}
          >
            {wishProviders.map((p) => {
              const env = agentFor(p.provider);
              return (
                <option key={p.provider} value={p.provider}>
                  {env ? `${p.name} · ${agentStateText(env)}` : p.name}
                </option>
              );
            })}
          </select>
        </label>
        {agents.checking && !agents.env && (
          <p className="form-tip">{t("settings.agent_checking")}</p>
        )}
        {agent && needsSetup(agent) && (
          <div className="agent-warning" role="status">
            <span>
              {agent.state === ProviderState.MISSING
                ? t("agents.make_missing", { agent: agent.name })
                : t("agents.make_signed_out", { agent: agent.name })}
            </span>
          </div>
        )}
        {provider !== current && (
          <>
            <p className="form-tip">
              {t("provider.handover", { agent: name })}
            </p>
            <p className="form-tip">{t("provider.tasks", { agent: name })}</p>
          </>
        )}
        {error && <p className="login-message">{error}</p>}
        <div className="modal-footer">
          <button type="button" className="button secondary" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button
            type="submit"
            className="button accent"
            disabled={busy || provider === current}
          >
            {t("provider.submit", { agent: name })}
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

// Settings: the agents djinn finds on this machine, and how to set them up; the language of the page.
export function Settings({ onClose }: { onClose: () => void }) {
  const agents = useAgents();
  const env = agents.env;
  const [theme, setThemeState] = useState<Theme>(chosenTheme);
  return (
    <ModalFrame
      title={t("app.connections")}
      eyebrow={t("settings.eyebrow")}
      onClose={onClose}
      wide
    >
      <AgentSetupPanel agents={agents} />
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
