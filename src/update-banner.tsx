// A discreet banner at the top of the window when a newer Djinn waits at the path of the running one (installed with
// `go tool task install`), with the button that restarts on it. Nothing restarts without that click. After a restart,
// it lists the terminals that did not start again. Shown only when djinn serves the page (a DjinnProvider). A release
// links its notes, which the system's browser opens. Once a batch of finished work is committed into a wish's
// integration branch, in a project that names an install command, it proposes to install that build and restart on
// it, with what changed and what to check.
import { useEffect, useState } from "react";

import { useDjinn } from "./data/djinn";
import type { UpdateState } from "./data/update";
import { t } from "./i18n";
import "./update-banner.css";

export type Phase =
  | { kind: "idle" }
  | { kind: "restarting" }
  | { kind: "installing" }
  | { kind: "installed"; sha: string }
  | { kind: "failed"; message: string };

export function UpdateBanner() {
  const djinn = useDjinn();
  const api = djinn?.update;
  const [state, setState] = useState<UpdateState | undefined>();
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const [dismissed, setDismissed] = useState(false);
  // The build dismissed, by its commit: a newer one shows again.
  const [dismissedBuild, setDismissedBuild] = useState("");

  useEffect(() => api?.subscribe(setState), [api]);
  // A new Djinn answers on a page that reloads: what it reports replaces what this one said.
  useEffect(() => {
    if (state?.ready === "") setPhase({ kind: "idle" });
  }, [state?.ready]);

  if (!api || !state) return null;

  async function install(build?: string) {
    setPhase({ kind: build ? "installing" : "restarting" });
    try {
      const res = await api!.update(build);
      // A build that installed no newer Djinn restarts nothing.
      if (build && !res.version) setPhase({ kind: "installed", sha: build });
      else setPhase({ kind: "restarting" });
    } catch (error) {
      setPhase({
        kind: "failed",
        message: error instanceof Error ? error.message : String(error),
      });
    }
  }

  return (
    <UpdateBannerView
      state={state}
      phase={phase}
      dismissed={dismissed}
      dismissedBuild={dismissedBuild}
      onInstall={() => void install()}
      onInstallBuild={(sha) => void install(sha)}
      onDismiss={() => setDismissed(true)}
      onDismissBuild={(sha) => setDismissedBuild(sha)}
      onNotes={(url) =>
        void djinn!.clients.ui
          .openExternal({ url })
          .catch(() => window.open(url, "_blank", "noopener"))
      }
    />
  );
}

// UpdateBannerView is the banner for a state, without its hooks: the screens render it alone in the tests.
export function UpdateBannerView({
  state,
  phase,
  dismissed,
  dismissedBuild = "",
  onInstall,
  onInstallBuild = () => {},
  onDismiss,
  onDismissBuild = () => {},
  onNotes,
}: {
  state: UpdateState;
  phase: Phase;
  dismissed: boolean;
  dismissedBuild?: string;
  onInstall: () => void;
  onInstallBuild?: (sha: string) => void;
  onDismiss: () => void;
  onDismissBuild?: (sha: string) => void;
  onNotes: (url: string) => void;
}) {
  const notResumed = dismissed ? [] : state.notResumed;
  const build = state.build?.sha === dismissedBuild ? undefined : state.build;
  if (
    !state.ready &&
    !notResumed.length &&
    !build &&
    phase.kind !== "installed"
  )
    return null;
  // Only a web link opens: anything else from a release stays out of the banner.
  const notes = /^https?:\/\//i.test(state.notesUrl) ? state.notesUrl : "";
  return (
    <div className="update-banner" role="status">
      {state.ready && (
        <div className="update-banner-row">
          <span
            title={t("update.versions", {
              current: state.current,
              ready: state.ready,
            })}
          >
            {phase.kind === "failed"
              ? t("update.failed", { message: phase.message })
              : phase.kind === "restarting"
                ? t("update.restarting")
                : t("update.ready")}
          </span>
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
          {phase.kind !== "restarting" && (
            <button type="button" onClick={onInstall}>
              {t("update.install")}
            </button>
          )}
        </div>
      )}
      {build && (
        <div className="update-banner-row update-build">
          <div>
            <span>
              {phase.kind === "failed"
                ? t("update.failed", { message: phase.message })
                : t("update.build", {
                    tasks: build.tasks.join(", "),
                    branch: build.branch,
                    project: build.project,
                  })}{" "}
              <code>{build.sha.slice(0, 8)}</code>
            </span>
            {build.changes.length > 0 && (
              <>
                <strong>{t("update.build_changes")}</strong>
                <ul>
                  {build.changes.map((line, i) => (
                    <li key={i}>{line}</li>
                  ))}
                </ul>
              </>
            )}
            {build.checks.length > 0 && (
              <>
                <strong>{t("update.build_checks")}</strong>
                <ul>
                  {build.checks.map((line, i) => (
                    <li key={i}>{line}</li>
                  ))}
                </ul>
              </>
            )}
          </div>
          {phase.kind === "installing" || phase.kind === "restarting" ? (
            <span>
              {t(
                phase.kind === "installing"
                  ? "update.build_installing"
                  : "update.restarting",
              )}
            </span>
          ) : (
            <>
              <button type="button" onClick={() => onInstallBuild(build.sha)}>
                {t("update.build_install")}
              </button>
              <button type="button" onClick={() => onDismissBuild(build.sha)}>
                {t("update.dismiss")}
              </button>
            </>
          )}
        </div>
      )}
      {phase.kind === "installed" && !build && (
        <div className="update-banner-row">
          <span>
            {t("update.build_installed", { sha: phase.sha.slice(0, 8) })}
          </span>
        </div>
      )}
      {notResumed.length > 0 && (
        <div className="update-banner-row">
          <div>
            <span>{t("update.not_resumed")}</span>
            <ul>
              {notResumed.map((line) => (
                <li key={line}>{line}</li>
              ))}
            </ul>
          </div>
          <button type="button" onClick={onDismiss}>
            {t("update.dismiss")}
          </button>
        </div>
      )}
    </div>
  );
}
