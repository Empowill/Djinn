// The lead's lifecycle on the wish page: a lead can be running, have never started, or have ended while retaining
// its terminal output. The page keeps these states visible even when the terminal is folded away.
import { RotateCcw, Terminal } from "lucide-react";
import { useState } from "react";

import type { Wish } from "../gen/ts/plan/v1/plan_pb";
import { t } from "./i18n";
import { StatusBadge } from "./status";

export function LeadStatus({
  wish,
  onView,
  onStart,
}: {
  wish: Wish;
  onView: () => void;
  onStart: () => Promise<void>;
}) {
  const started = !!wish.lead;
  const running = wish.leadRunning;
  const ended = !running && !!wish.leadExit;
  const error = wish.leadError;
  const [starting, setStarting] = useState(false);
  const code = wish.leadExit?.code ?? 0;
  const tone = running
    ? "running"
    : ended && code !== 0
      ? "failed"
      : ended
        ? "done"
        : "planned";
  const label = running
    ? t("lead_status.running")
    : ended
      ? t("lead_status.ended", { code: wish.leadExit?.code ?? 0 })
      : t("lead_status.not_started");
  const detail = running
    ? t("lead_status.running_detail")
    : ended
      ? t("lead_status.ended_detail")
      : t("lead_status.not_started_detail");

  return (
    <section className="lead-status" aria-label={t("lead_status.label")}>
      <div className="lead-status-copy">
        <StatusBadge tone={tone} label={label} />
        <span className="lead-status-detail">{detail}</span>
        {error && (
          <span className="lead-status-error" role="alert">
            {error}
          </span>
        )}
      </div>
      <div className="lead-status-actions">
        {(running || ended) && (
          <button className="button secondary small" onClick={onView}>
            <Terminal size={13} aria-hidden="true" />
            {t("lead_status.view_terminal")}
          </button>
        )}
        {!running && (
          <StartLeadButton
            first={!started && !ended && !error}
            busy={starting}
            onStart={async () => {
              if (starting) return;
              setStarting(true);
              try {
                await onStart();
              } finally {
                setStarting(false);
              }
            }}
          />
        )}
      </div>
    </section>
  );
}

function StartLeadButton({
  first,
  busy,
  onStart,
}: {
  first: boolean;
  busy: boolean;
  onStart: () => Promise<void>;
}) {
  return (
    <button
      className="button accent small"
      onClick={() => void onStart()}
      disabled={busy}
      title={
        first ? t("lead_status.start_detail") : t("lead_status.retry_detail")
      }
    >
      <RotateCcw size={13} aria-hidden="true" />
      {first ? t("lead_status.start") : t("lead_status.retry")}
    </button>
  );
}
