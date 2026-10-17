// The window's dialogs on the services: make a wish, add a project, look at a project, its checks and its skills, and the
// settings: those that stay on this page (the language, the theme), and the global shortcut, which djinn takes.
import {
  ArrowRight,
  Check,
  Folder,
  FolderOpen,
  LoaderCircle,
  Search,
} from "lucide-react";
import {
  type FormEvent,
  useEffect,
  useId,
  useMemo,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import {
  Allowance,
  Change,
  type CheckRun,
  CheckWhen,
  type Lead,
  type LeadExit,
  type Project,
  type ProjectCheck,
  ProjectPush,
  type ProjectServiceShowResponse,
  type ProjectSync,
  type Question,
  type Skill,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import {
  NotificationAccess,
  type Shortcut,
  type UiServiceGetEnvironmentResponse,
} from "../gen/ts/ui/v1/ui_pb";
import { AgentSetupPanel, needsSetup, useAgents } from "./agent-setup";
import { CommandText } from "./copy-command";
import { message } from "./data/client";
import { useClients, useData, useDjinn, useOptionalData } from "./data/djinn";
import { MAX_ACTIVE, noLead, span, when } from "./data/format";
import { Brand, ModalFrame } from "./frame";
import { InboxSources } from "./inbox";
import { SyncLines } from "./project-sync";
import {
  chosenLanguage,
  language,
  languageName,
  languages,
  setLanguage,
  systemLanguage,
  t,
  type TextKey,
} from "./i18n";
import {
  type TextSize,
  chosenTextSize,
  setTextSize as applyTextSize,
  textSizes,
} from "./text-size";
import { type Theme, chosenTheme, setTheme as applyTheme } from "./theme";
import { WishComposer } from "./wish-composer";
import { WishDropdown } from "./wish-dropdown";
import { leadTrouble } from "./wish-head";
import { WishSmoke } from "./wish-smoke";
import { MAX_PROMPT_CHARACTERS, promptTooLong } from "./wish-composer-markdown";
import { platformShortcut, TooltipButton } from "./tooltip";
import { WorkspaceAction } from "./workspace-action";

// MakeWish sends the complete request. Naming the wish belongs to the server and its lead. With three wishes active,
// it is made paused, as the lamp would refuse a fourth. The rights chosen are given once it is made (WishService.Allow,
// per project); one that fails leaves the wish made, and says so.
export function MakeWish({
  projects,
  active,
  lead,
  leadExit: leadExitProp,
  leadStartError: leadStartErrorProp,
  onMade,
  onClose,
  onResumeLead,
  onBusyChange,
  backActionRef,
  onBackSetupChange,
}: {
  projects: Project[];
  active: number;
  lead?: Lead;
  leadExit?: LeadExit;
  leadStartError?: string;
  onMade: (wish: Wish, warning?: string) => void;
  onClose: () => void;
  onResumeLead?: () => Promise<unknown>;
  onBusyChange?: (busy: boolean) => void;
  backActionRef?: { current: (() => void) | null };
  onBackSetupChange?: (setup: boolean) => void;
}) {
  const djinn = useDjinn();
  const clients = djinn?.clients;
  const store = djinn?.store;
  const storeLead = useOptionalData((s) => s.lead, undefined);
  const storeLeadExit = useOptionalData((s) => s.leadExit, undefined);
  const storeLeadStartError = useOptionalData((s) => s.leadStartError, "");
  const storeLoaded = useOptionalData((s) => s.loaded, true);
  const full = active >= MAX_ACTIVE;
  const [prompt, setPrompt] = useState("");
  const [hasText, setHasText] = useState(false);
  const overLimit = useMemo(() => promptTooLong(prompt), [prompt]);
  const [allowance, setAllowance] = useState(Allowance.NONE);
  const [chosen, setChosen] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [startingLead, setStartingLead] = useState(false);
  const [leadStarted, setLeadStarted] = useState(false);
  const submitting = useRef(false);
  const submissionFocus = useRef<HTMLElement | null>(null);
  const [search, setSearch] = useState("");
  const form = useRef<HTMLFormElement>(null);
  const page = useRef<HTMLElement>(null);
  const promptId = useId();
  const searchId = useId();
  // Whether an agent can run the wish: with none ready, the setup of the agents shows in place of the form, which
  // keeps what was typed.
  const agents = useAgents();
  const [setup, setSetup] = useState(false);
  const noAgent =
    !!agents.env?.providers.length && agents.env.providers.every(needsSetup);
  const effectiveLead = lead ?? storeLead;
  const effectiveLeadExit = leadExitProp ?? storeLeadExit;
  const effectiveLeadStartError = leadStartErrorProp ?? storeLeadStartError;
  const trouble = leadTrouble(effectiveLeadExit, effectiveLeadStartError);
  const leadStopped =
    !leadStarted && storeLoaded && (noLead(effectiveLead) || !!trouble);
  useEffect(() => {
    setLeadStarted(false);
  }, [effectiveLead, effectiveLeadExit, effectiveLeadStartError]);
  const previousFocus = useRef<HTMLElement | null>(
    typeof document !== "undefined"
      ? (document.activeElement as HTMLElement | null)
      : null,
  );
  useEffect(() => {
    const focus = previousFocus.current;
    return () => {
      if (focus?.isConnected) focus.focus({ preventScroll: true });
    };
  }, []);
  useLayoutEffect(() => {
    const focusTarget = setup
      ? document.querySelector<HTMLElement>(".app-toolbar-back")
      : (page.current?.querySelector<HTMLElement>(".wish-composer-input") ??
        document.getElementById(promptId));
    focusTarget?.focus({ preventScroll: true });
  }, [setup, promptId]);
  useEffect(() => {
    const action = () => {
      if (setup) setSetup(false);
      else onClose();
    };
    if (backActionRef) backActionRef.current = action;
    onBackSetupChange?.(setup);
    return () => {
      if (backActionRef?.current === action) backActionRef.current = null;
      onBackSetupChange?.(false);
    };
  }, [backActionRef, onBackSetupChange, onClose, setup]);
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
  const selectedProjectNames = selected
    .map((id) => projects.find((project) => project.id === id)?.name)
    .filter((name): name is string => Boolean(name));
  const submitShortcut = platformShortcut("Enter", t("app.shortcut_enter"));
  const submitLabel = busy
    ? t("make.submitting")
    : full
      ? t("make.submit_paused")
      : t("make.submit");
  const startLead = async () => {
    if (startingLead || busy) return;
    setStartingLead(true);
    setError("");
    try {
      if (onResumeLead) await onResumeLead();
      else await clients!.lead.resume({});
      setLeadStarted(true);
      void store?.changed("", [Change.WISH]);
    } catch (err) {
      setError(message(err));
    } finally {
      setStartingLead(false);
    }
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (
      submitting.current ||
      leadStopped ||
      !hasText ||
      !prompt.trim() ||
      overLimit
    )
      return;
    submitting.current = true;
    submissionFocus.current =
      typeof document !== "undefined"
        ? (document.activeElement as HTMLElement | null)
        : null;
    setBusy(true);
    onBusyChange?.(true);
    setError("");
    try {
      const res = await clients!.wishes.make({
        prompt: prompt.trim(),
        projectIds: selected,
        paused: full,
      });
      const made = res.wish;
      if (!made) return;
      let warning: string | undefined;
      if (allowance !== Allowance.NONE)
        for (const projectId of selected)
          try {
            await clients!.wishes.allow({
              wishId: made.id,
              projectId,
              mode: allowance,
            });
          } catch (err) {
            warning = message(err);
          }
      onMade(made, warning);
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
      <div className="wish-creation-scroll">
        <div className="wish-creation-content">
          <div className="wish-creation-intro">
            <h1 id={`${promptId}-heading`}>
              {setup ? t("agents.title") : t("make.title")}
            </h1>
            {setup && <p>{t("make.draft_kept")}</p>}
          </div>
          {setup && <AgentSetupPanel agents={agents} />}
          {/* Keep the editor mounted during setup so the draft and native undo history survive configuration. */}
          <form
            ref={form}
            hidden={setup}
            className="wish-creation-form"
            onSubmit={(event) => void submit(event)}
            aria-busy={busy}
          >
            <WishSmoke />
            <div className="wish-creation-workspace">
              <div className="wish-creation-request">
                <label
                  id={`${promptId}-label`}
                  htmlFor={promptId}
                  className="wish-creation-sr-only"
                >
                  {t("make.what")}
                </label>
                <div
                  className="wish-creation-controls"
                  aria-label={t("make.options")}
                >
                  <WishDropdown
                    label={t("make.projects")}
                    icon={<Folder size={13} />}
                    className="wish-tag-dropdown"
                    value={
                      <>
                        <span className="wish-dropdown-project-label">
                          {selectedProjectNames.length > 0
                            ? selectedProjectNames.join(", ")
                            : t("make.projects").split(/\s+/)[0]}
                        </span>
                      </>
                    }
                    disabled={busy}
                  >
                    {projects.length > 0 && (
                      <div className="wish-project-search">
                        <Search size={14} aria-hidden="true" />
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
                          disabled={busy}
                        />
                      </div>
                    )}
                    <fieldset disabled={busy} className="wish-project-options">
                      <legend className="wish-creation-sr-only">
                        {t("make.projects")}
                      </legend>
                      {filtered.map((project) => (
                        <label
                          key={project.id}
                          className={`wish-project-option ${selected.includes(project.id) ? "is-selected" : ""}`}
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
                          <span>{project.name}</span>
                          <span
                            id={`${promptId}-project-${project.id}-folder`}
                            className="wish-creation-sr-only"
                          >
                            {project.directory || t("make.folder_missing")}
                          </span>
                          <Check size={13} aria-hidden="true" />
                        </label>
                      ))}
                      {filtered.length === 0 && (
                        <p className="wish-project-empty" role="status">
                          {projects.length
                            ? t("make.no_matches")
                            : t("make.no_projects")}
                        </p>
                      )}
                    </fieldset>
                  </WishDropdown>
                  {selected.length > 0 && (
                    <WishDropdown
                      label={t("make.allowance")}
                      value={
                        allowance === Allowance.EDIT
                          ? t("rights.edit")
                          : allowance === Allowance.AUTO
                            ? t("rights.auto")
                            : t("rights.none")
                      }
                      disabled={busy}
                    >
                      <div
                        className="wish-dropdown-options"
                        role="listbox"
                        aria-label={t("make.allowance")}
                      >
                        {[
                          [Allowance.NONE, t("rights.none")],
                          [Allowance.EDIT, t("rights.edit")],
                          [Allowance.AUTO, t("rights.auto")],
                        ].map(([value, name]) => (
                          <button
                            key={value}
                            type="button"
                            className="wish-dropdown-option"
                            role="option"
                            aria-selected={allowance === value}
                            data-wish-dropdown-close="true"
                            onClick={() => setAllowance(value as Allowance)}
                          >
                            {name}
                          </button>
                        ))}
                      </div>
                    </WishDropdown>
                  )}
                  {noAgent && (
                    <div className="agent-warning" role="status">
                      <span>{t("make.no_agent")}</span>
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
                  {!noAgent && leadStopped && (
                    <div className="agent-warning" role="status">
                      <span>{trouble ? trouble.text : t("make.no_lead")}</span>
                      <CommandText text="djinn lead resume" />
                      <button
                        type="button"
                        className="button secondary small"
                        disabled={busy || startingLead}
                        onClick={() => void startLead()}
                      >
                        {startingLead
                          ? t("make.starting_lead")
                          : t("lead_message.resume")}
                      </button>
                    </div>
                  )}
                  {agents.error && (
                    <span className="wish-creation-inline-status" role="alert">
                      {agents.error}
                    </span>
                  )}
                  {full && (
                    <span
                      className="wish-creation-status is-full"
                      role="status"
                    >
                      <span className="status-dot neutral" />
                      {t("make.full", { max: MAX_ACTIVE })}
                    </span>
                  )}
                </div>
                <WishComposer
                  id={promptId}
                  describedBy={overLimit ? `${promptId}-limit` : undefined}
                  disabled={busy}
                  invalid={overLimit}
                  onChange={(markdown, hasText) => {
                    setPrompt(markdown);
                    setHasText(hasText);
                  }}
                />
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
              </div>
            </div>
            {error && (
              <p className="wish-creation-error" role="alert">
                {error}
              </p>
            )}
            <div className="wish-creation-footer">
              <WorkspaceAction
                tone="primary"
                className="workspace-action-create"
              >
                <TooltipButton
                  type="submit"
                  className="button accent workspace-action-control"
                  disabled={
                    busy ||
                    leadStopped ||
                    !hasText ||
                    !prompt.trim() ||
                    overLimit
                  }
                  label={submitLabel}
                  tooltip={submitLabel}
                  shortcut={submitShortcut.label}
                  aria-keyshortcuts={submitShortcut.aria}
                >
                  {submitLabel}
                  {busy ? (
                    <LoaderCircle className="wish-creation-spinner" size={14} />
                  ) : (
                    <ArrowRight size={14} />
                  )}
                </TooltipButton>
              </WorkspaceAction>
            </div>
          </form>
        </div>
      </div>
    </section>
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

// ProjectPanel shows a project as djinn knows it, the checks Djinn runs on its work with their last runs
// (ProjectService.Show), and its skills (SkillService.List): its own and those it summons.
export function ProjectPanel({
  project,
  onClose,
  onToast,
}: {
  project: Project;
  onClose: () => void;
  onToast?: (text: string) => void;
}) {
  const clients = useClients();
  const [skills, setSkills] = useState<Skill[] | undefined>();
  const [shown, setShown] = useState<ProjectServiceShowResponse>();
  const [error, setError] = useState("");
  const allSources = useData((s) => s.sources);
  const sources = useMemo(
    () => allSources.filter((src) => src.project === project.name),
    [allSources, project.name],
  );

  useEffect(() => {
    let live = true;
    clients.skills
      .list({ project: project.id })
      .then((res) => live && setSkills(res.skills))
      .catch((err) => live && setError(message(err)));
    clients.projects
      .show({ project: project.id })
      .then((res) => live && setShown(res))
      .catch((err) => live && setError(message(err)));
    return () => {
      live = false;
    };
  }, [clients, project.id]);

  const currentPush =
    shown?.project?.push ?? project.push ?? ProjectPush.STANDARD;
  const syncs = shown?.project?.syncs ?? project.syncs;

  // A push says how it went on its sync line, never in a toast: the line reads its answer, or its error.
  const handlePush = (sync: ProjectSync) =>
    clients.projects
      .push({ project: project.id, wish: sync.wishId, branch: sync.branch })
      .finally(() =>
        clients.projects
          .show({ project: project.id })
          .then(setShown)
          .catch(() => undefined),
      );

  // A watcher command allowed or taken back: the commands the answer lists replace the shown ones.
  const handleAllow = async (watch: string, revoke: boolean) => {
    try {
      const res = await clients.projects.allow({
        project: project.id,
        watch,
        revoke,
      });
      setShown((prev) =>
        prev ? { ...prev, watchCommands: res.watchCommands } : prev,
      );
      return true;
    } catch (err) {
      const msg = message(err);
      setError(msg);
      onToast?.(msg);
      return false;
    }
  };

  const handleSetPush = async (nextPush: ProjectPush) => {
    try {
      const res = await clients.projects.setPush({
        project: project.id,
        push: nextPush,
      });
      setShown((prev) =>
        prev
          ? { ...prev, project: res.project }
          : ({ project: res.project } as ProjectServiceShowResponse),
      );
    } catch (err) {
      const msg = message(err);
      setError(msg);
      onToast?.(msg);
    }
  };

  return (
    <ModalFrame
      title={project.name}
      eyebrow={t("project.eyebrow")}
      onClose={onClose}
      wide
    >
      {shown?.problems?.map((p) => (
        <p className="login-message" key={p}>
          {p}
        </p>
      ))}
      {shown?.warnings?.map((w) => (
        <p className="login-message" key={w}>
          {w}
        </p>
      ))}
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
      {project.git && (
        <div className="setting-row">
          <div>
            <strong>{t("project.push_cadence")}</strong>
            <p>
              {currentPush === ProjectPush.ON_DEMAND
                ? t("project.push_on_demand_detail")
                : t("project.push_standard_detail")}
            </p>
            <SyncLines syncs={syncs} onPush={handlePush} />
          </div>
          <div className="project-push-actions">
            <div
              className="segmented-switch"
              role="group"
              aria-label={t("project.push_cadence")}
            >
              <button
                type="button"
                className={
                  currentPush !== ProjectPush.ON_DEMAND ? "active" : ""
                }
                onClick={() => handleSetPush(ProjectPush.STANDARD)}
              >
                {t("project.push_standard")}
              </button>
              <button
                type="button"
                className={
                  currentPush === ProjectPush.ON_DEMAND ? "active" : ""
                }
                onClick={() => handleSetPush(ProjectPush.ON_DEMAND)}
              >
                {t("project.push_on_demand")}
              </button>
            </div>
          </div>
        </div>
      )}
      <div className="settings-divider" />
      <h3>{t("project.checks")}</h3>
      {shown === undefined && !error && (
        <p className="muted-text">{t("common.loading")}</p>
      )}
      {shown && (
        <ProjectChecks
          setup={shown.setup}
          checks={shown.checks}
          runs={shown.project?.checkRuns ?? []}
        />
      )}
      <div className="settings-divider" />
      <h3>{t("project.watch_commands")}</h3>
      <WatchCommands
        commands={shown?.watchCommands ?? []}
        onAllow={(watch) => handleAllow(watch, false)}
        onRevoke={(watch) => handleAllow(watch, true)}
      />
      <div className="settings-divider" />
      <h3>{t("project.sources")}</h3>
      <p className="muted-text">{t("project.sources_detail")}</p>
      <InboxSources sources={sources} onToast={(text) => onToast?.(text)} />
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

// WatchCommands lists the watcher commands the developer allows in a project for themselves, each with a button that
// takes it back, and a field that allows one more: their own settings, never the repository's.
export function WatchCommands({
  commands,
  onAllow,
  onRevoke,
}: {
  commands: string[];
  onAllow: (watch: string) => Promise<boolean>;
  onRevoke: (watch: string) => void;
}) {
  const [draft, setDraft] = useState("");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (draft.trim() && (await onAllow(draft.trim()))) setDraft("");
  };
  return (
    <>
      <p className="muted-text">{t("project.watch_commands_detail")}</p>
      {commands.map((command) => (
        <div className="setting-row" key={command}>
          <div>
            <code>{command}</code>
          </div>
          <button
            type="button"
            className="button secondary"
            onClick={() => onRevoke(command)}
          >
            {t("project.watch_revoke")}
          </button>
        </div>
      ))}
      <form className="folder-field" onSubmit={submit}>
        <input
          aria-label={t("project.watch_command")}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder={t("project.watch_placeholder")}
          spellCheck={false}
        />
        <button
          type="submit"
          className="button secondary"
          disabled={!draft.trim()}
        >
          {t("project.watch_allow")}
        </button>
      </form>
    </>
  );
}

const whenKeys = {
  [CheckWhen.UNSPECIFIED]: "project.check_setup",
  [CheckWhen.COMMIT]: "project.check_commit",
  [CheckWhen.PUSH]: "project.check_push",
} as const;

// ProjectChecks says what Djinn runs on a project's work: the setup that makes a fresh worktree ready, and each check
// with when it runs, before a commit or before a push; each with its last run, the commit it checked and how it
// ended, its output on hover when it failed.
export function ProjectChecks({
  setup,
  checks,
  runs,
}: {
  setup: string;
  checks: ProjectCheck[];
  runs: CheckRun[];
}) {
  if (!setup && checks.length === 0)
    return <p className="muted-text">{t("project.no_checks")}</p>;
  const last = (name: string, isSetup: boolean) =>
    runs.find(
      (r) => r.setup === isSetup && r.name.toLowerCase() === name.toLowerCase(),
    );
  const row = (
    key: string,
    name: string,
    command: string,
    whens: CheckWhen[],
    run?: CheckRun,
  ) => (
    <div className="setting-row" key={key}>
      <div>
        <strong>{name}</strong>
        <p>
          <code>{command}</code>
        </p>
        <p
          className={run && !run.passed ? "login-message" : "muted-text"}
          title={run?.output || undefined}
        >
          {runText(run)}
        </p>
      </div>
      <span>
        {whens.map((w) => (
          <span className="badge muted" key={w}>
            {t(whenKeys[w])}
          </span>
        ))}
      </span>
    </div>
  );
  return (
    <>
      <p className="muted-text">{t("project.checks_detail")}</p>
      {setup &&
        row(
          "setup",
          "setup",
          setup,
          [CheckWhen.UNSPECIFIED],
          last("setup", true),
        )}
      {checks.map((c) =>
        row(`check-${c.name}`, c.name, c.command, c.when, last(c.name, false)),
      )}
    </>
  );
}

// runText says how a check last ran: never yet, passed on a commit, or failed and why, its first line.
function runText(run?: CheckRun): string {
  if (!run) return t("project.check_never");
  const at = {
    sha: run.sha.slice(0, 8),
    when: when(run.endTime),
    took: span(Number(run.durationMs)),
  };
  if (run.passed) return t("project.check_passed", at);
  const reason = run.reason.split("\n")[0].replace(/:$/, "");
  return t("project.check_red", { ...at, reason });
}

// ShortcutField is the global shortcut that brings the window forward, typed in as Ctrl+Alt+Space; empty turns it
// off. Saved on Enter or when the field is left. Disabled where djinn cannot take one: the browser.
export function ShortcutField({
  shortcut,
  onSave,
}: {
  shortcut?: Shortcut;
  onSave: (chord: string) => Promise<void>;
}) {
  const id = useId();
  const [draft, setDraft] = useState<string>();
  const [error, setError] = useState("");
  const available = shortcut?.available ?? false;
  const value = draft ?? shortcut?.chord ?? "";
  const save = () => {
    if (draft === undefined || draft === shortcut?.chord) return;
    onSave(draft).then(
      () => {
        setDraft(undefined);
        setError("");
      },
      (err) => setError(message(err)),
    );
  };
  return (
    <div className="setting-row">
      <div>
        <label htmlFor={id}>
          <strong>{t("settings.shortcut")}</strong>
        </label>
        <p>
          {available
            ? t("settings.shortcut_detail", {
                chord: shortcut?.defaultChord ?? "",
              })
            : t("settings.shortcut_unavailable")}
        </p>
        {available && shortcut?.problem && !error && (
          <p className="login-message">
            {t("settings.shortcut_problem", { problem: shortcut.problem })}
          </p>
        )}
        {error && <p className="login-message">{error}</p>}
      </div>
      <input
        id={id}
        value={value}
        disabled={!available}
        placeholder={t("settings.shortcut_off")}
        spellCheck={false}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => {
          if (e.key === "Enter") save();
        }}
      />
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

const textSizeLabel: Record<TextSize, TextKey> = {
  large: "settings.text_size_large",
  "": "settings.text_size_default",
  compact: "settings.text_size_compact",
  "very-compact": "settings.text_size_very_compact",
};

// Settings: the agents djinn finds on this machine, the system notifications, the language, the theme and the text size
// of the page, and the global shortcut. The documentation, the Grimoire, has its own entry in the side panel (W89).
export function Settings({ onClose }: { onClose: () => void }) {
  const clients = useClients();
  const [env, setEnv] = useState<UiServiceGetEnvironmentResponse>();
  const agents = useAgents();
  const [theme, setTheme] = useState<Theme>(chosenTheme);
  const [textSize, setTextSize] = useState<TextSize>(chosenTextSize);
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
            setTheme(next);
            applyTheme(next);
          }}
        >
          <option value="">{t("settings.theme_system")}</option>
          <option value="dark">{t("settings.theme_dark")}</option>
          <option value="light">{t("settings.theme_light")}</option>
        </select>
      </div>
      <div className="setting-row">
        <div>
          <strong>{t("settings.text_size")}</strong>
          <p>{t("settings.text_size_detail")}</p>
        </div>
        <select
          value={textSize}
          aria-label={t("settings.text_size")}
          onChange={(e) => {
            const next = e.target.value as TextSize;
            setTextSize(next);
            applyTextSize(next);
          }}
        >
          {textSizes.map((size) => (
            <option key={size} value={size}>
              {t(textSizeLabel[size])}
            </option>
          ))}
        </select>
      </div>
      <ShortcutField
        shortcut={env?.shortcut}
        onSave={async (chord) => {
          const res = await clients.ui.setShortcut({ chord });
          setEnv((prev) => prev && { ...prev, shortcut: res.shortcut });
        }}
      />
      <div className="settings-foot">
        <Brand small />
        <span>Djinn {env?.version}</span>
      </div>
    </ModalFrame>
  );
}

// MoveQuestionDialog asks which other wish an open question should move to, and whether its tasks should follow.
export function MoveQuestionDialog({
  question,
  wishes,
  currentWishId,
  onClose,
  onMove,
}: {
  question: Question;
  wishes: readonly Wish[];
  currentWishId: string;
  onClose: () => void;
  onMove: (
    wishId: string,
    id: string,
    targetWish: string,
    follow: boolean,
  ) => Promise<unknown>;
}) {
  const otherWishes = wishes.filter((w) => w.id !== currentWishId);
  const [targetWish, setTargetWish] = useState(otherWishes[0]?.id ?? "");
  const [follow, setFollow] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!targetWish) return;
    setBusy(true);
    try {
      await onMove(currentWishId, question.id, targetWish, follow);
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <ModalFrame
      title={t("question.move_dialog_title", { code: question.code })}
      eyebrow={question.text}
      onClose={onClose}
    >
      <form className="form-fields" onSubmit={(e) => void submit(e)}>
        <label>
          <span>{t("question.move_select_wish")}</span>
          <select
            value={targetWish}
            onChange={(e) => setTargetWish(e.target.value)}
            disabled={busy || otherWishes.length === 0}
            autoFocus
          >
            {otherWishes.map((w) => (
              <option key={w.id} value={w.id}>
                {w.title}
              </option>
            ))}
          </select>
        </label>
        <label className="wish-project-choice">
          <input
            type="checkbox"
            checked={follow}
            onChange={(e) => setFollow(e.target.checked)}
            disabled={busy}
          />
          <span>{t("question.move_follow")}</span>
        </label>
        {error && <p className="login-message">{error}</p>}
        <div className="modal-footer">
          <button type="button" className="button secondary" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button
            type="submit"
            className="button accent"
            disabled={busy || !targetWish}
          >
            {t("question.move_confirm")}
            <ArrowRight size={14} />
          </button>
        </div>
      </form>
    </ModalFrame>
  );
}

// WithdrawQuestionDialog closes an open question without an answer.
export function WithdrawQuestionDialog({
  question,
  currentWishId,
  onClose,
  onWithdraw,
}: {
  question: Question;
  currentWishId: string;
  onClose: () => void;
  onWithdraw: (wishId: string, id: string, note: string) => Promise<unknown>;
}) {
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    try {
      await onWithdraw(currentWishId, question.id, note.trim());
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <ModalFrame
      title={t("question.withdraw_dialog_title", { code: question.code })}
      eyebrow={question.text}
      onClose={onClose}
    >
      <form className="form-fields" onSubmit={(e) => void submit(e)}>
        <label>
          <span>{t("question.withdraw_note")}</span>
          <textarea
            autoFocus
            rows={3}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder={t("question.withdraw_note_placeholder")}
            disabled={busy}
          />
        </label>
        {error && <p className="login-message">{error}</p>}
        <div className="modal-footer">
          <button type="button" className="button secondary" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button
            type="submit"
            className="button danger-button"
            disabled={busy}
          >
            {t("question.withdraw_confirm")}
          </button>
        </div>
      </form>
    </ModalFrame>
  );
}
