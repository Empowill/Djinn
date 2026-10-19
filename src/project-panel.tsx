// A project's configuration, as Djinn reads it on this machine: what waits for you first, its reference branch, the
// checks Djinn runs on its work, the sources of the inbox this machine reads, the role of each skill, how it pushes,
// its settings and where each comes from, and the watcher commands you allow.
import {
  Check,
  Flame,
  GitBranch,
  GitCommitHorizontal,
  GitFork,
  Info,
  Plug,
  Unplug,
} from "lucide-react";
import {
  type FormEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  Change,
  type CheckRun,
  CheckWhen,
  type ConfigChange,
  type ConfigWatch,
  type Project,
  type ProjectCheck,
  type ProjectNotice,
  ProjectNoticeAction,
  ProjectNoticeKind,
  ProjectNoticeTopic,
  ProjectPush,
  type ProjectReference,
  PushStrategy,
  type ProjectServiceShowResponse,
  type ProjectSetting,
  type ProjectSync,
  SettingSource,
  type Skill,
} from "../gen/ts/plan/v1/plan_pb";
import { Command } from "./copy-command";
import { message } from "./data/client";
import { useClients, useData, useStore } from "./data/djinn";
import { span, when } from "./data/format";
import { ModalFrame } from "./frame";
import { t } from "./i18n";
import { InboxSources } from "./inbox";
import { SyncLines } from "./project-sync";
import { UpdateModelsSection } from "./summoning-circle";
import "./project-panel.css";

// The settings other parts of the panel show, with their own words: not repeated in its settings.
const shownElsewhere = new Set([
  "setup",
  "checks",
  "main_branch",
  "push",
  "watch_commands",
]);

const sourceKeys = {
  [SettingSource.UNSPECIFIED]: "project.from_default",
  [SettingSource.DEFAULT]: "project.from_default",
  [SettingSource.REPOSITORY]: "project.from_team",
  [SettingSource.DEVELOPER]: "project.from_you",
  [SettingSource.WISH]: "project.from_wish",
} as const;

// From says where a value comes from: Djinn's default, the team's file or yours; the file itself on hover.
export function From({
  source,
  shown,
}: {
  source: SettingSource;
  shown?: ProjectServiceShowResponse;
}) {
  const file =
    source === SettingSource.REPOSITORY
      ? shown?.repositoryFile
      : source === SettingSource.DEVELOPER
        ? shown?.developerFile
        : "";
  return (
    <span className="config-from" title={file || undefined}>
      {t(sourceKeys[source])}
    </span>
  );
}

// ProjectPanel shows a project's configuration as Djinn reads it (ProjectService.Show), the things to fix first,
// and its skills (SkillService.List): its own and those it summons, each with its role for Djinn.
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
  const store = useStore();
  const [skills, setSkills] = useState<Skill[] | undefined>();
  const [shown, setShown] = useState<ProjectServiceShowResponse>();
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

  const currentPush =
    shown?.project?.push ?? project.push ?? ProjectPush.STANDARD;
  const syncs = shown?.project?.syncs ?? project.syncs;
  const notices = shown?.notices ?? [];
  const toFix = notices.filter((n) => n.kind === ProjectNoticeKind.TO_FIX);
  const branchInfo = notices.filter(
    (n) =>
      n.kind !== ProjectNoticeKind.TO_FIX &&
      n.topic === ProjectNoticeTopic.BRANCH,
  );
  const information = notices.filter(
    (n) => n.kind !== ProjectNoticeKind.TO_FIX && !branchInfo.includes(n),
  );
  const setting = (name: string) =>
    shown?.settings.find((s) => s.name === name);

  const fail = (err: unknown) => {
    const msg = message(err);
    setError(msg);
    onToast?.(msg);
  };

  // A notice's action, by its button: the panel reads the notices again after it.
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

  // A pull says on its sync line why it was refused; pulled, the line follows the branch.
  const handlePull = (sync: ProjectSync) =>
    clients.projects
      .pull({ project: project.id, branch: sync.branch })
      .finally(() => void reshow());

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
      fail(err);
      return false;
    }
  };

  // The branches the reference branch may name: the remote's, as this copy last fetched them.
  const loadReferenceBranches = () =>
    clients.projects
      .branches({ project: project.id, reference: true })
      .then((res) => res.branches);

  // A reference branch chosen, or your choice taken back (""): written in your own file. The notices, and the
  // projects' count of things to fix, are read again.
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
      setShown((prev) =>
        prev
          ? { ...prev, project: res.project }
          : ({ project: res.project } as ProjectServiceShowResponse),
      );
    } catch (err) {
      fail(err);
    }
  };

  return (
    <ModalFrame
      title={project.name}
      eyebrow={t("project.eyebrow")}
      onClose={onClose}
      wide
    >
      <p className="project-where">
        <span>{project.directory || t("project.no_folder_detail")}</span>
        {project.remote && <span>{project.remote}</span>}
        <span className="badge muted">
          {project.git ? t("project.git") : t("project.not_git")}
        </span>
      </p>
      {error && <p className="login-message">{error}</p>}
      {shown === undefined && !error && (
        <p className="muted-text">{t("common.loading")}</p>
      )}
      {toFix.length > 0 && (
        <section className="config-box to-fix">
          <h3>
            <Flame size={14} aria-hidden="true" />
            {t("project.to_fix_heading", { count: toFix.length })}
          </h3>
          {toFix.map((n) => (
            <NoticeLine
              key={`${n.topic} ${n.subject} ${n.text}`}
              notice={n}
              busy={busy === n.subject}
              onAct={(plug) => void act(n, plug)}
            />
          ))}
        </section>
      )}
      {project.git && shown && (
        <ReferenceBranch
          reference={shown.reference}
          missing={toFix.some((n) => n.topic === ProjectNoticeTopic.BRANCH)}
          notices={branchInfo}
          shown={shown}
          load={loadReferenceBranches}
          onChoose={handleSetBranch}
        />
      )}
      {information.length > 0 && (
        <section className="config-box">
          <h3>{t("project.information")}</h3>
          {information.map((n) => (
            <NoticeLine
              key={`${n.topic} ${n.subject} ${n.text}`}
              notice={n}
              busy={busy === n.subject}
              onAct={(plug) => void act(n, plug)}
            />
          ))}
        </section>
      )}
      {shown && (
        <section className="config-box">
          <h3>{t("project.checks")}</h3>
          <ProjectChecks
            setup={shown.setup}
            checks={shown.checks}
            runs={shown.project?.checkRuns ?? []}
            setupFrom={setting("setup")?.source}
            checksFrom={setting("checks")?.source}
            shown={shown}
          />
        </section>
      )}
      <section className="config-box">
        <h3>
          {t("project.sources")}
          <span className="config-from">{t("project.sources_where")}</span>
        </h3>
        <p className="muted-text">{t("project.sources_detail")}</p>
        <InboxSources sources={sources} onToast={(text) => onToast?.(text)} />
      </section>
      <section className="config-box">
        <h3>
          {skills
            ? t("project.skills_count", { count: skills.length })
            : t("project.skills")}
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
      </section>
      {project.git && shown?.watch && (
        <ConfigChanges
          watch={shown.watch}
          changes={shown.changes}
          busy={busy}
          onSeen={(c) => void handleSeen(c)}
        />
      )}
      {project.git && (
        <section className="config-box">
          <div className="setting-row">
            <div>
              <strong>{t("project.push_cadence")}</strong>
              {setting("push") && (
                <From source={setting("push")!.source} shown={shown} />
              )}
              <p>
                {currentPush === ProjectPush.ON_DEMAND
                  ? t("project.push_on_demand_detail")
                  : t("project.push_standard_detail")}
              </p>
              <SyncLines
                syncs={syncs}
                onPush={handlePush}
                onPull={handlePull}
              />
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
        </section>
      )}
      {shown && shown.settings.length > 0 && (
        <ProjectSettings settings={shown.settings} shown={shown} />
      )}
      <section className="config-box">
        <UpdateModelsSection projectId={project.id} />
      </section>
      <section className="config-box">
        <h3>{t("project.watch_commands")}</h3>
        <WatchCommands
          commands={shown?.watchCommands ?? []}
          onAllow={(watch) => handleAllow(watch, false)}
          onRevoke={(watch) => handleAllow(watch, true)}
        />
      </section>
    </ModalFrame>
  );
}

// NoticeLine is one thing Djinn found in a project's configuration: its sentence, the error as read, and the action
// that settles it, by its button or where to do it.
export function NoticeLine({
  notice,
  busy,
  onAct,
}: {
  notice: ProjectNotice;
  busy: boolean;
  // plug: true plugs the source in, false says no to it; unset for the other buttons.
  onAct: (plug?: boolean) => void;
}) {
  const toFix = notice.kind === ProjectNoticeKind.TO_FIX;
  const button = (label: string, effect: string, plug?: boolean): ReactNode => (
    <div className="notice-choice">
      <button
        type="button"
        className="button small secondary"
        disabled={busy}
        onClick={() => onAct(plug)}
      >
        {plug === true && <Plug size={14} />}
        {plug === false && <Unplug size={14} />}
        {label}
      </button>
      <p>{effect}</p>
    </div>
  );
  let actions: ReactNode = null;
  switch (notice.action) {
    case ProjectNoticeAction.FIX_FILE:
      if (notice.file)
        actions = (
          <p className="notice-where">
            {t("project.notice_fix_file")} <Command text={notice.file} />
          </p>
        );
      break;
    case ProjectNoticeAction.NAME_BRANCH:
      if (notice.file)
        actions = (
          <p className="notice-where">
            {t("project.notice_name_branch")} <Command text={notice.file} />
          </p>
        );
      break;
    case ProjectNoticeAction.PLUG_SOURCE:
      actions = (
        <div className="notice-choices">
          {button(t("inbox.plug"), t("project.notice_plug_effect"), true)}
          {button(
            t("project.notice_decline"),
            t("project.notice_decline_effect"),
            false,
          )}
        </div>
      );
      break;
    case ProjectNoticeAction.UNPLUG_SOURCE:
      actions = (
        <div className="notice-choices">
          {button(t("inbox.unplug"), t("inbox.unplug_detail"), false)}
        </div>
      );
      break;
    case ProjectNoticeAction.ACKNOWLEDGE_CHANGE:
      actions = (
        <div className="notice-choices">
          {button(t("project.change_seen"), t("project.change_seen_effect"))}
        </div>
      );
      break;
    case ProjectNoticeAction.UNSUMMON_SKILL:
      actions = (
        <div className="notice-choices">
          {button(
            t("project.notice_unsummon"),
            t("project.notice_unsummon_effect"),
          )}
        </div>
      );
      break;
  }
  return (
    <div className={`notice ${toFix ? "to-fix" : ""}`}>
      <p className="notice-text">
        {toFix ? (
          <Flame size={13} aria-hidden="true" />
        ) : (
          <Info size={13} aria-hidden="true" />
        )}
        <span>{notice.text}</span>
      </p>
      {notice.detail && <pre className="notice-detail">{notice.detail}</pre>}
      {actions}
      {notice.command && (
        <p className="notice-where">
          {t("project.notice_command")} <Command text={notice.command} />
        </p>
      )}
    </div>
  );
}

// waits tells whether a change of the journal waits for your look: found after the first look, not marked seen, and
// a line of it asks (plan.Waits).
export function waits(change: ConfigChange): boolean {
  return (
    !change.firstLook && !change.seenTime && change.lines.some((l) => l.asks)
  );
}

// ConfigChanges is the journal of changes of a project's configuration on its reference branch, the newest first:
// each commit that changed it, what it changed in sentences, and a Seen button on those that wait for your look.
// Above, how the watch went: a failed fetch, said as git said it.
export function ConfigChanges({
  watch,
  changes,
  busy,
  onSeen,
}: {
  watch: ConfigWatch;
  changes: ConfigChange[];
  // The change being marked seen.
  busy: string;
  onSeen: (change: ConfigChange) => void;
}) {
  return (
    <section className="config-box">
      <h3>
        {t("project.changes", { ref: watch.ref })}
        <span className="config-from">{t("project.changes_where")}</span>
      </h3>
      {watch.fetchError && (
        <div className="notice">
          <p className="notice-text">
            <Info size={13} aria-hidden="true" />
            <span>{t("project.changes_fetch_failed")}</span>
          </p>
          <pre className="notice-detail">{watch.fetchError}</pre>
        </div>
      )}
      {changes.length === 0 && (
        <p className="muted-text">{t("project.changes_none")}</p>
      )}
      {changes.map((c) => (
        <ChangeLine
          key={c.id}
          change={c}
          busy={busy === c.id}
          onSeen={() => onSeen(c)}
        />
      ))}
    </section>
  );
}

// ChangeLine is one entry of the journal of changes: when, which commit and who; its title; what it changed; and
// whether it waits for your look, was seen, or was applied with nothing to decide.
function ChangeLine({
  change,
  busy,
  onSeen,
}: {
  change: ConfigChange;
  busy: boolean;
  onSeen: () => void;
}) {
  const waiting = waits(change);
  const state = waiting
    ? ""
    : change.firstLook
      ? t("project.change_history")
      : change.seenTime
        ? t("project.change_seen_at", { when: when(change.seenTime) })
        : t("project.change_applied");
  return (
    <div className={`config-line config-change ${waiting ? "waits" : ""}`}>
      {waiting ? (
        <Flame size={14} aria-hidden="true" />
      ) : (
        <GitCommitHorizontal size={14} aria-hidden="true" />
      )}
      <div>
        <p className="change-head">
          <span>{when(change.commitTime)}</span>
          <code>{change.commit.slice(0, 7)}</code>
          {change.author && <span>{change.author}</span>}
          {change.commits > 1 && (
            <span>
              {t("project.change_commits", { count: change.commits })}
            </span>
          )}
        </p>
        {change.title && <strong>{change.title}</strong>}
        <ul className="change-lines">
          {change.lines.map((l, i) => (
            <li key={i} className={waiting && l.asks ? "asks" : ""}>
              {l.text}
            </li>
          ))}
        </ul>
        {waiting ? (
          <div className="notice-choices">
            <div className="notice-choice">
              <button
                type="button"
                className="button small secondary"
                disabled={busy}
                onClick={onSeen}
              >
                <Check size={14} />
                {t("project.change_seen")}
              </button>
              <p>{t("project.change_seen_effect")}</p>
            </div>
          </div>
        ) : (
          <p className="muted-text">{state}</p>
        )}
      </div>
    </div>
  );
}

// ReferenceBranch is the branch Djinn takes for a project's main: the ref and commit it reads, where its name comes
// from, what it does with it; and the branch the copy has checked out when it is another.
export function ReferenceBranch({
  reference,
  missing,
  notices,
  shown,
  load,
  onChoose,
}: {
  reference?: ProjectReference;
  // A notice to fix says Djinn finds none.
  missing: boolean;
  // The information about the branches: the copy on another branch.
  notices: ProjectNotice[];
  shown?: ProjectServiceShowResponse;
  // The branches it may name, and the choice of one: without them, no selector.
  load?: () => Promise<string[]>;
  onChoose?: (branch: string) => Promise<boolean>;
}) {
  if (!reference && !missing) return null;
  return (
    <section className="config-box">
      <div className="config-line">
        <GitBranch size={14} aria-hidden="true" />
        <div>
          <strong>{t("project.reference")}</strong>
          {reference ? (
            <p>
              <code>{reference.branch}</code>{" "}
              {reference.commit
                ? t("project.reference_at", {
                    ref: reference.ref,
                    commit: reference.commit.slice(0, 7),
                  })
                : t("project.reference_not_fetched", {
                    ref: reference.ref,
                  })}{" "}
              <From source={reference.source} shown={shown} />
            </p>
          ) : (
            <p>{t("project.reference_none")}</p>
          )}
          <p className="muted-text">{t("project.reference_detail")}</p>
          {load && onChoose && (
            <BranchChoice
              reference={reference}
              load={load}
              onChoose={onChoose}
            />
          )}
        </div>
      </div>
      {reference && (
        <WorkersStart
          reference={reference}
          strategy={shown?.project?.pushStrategy}
        />
      )}
      {notices.map((n) => (
        <div className="config-line" key={n.text}>
          <Info size={14} aria-hidden="true" />
          <div>
            <p>{n.text}</p>
          </div>
        </div>
      ))}
    </section>
  );
}

// WorkersStart says where a worker's worktree starts, for each push strategy, the project's own marked: never from
// the developer's copy, whatever it has checked out (W227).
export function WorkersStart({
  reference,
  strategy,
}: {
  reference: ProjectReference;
  strategy?: PushStrategy;
}) {
  const perAzima = strategy === PushStrategy.AZIMA;
  const mark = (on: boolean) =>
    on && <span className="badge muted">{t("project.start_default")}</span>;
  return (
    <div className="config-line">
      <GitFork size={14} aria-hidden="true" />
      <div>
        <strong>{t("project.start")}</strong>
        <p>
          {t("project.start_wish", { branch: reference.branch })}{" "}
          {mark(!perAzima)}
        </p>
        <p>
          {t("project.start_azima", {
            ref: reference.ref,
            branch: reference.branch,
          })}{" "}
          {mark(perAzima)}
        </p>
        <p className="muted-text">{t("project.start_never")}</p>
      </div>
    </div>
  );
}

// BranchChoice changes a project's reference branch for you: a list of the remote's branches, read when it opens,
// and where the choice is written. Your choice can be taken back, the team's file or Djinn's default then naming it.
export function BranchChoice({
  reference,
  load,
  onChoose,
}: {
  reference?: ProjectReference;
  load: () => Promise<string[]>;
  // branch "": take your choice back. False when it was refused, the panel saying why.
  onChoose: (branch: string) => Promise<boolean>;
}) {
  const [branches, setBranches] = useState<string[] | undefined>();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const [picked, setPicked] = useState("");
  const [busy, setBusy] = useState(false);
  const yours = reference?.source === SettingSource.DEVELOPER;

  const start = async () => {
    setOpen(true);
    setError("");
    setPicked(reference?.branch ?? "");
    try {
      setBranches(await load());
    } catch (err) {
      setError(message(err));
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    const ok = await onChoose(picked);
    setBusy(false);
    if (ok) setOpen(false);
  };

  if (!open)
    return (
      <button
        type="button"
        className="button small secondary"
        onClick={() => void start()}
      >
        {t("project.reference_change")}
      </button>
    );
  // The current branch stays listed even when this copy has not fetched it.
  const listed =
    branches && reference && !branches.includes(reference.branch)
      ? [reference.branch, ...branches]
      : branches;
  return (
    <form className="branch-choice" onSubmit={(e) => void submit(e)}>
      {error && <p className="login-message">{error}</p>}
      <div className="branch-choice-row">
        {listed === undefined && !error && (
          <p className="muted-text">{t("project.reference_reading")}</p>
        )}
        {listed !== undefined && (
          <>
            <select
              aria-label={t("project.reference")}
              value={picked}
              onChange={(e) => setPicked(e.target.value)}
            >
              {(!reference || yours) && (
                <option value="">
                  {yours
                    ? t("project.reference_unset")
                    : t("project.reference_pick")}
                </option>
              )}
              {listed.map((b) => (
                <option key={b} value={b}>
                  {b}
                </option>
              ))}
            </select>
            <button
              type="submit"
              className="button small secondary"
              disabled={
                busy || (picked === "" ? !yours : picked === reference?.branch)
              }
            >
              {t("project.reference_choose")}
            </button>
          </>
        )}
        <button
          type="button"
          className="button small secondary"
          onClick={() => setOpen(false)}
        >
          {t("common.cancel")}
        </button>
      </div>
      <p className="muted-text">{t("project.reference_where")}</p>
    </form>
  );
}

// skillRoles says what a skill is to Djinn: the wish template it offers, the inbox source it declares, the babysit
// of the project's pull requests; or know-how for the workers alone.
function skillRoles(skill: Skill): string[] {
  const roles: string[] = [];
  if (skill.template)
    roles.push(t("project.skill_template", { title: skill.template }));
  if (skill.inboxSource) roles.push(t("project.skill_source"));
  if (skill.babysit) roles.push(t("project.skill_babysit"));
  return roles;
}

// SkillLine is one skill of a project: what it does, its role for Djinn, and where it lives.
export function SkillLine({
  skill,
  project,
  toFix,
}: {
  skill: Skill;
  project: Project;
  // A notice to fix names it: Djinn cannot read it, or cannot use part of it.
  toFix: boolean;
}) {
  const roles = skillRoles(skill);
  const role = skill.missing
    ? ""
    : toFix
      ? t("project.skill_to_fix")
      : roles.length
        ? roles.join(" · ")
        : t("project.skill_know_how");
  const prefix = project.directory ? `${project.directory}/` : "";
  const folder =
    !skill.source && prefix && skill.directory.startsWith(prefix)
      ? skill.directory.slice(prefix.length, skill.directory.lastIndexOf("/"))
      : "";
  return (
    <div className="setting-row skill-row">
      <div>
        <strong>{skill.name}</strong>
        {folder && <span className="config-from">{folder}</span>}
        <p>{skill.missing || skill.description}</p>
        {role && (
          <p className={toFix ? "skill-role to-fix" : "skill-role"}>{role}</p>
        )}
      </div>
      {skill.source && (
        <span className="badge muted">
          {t("project.summoned", { source: skill.source })}
        </span>
      )}
    </div>
  );
}

// ProjectSettings lists a project's settings, each with its value and where it comes from: those a file sets first,
// those at Djinn's default folded under them.
export function ProjectSettings({
  settings,
  shown,
}: {
  settings: ProjectSetting[];
  shown?: ProjectServiceShowResponse;
}) {
  const rows = settings.filter((s) => !shownElsewhere.has(s.name));
  const isDefault = (s: ProjectSetting) =>
    s.source === SettingSource.DEFAULT ||
    s.source === SettingSource.UNSPECIFIED;
  const set = rows.filter((s) => !isDefault(s));
  const defaults = rows.filter(isDefault);
  const row = (s: ProjectSetting) => (
    <div className="config-setting" key={s.name}>
      <code>{s.name}</code>
      <span className={s.value ? "" : "muted-text"}>
        {s.value || t("project.setting_empty")}
      </span>
      <From source={s.source} shown={shown} />
    </div>
  );
  return (
    <section className="config-box">
      <h3>{t("project.settings")}</h3>
      <p className="muted-text">{t("project.settings_detail")}</p>
      {set.map(row)}
      {defaults.length > 0 && (
        <details className="config-more">
          <summary>
            {t("project.settings_defaults", { count: defaults.length })}
          </summary>
          {defaults.map(row)}
        </details>
      )}
    </section>
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
  setupFrom,
  checksFrom,
  shown,
}: {
  setup: string;
  checks: ProjectCheck[];
  runs: CheckRun[];
  // Where the setup and the checks come from: Djinn's default, the team's file or yours.
  setupFrom?: SettingSource;
  checksFrom?: SettingSource;
  shown?: ProjectServiceShowResponse;
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
    from?: SettingSource,
  ) => (
    <div className="setting-row" key={key}>
      <div>
        <strong>{name}</strong>
        {from !== undefined && <From source={from} shown={shown} />}
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
          setupFrom,
        )}
      {checks.map((c) =>
        row(
          `check-${c.name}`,
          c.name,
          c.command,
          c.when,
          last(c.name, false),
          checksFrom,
        ),
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
