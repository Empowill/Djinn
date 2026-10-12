// The branches Djinn pushes that are out of sync with their remote, one short line each: the branch, then ↑ the
// commits to push and ↓ those the remote has that it lacks; what it belongs to and the full sentence on hover; a small
// button pushes it. The side panel's project row, the azima card and the project dialog read them alike.
// A push shows on its line until it ends, as the server records it (ProjectSync.push), whoever started it: a spinner
// while its push checks and git push run, a check mark a few seconds once pushed, a cross with the reason when its
// checks hold it or it failed.
import {
  ArrowUpFromLine,
  Check,
  ChevronDown,
  ChevronRight,
  LoaderCircle,
  X,
} from "lucide-react";
import { useEffect, useState } from "react";

import {
  PushStage,
  type ProjectSync,
  type ProjectServicePushResponse,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { syncDescription } from "./data/format";
import { t } from "./i18n";

// How long a line shows its check mark once its branch is pushed.
export const PUSHED_SHOWN_MS = 8000;

// isOutOfSync tells whether the branch has commits to push, or the remote commits it lacks.
export function isOutOfSync(sync?: ProjectSync): sync is ProjectSync {
  return Boolean(sync && (sync.ahead > 0 || sync.behind > 0));
}

// isPushRunning tells whether a push of the branch runs now: its push checks, or git push.
export function isPushRunning(sync?: ProjectSync): boolean {
  const stage = sync?.push?.stage;
  return stage === PushStage.CHECKING || stage === PushStage.PUSHING;
}

// pushedAt is when the branch's last push ended pushed, in milliseconds; 0 when it did not.
function pushedAt(sync: ProjectSync): number {
  return sync.push?.stage === PushStage.PUSHED ? msOf(sync.push.endTime) : 0;
}

// isSyncShown tells whether the branch has a line: out of sync, its push running, or pushed a moment ago.
export function isSyncShown(
  sync: ProjectSync | undefined,
  now = Date.now(),
): sync is ProjectSync {
  if (!sync) return false;
  return (
    isOutOfSync(sync) ||
    isPushRunning(sync) ||
    now - pushedAt(sync) < PUSHED_SHOWN_MS
  );
}

// PushOutcome is what a push the window started answered, while the server's own state does not say more: the remote
// held the branch already, or why it failed; at is when it answered, in milliseconds.
export type PushOutcome = { upToDate?: boolean; error?: string; at?: number };

// msOf is t in milliseconds; 0 without it.
function msOf(t?: { seconds: bigint; nanos: number }): number {
  return t ? Number(t.seconds) * 1000 + Math.floor(t.nanos / 1e6) : 0;
}

// PushStatus is what a line shows of its push: kind picks its icon, title says it on hover.
export type PushStatus = {
  kind: "" | "running" | "pushed" | "held" | "failed" | "up-to-date";
  title: string;
};

// pushStatus is what the line of sync shows of its push, from the server's state first, then from outcome, what the
// window's own push answered unless a push started since; pushing: the window's push of it waits for its answer.
export function pushStatus(
  sync: ProjectSync,
  outcome: PushOutcome | undefined,
  pushing = false,
  now = Date.now(),
): PushStatus {
  const run = sync.push;
  if (pushing && !isPushRunning(sync))
    return {
      kind: "running",
      title: t("project.pushing_branch", { branch: sync.branch }),
    };
  if (outcome && msOf(run?.startTime) > (outcome.at ?? 0)) outcome = undefined;
  switch (run?.stage) {
    case PushStage.CHECKING:
      return {
        kind: "running",
        title: run.check
          ? t("project.push_checking", { check: run.check })
          : t("project.push_checking_all"),
      };
    case PushStage.PUSHING:
      return {
        kind: "running",
        title: t("project.push_pushing", {
          branch: sync.branch,
          remote: sync.remote,
        }),
      };
    case PushStage.HELD:
      return {
        kind: "held",
        title: [
          t("project.push_held"),
          run.note,
          sync.correction &&
            t("project.push_corrected", { code: sync.correction }),
        ]
          .filter(Boolean)
          .join("\n"),
      };
    case PushStage.FAILED:
      return {
        kind: "failed",
        title: [t("project.push_failed"), run.note].filter(Boolean).join("\n"),
      };
  }
  if (outcome?.error)
    return {
      kind: "failed",
      title: [t("project.push_failed"), outcome.error].join("\n"),
    };
  if (outcome?.upToDate)
    return {
      kind: "up-to-date",
      title: t("project.push_up_to_date_detail", {
        remote: sync.remote,
        target: sync.target,
      }),
    };
  if (now - pushedAt(sync) < PUSHED_SHOWN_MS)
    return {
      kind: "pushed",
      title: t("project.push_pushed", {
        remote: sync.remote,
        target: sync.target,
      }),
    };
  return { kind: "", title: "" };
}

// usePushes runs the window's pushes of a list of lines, one at a time: which branch pushes now, and what each push
// answered. An error is said on its line, never as a toast: onPush lets it through.
export function usePushes(
  onPush?: (sync: ProjectSync) => Promise<unknown> | void,
) {
  const [pushing, setPushing] = useState("");
  const [outcomes, setOutcomes] = useState<Record<string, PushOutcome>>({});
  const push = async (sync: ProjectSync) => {
    if (!onPush || pushing) return;
    setPushing(sync.branch);
    let outcome: PushOutcome = {};
    try {
      const res = (await onPush(sync)) as
        Partial<ProjectServicePushResponse> | undefined;
      outcome = { upToDate: Boolean(res?.upToDate) };
    } catch (err) {
      outcome = { error: message(err) };
    } finally {
      setOutcomes((all) => ({
        ...all,
        [sync.branch]: { ...outcome, at: Date.now() },
      }));
      setPushing("");
    }
  };
  return { pushing, outcomes, push };
}

// useShownTick renders again when the check mark of a line among syncs is due to go.
function useShownTick(syncs: readonly ProjectSync[], now?: number) {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (now !== undefined) return;
    const at = Date.now();
    const due = syncs
      .map((sync) => pushedAt(sync) + PUSHED_SHOWN_MS - at)
      .filter((left) => left > 0);
    if (due.length === 0) return;
    const timer = setTimeout(
      () => setTick((n) => n + 1),
      Math.min(...due) + 50,
    );
    return () => clearTimeout(timer);
  }, [syncs, now, tick]);
}

// syncTitle says what the branch belongs to, its wish and its azima, then how it stands against its remote.
export function syncTitle(sync: ProjectSync): string {
  const owner = sync.azimaId
    ? t("project.sync_of_azima", {
        code: sync.azimaCode,
        title: sync.azimaTitle,
        wish: sync.wishTitle,
      })
    : sync.wishTitle && t("project.sync_of_wish", { wish: sync.wishTitle });
  return [owner, syncDescription(sync)].filter(Boolean).join("\n");
}

// SyncLine is one branch's line. busy is why its button waits: a push runs; pushing: its own, the window's. outcome
// is what the window's push of it answered; now, for tests, the time its check mark is measured against.
export function SyncLine({
  sync,
  busy = "",
  pushing = false,
  outcome,
  now,
  onPush,
}: {
  sync: ProjectSync;
  busy?: string;
  pushing?: boolean;
  outcome?: PushOutcome;
  now?: number;
  onPush?: () => void;
}) {
  useShownTick([sync], now);
  const status = pushStatus(sync, outcome, pushing, now);
  const running = status.kind === "running";
  const label = running
    ? status.title
    : busy || t("project.push_branch", { branch: sync.branch });
  // Nothing to push, no button: the remote holds the branch, or has commits it lacks only. A held push may go once its
  // checks pass, even with the branch pushed by hand.
  const pushable =
    sync.ahead > 0 || status.kind === "held" || status.kind === "running";
  return (
    <div className="sync-line" title={syncTitle(sync)}>
      <span className="sync-branch">{sync.branch}</span>
      <span className="sync-counts">
        {sync.ahead > 0 && <span className="sync-ahead">↑{sync.ahead}</span>}
        {sync.behind > 0 && <span className="sync-behind">↓{sync.behind}</span>}
        {status.kind === "up-to-date" && (
          <span className="sync-note" title={status.title}>
            {t("project.push_up_to_date")}
          </span>
        )}
      </span>
      {(status.kind === "pushed" ||
        status.kind === "held" ||
        status.kind === "failed") && (
        <span
          className={`sync-status ${status.kind}`}
          role="img"
          title={status.title}
          aria-label={status.title}
        >
          {status.kind === "pushed" ? <Check size={12} /> : <X size={12} />}
        </span>
      )}
      {onPush && pushable && (
        <button
          type="button"
          className="icon-button sync-push"
          disabled={Boolean(busy) || running}
          title={label}
          aria-label={label}
          onClick={(e) => {
            e.stopPropagation();
            onPush();
          }}
        >
          {running ? (
            <LoaderCircle className="sync-spinner" size={12} />
          ) : (
            <ArrowUpFromLine size={12} />
          )}
        </button>
      )}
    </div>
  );
}

// SyncLines lists the branches out of sync among syncs, or whose push runs or just ended, in their order; more than
// three fold behind the first two. While one pushes, every button waits.
export function SyncLines({
  syncs,
  now,
  onPush,
}: {
  syncs?: readonly ProjectSync[];
  now?: number;
  onPush?: (sync: ProjectSync) => Promise<unknown> | void;
}) {
  const { pushing, outcomes, push } = usePushes(onPush);
  const [open, setOpen] = useState(false);
  const all = syncs ?? [];
  useShownTick(all, now);
  const out = all.filter((sync) => isSyncShown(sync, now));
  if (out.length === 0) return null;
  const folds = out.length > 3;
  const shown = folds && !open ? out.slice(0, 2) : out;
  const running = pushing || all.some(isPushRunning);
  const busy = (sync: ProjectSync) =>
    pushing === sync.branch
      ? t("project.pushing_branch", { branch: sync.branch })
      : running && !isPushRunning(sync)
        ? t("project.push_running")
        : "";
  return (
    <div className="sync-lines">
      {shown.map((sync) => (
        <SyncLine
          key={sync.branch}
          sync={sync}
          busy={busy(sync)}
          pushing={pushing === sync.branch}
          outcome={outcomes[sync.branch]}
          now={now}
          onPush={onPush && (() => void push(sync))}
        />
      ))}
      {folds && (
        <button
          type="button"
          className="sync-fold"
          aria-expanded={open}
          onClick={(e) => {
            e.stopPropagation();
            setOpen(!open);
          }}
        >
          {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
          <span>
            {open
              ? t("project.syncs_fewer")
              : t("project.syncs_more", { count: out.length - 2 })}
          </span>
        </button>
      )}
    </div>
  );
}
