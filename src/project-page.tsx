// A project's settings, a page of the window in tabs (W111, L23): what situates the project and readies its worktrees,
// what its workers get by default, its gates and checks, how its work integrates and pushes, the brief its workers get,
// its skills, its permissions, and what changed in its configuration on the reference branch. Each setting shows its
// value in the repository's djinn.yaml, in your own file on this machine, and the one Djinn retains; what waits for you
// stays above the tabs.
import { Flame, Sparkles } from "lucide-react";
import {
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { Mode, type Permissions } from "../gen/ts/djinn/v1/agents_pb";
import type { ProjectGate } from "../gen/ts/djinn/v1/project_config_pb";
import {
  Change,
  type ConfigChange,
  type Project,
  type ProjectNotice,
  ProjectNoticeAction,
  ProjectNoticeKind,
  ProjectNoticeTopic,
  ProjectPush,
  type ProjectServiceGatesResponse,
  type ProjectServiceShowResponse,
  type ProjectSetting,
  type ProjectSync,
  type Skill,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useData, useStore } from "./data/djinn";
import { t, type TextKey } from "./i18n";
import { InboxSources } from "./inbox";
import { OperatingLoad } from "./operating-load";
import { PageHeader } from "./page-header";
import {
  ConfigChanges,
  From,
  NoticeLine,
  ProjectChecks,
  ReferenceBranch,
  SkillLine,
  ToFixBox,
  WatchCommands,
  WorkersStart,
  waits,
} from "./project-panel";
import { SyncLines } from "./project-sync";
import { usePlace } from "./view-history";
import "./project-panel.css";

// PROJECT_PAGE starts the window's page value of a project's settings: the project's identifier follows it.
export const PROJECT_PAGE = "project:";

export const PROJECT_TABS = [
  "general",
  "workers",
  "gates",
  "integration",
  "brief",
  "skills",
  "permissions",
  "changes",
] as const;
export type ProjectTab = (typeof PROJECT_TABS)[number];

const tabLabels: Record<ProjectTab, TextKey> = {
  general: "project.tab_general",
  workers: "project.tab_workers",
  gates: "project.tab_gates",
  integration: "project.tab_integration",
  brief: "project.tab_brief",
  skills: "project.tab_skills",
  permissions: "project.tab_permissions",
  changes: "project.tab_changes",
};

const tabDetails: Record<ProjectTab, TextKey> = {
  general: "project.tab_general_detail",
  workers: "project.tab_workers_detail",
  gates: "project.tab_gates_detail",
  integration: "project.tab_integration_detail",
  brief: "project.tab_brief_detail",
  skills: "project.tab_skills_detail",
  permissions: "project.tab_permissions_detail",
  changes: "project.tab_changes_detail",
};

// The name each setting shows under, its key in the files below it.
const settingLabels: Partial<Record<string, TextKey>> = {
  main_branch: "project.s_main_branch",
  branch: "project.s_branch",
  setup: "project.s_setup",
  install: "project.s_install",
  provider: "project.s_provider",
  model: "project.s_model",
  max_budget_usd: "project.s_max_budget_usd",
  correction_attempts: "project.s_correction_attempts",
  enlighten_workers: "project.s_enlighten_workers",
  question_provider: "project.s_question_provider",
  question_model: "project.s_question_model",
  question_budget_usd: "project.s_question_budget_usd",
  checks: "project.s_checks",
  test_one: "project.s_test_one",
  push: "project.s_push",
  push_strategy: "project.s_push_strategy",
  merge_main: "project.s_merge_main",
  merge_main_minutes: "project.s_merge_main_minutes",
  generated: "project.s_generated",
  generate: "project.s_generate",
  babysit: "project.s_babysit",
  install_questions: "project.s_install_questions",
  install_releases: "project.s_install_releases",
  install_builds: "project.s_install_builds",
};

// ProjectPage is a project's settings as Djinn reads them (ProjectService.Show), in tabs, with what waits for you first.
export function ProjectPage({
  project,
  onToast,
  onOpenCircle,
  initialTab = "general",
  initialShown,
  initialSkills,
}: {
  project: Project;
  onToast?: (text: string) => void;
  // Opens the Summoning Circle, which picks the agent and the model on this machine.
  onOpenCircle?: () => void;
  initialTab?: ProjectTab;
  // The project as a test shows it, and its skills; else read from Djinn.
  initialShown?: ProjectServiceShowResponse;
  initialSkills?: Skill[];
}) {
  const clients = useClients();
  const store = useStore();
  const [tab, setTab] = useState<ProjectTab>(initialTab);
  usePlace("tab", tab, (to) =>
    setTab(PROJECT_TABS.find((x) => x === to) ?? "general"),
  );
  const [skills, setSkills] = useState<Skill[] | undefined>(initialSkills);
  const [shown, setShown] = useState<ProjectServiceShowResponse | undefined>(
    initialShown,
  );
  const [gates, setGates] = useState<ProjectServiceGatesResponse>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const allSources = useData((s) => s.sources);
  const sources = useMemo(
    () => allSources.filter((src) => src.project === project.name),
    [allSources, project.name],
  );

  const showSeq = useRef(0);
  const reshow = useCallback(() => {
    const seq = ++showSeq.current;
    return clients.projects
      .show({ project: project.id })
      .then((res) => {
        if (seq === showSeq.current) setShown(res);
      })
      .catch(() => undefined);
  }, [clients, project.id]);

  useEffect(() => {
    let live = true;
    clients.skills
      .list({ project: project.id })
      .then((res) => live && setSkills(res.skills))
      .catch((err) => live && setError(message(err)));
    clients.projects
      .gates({ project: project.id })
      .then((res) => live && setGates(res))
      .catch(() => undefined);
    const seq = ++showSeq.current;
    clients.projects
      .show({ project: project.id })
      .then((res) => {
        if (live && seq === showSeq.current) setShown(res);
      })
      .catch((err) => live && setError(message(err)));
    return () => {
      live = false;
    };
  }, [clients, project.id]);

  // A source plugged in or said no to here, or from the command line, changes what waits: read the notices again.
  const firstSources = useRef(sources);
  useEffect(() => {
    if (firstSources.current !== sources) {
      firstSources.current = sources;
      void reshow();
    }
  }, [sources, reshow]);

  // The projects read again (a look at the reference branch found a change, one was marked seen elsewhere): read
  // the journal of changes and the notices again.
  const listed = useData((s) => s.projects.find((p) => p.id === project.id));
  const firstListed = useRef(listed);
  useEffect(() => {
    if (firstListed.current !== listed) {
      firstListed.current = listed;
      void reshow();
    }
  }, [listed, reshow]);

  const notices = shown?.notices ?? [];
  const toFix = notices.filter((n) => n.kind === ProjectNoticeKind.TO_FIX);
  const warnings = notices.filter((n) => n.kind === ProjectNoticeKind.WARNING);
  const branchInfo = notices.filter(
    (n) =>
      n.kind === ProjectNoticeKind.INFORMATION &&
      n.topic === ProjectNoticeTopic.BRANCH,
  );
  const information = notices.filter(
    (n) => n.kind === ProjectNoticeKind.INFORMATION && !branchInfo.includes(n),
  );
  const unseen = (shown?.changes ?? []).filter(waits).length;

  const fail = (err: unknown) => {
    const msg = message(err);
    setError(msg);
    onToast?.(msg);
  };

  // A notice's action, by its button: the page reads the notices again after it.
  const act = async (notice: ProjectNotice, plug?: boolean) => {
    setBusy(notice.subject);
    try {
      if (notice.action === ProjectNoticeAction.ACKNOWLEDGE_CHANGE) {
        await clients.projects.acknowledge({
          project: project.id,
          change: notice.subject,
        });
      } else if (notice.action === ProjectNoticeAction.UNSUMMON_SKILL) {
        await clients.skills.unsummon({
          skill: notice.subject,
          from: project.id,
        });
        const res = await clients.skills.list({ project: project.id });
        setSkills(res.skills);
      } else if (plug) {
        await clients.inbox.plug({ source: notice.subject });
        onToast?.(t("inbox.plugged_toast", { source: notice.subject }));
      } else {
        await clients.inbox.unplug({ source: notice.subject });
        onToast?.(t("inbox.unplugged_toast", { source: notice.subject }));
      }
    } catch (err) {
      fail(err);
    } finally {
      setBusy("");
      void store.changed("", [Change.INBOX, Change.PROJECT]);
      void reshow();
    }
  };

  // A change of the journal marked seen: the gear goes out once nothing else waits.
  const handleSeen = async (change: ConfigChange) => {
    setBusy(change.id);
    try {
      await clients.projects.acknowledge({
        project: project.id,
        change: change.id,
      });
    } catch (err) {
      fail(err);
    } finally {
      setBusy("");
      void store.changed("", [Change.PROJECT]);
      void reshow();
    }
  };

  // A push says how it went on its sync line, never in a toast: the line reads its answer, or its error.
  const handlePush = (sync: ProjectSync) =>
    clients.projects
      .push({ project: project.id, wish: sync.wishId, branch: sync.branch })
      .finally(() => void reshow());

  const handlePull = (sync: ProjectSync) =>
    clients.projects
      .pull({ project: project.id, branch: sync.branch })
      .finally(() => void reshow());

  // A watcher's or a serve's command allowed or taken back: the commands the answer lists replace the shown ones.
  const handleAllow = async (
    kind: "watch" | "serve",
    command: string,
    revoke: boolean,
  ) => {
    try {
      const res = await clients.projects.allow({
        project: project.id,
        [kind]: command,
        revoke,
      });
      setShown((prev) =>
        prev
          ? {
              ...prev,
              watchCommands: res.watchCommands,
              serveCommands: res.serveCommands,
            }
          : prev,
      );
      void reshow();
      return true;
    } catch (err) {
      fail(err);
      return false;
    }
  };

  // A gate turned off on this machine, or back on: written in your own file, never in the repository.
  const handleGate = async (name: string, off: boolean) => {
    setBusy(`gate ${name}`);
    try {
      setGates(
        await clients.projects.gates(
          off
            ? { project: project.id, off: name }
            : { project: project.id, on: name },
        ),
      );
      void reshow();
    } catch (err) {
      fail(err);
    } finally {
      setBusy("");
    }
  };

  const loadReferenceBranches = () =>
    clients.projects
      .branches({ project: project.id, reference: true })
      .then((res) => res.branches);

  const handleSetBranch = async (branch: string) => {
    try {
      await clients.projects.setBranch(
        branch
          ? { project: project.id, branch }
          : { project: project.id, unset: true },
      );
      return true;
    } catch (err) {
      fail(err);
      return false;
    } finally {
      void store.changed("", [Change.PROJECT]);
      void reshow();
    }
  };

  const handleSetPush = async (nextPush: ProjectPush) => {
    try {
      const res = await clients.projects.setPush({
        project: project.id,
        push: nextPush,
      });
      setShown((prev) => (prev ? { ...prev, project: res.project } : prev));
      void reshow();
    } catch (err) {
      fail(err);
    }
  };

  const reference = shown?.reference;
  const panel = (): ReactNode => {
    if (!shown) return null;
    switch (tab) {
      case "general":
        return (
          <GeneralTab
            project={project}
            shown={shown}
            information={information}
            branchInfo={branchInfo}
            missingBranch={toFix.some(
              (n) => n.topic === ProjectNoticeTopic.BRANCH,
            )}
            busy={busy}
            onAct={(n, plug) => void act(n, plug)}
            load={loadReferenceBranches}
            onChoose={handleSetBranch}
          />
        );
      case "workers":
        return <WorkersTab shown={shown} onOpenCircle={onOpenCircle} />;
      case "gates":
        return (
          <GatesTab
            shown={shown}
            gates={gates}
            busy={busy}
            onGate={(name, off) => void handleGate(name, off)}
          />
        );
      case "integration":
        return (
          <IntegrationTab
            project={project}
            shown={shown}
            onPush={handlePush}
            onPull={handlePull}
            onSetPush={(p) => void handleSetPush(p)}
          />
        );
      case "brief":
        return <BriefTab shown={shown} />;
      case "skills":
        return (
          <section className="config-box">
            <h3>
              {skills
                ? t("project.skills_count", { count: skills.length })
                : t("project.skills")}
              <span className="config-from">{t("project.skills_where")}</span>
            </h3>
            {skills === undefined && !error && (
              <p className="muted-text">{t("common.loading")}</p>
            )}
            {skills?.length === 0 && (
              <p className="muted-text">{t("project.no_skills")}</p>
            )}
            {skills?.map((skill) => (
              <SkillLine
                key={`${skill.source}/${skill.name}`}
                skill={skill}
                project={project}
                toFix={toFix.some(
                  (n) =>
                    n.topic === ProjectNoticeTopic.SKILL &&
                    n.subject === (skill.source || skill.name),
                )}
              />
            ))}
            <h4 className="settings-sub">
              {t("project.sources")}
              <span className="config-from">{t("project.sources_where")}</span>
            </h4>
            <p className="muted-text">{t("project.sources_detail")}</p>
            <InboxSources
              sources={sources}
              onToast={(text) => onToast?.(text)}
            />
          </section>
        );
      case "permissions":
        return (
          <PermissionsTab
            shown={shown}
            onAllow={(kind, command) => handleAllow(kind, command, false)}
            onRevoke={(kind, command) => void handleAllow(kind, command, true)}
          />
        );
      case "changes":
        return project.git && shown.watch ? (
          <ConfigChanges
            watch={shown.watch}
            changes={shown.changes}
            busy={busy}
            onSeen={(c) => void handleSeen(c)}
          />
        ) : (
          <p className="muted-text project-tab-empty">
            {t("project.changes_unwatched")}
          </p>
        );
    }
  };

  return (
    <div className="wish-view project-page">
      <PageHeader middle={<OperatingLoad />}>
        <span>{t("project.crumb_projects")}</span>
        <span className="crumb-separator" aria-hidden="true">
          ›
        </span>
        <span>{project.name}</span>
        <span className="crumb-separator" aria-hidden="true">
          ›
        </span>
        <strong>{t("project.crumb_settings")}</strong>
      </PageHeader>
      <div className="mission-scroll">
        <div className="hero mission-header">
          <div className="hero-copy">
            <span className="eyebrow">{t("project.eyebrow")}</span>
            <h1>{project.name}</h1>
            <p className="project-where">
              <span>{project.directory || t("project.no_folder_detail")}</span>
              {project.remote && <span>{project.remote}</span>}
              <span className="badge muted">
                {project.git ? t("project.git") : t("project.not_git")}
              </span>
            </p>
            {shown && (
              <p className="project-files">
                {reference?.commit && (
                  <span>
                    {t("project.files_reference", {
                      ref: reference.ref,
                      commit: reference.commit.slice(0, 7),
                    })}
                  </span>
                )}
                <span title={shown.repositoryFile || undefined}>
                  {t("project.files_repository")}
                </span>
                <span title={shown.developerFile || undefined}>
                  {t("project.files_local")}
                </span>
              </p>
            )}
          </div>
        </div>
        <div className="overview-content project-body">
          {error && <p className="login-message">{error}</p>}
          {shown === undefined && !error && (
            <p className="muted-text">{t("common.loading")}</p>
          )}
          <ToFixBox
            toFix={toFix}
            warnings={warnings}
            busy={busy}
            onAct={(n, plug) => void act(n, plug)}
          />
          <div className="view-tabs project-tabs" role="tablist">
            {PROJECT_TABS.map((id) => (
              <button
                key={id}
                type="button"
                role="tab"
                id={`project-tab-${id}`}
                aria-selected={tab === id}
                className={tab === id ? "active" : ""}
                onClick={() => setTab(id)}
              >
                {t(tabLabels[id])}
                {id === "skills" && skills !== undefined && (
                  <span className="count">{skills.length}</span>
                )}
                {id === "changes" && unseen > 0 && (
                  <span
                    className="count project-unseen"
                    title={t("project.changes_unseen", { count: unseen })}
                  >
                    <Flame size={11} aria-hidden="true" />
                    {unseen}
                  </span>
                )}
              </button>
            ))}
          </div>
          <div
            role="tabpanel"
            className="project-tab"
            aria-labelledby={`project-tab-${tab}`}
          >
            <p className="project-tab-detail">{t(tabDetails[tab])}</p>
            {panel()}
          </div>
        </div>
      </div>
    </div>
  );
}

// SettingRow is one setting of a table: the setting by its key, how it shows (its name, a mark beside it, a line under
// it), its values.
export interface SettingRow {
  name: string;
  mark?: ReactNode;
  note?: ReactNode;
  // The value retained, as it reads: a word in place of the raw one.
  shows?: (setting: ProjectSetting) => ReactNode;
}

// SettingsTable shows settings with their value in the repository's file, in your own and the one retained, and where
// that one comes from: a repository's value your file overrides is struck.
export function SettingsTable({
  title,
  where,
  rows,
  shown,
  children,
}: {
  title: string;
  // Where these settings live, beside the title.
  where?: string;
  rows: SettingRow[];
  shown: ProjectServiceShowResponse;
  children?: ReactNode;
}) {
  const side = (value: string | undefined, struck: boolean) =>
    value === undefined ? (
      <span className="setting-absent">{t("project.value_absent")}</span>
    ) : (
      <code className={struck ? "setting-struck" : ""}>
        {value || t("project.setting_empty")}
      </code>
    );
  return (
    <section className="config-box settings-box">
      <h3>
        {title}
        {where && <span className="config-from">{where}</span>}
      </h3>
      <table className="settings-table">
        <thead>
          <tr>
            <th scope="col">{t("project.col_setting")}</th>
            <th scope="col">{t("project.col_repository")}</th>
            <th scope="col">{t("project.col_local")}</th>
            <th scope="col">{t("project.col_retained")}</th>
          </tr>
        </thead>
        {rows.map((row) => {
          const s = shown.settings.find((x) => x.name === row.name);
          if (!s) return null;
          const overridden =
            s.repository !== undefined && s.developer !== undefined;
          return (
            <tbody key={row.name} className="setting-group">
              <tr data-setting={row.name}>
                <th scope="row">
                  <span className="setting-label">
                    {settingLabels[row.name]
                      ? t(settingLabels[row.name]!)
                      : row.name}
                    {row.mark}
                  </span>
                  <code className="setting-key">{row.name}</code>
                </th>
                <td data-side="repository">{side(s.repository, overridden)}</td>
                <td data-side="local">{side(s.developer, false)}</td>
                <td data-side="retained">
                  <span className="setting-retained">
                    {row.shows
                      ? row.shows(s)
                      : s.value || t("project.setting_empty")}
                  </span>
                  <From source={s.source} shown={shown} />
                </td>
              </tr>
              {row.note && (
                <tr className="setting-note">
                  <td colSpan={4}>{row.note}</td>
                </tr>
              )}
            </tbody>
          );
        })}
      </table>
      {children}
    </section>
  );
}

const onOff = (s: ProjectSetting) =>
  s.value === "true" ? t("project.on") : t("project.off");

// GeneralTab situates the project and readies its worktrees: its reference branch, its branches' names, and the life of
// a worktree, the setup it needs (required) and the install proposed after a push (optional).
function GeneralTab({
  project,
  shown,
  information,
  branchInfo,
  missingBranch,
  busy,
  onAct,
  load,
  onChoose,
}: {
  project: Project;
  shown: ProjectServiceShowResponse;
  information: ProjectNotice[];
  branchInfo: ProjectNotice[];
  missingBranch: boolean;
  busy: string;
  onAct: (n: ProjectNotice, plug?: boolean) => void;
  load: () => Promise<string[]>;
  onChoose: (branch: string) => Promise<boolean>;
}) {
  const install = shown.settings.find((s) => s.name === "install");
  const installOn = Boolean(install?.value);
  const setupRun = (shown.project?.checkRuns ?? []).find(
    (r) => r.setup && r.name.toLowerCase() === "setup",
  );
  return (
    <>
      {information.length > 0 && (
        <section className="config-box">
          <h3>{t("project.information")}</h3>
          {information.map((n) => (
            <NoticeLine
              key={`${n.topic} ${n.subject} ${n.text}`}
              notice={n}
              busy={busy === n.subject}
              onAct={(plug) => onAct(n, plug)}
            />
          ))}
        </section>
      )}
      {project.git && (
        <ReferenceBranch
          reference={shown.reference}
          missing={missingBranch}
          notices={branchInfo}
          shown={shown}
          load={load}
          onChoose={onChoose}
        />
      )}
      <SettingsTable
        title={t("project.branches")}
        shown={shown}
        rows={[{ name: "main_branch" }, { name: "branch" }]}
      >
        <p className="settings-foot">{t("project.branches_foot")}</p>
      </SettingsTable>
      <SettingsTable
        title={t("project.lifecycle")}
        where={t("project.lifecycle_where")}
        shown={shown}
        rows={[
          {
            name: "setup",
            mark: <span className="badge muted">{t("project.required")}</span>,
            note: (
              <>
                {t("project.setup_detail")}
                {setupRun && (
                  <ProjectChecks
                    setup={shown.setup}
                    checks={[]}
                    runs={setupRun ? [setupRun] : []}
                    shown={shown}
                  />
                )}
              </>
            ),
          },
          {
            name: "install",
            mark: (
              <>
                <span className="badge muted">{t("project.optional")}</span>
                <span
                  className={`badge ${installOn ? "" : "muted"}`.trim()}
                  data-install={installOn ? "on" : "off"}
                >
                  {installOn ? t("project.on") : t("project.off")}
                </span>
              </>
            ),
            note: installOn
              ? t("project.install_on_detail")
              : t("project.install_off_detail"),
          },
        ]}
      />
    </>
  );
}

// WorkersTab says what the project gives a worker when neither its task nor the Summoning Circle names anything: its
// agent, model, budget, corrections; its question workers'; the ceilings of the containers its workers start.
function WorkersTab({
  shown,
  onOpenCircle,
}: {
  shown: ProjectServiceShowResponse;
  onOpenCircle?: () => void;
}) {
  const containers = shown.containers;
  const ceiling = (value: number | undefined) =>
    value ? (
      <code>{value}</code>
    ) : (
      <span className="setting-absent">{t("project.value_absent")}</span>
    );
  return (
    <>
      <section className="config-box project-circle">
        <p>
          <Sparkles size={14} aria-hidden="true" />
          {t("project.circle_first")}
        </p>
        {onOpenCircle && (
          <button
            type="button"
            className="button small secondary"
            onClick={onOpenCircle}
          >
            {t("project.circle_open")}
          </button>
        )}
      </section>
      <SettingsTable
        title={t("project.worker_defaults")}
        shown={shown}
        rows={[
          { name: "provider" },
          { name: "model" },
          {
            name: "max_budget_usd",
            shows: (s) => (s.value ? `${s.value} $` : t("project.budget_none")),
          },
          { name: "correction_attempts" },
          { name: "enlighten_workers", shows: onOff },
        ]}
      />
      <SettingsTable
        title={t("project.questions")}
        shown={shown}
        rows={[
          { name: "question_provider" },
          { name: "question_model" },
          {
            name: "question_budget_usd",
            shows: (s) => (s.value ? `${s.value} $` : t("project.budget_none")),
          },
        ]}
      />
      <section className="config-box settings-box">
        <h3>
          {t("project.containers")}
          <span className="config-from">{t("project.containers_where")}</span>
        </h3>
        <p className="settings-lead">{t("project.containers_detail")}</p>
        <table className="settings-table">
          <thead>
            <tr>
              <th scope="col">{t("project.col_ceiling")}</th>
              <th scope="col">{t("project.col_repository")}</th>
              <th scope="col">{t("project.col_local")}</th>
              <th scope="col">{t("project.col_retained")}</th>
            </tr>
          </thead>
          <tbody>
            <tr data-setting="memory_mib">
              <th scope="row">
                <span className="setting-label">{t("project.c_memory")}</span>
                <code className="setting-key">memory_mib</code>
              </th>
              <td>{ceiling(containers?.memoryMib)}</td>
              <td>
                <span className="setting-absent">
                  {t("project.repository_only")}
                </span>
              </td>
              <td>
                <span className="setting-retained">
                  {containers?.memoryMib
                    ? t("project.c_memory_value", {
                        mib: containers.memoryMib,
                      })
                    : t("project.c_memory_default")}
                </span>
              </td>
            </tr>
            <tr data-setting="pids">
              <th scope="row">
                <span className="setting-label">{t("project.c_pids")}</span>
                <code className="setting-key">pids</code>
              </th>
              <td>{ceiling(containers?.pids)}</td>
              <td>
                <span className="setting-absent">
                  {t("project.repository_only")}
                </span>
              </td>
              <td>
                <span className="setting-retained">
                  {containers?.pids || t("project.c_pids_default")}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
        <p className="settings-foot">{t("project.containers_not")}</p>
      </section>
    </>
  );
}

// GatesTab says what Djinn checks before it merges and pushes, and which commands of the workers wait for each other:
// the checks, each through a gate of its name, and the gates the project names in its djinn.yaml, read-only here; a
// gate turned off on this machine, and back on.
function GatesTab({
  shown,
  gates,
  busy,
  onGate,
}: {
  shown: ProjectServiceShowResponse;
  gates?: ProjectServiceGatesResponse;
  busy: string;
  onGate: (name: string, off: boolean) => void;
}) {
  const setting = (name: string) => shown.settings.find((s) => s.name === name);
  const declared = [
    ...(gates?.gates ?? []).map((g) => ({ gate: g, off: false })),
    ...(gates?.offGates ?? []).map((g) => ({ gate: g, off: true })),
  ];
  return (
    <>
      <section className="config-box">
        <h3>
          {t("project.checks")}
          <span className="config-from">{t("project.checks_where")}</span>
        </h3>
        <ProjectChecks
          setup=""
          checks={shown.checks}
          runs={shown.project?.checkRuns ?? []}
          checksFrom={setting("checks")?.source}
          shown={shown}
        />
      </section>
      <SettingsTable
        title={t("project.checks_settings")}
        where={t("project.checks_where")}
        shown={shown}
        rows={[
          { name: "checks" },
          {
            name: "test_one",
            shows: (s) => s.value || t("project.test_one_none"),
          },
        ]}
      />
      <section className="config-box gates-box">
        <h3>
          {t("project.gates")}
          <span className="config-from">{t("project.gates_where")}</span>
        </h3>
        <p className="settings-lead">{t("project.gates_detail")}</p>
        <p className="settings-lead">{t("project.gates_match")}</p>
        {gates === undefined && (
          <p className="muted-text">{t("common.loading")}</p>
        )}
        {gates && declared.length === 0 && (
          <p className="muted-text">{t("project.gates_none")}</p>
        )}
        {declared.map(({ gate, off }) => (
          <GateCard
            key={gate.name}
            gate={gate}
            project={shown.project?.name ?? ""}
            off={off}
            busy={busy === `gate ${gate.name}`}
            onToggle={() => onGate(gate.name, !off)}
          />
        ))}
        {(gates?.warnings ?? []).map((w) => (
          <p className="login-message" key={w}>
            {w}
          </p>
        ))}
        <p className="settings-foot">{t("project.gates_local")}</p>
      </section>
    </>
  );
}

// GateCard is one gate the project declares: its commands alone and beside, read-only, and whether it holds on this
// machine.
function GateCard({
  gate,
  project,
  off,
  busy,
  onToggle,
}: {
  gate: ProjectGate;
  project: string;
  off: boolean;
  busy: boolean;
  onToggle: () => void;
}) {
  const list = (patterns: string[]) =>
    patterns.length ? (
      <ul className="gate-patterns">
        {patterns.map((p) => (
          <li key={p}>
            <code>{p}</code>
          </li>
        ))}
      </ul>
    ) : (
      <p className="setting-absent">{t("project.gate_no_pattern")}</p>
    );
  return (
    <div
      className={`gate-card ${off ? "off" : ""}`.trim()}
      data-gate={gate.name}
    >
      <div className="gate-card-head">
        <strong>
          {gate.name}
          {project && <span className="gate-project">@{project}</span>}
        </strong>
        <span className="badge muted">
          {off ? t("project.gate_off") : t("project.gate_on")}
        </span>
        <button
          type="button"
          className="button small secondary"
          disabled={busy}
          onClick={onToggle}
        >
          {off ? t("project.gate_turn_on") : t("project.gate_turn_off")}
        </button>
      </div>
      <div className="gate-sides">
        <div>
          <p className="gate-side">
            {t("project.gate_alone", { count: gate.alone.length })}
          </p>
          {list(gate.alone)}
        </div>
        <div>
          <p className="gate-side">
            {t("project.gate_beside", { count: gate.beside.length })}
          </p>
          {list(gate.beside)}
        </div>
      </div>
    </div>
  );
}

// IntegrationTab says when the project's work joins main and the remote, and when to install: the push cadence, the
// branch of new wishes, main brought in, the generated files' conflicts, the babysit, and the install's moments.
function IntegrationTab({
  project,
  shown,
  onPush,
  onPull,
  onSetPush,
}: {
  project: Project;
  shown: ProjectServiceShowResponse;
  onPush: (sync: ProjectSync) => Promise<unknown>;
  onPull: (sync: ProjectSync) => Promise<unknown>;
  onSetPush: (push: ProjectPush) => void;
}) {
  const currentPush =
    shown.project?.push ?? project.push ?? ProjectPush.STANDARD;
  const syncs = shown.project?.syncs ?? project.syncs;
  const push = shown.settings.find((s) => s.name === "push");
  return (
    <>
      {project.git && (
        <section className="config-box">
          <div className="setting-row">
            <div>
              <strong>{t("project.push_cadence")}</strong>
              {push && <From source={push.source} shown={shown} />}
              <p>
                {currentPush === ProjectPush.ON_DEMAND
                  ? t("project.push_on_demand_detail")
                  : t("project.push_standard_detail")}
              </p>
              <SyncLines syncs={syncs} onPush={onPush} onPull={onPull} />
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
                  onClick={() => onSetPush(ProjectPush.STANDARD)}
                >
                  {t("project.push_standard")}
                </button>
                <button
                  type="button"
                  className={
                    currentPush === ProjectPush.ON_DEMAND ? "active" : ""
                  }
                  onClick={() => onSetPush(ProjectPush.ON_DEMAND)}
                >
                  {t("project.push_on_demand")}
                </button>
              </div>
            </div>
          </div>
        </section>
      )}
      <SettingsTable
        title={t("project.integration")}
        shown={shown}
        rows={[
          { name: "push" },
          {
            name: "push_strategy",
            note: (
              <>
                {t("project.push_strategy_detail")}
                {shown.reference && (
                  <WorkersStart
                    reference={shown.reference}
                    strategy={shown.project?.pushStrategy}
                  />
                )}
              </>
            ),
          },
          { name: "merge_main" },
          { name: "merge_main_minutes" },
          { name: "generated" },
          { name: "generate" },
          {
            name: "babysit",
            shows: onOff,
            note: t("project.babysit_detail"),
          },
        ]}
      />
      <SettingsTable
        title={t("project.install")}
        shown={shown}
        rows={[
          { name: "install_questions" },
          { name: "install_releases", shows: onOff },
          { name: "install_builds", shows: onOff },
        ]}
      >
        <p className="settings-foot">{t("project.install_foot")}</p>
      </SettingsTable>
    </>
  );
}

// BriefTab is the text Djinn adds to the first prompt of each worker that edits the project, as it stands: drawn from
// its settings. The team's own instructions to workers go in the repository's agent instructions (decision Q61 C).
function BriefTab({ shown }: { shown: ProjectServiceShowResponse }) {
  return (
    <>
      <section className="config-box">
        <h3>
          {t("project.brief")}
          <span className="config-from">{t("project.brief_where")}</span>
        </h3>
        {shown.workersBrief ? (
          <p className="workers-brief">{shown.workersBrief}</p>
        ) : (
          <p className="muted-text">{t("project.brief_none")}</p>
        )}
      </section>
      <section className="config-box">
        <h3>{t("project.brief_team")}</h3>
        <p className="settings-lead">{t("project.brief_team_detail")}</p>
      </section>
    </>
  );
}

// PermissionsTab says what the project's workers may do, from its .agents/permissions.txtpb read on the reference
// branch, where it applies and how each agent receives it; and the commands you add for yourself on this machine, for
// the watchers and an ifrit's serve.
function PermissionsTab({
  shown,
  onAllow,
  onRevoke,
}: {
  shown: ProjectServiceShowResponse;
  onAllow: (kind: "watch" | "serve", command: string) => Promise<boolean>;
  onRevoke: (kind: "watch" | "serve", command: string) => void;
}) {
  const perms: Permissions | undefined = shown.permissions;
  const ref = shown.reference?.ref;
  const yesNo = (v: boolean) => (v ? t("project.yes") : t("project.no"));
  return (
    <>
      <section className="config-box">
        <h3>
          {t("project.permissions")}
          <span className="config-from">
            {ref
              ? t("project.permissions_where_ref", { ref })
              : t("project.permissions_where")}
          </span>
        </h3>
        {perms ? (
          <dl className="permissions-list">
            <dt>{t("project.perm_edit")}</dt>
            <dd>{yesNo(perms.edit)}</dd>
            <dt>{t("project.perm_mode")}</dt>
            <dd>
              {perms.mode === Mode.AUTO && perms.edit
                ? t("project.perm_mode_auto")
                : t("project.perm_mode_listed")}
            </dd>
            <dt>{t("project.perm_network")}</dt>
            <dd>{yesNo(perms.network)}</dd>
            <dt>
              {t("project.perm_commands", { count: perms.commands.length })}
            </dt>
            <dd>
              <CommandList commands={perms.commands} />
            </dd>
            <dt>
              {t("project.perm_denied", {
                count: perms.deniedCommands.length,
              })}
            </dt>
            <dd>
              <CommandList commands={perms.deniedCommands} />
            </dd>
          </dl>
        ) : (
          <p className="muted-text">{t("project.permissions_none")}</p>
        )}
      </section>
      <section className="config-box permissions-context">
        <h3>{t("project.perm_context")}</h3>
        <p className="settings-lead">{t("project.perm_context_detail")}</p>
        <table className="settings-table agents-table">
          <thead>
            <tr>
              <th scope="col">{t("project.col_agent")}</th>
              <th scope="col">{t("project.col_commands")}</th>
              <th scope="col">{t("project.col_denied")}</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <th scope="row">Claude</th>
              <td>{t("project.perm_claude")}</td>
              <td>{t("project.perm_claude_denied")}</td>
            </tr>
            <tr>
              <th scope="row">Codex</th>
              <td>{t("project.perm_codex")}</td>
              <td>{t("project.perm_codex_denied")}</td>
            </tr>
            <tr>
              <th scope="row">Antigravity</th>
              <td>{t("project.perm_agy")}</td>
              <td>{t("project.perm_agy_denied")}</td>
            </tr>
            <tr>
              <th scope="row">{t("project.perm_djinn_runs")}</th>
              <td>{t("project.perm_watchers")}</td>
              <td>{t("project.perm_watchers_denied")}</td>
            </tr>
          </tbody>
        </table>
        <p className="settings-foot">{t("project.perm_not_applied")}</p>
      </section>
      <section className="config-box">
        <h3>
          {t("project.local_additions")}
          <span className="config-from">{t("project.local_only")}</span>
        </h3>
        <p className="settings-lead">{t("project.local_additions_detail")}</p>
        <h4 className="settings-sub">{t("project.watch_commands")}</h4>
        <WatchCommands
          commands={shown.watchCommands}
          onAllow={(command) => onAllow("watch", command)}
          onRevoke={(command) => onRevoke("watch", command)}
        />
        <h4 className="settings-sub">{t("project.serve_commands")}</h4>
        <WatchCommands
          kind="serve"
          commands={shown.serveCommands}
          onAllow={(command) => onAllow("serve", command)}
          onRevoke={(command) => onRevoke("serve", command)}
        />
      </section>
    </>
  );
}

function CommandList({ commands }: { commands: string[] }) {
  if (commands.length === 0)
    return <span className="setting-absent">{t("project.none")}</span>;
  return (
    <ul className="gate-patterns">
      {commands.map((c) => (
        <li key={c}>
          <code>{c}</code>
        </li>
      ))}
    </ul>
  );
}

// projectPageOf is the project a page value names, its identifier after PROJECT_PAGE; "" for another page.
export function projectPageOf(page: string | undefined): string {
  return page?.startsWith(PROJECT_PAGE) ? page.slice(PROJECT_PAGE.length) : "";
}
