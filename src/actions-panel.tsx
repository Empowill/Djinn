import { AnimatePresence, motion } from "motion/react";
import {
  ArrowUpRight,
  Check,
  ChevronDown,
  Loader2,
  Play,
  Square,
  Terminal,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { Artifact, TaskAction } from "./types";
import "./actions-panel.css";

/**
 * Actions and supports share the native run id. A missing run id stays in an
 * explicit legacy bucket; it is never replaced with a made-up run id.
 */
export const LEGACY_TURN_KEY = "turn:legacy";

export type TurnGroup = {
  key: string;
  runId?: string;
  actions: TaskAction[];
  artifacts: Artifact[];
  latestAt?: string;
  active: boolean;
};

export type ActionsPanelProps = {
  actions: TaskAction[];
  artifacts?: Artifact[];
  onAction: (
    action: TaskAction,
    operation: "run" | "stop" | "complete" | "open",
  ) => Promise<boolean>;
  onOpenArtifact?: (artifactId: string) => void;
  busyIds?: string[];
  /** FlightEvent run ids in chronological order (oldest first). */
  runOrder?: string[];
  /** Current native run, including a run that has produced no result yet. */
  activeRunId?: string;
  readOnly?: boolean;
};

type TurnItem =
  | { kind: "action"; item: TaskAction; index: number }
  | { kind: "artifact"; item: Artifact; index: number };

function turnKey(runId?: string) {
  return runId ? `turn:${runId}` : LEGACY_TURN_KEY;
}

function initialItemTime(item: TaskAction | Artifact) {
  if ("createdAt" in item) return item.createdAt;
  // Artifact revisions retain their first saved timestamp in revisions. Using
  // updatedAt directly would make an old turn jump above a newer one.
  return item.revisions?.[0]?.updatedAt || item.updatedAt;
}

function timestamp(value?: string) {
  const parsed = value ? Date.parse(value) : Number.NaN;
  return Number.isFinite(parsed) ? parsed : Number.NEGATIVE_INFINITY;
}

function uniqueRunIds(runOrder: string[]) {
  return [
    ...new Set(
      runOrder.filter(
        (runId) => typeof runId === "string" && runId.length > 0,
      ),
    ),
  ];
}

/**
 * Groups the complete restitution by native run. `runOrder` is oldest first,
 * matching the task event list, so the most recent turn is returned first.
 */
export function groupTurnItems(
  actions: TaskAction[],
  artifacts: Artifact[] = [],
  runOrder: string[] = [],
  activeRunId?: string,
): TurnGroup[] {
  const groups = new Map<
    string,
    { runId?: string; items: TurnItem[]; latestAt?: string }
  >();
  const ensure = (key: string, runId?: string) => {
    const existing = groups.get(key);
    if (existing) return existing;
    const created = {
      runId,
      items: [] as TurnItem[],
      latestAt: undefined as string | undefined,
    };
    groups.set(key, created);
    return created;
  };
  actions.forEach((action, index) => {
    const group = ensure(turnKey(action.runId), action.runId || undefined);
    group.items.push({ kind: "action", item: action, index });
    if (
      !group.latestAt ||
      timestamp(initialItemTime(action)) > timestamp(group.latestAt)
    )
      group.latestAt = initialItemTime(action);
  });
  artifacts.forEach((artifact, index) => {
    const group = ensure(turnKey(artifact.runId), artifact.runId || undefined);
    group.items.push({ kind: "artifact", item: artifact, index });
    if (
      !group.latestAt ||
      timestamp(initialItemTime(artifact)) > timestamp(group.latestAt)
    )
      group.latestAt = initialItemTime(artifact);
  });

  const ordered = uniqueRunIds(runOrder);
  const ranks = new Map(ordered.map((runId, index) => [runId, index]));
  const activeKey = activeRunId ? turnKey(activeRunId) : undefined;
  if (activeRunId) ensure(activeKey!, activeRunId);

  return [...groups.entries()]
    .map(([key, group]) => ({
      key,
      runId: group.runId,
      actions: group.items
        .filter(
          (entry): entry is Extract<TurnItem, { kind: "action" }> =>
            entry.kind === "action",
        )
        .sort(
          (a, b) =>
            timestamp(b.item.createdAt) - timestamp(a.item.createdAt) ||
            a.index - b.index,
        )
        .map((entry) => entry.item),
      artifacts: group.items
        .filter(
          (entry): entry is Extract<TurnItem, { kind: "artifact" }> =>
            entry.kind === "artifact",
        )
        .sort(
          (a, b) =>
            timestamp(initialItemTime(b.item)) -
              timestamp(initialItemTime(a.item)) ||
            a.index - b.index,
        )
        .map((entry) => entry.item),
      latestAt: group.latestAt,
      active: key === activeKey,
    }))
    .sort((a, b) => {
      if (a.active !== b.active) return a.active ? -1 : 1;
      if (a.key === LEGACY_TURN_KEY || b.key === LEGACY_TURN_KEY)
        return a.key === LEGACY_TURN_KEY ? 1 : -1;
      const aRank = a.runId ? ranks.get(a.runId) : undefined;
      const bRank = b.runId ? ranks.get(b.runId) : undefined;
      if (aRank !== undefined || bRank !== undefined)
        return (
          (bRank ?? Number.NEGATIVE_INFINITY) -
          (aRank ?? Number.NEGATIVE_INFINITY)
        );
      return timestamp(b.latestAt) - timestamp(a.latestAt);
    });
}

function turnLabel(group: TurnGroup, index: number) {
  if (group.key === LEGACY_TURN_KEY) return "Historique · tour non identifié";
  if (group.active) return "Tour en cours";
  if (group.latestAt && Number.isFinite(Date.parse(group.latestAt))) {
    return `Tour du ${new Date(group.latestAt).toLocaleString("fr-FR", {
      day: "2-digit",
      month: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    })}`;
  }
  return `Tour ${index + 1}`;
}

function artifactTypeLabel(type: Artifact["type"]) {
  return {
    diagram: "Diagramme",
    wireframe: "Wireframe",
    document: "Document",
    code: "Code",
    screenshot: "Capture",
    visualization: "Visualisation",
  }[type];
}

export function ActionsPanel({
  actions,
  artifacts = [],
  onAction,
  onOpenArtifact,
  busyIds = [],
  runOrder = [],
  activeRunId,
  readOnly = false,
}: ActionsPanelProps) {
  const groups = useMemo(
    () => groupTurnItems(actions, artifacts, runOrder, activeRunId),
    [actions, artifacts, runOrder, activeRunId],
  );
  const newestKey = groups[0]?.key;
  const [openTurnKey, setOpenTurnKey] = useState<string | null>(
    newestKey ?? null,
  );
  const seenKeys = useRef<Set<string>>(new Set());
  const previousNewest = useRef<string | undefined>(undefined);

  useEffect(() => {
    const currentKeys = new Set(groups.map((group) => group.key));
    const firstRender = seenKeys.current.size === 0;
    const newTurnArrived = groups.some(
      (group) => !seenKeys.current.has(group.key),
    );
    if (
      newestKey &&
      (firstRender ||
        (newTurnArrived && newestKey !== previousNewest.current))
    )
      setOpenTurnKey(newestKey);
    else if (openTurnKey && !currentKeys.has(openTurnKey))
      setOpenTurnKey(newestKey ?? null);
    seenKeys.current = currentKeys;
    previousNewest.current = newestKey;
  }, [groups, newestKey, openTurnKey]);

  if (!groups.length) return null;
  const pendingCount = actions.filter(
    (action) => action.status !== "done" && action.status !== "stopped",
  ).length;
  const totalCount = actions.length + artifacts.length;
  return (
    <section
      id="actions-section"
      className="actions-section"
      aria-label="Restitution de la mission"
    >
      <div className="section-heading">
        <h2>
          Restitution
          <span className="count">{pendingCount || totalCount}</span>
        </h2>
      </div>
      <AnimatePresence initial={false}>
        {groups.map((group, index) => (
          <details
            key={group.key}
            open={openTurnKey === group.key}
            className={`action-turn ${group.active ? "is-active" : ""}`}
            data-turn-key={group.key}
            onToggle={(event) => {
              const element = event.currentTarget as HTMLDetailsElement;
              setOpenTurnKey((current) =>
                element.open ? group.key : current === group.key ? null : current,
              );
            }}
          >
            <summary className="action-turn-summary">
              <span className="action-turn-title">
                <ChevronDown size={15} aria-hidden="true" />
                <strong>{turnLabel(group, index)}</strong>
                {group.active && <span className="turn-live">En cours</span>}
              </span>
              <span className="action-turn-count">
                {group.actions.length + group.artifacts.length} élément
                {group.actions.length + group.artifacts.length > 1 ? "s" : ""}
              </span>
            </summary>
            <div className="action-turn-content">
              {group.artifacts.length > 0 && (
                <div className="turn-artifacts" aria-label="Supports produits">
                  <h3>Supports produits</h3>
                  {group.artifacts.map((artifact) => (
                    <div className="artifact-result-card" key={artifact.id}>
                      <div className="artifact-result-copy">
                        <strong>{artifact.title}</strong>
                        <span>{artifactTypeLabel(artifact.type)}</span>
                      </div>
                      {onOpenArtifact && (
                        <button
                          className="button secondary small"
                          type="button"
                          onClick={() => onOpenArtifact(artifact.id)}
                        >
                          Ouvrir le support
                          <ArrowUpRight size={13} />
                        </button>
                      )}
                    </div>
                  ))}
                </div>
              )}
              {group.actions.map((action) => {
                const busy = busyIds.includes(action.id);
                const ready = action.status === "ready" && !!action.url;
                const running = action.status === "running";
                const done = action.status === "done";
                return (
                  <motion.article
                    key={action.id}
                    id={`action-${action.id}`}
                    layout
                    className={`action-card is-${action.status}`}
                    initial={{ opacity: 0, y: 10 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: -6 }}
                    transition={{ duration: 0.22 }}
                  >
                    <div className="action-line">
                      <span
                        className={`action-symbol ${running ? "is-spinning" : ""}`}
                        aria-hidden="true"
                      >
                        {running ? (
                          <Loader2 size={18} />
                        ) : done ? (
                          <Check size={18} />
                        ) : action.kind === "server" ? (
                          <Terminal size={18} />
                        ) : (
                          <ArrowUpRight size={18} />
                        )}
                      </span>
                      <div className="action-copy">
                        <strong>{action.title}</strong>
                        <span>
                          {running
                            ? "Démarrage…"
                            : action.status === "error"
                              ? "Le lancement a échoué"
                              : action.status === "stopped"
                                ? "Arrêté"
                                : ready
                                  ? action.url
                                  : done
                                    ? "Terminé"
                                    : action.detail?.slice(0, 180)}
                        </span>
                      </div>
                      <div className="action-buttons">
                        {!readOnly && action.kind === "server" && ready && (
                          <button
                            className="button secondary small"
                            disabled={busy}
                            onClick={() => void onAction(action, "open")}
                          >
                            Ouvrir
                            <ArrowUpRight size={13} />
                          </button>
                        )}
                        {!readOnly &&
                          action.kind === "server" &&
                          (ready || running) && (
                            <button
                              className="icon-button"
                              aria-label={`Arrêter ${action.title}`}
                              disabled={busy}
                              onClick={() => void onAction(action, "stop")}
                            >
                              <Square size={13} />
                            </button>
                          )}
                        {!readOnly &&
                          action.kind === "server" &&
                          !ready &&
                          !running && (
                            <button
                              className="button secondary small"
                              disabled={busy}
                              onClick={() => void onAction(action, "run")}
                            >
                              <Play size={12} />
                              {action.status === "pending" ? "Lancer" : "Relancer"}
                            </button>
                          )}
                        {!readOnly &&
                          action.kind === "link" &&
                          action.url &&
                          !done && (
                            <button
                              className="button secondary small"
                              disabled={busy}
                              onClick={() => void onAction(action, "open")}
                            >
                              Ouvrir
                              <ArrowUpRight size={13} />
                            </button>
                          )}
                        {!readOnly && action.kind === "manual" && !done && (
                          <button
                            className="button secondary small"
                            disabled={busy}
                            onClick={() => void onAction(action, "complete")}
                          >
                            <Check size={13} />
                            Fait
                          </button>
                        )}
                      </div>
                    </div>
                    {(action.error ||
                      (action.detail &&
                        (ready || running || action.detail.length > 180))) && (
                      <details className="action-detail">
                        <summary>
                          Détails
                          <ChevronDown size={12} />
                        </summary>
                        <p>{action.error || action.detail}</p>
                        {action.script && <code>{action.script}</code>}
                      </details>
                    )}
                  </motion.article>
                );
              })}
              {!group.actions.length && !group.artifacts.length && (
                <p className="action-turn-empty">
                  Aucune action ni support pour le moment.
                </p>
              )}
            </div>
          </details>
        ))}
      </AnimatePresence>
    </section>
  );
}
