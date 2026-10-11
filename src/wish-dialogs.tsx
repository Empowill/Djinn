// The window's dialogs on the services: make a wish, add a project, look at a project, its checks and its skills, and the
// settings: those that stay on this page (the language, the theme), and the global shortcut, which djinn takes.
import { ArrowRight, BookOpen, FolderOpen, Terminal } from "lucide-react";
import { type FormEvent, useEffect, useId, useState } from "react";

import {
  type CheckRun,
  CheckWhen,
  type Project,
  type ProjectCheck,
  ProjectPush,
  type ProjectServiceShowResponse,
  type Question,
  type Skill,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import type {
  Shortcut,
  UiServiceGetEnvironmentResponse,
} from "../gen/ts/ui/v1/ui_pb";
import { message } from "./data/client";
import { useClients } from "./data/djinn";
import { MAX_ACTIVE, span, syncDescription, when } from "./data/format";
import { Brand, ModalFrame } from "./frame";
import {
  chosenLanguage,
  languageName,
  languages,
  setLanguage,
  systemLanguage,
  t,
} from "./i18n";
import { type Theme, chosenTheme, setTheme as applyTheme } from "./theme";

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

// syncDescription says how the integration branch stands against its remote target.
export { syncDescription };

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
  const [pushing, setPushing] = useState(false);

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
  const currentSync = shown?.project?.sync ?? project.sync;
  const isOutOfSync = Boolean(
    currentSync && (currentSync.ahead > 0 || currentSync.behind > 0),
  );
  const syncText = syncDescription(currentSync);

  const handlePush = async () => {
    setPushing(true);
    try {
      await clients.projects.push({ project: project.id });
      const res = await clients.projects.show({ project: project.id });
      setShown(res);
    } catch (err) {
      const msg = message(err);
      setError(msg);
      onToast?.(msg);
    } finally {
      setPushing(false);
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
            {isOutOfSync && <p className="project-sync-status">{syncText}</p>}
          </div>
          <div className="project-push-actions">
            {isOutOfSync && (
              <button
                type="button"
                className="button accent small"
                disabled={pushing}
                onClick={handlePush}
              >
                {pushing ? t("project.pushing") : t("project.push")}
              </button>
            )}
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

// Settings: the agents djinn finds on this machine, the language and the theme of the page, the global shortcut, and
// the way to the documentation.
export function Settings({
  onClose,
  onDocs,
}: {
  onClose: () => void;
  onDocs: () => void;
}) {
  const clients = useClients();
  const [env, setEnv] = useState<UiServiceGetEnvironmentResponse>();
  const [theme, setTheme] = useState<Theme>(chosenTheme);
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
            setTheme(next);
            applyTheme(next);
          }}
        >
          <option value="">{t("settings.theme_system")}</option>
          <option value="dark">{t("settings.theme_dark")}</option>
          <option value="light">{t("settings.theme_light")}</option>
        </select>
      </div>
      <ShortcutField
        shortcut={env?.shortcut}
        onSave={async (chord) => {
          const res = await clients.ui.setShortcut({ chord });
          setEnv((prev) => prev && { ...prev, shortcut: res.shortcut });
        }}
      />
      <div className="setting-row">
        <div>
          <strong>{t("settings.docs")}</strong>
          <p>{t("settings.docs_detail")}</p>
        </div>
        <button type="button" className="button secondary" onClick={onDocs}>
          <BookOpen size={14} />
          {t("settings.docs_open")}
        </button>
      </div>
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
