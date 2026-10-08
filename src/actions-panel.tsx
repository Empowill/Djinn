import { AnimatePresence, motion } from "motion/react";
import {
  ArrowUpRight,
  Check,
  ChevronDown,
  Loader2,
  Play,
  Square,
  Terminal,
  CircleAlert,
  MessageSquare,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { Artifact, TaskAction } from "./types";
import "./actions-panel.css";
import { language, t } from "./i18n";

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
  onRecordTestResult?: (
    action: TaskAction,
    outcome: "passed" | "problem" | "deferred",
    detail?: string,
  ) => Promise<boolean>;
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
      runOrder.filter((runId) => typeof runId === "string" && runId.length > 0),
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
              timestamp(initialItemTime(a.item)) || a.index - b.index,
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
  if (group.key === LEGACY_TURN_KEY) return t("actions.turn_legacy");
  if (group.active) return t("actions.turn_active");
  if (group.latestAt && Number.isFinite(Date.parse(group.latestAt))) {
    return t("actions.turn_dated", {
      date: new Date(group.latestAt).toLocaleString(language, {
        day: "2-digit",
        month: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
      }),
    });
  }
  return t("actions.turn_numbered", { number: index + 1 });
}

function artifactTypeLabel(type: Artifact["type"]) {
  return {
    diagram: t("actions.type_diagram"),
    wireframe: t("actions.type_wireframe"),
    document: t("actions.type_document"),
    code: t("actions.type_code"),
    screenshot: t("actions.type_screenshot"),
    visualization: t("actions.type_visualization"),
  }[type];
}

function groupHasReadyTest(group: TurnGroup): boolean {
  return group.actions.some(
    (action) => action.kind === "server" && action.status === "ready",
  );
}

function sameSet(left: Set<string>, right: Set<string>): boolean {
  if (left.size !== right.size) return false;
  for (const value of left) if (!right.has(value)) return false;
  return true;
}

function actionStatusText(
  action: TaskAction,
  ready: boolean,
  running: boolean,
) {
  if (running) return t("actions.status_preparing");
  if (action.status === "error") return t("actions.status_failed");
  if (action.status === "stopped") return t("actions.status_stopped");
  if (ready)
    return action.url
      ? t("actions.status_ready")
      : t("actions.status_ready_no_url");
  if (action.status === "pending") return t("actions.status_pending");
  if (action.status === "done") return t("actions.status_done");
  return action.detail?.slice(0, 180) || t("actions.status_to_prepare");
}

function TaskActionCard({
  action,
  busy,
  readOnly,
  onAction,
  onRecordTestResult,
}: {
  action: TaskAction;
  busy: boolean;
  readOnly: boolean;
  onAction: ActionsPanelProps["onAction"];
  onRecordTestResult?: ActionsPanelProps["onRecordTestResult"];
}) {
  const [feedbackOpen, setFeedbackOpen] = useState(false);
  const [feedback, setFeedback] = useState("");
  const [resultBusy, setResultBusy] = useState(false);
  const [resultError, setResultError] = useState("");
  const ready = action.status === "ready";
  const running = action.status === "running";
  const done = action.status === "done";
  const isTest =
    action.kind === "server" ||
    !!(action.testInstructions?.length || action.expectedResult);
  const testReady = action.kind === "server" ? ready : isTest;
  const outcome = action.testResult?.status;
  const testBusy = resultBusy || busy;
  const recordResult = async (
    status: "passed" | "problem" | "deferred",
    detail?: string,
  ) => {
    if (!onRecordTestResult || testBusy) {
      if (!onRecordTestResult) setResultError(t("actions.result_unavailable"));
      return;
    }
    setResultBusy(true);
    setResultError("");
    try {
      const ok = await onRecordTestResult(action, status, detail);
      if (!ok) setResultError(t("actions.result_failed"));
      else {
        setFeedback("");
        setFeedbackOpen(false);
      }
    } catch (cause) {
      setResultError(
        cause instanceof Error ? cause.message : t("actions.result_failed"),
      );
    } finally {
      setResultBusy(false);
    }
  };
  return (
    <motion.article
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
          <span>{actionStatusText(action, ready, running)}</span>
          {action.error && (
            <span className="action-error-text">{action.error}</span>
          )}
          {ready && action.detail && <span>{action.detail}</span>}
        </div>
        <div className="action-buttons">
          {!readOnly && isTest && ready && action.url && (
            <button
              className="button secondary small"
              disabled={busy}
              onClick={() => void onAction(action, "open")}
            >
              {t("actions.open_test")}
              <ArrowUpRight size={13} />
            </button>
          )}
          {!readOnly && isTest && (ready || running) && (
            <button
              className="icon-button"
              aria-label={t("actions.stop", { title: action.title })}
              disabled={busy}
              onClick={() => void onAction(action, "stop")}
            >
              <Square size={13} />
            </button>
          )}
          {!readOnly && action.kind === "server" && !ready && !running && (
            <button
              className="button secondary small"
              disabled={busy}
              onClick={() => void onAction(action, "run")}
            >
              <Play size={12} />
              {action.status === "error"
                ? t("common.retry")
                : t("actions.prepare_test")}
            </button>
          )}
          {!readOnly && action.kind === "link" && action.url && !done && (
            <button
              className="button secondary small"
              disabled={busy}
              onClick={() => void onAction(action, "open")}
            >
              {t("actions.open")}
              <ArrowUpRight size={13} />
            </button>
          )}
          {!readOnly && action.kind === "manual" && !isTest && !done && (
            <button
              className="button secondary small"
              disabled={busy}
              onClick={() => void onAction(action, "complete")}
            >
              <Check size={13} />
              {t("actions.mark_done")}
            </button>
          )}
        </div>
      </div>
      {isTest && testReady && (
        <div className="test-check-panel">
          <div className="test-origin">
            <span>{t("actions.ticket_title")}</span>
            <strong>{action.title}</strong>
            {action.directory && <code>{action.directory}</code>}
          </div>
          {action.expectedResult && (
            <p className="test-expected">
              <strong>{t("actions.expected_result")}</strong>{" "}
              {action.expectedResult}
            </p>
          )}
          {action.testInstructions?.length ? (
            <ol className="test-instructions">
              {action.testInstructions.map((instruction, index) => (
                <li key={`${action.id}-instruction-${index}`}>{instruction}</li>
              ))}
            </ol>
          ) : null}
          {outcome && (
            <p className={`test-result is-${outcome}`}>
              {outcome === "passed"
                ? t("actions.result_passed")
                : outcome === "problem"
                  ? t("actions.result_problem")
                  : t("actions.result_deferred")}
              {action.testResult?.detail && ` · ${action.testResult.detail}`}
            </p>
          )}
          {!readOnly && (
            <div className="test-result-actions">
              <button
                className="button secondary small"
                disabled={testBusy}
                onClick={() => void recordResult("passed")}
              >
                <Check size={13} /> {t("actions.result_passed")}
              </button>
              <button
                className="button secondary small"
                disabled={testBusy}
                onClick={() => setFeedbackOpen(true)}
              >
                <MessageSquare size={13} /> {t("actions.report_problem")}
              </button>
              <button
                className="text-button"
                disabled={testBusy}
                onClick={() => void recordResult("deferred")}
              >
                {t("actions.test_later")}
              </button>
            </div>
          )}
          {feedbackOpen && !readOnly && (
            <div className="test-feedback">
              <label htmlFor={`test-feedback-${action.id}`}>
                {t("actions.describe_problem")}
              </label>
              <textarea
                id={`test-feedback-${action.id}`}
                value={feedback}
                onChange={(event) => setFeedback(event.target.value)}
                rows={2}
                autoFocus
                placeholder={t("actions.problem_placeholder")}
                disabled={testBusy}
              />
              <div>
                <button
                  className="button accent small"
                  disabled={!feedback.trim() || testBusy}
                  onClick={() => void recordResult("problem", feedback.trim())}
                >
                  {t("actions.send_problem")}
                </button>
                <button
                  className="text-button"
                  disabled={testBusy}
                  onClick={() => setFeedbackOpen(false)}
                >
                  {t("common.cancel")}
                </button>
              </div>
            </div>
          )}
          {resultError && (
            <p className="test-result-error" role="alert">
              <CircleAlert size={13} /> {resultError}
            </p>
          )}
        </div>
      )}
      {(action.error ||
        (action.detail &&
          (ready || running || action.detail.length > 180))) && (
        <details className="action-detail" open={action.status === "error"}>
          <summary>
            {action.status === "error"
              ? t("actions.error_detail")
              : t("actions.details")}
            <ChevronDown size={12} />
          </summary>
          <p>{action.error || action.detail}</p>
          {action.script && <code>{action.script}</code>}
        </details>
      )}
    </motion.article>
  );
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
  onRecordTestResult,
}: ActionsPanelProps) {
  const groups = useMemo(
    () => groupTurnItems(actions, artifacts, runOrder, activeRunId),
    [actions, artifacts, runOrder, activeRunId],
  );
  const [openTurnKeys, setOpenTurnKeys] = useState<Set<string>>(
    () =>
      new Set(
        groups
          .filter(
            (group, index) =>
              index === 0 || group.active || groupHasReadyTest(group),
          )
          .map((group) => group.key),
      ),
  );
  const seenKeys = useRef<Set<string>>(
    new Set(groups.map((group) => group.key)),
  );
  const previousActiveKey = useRef<string | undefined>(
    groups.find((group) => group.active)?.key,
  );
  const previousReadyKeys = useRef(
    new Set(groups.filter(groupHasReadyTest).map((group) => group.key)),
  );

  useEffect(() => {
    const currentKeys = new Set(groups.map((group) => group.key));
    const newTurnKeys = groups
      .filter((group) => !seenKeys.current.has(group.key))
      .map((group) => group.key);
    const newlyReadyKeys = groups
      .filter(
        (group) =>
          groupHasReadyTest(group) && !previousReadyKeys.current.has(group.key),
      )
      .map((group) => group.key);
    const activeKey = groups.find((group) => group.active)?.key;
    setOpenTurnKeys((current) => {
      const next = new Set([...current].filter((key) => currentKeys.has(key)));
      for (const key of [...newTurnKeys, ...newlyReadyKeys]) next.add(key);
      if (activeKey) next.add(activeKey);
      if (activeKey && activeKey !== previousActiveKey.current) {
        for (const group of groups) {
          if (group.key !== activeKey && !groupHasReadyTest(group))
            next.delete(group.key);
        }
      }
      return sameSet(current, next) ? current : next;
    });
    seenKeys.current = currentKeys;
    previousReadyKeys.current = new Set(
      groups.filter(groupHasReadyTest).map((group) => group.key),
    );
    previousActiveKey.current = activeKey;
  }, [groups]);

  if (!groups.length) return null;
  const pendingCount = actions.filter(
    (action) => action.status !== "done" && action.status !== "stopped",
  ).length;
  const totalCount = actions.length + artifacts.length;
  return (
    <section
      id="actions-section"
      className="actions-section"
      aria-label={t("actions.title")}
    >
      <div className="section-heading">
        <h2>
          {t("actions.title")}
          <span className="count">{pendingCount || totalCount}</span>
        </h2>
      </div>
      <AnimatePresence initial={false}>
        {groups.map((group, index) => (
          <details
            key={group.key}
            open={openTurnKeys.has(group.key)}
            className={`action-turn ${group.active ? "is-active" : ""}`}
            data-turn-key={group.key}
            onToggle={(event) => {
              const element = event.currentTarget as HTMLDetailsElement;
              setOpenTurnKeys((current) => {
                const next = new Set(current);
                if (element.open) next.add(group.key);
                else next.delete(group.key);
                return next;
              });
            }}
          >
            <summary className="action-turn-summary">
              <span className="action-turn-title">
                <ChevronDown size={15} aria-hidden="true" />
                <strong>{turnLabel(group, index)}</strong>
                {group.active && (
                  <span className="turn-live">{t("actions.turn_live")}</span>
                )}
              </span>
              <span className="action-turn-count">
                {t("actions.item_count", {
                  count: group.actions.length + group.artifacts.length,
                })}
              </span>
            </summary>
            <div className="action-turn-content">
              {group.artifacts.length > 0 && (
                <div
                  className="turn-artifacts"
                  aria-label={t("actions.artifacts")}
                >
                  <h3>{t("actions.artifacts")}</h3>
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
                          {t("actions.open_artifact")}
                          <ArrowUpRight size={13} />
                        </button>
                      )}
                    </div>
                  ))}
                </div>
              )}
              {group.actions.map((action) => {
                const busy = busyIds.includes(action.id);
                return (
                  <TaskActionCard
                    key={action.id}
                    action={action}
                    busy={busy}
                    readOnly={readOnly}
                    onAction={onAction}
                    onRecordTestResult={onRecordTestResult}
                  />
                );
              })}
              {!group.actions.length && !group.artifacts.length && (
                <p className="action-turn-empty">{t("actions.empty")}</p>
              )}
            </div>
          </details>
        ))}
      </AnimatePresence>
    </section>
  );
}
