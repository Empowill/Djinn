// A discreet banner at the top of the window when a newer Djinn waits at the path of the running one (installed with
// `go tool task install`), with the button that restarts on it. Nothing restarts without that click. After a restart,
// it lists the terminals that did not start again. Shown only when djinn serves the page (a DjinnProvider).
import { useEffect, useState } from "react";

import { useDjinn } from "./data/djinn";
import type { UpdateState } from "./data/update";
import { t } from "./i18n";
import "./update-banner.css";

type Phase =
  | { kind: "idle" }
  | { kind: "restarting" }
  | { kind: "failed"; message: string };

export function UpdateBanner() {
  const api = useDjinn()?.update;
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

  const notResumed = dismissed ? [] : state.notResumed;
  if (!state.ready && !notResumed.length) return null;
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
          {phase.kind !== "restarting" && (
            <button type="button" onClick={() => void install()}>
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
          <button type="button" onClick={() => setDismissed(true)}>
            {t("update.dismiss")}
          </button>
        </div>
      )}
    </div>
  );
}
