import { agentDisplayState } from "./agent-state";
import type { ReactNode } from "react";
import {
  Check,
  ChevronDown,
  CircleAlert,
  Clock3,
  Loader2,
  MessageSquare,
  Play,
} from "lucide-react";
import type { Agent, MissionWorkItem, Task, TaskAction } from "./types";
import "./mission-test-board.css";
import { t } from "./i18n";

type TestAction = TaskAction & { testStartedAt?: string };

type TestState = {
  key: string;
  label: string;
  tone: "pending" | "running" | "blocked" | "ready" | "done";
  action?: TaskAction;
};

export interface MissionTestBoardProps {
  task: Task;
  actions?: TaskAction[];
  /** Focuses the native action card; the board does not duplicate its controls. */
  onAction?: (action: TaskAction) => void;
  /** Optional slot for controls owned by the native action card. */
  renderActions?: ReactNode | ((action: TaskAction) => ReactNode);
}

function testStartedAt(action?: TaskAction): string | undefined {
  return (action as TestAction | undefined)?.testStartedAt;
}

function manualRecipe(action?: TaskAction): boolean {
  return (
    !!action &&
    action.kind !== "server" &&
    !!(action.testInstructions?.length || action.expectedResult)
  );
}

function timestamp(value?: string): number {
  const parsed = value ? Date.parse(value) : Number.NaN;
  return Number.isFinite(parsed) ? parsed : Number.NEGATIVE_INFINITY;
}

function latestAction(actions: TaskAction[]): TaskAction | undefined {
  // Recipe creation defines the version; feedback on an older version must not
  // hide a newly published correction that is ready for another test.
  return [...actions]
    .reverse()
    .sort(
      (left, right) => timestamp(right.createdAt) - timestamp(left.createdAt),
    )[0];
}

function matchWorkItem(action: TaskAction, item: MissionWorkItem): boolean {
  return (
    action.workItemId === item.id ||
    (!!item.ticket && action.target === item.ticket) ||
    action.target === item.title
  );
}

function isCorrectionAfterFeedback(
  item: MissionWorkItem,
  action: TaskAction | undefined,
): boolean {
  const feedbackAt = timestamp(action?.testResult?.recordedAt);
  return (
    item.status === "running" &&
    feedbackAt !== Number.NEGATIVE_INFINITY &&
    timestamp(item.updatedAt) > feedbackAt
  );
}

function statusForWorkItem(
  item: MissionWorkItem,
  matchedActions: TaskAction[],
): TestState {
  const action = latestAction(matchedActions);
  const outcomeAction = action;
  const outcome = action?.testResult?.status;
  if (
    (action?.status === "ready" || manualRecipe(action)) &&
    testStartedAt(action) &&
    timestamp(testStartedAt(action)) > timestamp(action?.testResult?.recordedAt)
  )
    return {
      key: "testing",
      label: t("test_board.state_testing"),
      tone: "running",
      action,
    };

  if (outcome === "problem") {
    if (isCorrectionAfterFeedback(item, outcomeAction)) {
      return {
        key: "correction",
        label: t("test_board.state_correcting"),
        tone: "running",
        action,
      };
    }
    return {
      key: "problem",
      label: t("test_board.state_problem"),
      tone: "blocked",
      action,
    };
  }
  if (outcome === "passed")
    return {
      key: "passed",
      label: t("test_board.state_passed"),
      tone: "done",
      action,
    };
  if (outcome === "deferred")
    return {
      key: "deferred",
      label: t("test_board.state_deferred"),
      tone: "pending",
      action,
    };

  if (action?.status === "ready" || manualRecipe(action))
    return {
      key: "ready",
      label: t("test_board.state_ready"),
      tone: "ready",
      action,
    };
  if (action?.status === "running")
    return {
      key: "preparing",
      label: t("test_board.state_preparing"),
      tone: "running",
      action,
    };
  if (action?.status === "error")
    return {
      key: "error",
      label: t("test_board.state_error"),
      tone: "blocked",
      action,
    };
  if (action && ["pending", "stopped"].includes(action.status))
    return {
      key: "prepare",
      label: t("test_board.state_prepare"),
      tone: "pending",
      action,
    };

  if (item.status === "running")
    return {
      key: "running",
      label: t("test_board.state_running"),
      tone: "running",
      action,
    };
  if (item.status === "blocked")
    return {
      key: "blocked",
      label: t("test_board.state_blocked"),
      tone: "blocked",
      action,
    };
  if (item.status === "ready")
    return {
      key: "ready-work",
      label: t("test_board.state_ready_work"),
      tone: "ready",
      action,
    };
  if (item.status === "done")
    return {
      key: "done-work",
      label: t("test_board.state_done"),
      tone: "done",
      action,
    };
  return {
    key: "pending",
    label: t("test_board.state_pending"),
    tone: "pending",
    action,
  };
}

function statusForLegacyAction(action: TaskAction): TestState {
  const outcome = action.testResult?.status;
  if (
    (action.status === "ready" || manualRecipe(action)) &&
    testStartedAt(action) &&
    timestamp(testStartedAt(action)) > timestamp(action.testResult?.recordedAt)
  )
    return {
      key: "testing",
      label: t("test_board.state_testing"),
      tone: "running",
      action,
    };
  if (outcome === "passed")
    return {
      key: "passed",
      label: t("test_board.state_passed"),
      tone: "done",
      action,
    };
  if (outcome === "problem")
    return {
      key: "problem",
      label: t("test_board.state_problem"),
      tone: "blocked",
      action,
    };
  if (outcome === "deferred")
    return {
      key: "deferred",
      label: t("test_board.state_deferred"),
      tone: "pending",
      action,
    };
  if (testStartedAt(action) && action.status === "ready")
    return {
      key: "testing",
      label: t("test_board.state_testing"),
      tone: "running",
      action,
    };
  if (action.status === "ready" || manualRecipe(action))
    return {
      key: "ready",
      label: t("test_board.state_ready"),
      tone: "ready",
      action,
    };
  if (action.status === "running")
    return {
      key: "preparing",
      label: t("test_board.state_preparing"),
      tone: "running",
      action,
    };
  if (action.status === "error")
    return {
      key: "error",
      label: t("test_board.state_error"),
      tone: "blocked",
      action,
    };
  return {
    key: "prepare",
    label: t("test_board.state_prepare"),
    tone: "pending",
    action,
  };
}

function agentForItem(task: Task, item: MissionWorkItem): Agent | undefined {
  return item.agentId
    ? task.agents.find((agent) => agent.id === item.agentId)
    : undefined;
}

function Details({
  item,
  action,
}: {
  item?: MissionWorkItem;
  action?: TaskAction;
}) {
  const details = [
    item?.detail,
    action?.detail,
    action?.testResult?.detail &&
      t("test_board.detail_feedback", { detail: action.testResult.detail }),
    action?.expectedResult &&
      t("test_board.detail_expected", { detail: action.expectedResult }),
    item?.worktree &&
      t("test_board.detail_worktree", { detail: item.worktree }),
    item?.branch && t("test_board.detail_branch", { detail: item.branch }),
    action?.error && t("test_board.detail_error", { detail: action.error }),
  ].filter((value): value is string => Boolean(value?.trim()));
  if (!details.length) return null;
  return (
    <details className="mission-test-details">
      <summary>
        {t("test_board.details")}
        <ChevronDown size={12} />
      </summary>
      <div>
        {details.map((detail, index) => (
          <p key={`${index}-${detail}`}>{detail}</p>
        ))}
      </div>
    </details>
  );
}

function ActionSlot({
  action,
  renderActions,
}: {
  action: TaskAction | undefined;
  renderActions: MissionTestBoardProps["renderActions"];
}) {
  if (!action || !renderActions) return null;
  return (
    <span className="mission-test-native-actions">
      {typeof renderActions === "function"
        ? renderActions(action)
        : renderActions}
    </span>
  );
}

function WorkRow({
  task,
  item,
  actions,
  onAction,
  renderActions,
}: {
  task: Task;
  item: MissionWorkItem;
  actions: TaskAction[];
  onAction?: (action: TaskAction) => void;
  renderActions: MissionTestBoardProps["renderActions"];
}) {
  const matchedActions = actions.filter((action) =>
    matchWorkItem(action, item),
  );
  const state = statusForWorkItem(item, matchedActions);
  const agent = agentForItem(task, item);
  const display = agent && agentDisplayState(task, agent);
  if (
    display?.permission &&
    ["pending", "running", "blocked"].includes(item.status) &&
    state.action?.status !== "ready"
  ) {
    state.label = t("test_board.state_awaiting_permission");
    state.tone = "blocked";
  } else if (display?.question && state.action?.status !== "ready") {
    state.label = t("test_board.state_awaiting_answer");
    state.tone = "blocked";
  }
  const testable =
    state.key === "ready" &&
    (state.action?.status === "ready" || manualRecipe(state.action));
  return (
    <li className={`mission-test-row is-${state.tone}`}>
      <span
        className="mission-test-state"
        aria-label={state.label}
        title={state.label}
      >
        {state.key === "passed" ? (
          <Check size={14} />
        ) : state.key === "problem" ||
          state.key === "blocked" ||
          state.key === "error" ? (
          <CircleAlert size={14} />
        ) : state.key === "testing" ||
          state.key === "preparing" ||
          state.key === "correction" ? (
          <Loader2 size={14} />
        ) : state.key === "ready" || state.key === "ready-work" ? (
          <Play size={14} />
        ) : (
          <Clock3 size={14} />
        )}
      </span>
      <div className="mission-test-copy">
        <div className="mission-test-title-line">
          <strong>{item.title}</strong>
          {item.ticket && (
            <span className="mission-test-ticket">{item.ticket}</span>
          )}
        </div>
        <span className="mission-test-status">{state.label}</span>
        <Details item={item} action={state.action} />
      </div>
      {agent && (
        <span className="mission-test-agent" title={`Agent · ${agent.name}`}>
          <MessageSquare size={12} />
          <span>{agent.name}</span>
        </span>
      )}
      {testable && onAction && (
        <button
          type="button"
          className="button secondary small mission-test-button"
          onClick={() => onAction(state.action as TaskAction)}
          aria-controls={`action-${state.action?.id}`}
        >
          {t("test_board.test")}
        </button>
      )}
      <ActionSlot action={state.action} renderActions={renderActions} />
    </li>
  );
}

function LegacyActionRow({
  action,
  onAction,
  renderActions,
}: {
  action: TaskAction;
  onAction?: (action: TaskAction) => void;
  renderActions: MissionTestBoardProps["renderActions"];
}) {
  const state = statusForLegacyAction(action);
  return (
    <li className={`mission-test-row is-${state.tone}`}>
      <span
        className="mission-test-state"
        aria-label={state.label}
        title={state.label}
      >
        {state.key === "passed" ? <Check size={14} /> : <Clock3 size={14} />}
      </span>
      <div className="mission-test-copy">
        <div className="mission-test-title-line">
          <strong>{action.title}</strong>
        </div>
        <span className="mission-test-status">{state.label}</span>
        <Details action={action} />
      </div>
      {state.key === "ready" &&
        (state.action?.status === "ready" || manualRecipe(state.action)) &&
        onAction && (
          <button
            type="button"
            className="button secondary small mission-test-button"
            onClick={() => onAction(action)}
            aria-controls={`action-${action.id}`}
          >
            {t("test_board.test")}
          </button>
        )}
      <ActionSlot action={action} renderActions={renderActions} />
    </li>
  );
}

/** A compact mission-wide view of work items and the human test state. */
export function MissionTestBoard({
  task,
  actions = task.actions || [],
  onAction,
  renderActions,
}: MissionTestBoardProps) {
  const workItems = task.workItems || [];
  const matched = new Set<string>();
  const rows = workItems.map((item) => {
    for (const action of actions) {
      if (matchWorkItem(action, item)) matched.add(action.id);
    }
    return item;
  });
  const legacyActions = actions.filter(
    (action) =>
      (action.kind === "server" || manualRecipe(action)) &&
      !matched.has(action.id),
  );
  if (!rows.length && !legacyActions.length) return null;
  return (
    <section className="mission-test-board" aria-label={t("test_board.title")}>
      <div className="mission-test-board-heading">
        <div>
          <span className="eyebrow">{t("test_board.eyebrow")}</span>
          <h2>{t("test_board.title")}</h2>
        </div>
        <span className="mission-test-board-count">
          {rows.length + legacyActions.length}
        </span>
      </div>
      <ul className="mission-test-list">
        {rows.map((item) => (
          <WorkRow
            key={item.id}
            task={task}
            item={item}
            actions={actions}
            onAction={onAction}
            renderActions={renderActions}
          />
        ))}
        {legacyActions.map((action) => (
          <LegacyActionRow
            key={`legacy-${action.id}`}
            action={action}
            onAction={onAction}
            renderActions={renderActions}
          />
        ))}
      </ul>
    </section>
  );
}

export default MissionTestBoard;
