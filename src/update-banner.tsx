// A banner at the top of the window, raised in brass over the rest and headed by the djinn's mark, when a newer Djinn
// waits at the path of the running one (installed with `go tool task install`), with the button that restarts on it.
// Nothing restarts without that click. After a restart, it lists the terminals that did not start again. Shown only
// when djinn serves the page (a DjinnProvider). A release links its notes, which the system's browser opens. Once a
// batch of finished work is pushed to an integration branch, in a project that names an install command, it proposes
// to install that build and restart on it; while it installs, it says what the install waits for, then that it builds,
// and which step of its command runs, then that Djinn restarts. A build of the running Djinn's line installs by itself
// (W554): the banner then says the newer Djinn is ready, and its Restart is the person's click.
//
// What Djinn says of an install is the truth, not the call that asked for it (W554): a call whose answer never comes
// (a Djinn frozen, a lost link) leaves no spinner. Once Djinn says the install runs, its end comes from Djinn: installed,
// or failed with why and the last lines its command wrote, a Try again beside it. A click Djinn says nothing of within
// ANSWER fails, saying so; a restart that does not come within RESTART too.
//
// Each part holds one line: what waits, named by its commit, the branch and wish it comes from, and what it adds to the
// running Djinn or lacks of it, its actions beside it; the tasks and commits unfold on demand (W468). Each branch keeps
// its build: the branch the running Djinn was built from shows first, with any branch that carries its line on (its
// build holds the running commit: a wish moved its integration branch, W527), the other branches' folded under one
// line. A
// build, or the newer Djinn ready, dismissed with its ×, stays hidden, in this window's storage, until another comes.
import { type ReactNode, useEffect, useState } from "react";

import { useDjinn } from "./data/djinn";
import {
  type BuildProposal,
  InstallStep,
  type Installing,
  type UpdateState,
} from "./data/update";
import { Brand } from "./frame";
import { t } from "./i18n";
import "./update-banner.css";

export type Phase =
  | { kind: "idle" }
  | { kind: "restarting" }
  // seen: Djinn said it runs the install; its end comes from Djinn from then on.
  | { kind: "installing"; sha?: string; seen?: boolean }
  | { kind: "installed"; sha: string }
  | { kind: "failed"; message: string; sha?: string };

// How long a click waits for Djinn to say it installs, and for the restart to come, before the banner says it failed.
export const ANSWER = 30_000;
export const RESTART = 180_000;

// What the window dismissed, kept across reloads and restarts: a build by its commit, the newer Djinn ready by its
// version (readyKey).
const DISMISSED = "djinn.update.dismissed";
// The most dismissals kept: the oldest are forgotten first.
const KEEP = 50;

// readyKey is the key of the newer Djinn of version among the dismissed.
export const readyKey = (version: string) => `ready:${version}`;

function readDismissed(): string[] {
  try {
    const kept: unknown = JSON.parse(
      window.localStorage.getItem(DISMISSED) ?? "[]",
    );
    return Array.isArray(kept)
      ? kept.filter((k): k is string => typeof k === "string")
      : [];
  } catch {
    return [];
  }
}

export function UpdateBanner() {
  const djinn = useDjinn();
  const api = djinn?.update;
  const [state, setState] = useState<UpdateState | undefined>();
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const [dismissed, setDismissed] = useState(false);
  const [dismissedBuilds, setDismissedBuilds] = useState(readDismissed);

  useEffect(() => api?.subscribe(setState), [api]);
  // A new Djinn answers on a page that reloads: what it reports replaces what this one said.
  useEffect(() => {
    if (state?.ready === "") setPhase({ kind: "idle" });
  }, [state?.ready]);
  // Once Djinn says it installs the build clicked, how the install ends is Djinn's to say, answer or not.
  const running = state?.installing;
  useEffect(() => {
    if (phase.kind !== "installing" || !phase.sha) return;
    if (running?.sha === phase.sha && running.step !== InstallStep.FAILED) {
      if (!phase.seen) setPhase({ ...phase, seen: true });
    } else if (phase.seen || running?.sha === phase.sha) {
      setPhase({ kind: "idle" });
    }
  }, [running, phase]);
  // A click Djinn says nothing of, a restart that does not come: said, never an endless spinner.
  useEffect(() => {
    const wait =
      phase.kind === "installing" && !phase.seen
        ? ANSWER
        : phase.kind === "restarting"
          ? RESTART
          : 0;
    if (!wait) return;
    const timer = setTimeout(
      () =>
        setPhase({
          kind: "failed",
          message: t(
            phase.kind === "restarting"
              ? "update.no_restart"
              : "update.no_answer",
            { seconds: Math.round(wait / 1000) },
          ),
          sha: phase.kind === "installing" ? phase.sha : undefined,
        }),
      wait,
    );
    return () => clearTimeout(timer);
  }, [phase]);

  if (!api || !state) return null;

  async function install(build?: string) {
    setPhase(
      build ? { kind: "installing", sha: build } : { kind: "restarting" },
    );
    try {
      const res = await api!.update(build);
      // A build that installed no newer Djinn restarts nothing.
      if (build && !res.version) setPhase({ kind: "installed", sha: build });
      else setPhase({ kind: "restarting" });
    } catch (error) {
      setPhase({
        kind: "failed",
        message: error instanceof Error ? error.message : String(error),
        sha: build,
      });
    }
  }

  function dismissBuild(key: string) {
    const next = [...dismissedBuilds.filter((k) => k !== key), key].slice(
      -KEEP,
    );
    setDismissedBuilds(next);
    try {
      window.localStorage.setItem(DISMISSED, JSON.stringify(next));
    } catch {
      // No storage: dismissed until the page reloads.
    }
  }

  return (
    <UpdateBannerView
      state={state}
      phase={phase}
      dismissed={dismissed}
      dismissedBuilds={dismissedBuilds}
      onInstall={() => void install()}
      onInstallBuild={(sha) => void install(sha)}
      onDismiss={() => setDismissed(true)}
      onDismissBuild={dismissBuild}
      onNotes={(url) =>
        void djinn!.clients.ui
          .openExternal({ url })
          .catch(() => window.open(url, "_blank", "noopener"))
      }
    />
  );
}

// UpdateBannerView is the banner for a state, without its hooks: the screens render it alone in the tests. open names
// the parts unfolded at first, by key: "ready", a build's commit, "others".
export function UpdateBannerView({
  state,
  phase,
  dismissed,
  dismissedBuilds = [],
  open = [],
  onInstall,
  onInstallBuild = () => {},
  onDismiss,
  onDismissBuild = () => {},
  onNotes,
}: {
  state: UpdateState;
  phase: Phase;
  dismissed: boolean;
  dismissedBuilds?: string[];
  open?: string[];
  onInstall: () => void;
  onInstallBuild?: (sha: string) => void;
  onDismiss: () => void;
  onDismissBuild?: (key: string) => void;
  onNotes: (url: string) => void;
}) {
  const notResumed = dismissed ? [] : state.notResumed;
  // Where the install that runs now stands, as Djinn says it: from another window too, or after a reload.
  const installing = state.installing;
  // Once its update is asked, the newer Djinn stays, whatever was dismissed.
  const ready =
    state.ready &&
    (phase.kind === "restarting" ||
      (phase.kind === "failed" && !phase.sha) ||
      !dismissedBuilds.includes(readyKey(state.ready)))
      ? state.ready
      : "";
  const builds = (state.builds ?? []).filter(
    (b) => !dismissedBuilds.includes(b.sha),
  );
  // An install that failed, as Djinn says it: why, and the last lines its command wrote.
  const failed =
    installing?.step === InstallStep.FAILED ? installing : undefined;
  // The running line: its branch, and those that carry it on.
  const running = builds.filter((b) => b.running || b.continues);
  const others = builds.filter((b) => !b.running && !b.continues);
  if (
    !ready &&
    !notResumed.length &&
    !builds.length &&
    phase.kind !== "installed"
  )
    return null;
  // Only a web link opens: anything else from a release stays out of the banner.
  const notes = /^https?:\/\//i.test(state.notesUrl) ? state.notesUrl : "";
  // The djinn's mark heads the banner: the first of its parts carries it.
  const first = ready
    ? "ready"
    : running.length
      ? running[0].sha
      : others.length
        ? "others"
        : phase.kind === "installed"
          ? "installed"
          : "not-resumed";
  const readyBuild = ready ? state.readyBuild : undefined;

  const buildPart = (build: BuildProposal) => {
    const told = installing?.sha === build.sha ? installing : undefined;
    const fail = failed?.sha === build.sha ? failed : undefined;
    const busy =
      (told && !fail) ||
      (phase.kind === "installing" &&
        !phase.seen &&
        (!phase.sha || phase.sha === build.sha)) ||
      phase.kind === "restarting";
    return (
      <Part
        key={build.sha}
        mark={first === build.sha}
        open={open.includes(build.sha)}
        hover={origin(build)}
        title={
          fail ? (
            t("update.install_failed", {
              sha: build.sha.slice(0, 8),
              message: fail.error,
            })
          ) : phase.kind === "failed" && phase.sha === build.sha ? (
            t("update.failed", { message: phase.message })
          ) : (
            <>
              {t("update.build")} <Named build={build} />
            </>
          )
        }
        actions={
          busy ? (
            <span>
              {told
                ? installStep(told)
                : t(
                    phase.kind === "restarting"
                      ? "update.restarting"
                      : "update.build_installing",
                  )}
            </span>
          ) : (
            <button
              type="button"
              className="update-banner-primary"
              onClick={() => onInstallBuild(build.sha)}
            >
              {t(
                fail || (phase.kind === "failed" && phase.sha === build.sha)
                  ? "update.retry"
                  : "update.build_install",
              )}
            </button>
          )
        }
        details={
          (fail?.log.length || hasDetails(build)) && (
            <>
              {fail && fail.log.length > 0 && (
                <pre className="update-banner-log">{fail.log.join("\n")}</pre>
              )}
              {hasDetails(build) && <BuildDetails build={build} />}
            </>
          )
        }
        onDismiss={busy ? undefined : () => onDismissBuild(build.sha)}
      />
    );
  };

  return (
    <div className="update-banner" role="status">
      {ready && (
        <Part
          mark={first === "ready"}
          open={open.includes("ready")}
          hover={[
            t("update.versions", { current: state.current, ready }),
            readyBuild && origin(readyBuild),
          ]
            .filter(Boolean)
            .join("\n")}
          title={
            phase.kind === "failed" && !phase.sha ? (
              t("update.failed", { message: phase.message })
            ) : phase.kind === "restarting" ? (
              t("update.restarting")
            ) : state.installed ? (
              readyBuild ? (
                <>
                  {t("update.ready_version_build", { version: ready })}{" "}
                  <Named build={readyBuild} />
                </>
              ) : (
                t("update.ready_version", { version: ready })
              )
            ) : readyBuild ? (
              <>
                {t("update.ready_build")} <Named build={readyBuild} />
              </>
            ) : (
              t("update.ready")
            )
          }
          actions={
            <>
              {phase.kind !== "restarting" && (
                <button
                  type="button"
                  className="update-banner-primary"
                  onClick={onInstall}
                >
                  {t(state.installed ? "update.restart" : "update.install")}
                </button>
              )}
              {notes && (
                <a
                  href={notes}
                  target="_blank"
                  rel="noopener noreferrer"
                  onClick={(e) => {
                    e.preventDefault();
                    onNotes(notes);
                  }}
                >
                  {t("update.notes")}
                </a>
              )}
            </>
          }
          details={
            readyBuild &&
            hasDetails(readyBuild) && <BuildDetails build={readyBuild} />
          }
          onDismiss={
            phase.kind === "restarting"
              ? undefined
              : () => onDismissBuild(readyKey(ready))
          }
        />
      )}
      {running.map(buildPart)}
      {others.length > 0 && (
        <Part
          mark={first === "others"}
          open={open.includes("others")}
          title={t("update.other_builds", {
            count: others.length,
            branches: others.map((b) => b.branch).join(", "),
          })}
          details={
            <div className="update-banner-others">{others.map(buildPart)}</div>
          }
        />
      )}
      {phase.kind === "installed" &&
        !builds.some((b) => b.sha === phase.sha) && (
          <Part
            mark={first === "installed"}
            title={t("update.build_installed", { sha: phase.sha.slice(0, 8) })}
          />
        )}
      {notResumed.length > 0 && (
        <Part
          mark={first === "not-resumed"}
          title={t("update.not_resumed")}
          actions={
            <button
              type="button"
              className="update-banner-dismiss"
              onClick={onDismiss}
            >
              {t("update.dismiss")}
            </button>
          }
        >
          <ul className="update-banner-terminals">
            {notResumed.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </Part>
      )}
    </div>
  );
}

// origin says where a build comes from, in words, for the hover: its commit, branch, wish and project, then its gist.
function origin(build: BuildProposal): string {
  return [
    [build.sha.slice(0, 8), build.branch, build.wishTitle, build.project]
      .filter(Boolean)
      .join(" · "),
    gist(build).join(" · "),
  ]
    .filter(Boolean)
    .join("\n");
}

// gist says what a build adds to the running Djinn and what it lacks of it; without a comparison, the tasks it
// brings.
function gist(build: BuildProposal): string[] {
  return [
    build.base
      ? build.tasks.length
        ? t("update.adds_tasks", { count: build.tasks.length })
        : build.changesCount
          ? t("update.adds_commits", { count: build.changesCount })
          : ""
      : build.tasks.join(", "),
    build.lacksCount ? t("update.older", { count: build.lacksCount }) : "",
  ].filter(Boolean);
}

// Named is a build on its line: its short commit, the branch and wish it comes from, then what it adds to the running
// Djinn and what it lacks of it, quietly.
function Named({ build }: { build: BuildProposal }) {
  const said = gist(build);
  return (
    <>
      <code>{build.sha.slice(0, 8)}</code>
      {build.branch && (
        <>
          {" "}
          {build.continues
            ? t("update.from_continuing", {
                branch: build.branch,
                line: build.continues,
              })
            : t("update.from", { branch: build.branch })}
        </>
      )}
      {build.wishTitle && (
        <span className="update-banner-quiet"> · {build.wishTitle}</span>
      )}
      {said.length > 0 && (
        <span className="update-banner-quiet"> — {said.join(" · ")}</span>
      )}
    </>
  );
}

function hasDetails(build: BuildProposal): boolean {
  return (
    build.summaries.length > 0 ||
    build.tasks.length > 0 ||
    build.changes.length > 0 ||
    build.lacks.length > 0
  );
}

// BuildDetails is what a build holds, unfolded: the tasks it adds, each with its title and its worker's summary
// folded, the commits' titles folded, and those of the running Djinn it lacks, folded.
function BuildDetails({ build }: { build: BuildProposal }) {
  const summaries = build.summaries.length
    ? build.summaries
    : build.tasks.map((code) => ({ code, title: code, summary: "" }));
  return (
    <>
      {(summaries.length > 0 || build.changes.length > 0) && (
        <strong>
          {build.base
            ? t("update.adds_since", { base: build.base.slice(0, 8) })
            : t("update.build_changes")}
        </strong>
      )}
      {summaries.length > 0 && (
        <ul className="update-banner-tasks">
          {summaries.map((s, i) => (
            <li key={s.code || i}>
              {s.summary ? (
                <details className="update-banner-task">
                  <summary>
                    <TaskLine code={s.code} title={s.title} />
                  </summary>
                  <div className="update-banner-summary">{s.summary}</div>
                </details>
              ) : (
                <div className="update-banner-task-line">
                  <TaskLine code={s.code} title={s.title} />
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {build.changes.length > 0 && (
        <details className="update-banner-commits">
          <summary>
            {build.base
              ? t("update.commits", {
                  count: build.changesCount || build.changes.length,
                })
              : t("update.build_commits")}
          </summary>
          <ul>
            {build.changes.map((line, i) => (
              <li key={i}>{line}</li>
            ))}
          </ul>
        </details>
      )}
      {build.lacks.length > 0 && (
        <details className="update-banner-commits update-banner-lacks">
          <summary>
            {t("update.lacks", {
              count: build.lacksCount || build.lacks.length,
            })}
          </summary>
          <ul>
            {build.lacks.map((line, i) => (
              <li key={i}>{line}</li>
            ))}
          </ul>
        </details>
      )}
    </>
  );
}

function TaskLine({ code, title }: { code: string; title: string }) {
  return code ? (
    <>
      <code>{code}</code> {title}
    </>
  ) : (
    <>{title}</>
  );
}

// installStep says where an install stands: what it waits for, then that it prepares and builds, and the step of its
// command that runs, then that Djinn restarts. One Djinn installs by itself says so.
function installStep(installing: Installing): string {
  const step = (() => {
    switch (installing.step) {
      case InstallStep.WAITING:
        return t(
          installing.auto ? "update.auto_waiting" : "update.build_waiting",
          { why: installing.waiting },
        );
      case InstallStep.PREPARING:
        return t("update.build_preparing");
      case InstallStep.BUILDING:
        return t("update.build_building");
      case InstallStep.RESTARTING:
        return t("update.restarting");
    }
    return t("update.build_installing");
  })();
  const doing =
    installing.doing && installing.step !== InstallStep.WAITING
      ? ` (${installing.doing})`
      : "";
  return installing.auto && installing.step !== InstallStep.WAITING
    ? `${t("update.auto")} ${step}${doing}`
    : `${step}${doing}`;
}

// A part of the banner: one line, its title cut short when it does not fit (the hover tells it whole), its actions
// beside it, then the toggle that unfolds its details at the banner's full width, and the × that dismisses it. Its
// children show below at once.
function Part({
  mark,
  title,
  hover,
  actions,
  details,
  open: unfolded = false,
  onDismiss,
  children,
}: {
  mark: boolean;
  title: ReactNode;
  hover?: string;
  actions?: ReactNode;
  details?: ReactNode;
  open?: boolean;
  onDismiss?: () => void;
  children?: ReactNode;
}) {
  const [open, setOpen] = useState(unfolded);
  const body = details && open ? details : children;
  return (
    <section className="update-banner-part">
      <div className="update-banner-head">
        <div className="update-banner-title" title={hover}>
          {mark && <Brand small />}
          <span className="update-banner-text">{title}</span>
        </div>
        {(actions || details || onDismiss) && (
          <div className="update-banner-actions">
            {actions}
            {details && (
              <button
                type="button"
                className="update-banner-more"
                aria-expanded={open}
                onClick={() => setOpen(!open)}
              >
                {t(open ? "update.less" : "update.details")}
              </button>
            )}
            {onDismiss && (
              <button
                type="button"
                className="update-banner-close"
                aria-label={t("update.dismiss")}
                title={t("update.dismiss")}
                onClick={onDismiss}
              >
                ×
              </button>
            )}
          </div>
        )}
      </div>
      {body && <div className="update-banner-body">{body}</div>}
    </section>
  );
}
