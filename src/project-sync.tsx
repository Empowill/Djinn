// The branches Djinn pushes that are out of sync with their remote, one short line each: the branch, then ↑ the
// commits to push and ↓ those the remote has that it lacks; what it belongs to and the full sentence on hover; a small
// button pushes it. The side panel's project row, the azima card and the project dialog read them alike.
import { ArrowUpFromLine, ChevronDown, ChevronRight } from "lucide-react";
import { useState } from "react";

import type { ProjectSync } from "../gen/ts/plan/v1/plan_pb";
import { syncDescription } from "./data/format";
import { t } from "./i18n";

// isOutOfSync tells whether the branch has commits to push, or the remote commits it lacks.
export function isOutOfSync(sync?: ProjectSync): sync is ProjectSync {
  return Boolean(sync && (sync.ahead > 0 || sync.behind > 0));
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

// SyncLine is one branch out of sync. busy is why its button waits: a push runs.
export function SyncLine({
  sync,
  busy = "",
  onPush,
}: {
  sync: ProjectSync;
  busy?: string;
  onPush?: () => void;
}) {
  const label = busy || t("project.push_branch", { branch: sync.branch });
  return (
    <div className="sync-line" title={syncTitle(sync)}>
      <span className="sync-branch">{sync.branch}</span>
      <span className="sync-counts">
        {sync.ahead > 0 && <span className="sync-ahead">↑{sync.ahead}</span>}
        {sync.behind > 0 && <span className="sync-behind">↓{sync.behind}</span>}
      </span>
      {onPush && (
        <button
          type="button"
          className="icon-button sync-push"
          disabled={Boolean(busy)}
          title={label}
          aria-label={label}
          onClick={(e) => {
            e.stopPropagation();
            onPush();
          }}
        >
          <ArrowUpFromLine size={12} />
        </button>
      )}
    </div>
  );
}

// SyncLines lists the branches out of sync among syncs, in their order; more than three fold behind the first two.
// While one pushes, every button waits.
export function SyncLines({
  syncs,
  onPush,
}: {
  syncs?: readonly ProjectSync[];
  onPush?: (sync: ProjectSync) => Promise<unknown> | void;
}) {
  const [pushing, setPushing] = useState("");
  const [open, setOpen] = useState(false);
  const out = (syncs ?? []).filter(isOutOfSync);
  if (out.length === 0) return null;
  const folds = out.length > 3;
  const shown = folds && !open ? out.slice(0, 2) : out;
  const push = async (sync: ProjectSync) => {
    if (!onPush || pushing) return;
    setPushing(sync.branch);
    try {
      await onPush(sync);
    } catch {
      // The caller says what failed.
    } finally {
      setPushing("");
    }
  };
  const busy = (sync: ProjectSync) =>
    pushing === sync.branch
      ? t("project.pushing_branch", { branch: sync.branch })
      : pushing && t("project.push_running");
  return (
    <div className="sync-lines">
      {shown.map((sync) => (
        <SyncLine
          key={sync.branch}
          sync={sync}
          busy={busy(sync)}
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
