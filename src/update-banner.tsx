// A discreet banner at the top of the window when a newer Djinn waits at the path of the running one (installed with
// `go tool task install`), with the button that restarts on it. Nothing restarts without that click. After a restart,
// it lists the terminals that did not start again. Shown only when djinn serves the page (a DjinnProvider). A release
// links its notes, which the system's browser opens.
import { useEffect, useState } from "react";

import { useDjinn } from "./data/djinn";
import type { UpdateState } from "./data/update";
import { t } from "./i18n";
import "./update-banner.css";

export type Phase =
  | { kind: "idle" }
  | { kind: "restarting" }
  | { kind: "failed"; message: string };

export function UpdateBanner() {
  const djinn = useDjinn();
  const api = djinn?.update;
  const [state, setState] = useState<UpdateState | undefined>();
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const [dismissed, setDismissed] = useState(false);

  useEffect(() => api?.subscribe(setState), [api]);
  // A new Djinn answers on a page that reloads: what it reports replaces what this one said.
  useEffect(() => {
    if (state?.ready === "") setPhase({ kind: "idle" });
  }, [state?.ready]);

  if (!api || !state) return null;

  async function install() {
    setPhase({ kind: "restarting" });
    try {
      await api!.update();
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
      onInstall={() => void install()}
      onDismiss={() => setDismissed(true)}
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
  onInstall,
  onDismiss,
  onNotes,
}: {
  state: UpdateState;
  phase: Phase;
  dismissed: boolean;
  onInstall: () => void;
  onDismiss: () => void;
  onNotes: (url: string) => void;
}) {
  const notResumed = dismissed ? [] : state.notResumed;
  if (!state.ready && !notResumed.length) return null;
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
