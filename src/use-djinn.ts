import {
  restoreMissionInteractions,
  restoreLegacyReports,
} from "./mission-interactions";
import { useState, useEffect, useRef, useCallback } from "react";
import type {} from "../shim/focus";
import type {
  AppState,
  Task,
  RuntimeEvent,
  Environment,
  Question,
  Agent,
  Artifact,
  Phase,
  RunInput,
  TaskAction,
  Project,
  MissionStep,
  StepType,
  ProviderId,
  PermissionRequest,
  PermissionDecision,
} from "./types";
import { initialState, event, now, uid, newTask } from "./data";
import { enrichDemoState } from "./demo-supports";
// Imported whole: `t` names a task in most callbacks of this file.
import * as i18n from "./i18n";
import { compactStateHistory } from "./state-history";
import {
  buildMissionPrompt,
  compactRecords,
  missionSupports,
} from "./mission-context";
import { reconnectNativeRun } from "./mission-view";
import {
  StepCompletionTracker,
  selectCompletedStep,
} from "./step-notifications";
import {
  collectMissionJournal,
  serializeMissionJournal,
} from "./mission-journal-export";
import {
  validateAction,
  validateTask,
  validateState,
  validatePermission,
  validateStepResult as parseStepResult,
  validateWorkItem,
  validateMissionReport,
} from "./session-validation";
import {
  stepMode,
  startStep,
  finishStepRun,
  applyMissionTitle,
  approveStep,
  reopenStep,
  canStartStep,
  focusStep as focusWorkflowStep,
  canResumeAfterHumanInput,
  invalidateDependentSteps,
  STEP_TYPES,
  appendStep,
  createDiscussionStep,
  classifyDiscussion,
  applyWorkflowProposal,
  validateWorkflowProposal,
  validateProject,
  amendWorkflow,
} from "./workflow";
export { validateTask } from "./session-validation";

const key = "djinn.workspace.v1";
type RunMode = "plan" | "execute" | "review";
type PendingRun = {
  mode: RunMode;
  runId?: string;
  finished: boolean;
  sentInstructionIds?: string[];
};
const text = (value: unknown, fallback = "") =>
  typeof value === "string" ? value : fallback;
const record = (value: unknown): Record<string, unknown> =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
const message = (error: unknown) =>
  error instanceof Error ? error.message : String(error);
const describe = (value: unknown) =>
  typeof value === "string"
    ? value
    : value === undefined
      ? ""
      : JSON.stringify(value);
export function getNextRunMode(task: Task): RunMode {
  const step = task.steps?.find((s) => s.id === task.activeStepId);
  if (step) return stepMode(step.type);
  if (task.phase === "review" || task.phase === "delivery") return "review";
  return task.planCompleted || task.phase === "execution" ? "execute" : "plan";
}
function selectImages(artifacts: Artifact[]): NonNullable<RunInput["images"]> {
  const images: NonNullable<RunInput["images"]> = [];
  let total = 0;
  for (const artifact of artifacts) {
    if (images.length === 5) break;
    if (
      artifact.type !== "screenshot" ||
      !artifact.id ||
      artifact.id.length > 256 ||
      artifact.content.length > 8000000
    )
      continue;
    const match =
      /^data:(image\/(?:png|jpeg|webp));base64,([A-Za-z0-9+/]+={0,2})$/.exec(
        artifact.content,
      );
    if (!match || match[2].length % 4 === 1) continue;
    try {
      const bytes = atob(match[2]);
      if (
        !bytes.length ||
        bytes.length > 4 * 1024 * 1024 ||
        total + bytes.length > 12 * 1024 * 1024
      )
        continue;
      if (btoa(bytes).replace(/=+$/, "") !== match[2].replace(/=+$/, ""))
        continue;
      const valid =
        match[1] === "image/png"
          ? bytes.startsWith("\x89PNG\r\n\x1a\n")
          : match[1] === "image/jpeg"
            ? bytes.startsWith("\xff\xd8\xff")
            : bytes.startsWith("RIFF") && bytes.slice(8, 12) === "WEBP";
      if (!valid) continue;
      images.push({
        id: artifact.id,
        title: (artifact.title || artifact.id).slice(0, 1000),
        dataUrl: artifact.content,
      });
      total += bytes.length;
    } catch {
      /* Invalid or unsupported image data stays explicitly unavailable in the prompt. */
    }
  }
  return images;
}
function contextExcerpt(value: unknown, limit: number) {
  const serialized = JSON.stringify(value);
  return serialized.length > limit
    ? serialized.slice(0, limit) +
        " [Context abridged; full records are saved.]"
    : serialized;
}
function initialGuidance(task: Task, stepId?: string) {
  let budget = 16000;
  return (task.instructions || [])
    .filter(
      (i) =>
        (!i.stepId || i.stepId === stepId) &&
        !i.appliedAt &&
        (i.status !== "prevented" ||
          [
            "run_cancelled",
            "blocking_answers_required",
            "provider_not_started",
          ].includes(i.reason || "")),
    )
    .filter((i) => {
      if (budget < i.text.length || budget < 0) return false;
      budget -= i.text.length;
      return true;
    })
    .slice(0, 32)
    .map((i) => ({ id: i.id, text: i.text, agentId: i.agentId }));
}
export function useDjinn() {
  const [state, setState] = useState<AppState>(initialState);
  const [ready, setReady] = useState(false);
  const [persistenceEnabled, setPersistenceEnabled] = useState(false);
  const [environment, setEnvironment] = useState<Environment>({
    platform: "browser",
    appVersion: "0.1.3",
    providers: [
      { id: "codex", name: "Codex", available: false, authenticated: null },
      {
        id: "claude",
        name: "Claude Code",
        available: false,
        authenticated: null,
      },
    ],
  });
  const [toast, setToast] = useState("");
  const [authStatus, setAuthStatus] = useState("");
  const [questionAlerts, setQuestionAlerts] = useState<
    {
      id: string;
      taskId: string;
      questionId: string;
      title: string;
      body: string;
    }[]
  >([]);
  const [notificationStatus, setNotificationStatus] = useState("");
  const [browserPermission, setBrowserPermission] = useState<
    NotificationPermission | "unsupported"
  >(
    typeof Notification !== "undefined"
      ? Notification.permission
      : "unsupported",
  );
  const [starting, setStarting] = useState(false);
  const ref = useRef(state);
  ref.current = state;
  const knownQuestions = useRef(new Map<string, Set<string>>());
  const stepCompletions = useRef(new StepCompletionTracker());
  const knownActions = useRef(new Map<string, string>());
  const knownActionTasks = useRef(new Set<string>());
  const [actionAlerts, setActionAlerts] = useState<
    {
      id: string;
      taskId: string;
      actionId: string;
      title: string;
      body: string;
    }[]
  >([]);
  const [busyActionIds, setBusyActionIds] = useState<string[]>([]);
  const actionLocks = useRef(new Set<string>());
  const permissionLocks = useRef(new Set<string>());
  const [busyPermissionIds, setBusyPermissionIds] = useState<string[]>([]);
  const knownPermissions = useRef(new Set<string>());
  const pendingRuns = useRef(new Map<string, PendingRun>());
  const startLock = useRef(false);
  const instructionTransmissions = useRef(new Set<string>());
  const toastTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const notify = useCallback((value: string) => {
    setToast(value);
    clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToast(""), 4800);
  }, []);
  const openCompletedStep = useCallback((taskId: string, stepId: string) => {
    const step = ref.current.tasks
      .find((task) => task.id === taskId)
      ?.steps?.find((step) => step.id === stepId && step.status !== "pending");
    setState((state) => selectCompletedStep(state, taskId, stepId));
    if (step)
      window.dispatchEvent(
        new CustomEvent("djinn:step-focus", { detail: { type: step.type } }),
      );
  }, []);
  const commit = useCallback((update: (s: AppState) => AppState) => {
    const next = compactStateHistory(update(ref.current));
    ref.current = next;
    setState(next);
  }, []);
  const updateTask = useCallback(
    (id: string, update: (task: Task) => Task) => {
      commit((s) => ({
        ...s,
        tasks: s.tasks.map((t) => (t.id === id ? update(t) : t)),
      }));
    },
    [commit],
  );
  const runtimeReady = useRef(false);
  const bootEvents = useRef<RuntimeEvent[]>([]);
  const seenNativeEvents = useRef(new Set<string>());
  const eventReceiver = useRef<(e: RuntimeEvent) => void>(() => {});
  const resumeRequested = useRef(new Set<string>());
  const startRef = useRef<
    (mode?: RunMode, missionId?: string, stepId?: string) => Promise<void>
  >(async () => {});
  const resumeAfterStoppedRun = useCallback(
    (missionId: string, runId: string) => {
      const latest = ref.current.tasks.find((t) => t.id === missionId);
      if (!latest || latest.demo) return;
      const currentRun = pendingRuns.current.get(missionId)?.runId;
      if (currentRun && currentRun !== runId) return;
      if (latest.runId) {
        // A delayed receipt from an old run must not restart its replacement.
        if (latest.runId === runId) resumeRequested.current.add(missionId);
      } else if (canResumeAfterHumanInput(latest)) {
        resumeRequested.current.delete(missionId);
        queueMicrotask(() => void startRef.current(undefined, missionId));
      }
    },
    [],
  );
  useEffect(() => {
    let active = true;
    (async () => {
      try {
        const saved: unknown = window.djinn
          ? await window.djinn.loadState()
          : JSON.parse(localStorage.getItem(key) || "null");
        const restored =
          saved === null ? undefined : enrichDemoState(validateState(saved));
        const live =
          typeof window.djinn?.getRuntimeSnapshot === "function"
            ? await window.djinn.getRuntimeSnapshot()
            : undefined;
        const livePermissions =
          live?.permissions ||
          (typeof window.djinn?.getPendingPermissions === "function"
            ? await window.djinn.getPendingPermissions()
            : []);
        if (active) {
          if (restored) {
            for (const mission of restored.tasks) {
              Object.assign(mission, restoreLegacyReports(mission));
              const current = livePermissions
                .filter((p) => p.taskId === mission.id)
                .map((p) => validatePermission(p));
              const merged = new Map(
                (mission.permissions || []).map((p) => [p.id, p]),
              );
              current.forEach((p) => merged.set(p.id, p));
              mission.permissions = [...merged.values()].slice(-200);
            }
            if (typeof window.djinn?.getActions === "function") {
              for (const mission of restored.tasks) {
                try {
                  const live = (await window.djinn.getActions(mission.id)).map(
                    (a) => validateAction(a),
                  );
                  const merged = new Map(
                    (mission.actions || []).map((a) => [a.id, a]),
                  );
                  live.forEach((a) => {
                    const saved = merged.get(a.id);
                    const sameVersion =
                      !a.runId || !saved?.runId || a.runId === saved.runId;
                    merged.set(a.id, {
                      ...saved,
                      ...a,
                      testStartedAt:
                        sameVersion &&
                        (a.status === "ready" || a.kind !== "server")
                          ? saved?.testStartedAt
                          : undefined,
                      testResult:
                        sameVersion && a.status !== "running"
                          ? a.testResult || saved?.testResult
                          : a.testResult,
                    });
                  });
                  mission.actions = [...merged.values()];
                } catch {
                  /* The saved actions remain available without claiming a live server. */
                }
              }
            }
            for (const mission of restored.tasks)
              for (const id of mission.runtimeEventIds || [])
                seenNativeEvents.current.add(id);
            for (const run of live?.runs || []) {
              const mission = restored.tasks.find((t) => t.id === run.taskId);
              const reconnected = mission && reconnectNativeRun(mission, run);
              if (!mission || !reconnected) {
                notify(i18n.t("djinn.native_run_without_step"));
                continue;
              }
              Object.assign(mission, reconnected);
              pendingRuns.current.set(mission.id, {
                runId: run.runId,
                mode: run.mode,
                finished: false,
              });
            }
            // Recover durable tool publications through a compact native projection.
            // This never replays completion events or starts another provider.
            for (const mission of restored.tasks) {
              try {
                const publications =
                  typeof window.djinn?.getMissionInteractions === "function"
                    ? (await window.djinn.getMissionInteractions(mission.id))
                        .events
                    : live?.runs.find((run) => run.taskId === mission.id)
                        ?.structuredEvents || [];
                Object.assign(
                  mission,
                  restoreMissionInteractions(mission, publications),
                );
              } catch (error) {
                notify(
                  i18n.t("djinn.publications_unavailable", {
                    error: message(error),
                  }),
                );
              }
            }
            commit(() => restored);
          }
          runtimeReady.current = true;
          for (const e of [
            ...(live?.runs.flatMap((run) => run.events) || []),
            ...(live?.notificationClicks || []),
            ...bootEvents.current,
          ])
            eventReceiver.current(e);
          bootEvents.current = [];
          setPersistenceEnabled(true);
        }
      } catch (error) {
        if (active)
          notify(i18n.t("djinn.restore_failed", { error: message(error) }));
      } finally {
        if (active) {
          runtimeReady.current = true;
          setReady(true);
        }
      }
      if (window.djinn) {
        try {
          const env = await window.djinn.getEnvironment();
          if (active) setEnvironment(env);
        } catch (error) {
          if (active)
            notify(
              i18n.t("djinn.connections_unavailable", {
                error: message(error),
              }),
            );
        }
      }
    })();
    return () => {
      active = false;
    };
  }, [notify, commit]);
  useEffect(() => {
    if (!ready || !persistenceEnabled) return;
    const timer = setTimeout(() => {
      if (window.djinn)
        window.djinn
          .saveState(state)
          .catch((error) =>
            notify(i18n.t("djinn.save_failed", { error: message(error) })),
          );
      else
        try {
          localStorage.setItem(key, JSON.stringify(state));
        } catch {
          notify(i18n.t("djinn.storage_full"));
        }
    }, 500);
    return () => clearTimeout(timer);
  }, [state, ready, persistenceEnabled, notify]);
  useEffect(() => {
    if (!ready) return;
    for (const mission of state.tasks) {
      for (const permission of mission.permissions || []) {
        if (
          permission.status !== "pending" ||
          knownPermissions.current.has(permission.id)
        )
          continue;
        knownPermissions.current.add(permission.id);
        const alert = {
          taskId: mission.id,
          questionId: `permission:${permission.id}`,
          title: i18n.t("djinn.permission_awaited"),
          body: `${permission.agentName || permission.agentId} — ${permission.title}`.slice(
            0,
            1000,
          ),
        };
        if (window.djinn?.notifyQuestion) {
          void window.djinn
            .notifyQuestion(alert)
            .then((result) => {
              setNotificationStatus(
                result.shown
                  ? i18n.t("djinn.notification_sent")
                  : result.message || i18n.t("djinn.permission_in_wish"),
              );
            })
            .catch((error) => setNotificationStatus(message(error)));
        } else if (
          typeof Notification !== "undefined" &&
          Notification.permission === "granted"
        ) {
          try {
            const notification = new Notification(alert.title, {
              body: alert.body,
              tag: alert.questionId,
            });
            notification.onclick = () => {
              window.focus();
              setState((s) => ({ ...s, selectedId: mission.id }));
              window.dispatchEvent(
                new CustomEvent("djinn:permission-focus", {
                  detail: { taskId: mission.id, requestId: permission.id },
                }),
              );
              notification.close();
            };
          } catch (error) {
            setNotificationStatus(message(error));
          }
        }
      }
    }
  }, [ready, state.tasks]);
  useEffect(() => {
    if (!ready) return;
    for (const alert of stepCompletions.current.sync(state.tasks)) {
      if (typeof window.djinn?.notifyQuestion === "function") {
        window.djinn
          .notifyQuestion(alert)
          .then((result) =>
            setNotificationStatus(
              result.shown
                ? i18n.t("djinn.notification_sent")
                : result.message ||
                    result.error ||
                    i18n.t("djinn.check_macos_notifications"),
            ),
          )
          .catch((error) => setNotificationStatus(message(error)));
      } else if (
        typeof Notification !== "undefined" &&
        Notification.permission === "granted"
      ) {
        try {
          const notification = new Notification(alert.title, {
            body: alert.body,
            tag: `${alert.taskId}:${alert.stepId}`,
          });
          notification.onclick = () => {
            window.focus();
            openCompletedStep(alert.taskId, alert.stepId);
            notification.close();
          };
        } catch (error) {
          setNotificationStatus(message(error));
        }
      }
    }
  }, [ready, state.tasks, openCompletedStep]);
  useEffect(() => {
    if (!ready) return;
    const taskIds = new Set(state.tasks.map((t) => t.id));
    setQuestionAlerts((previous) => {
      const next = previous.filter((a) =>
        state.tasks.some(
          (t) =>
            t.id === a.taskId &&
            t.questions.some((q) => q.id === a.questionId && !q.answer),
        ),
      );
      return next.length === previous.length ? previous : next;
    });
    for (const id of knownQuestions.current.keys())
      if (!taskIds.has(id)) knownQuestions.current.delete(id);
    for (const mission of state.tasks) {
      const previous = knownQuestions.current.get(mission.id);
      knownQuestions.current.set(
        mission.id,
        new Set(mission.questions.map((q) => q.id)),
      );
      if (!previous) continue;
      for (const question of mission.questions) {
        if (previous.has(question.id) || question.answer) continue;
        const alert = {
          id: `${mission.id}:${question.id}`,
          taskId: mission.id,
          questionId: question.id,
          title: mission.title.slice(0, 256),
          body: question.title.slice(0, 1000),
        };
        setQuestionAlerts((previous) =>
          [alert, ...previous.filter((a) => a.id !== alert.id)].slice(0, 20),
        );
        if (window.djinn) {
          window.djinn
            .notifyQuestion(alert)
            .then((result) =>
              setNotificationStatus(
                result.shown
                  ? i18n.t("djinn.notification_sent")
                  : result.message ||
                      result.error ||
                      i18n.t("djinn.check_macos_notifications"),
              ),
            )
            .catch((error) => setNotificationStatus(message(error)));
        } else if (
          typeof Notification !== "undefined" &&
          Notification.permission === "granted"
        ) {
          try {
            const notification = new Notification(alert.title, {
              body: alert.body,
              tag: alert.id,
            });
            notification.onclick = () => {
              window.focus();
              window.dispatchEvent(
                new CustomEvent("djinn:question-focus", { detail: alert }),
              );
              setState((current) => ({ ...current, selectedId: alert.taskId }));
              notification.close();
            };
          } catch (error) {
            setNotificationStatus(message(error));
          }
        }
      }
    }
  }, [state.tasks, ready, notify]);
  useEffect(() => {
    if (!ready) return;
    setActionAlerts((previous) =>
      previous.filter((alert) =>
        state.tasks.some(
          (t) =>
            t.id === alert.taskId &&
            t.actions?.some(
              (a) =>
                a.id === alert.actionId &&
                ["pending", "ready", "error"].includes(a.status),
            ),
        ),
      ),
    );
    for (const mission of state.tasks) {
      if (!knownActionTasks.current.has(mission.id)) {
        knownActionTasks.current.add(mission.id);
        (mission.actions || []).forEach((a) =>
          knownActions.current.set(`${mission.id}:${a.id}`, a.status),
        );
        continue;
      }
      for (const action of mission.actions || []) {
        const id = `${mission.id}:${action.id}`;
        const previous = knownActions.current.get(id);
        knownActions.current.set(id, action.status);
        if (
          previous === action.status ||
          !["pending", "ready", "error"].includes(action.status)
        )
          continue;
        const alert = {
          id,
          taskId: mission.id,
          actionId: action.id,
          title: mission.title,
          body: action.title,
        };
        setActionAlerts((current) =>
          [alert, ...current.filter((a) => a.id !== id)].slice(0, 20),
        );
        if (typeof window.djinn?.notifyQuestion === "function")
          window.djinn
            .notifyQuestion({
              taskId: mission.id,
              questionId: `action:${action.id}`,
              title: mission.title,
              body:
                action.status === "ready"
                  ? i18n.t("djinn.action_ready", { title: action.title })
                  : action.title,
            })
            .then((result) =>
              setNotificationStatus(
                result.shown
                  ? i18n.t("djinn.notification_sent")
                  : result.message || i18n.t("djinn.check_macos_notifications"),
              ),
            )
            .catch((error) => setNotificationStatus(message(error)));
        else if (
          typeof Notification !== "undefined" &&
          Notification.permission === "granted"
        ) {
          try {
            const notification = new Notification(alert.title, {
              body: alert.body,
              tag: alert.id,
            });
            notification.onclick = () => {
              window.focus();
              setState((current) => ({ ...current, selectedId: alert.taskId }));
              window.dispatchEvent(
                new CustomEvent("djinn:action-focus", { detail: alert }),
              );
              notification.close();
            };
          } catch (error) {
            setNotificationStatus(message(error));
          }
        }
      }
    }
  }, [state.tasks, ready]);
  useEffect(() => {
    document.documentElement.dataset.motion = state.settings.reduceMotion
      ? "reduced"
      : "full";
  }, [state.settings.reduceMotion]);
  useEffect(() => {
    if (!window.djinn) return;
    const receive = (e: RuntimeEvent) => {
      if (!runtimeReady.current) {
        bootEvents.current.push(e);
        return;
      }
      if (e.eventId) {
        if (seenNativeEvents.current.has(e.eventId)) return;
        seenNativeEvents.current.add(e.eventId);
      }
      const d = record(e.data);
      if (e.type === "action") {
        try {
          const action = validateAction(d);
          updateTask(e.taskId, (t) => ({
            ...t,
            actions: [
              ...(t.actions || []).filter((a) => a.id !== action.id),
              {
                ...t.actions?.find((a) => a.id === action.id),
                ...action,
                stepId:
                  action.stepId ||
                  e.stepId ||
                  t.actions?.find((a) => a.id === action.id)?.stepId,
                runId:
                  action.runId ||
                  t.actions?.find((a) => a.id === action.id)?.runId,
                agentId:
                  action.agentId ||
                  t.actions?.find((a) => a.id === action.id)?.agentId,
                testStartedAt:
                  action.status === "running" ||
                  ["stopped", "error"].includes(action.status)
                    ? undefined
                    : action.testStartedAt ||
                      t.actions?.find((a) => a.id === action.id)?.testStartedAt,
                testResult:
                  action.status === "running"
                    ? undefined
                    : action.testResult ||
                      t.actions?.find((a) => a.id === action.id)?.testResult,
              },
            ].slice(-100),
          }));
        } catch (error) {
          notify(i18n.t("djinn.action_unavailable", { error: message(error) }));
        }
        return;
      }
      if (
        e.type === "permission_requested" ||
        e.type === "permission_resolved"
      ) {
        try {
          const request = validatePermission({
            ...record(d.request || d),
            taskId: e.taskId,
          });
          updateTask(e.taskId, (t) => ({
            ...t,
            permissions: [
              ...(t.permissions || []).filter((p) => p.id !== request.id),
              request,
            ].slice(-200),
          }));
        } catch (error) {
          notify(
            i18n.t("djinn.permission_unavailable", { error: message(error) }),
          );
        }
        return;
      }
      if (e.type === "notification_clicked") {
        const target = text(d.questionId);
        if (target.startsWith("step:") && e.taskId) {
          openCompletedStep(e.taskId, target.slice(5));
          return;
        }
        commit((s) => ({
          ...s,
          selectedId: s.tasks.some((t) => t.id === e.taskId)
            ? e.taskId
            : s.selectedId,
          tasks: s.tasks.map((t) =>
            t.id === e.taskId && t.activeStepId
              ? { ...t, selectedStepId: t.activeStepId }
              : t,
          ),
        }));
        const questionId = text(d.questionId);
        window.dispatchEvent(
          new CustomEvent(
            questionId.startsWith("permission:")
              ? "djinn:permission-focus"
              : questionId.startsWith("action:")
                ? "djinn:action-focus"
                : "djinn:question-focus",
            {
              detail: {
                taskId: e.taskId,
                questionId,
                actionId: questionId.slice(7),
                requestId: questionId.startsWith("permission:")
                  ? questionId.slice(11)
                  : undefined,
              },
            },
          ),
        );
        return;
      }
      const detail = text(d.text, text(d.message, text(d.detail)));
      const isAuth =
        e.type === "auth" ||
        e.type === "runtime.auth" ||
        !e.taskId ||
        d.operation === "login";
      if (isAuth) {
        const output = detail || text(d.status);
        if (output)
          setAuthStatus((previous) =>
            `${previous ? `${previous}\n` : ""}${output}`.slice(-12000),
          );
        if (["completed", "error", "cancelled"].includes(text(d.status))) {
          window.djinn
            ?.getEnvironment()
            .then(setEnvironment)
            .catch((error) =>
              notify(
                i18n.t("djinn.connections_unavailable", {
                  error: message(error),
                }),
              ),
            );
        }
        if (e.type === "error" || d.status === "error")
          notify(detail || i18n.t("djinn.provider_connection_failed"));
        return;
      }
      const agentId = text(d.agentId);
      const child =
        d.scope === "agent" ||
        Boolean(d.parentRunId) ||
        Boolean(agentId && agentId !== "lead");
      // Native lead passes have their own child run, but their result belongs to
      // the supervised root passage. Child lifecycle events still remain local.
      const leadResult = agentId === "lead" && !!d.parentRunId;
      const resultRunId = leadResult ? text(d.parentRunId) : e.runId;
      const session = pendingRuns.current.get(e.taskId);
      if (
        child &&
        d.parentRunId &&
        session?.runId &&
        d.parentRunId !== session.runId
      )
        return;
      if (!child && session && e.runId) {
        if (session.runId && session.runId !== e.runId) return;
        session.runId = e.runId;
        if (
          e.type === "status" &&
          ["completed", "cancelled", "error"].includes(text(d.status)) &&
          d.phase !== "provider_completed"
        )
          session.finished = true;
      }
      updateTask(e.taskId, (t) => {
        if (!child && t.runId && e.runId && e.runId !== t.runId) return t;
        if (child && d.parentRunId && t.runId && d.parentRunId !== t.runId)
          return t;
        const scope = text(e.stepId, text(d.stepId));
        if (scope && !t.steps?.some((s) => s.id === scope)) return t;
        // Child passages are accepted only for the current stage; past events remain saved.
        if (scope && scope !== t.activeStepId) return t;
        let next = {
          ...t,
          ...(e.eventId
            ? {
                runtimeEventIds: [...(t.runtimeEventIds || []), e.eventId],
              }
            : {}),
        };
        const add = (
          type: Task["events"][number]["type"],
          title: string,
          body = "",
          owner?: string,
        ) => {
          const entry = event(type, title, body, owner);
          if (e.eventId) entry.id = e.eventId;
          entry.stepId = scope || undefined;
          entry.time = Number.isFinite(Date.parse(e.timestamp))
            ? e.timestamp
            : entry.time;
          entry.runId = text(d.runId, e.runId);
          entry.worktree = text(d.worktree) || undefined;
          entry.branch = text(d.branch) || undefined;
          entry.actor = "agent";
          const life =
            d.status === "blocked"
              ? "blocked"
              : text(d.lifecycle).replace(/^agent_/, "");
          if (["started", "completed", "blocked"].includes(life))
            entry.lifecycle = life as typeof entry.lifecycle;
          if (
            e.type === "status" &&
            !child &&
            d.phase !== "provider_completed" &&
            ["completed", "cancelled", "error"].includes(text(d.status))
          )
            entry.lifecycle = "completed";
          next.events = [...next.events, entry];
        };
        if (e.type === "step_result" && (!child || leadResult)) {
          try {
            const result = parseStepResult({
              ...d,
              stepId: scope || t.activeStepId,
              runId: resultRunId,
              reportedAt: e.timestamp,
            });
            next.stepResult = result;
            next.steps = t.steps?.map((step) =>
              step.id === result.stepId
                ? { ...step, report: result, summary: result.summary }
                : step,
            );
            add(
              "note",
              result.status === "ready"
                ? i18n.t("djinn.result_ready")
                : result.status === "blocked"
                  ? i18n.t("djinn.preparation_blocked")
                  : i18n.t("djinn.answer_expected"),
              [result.summary, result.reason, result.nextAction]
                .filter(Boolean)
                .join("\n"),
              "lead",
            );
          } catch (error) {
            add(
              "error",
              i18n.t("djinn.invalid_result"),
              message(error),
              "lead",
            );
          }
        } else if (e.type === "work_item") {
          try {
            const item = validateWorkItem({
              ...d,
              agentId: text(d.assignedAgentId, text(d.agentId)) || undefined,
              stepId: scope || t.activeStepId,
              runId: e.runId,
              updatedAt: e.timestamp,
            });
            const existing = t.workItems?.find((work) => work.id === item.id);
            next.workItems = [
              ...(t.workItems || []).filter((work) => work.id !== item.id),
              { ...existing, ...item },
            ].slice(-200);
          } catch (error) {
            add("error", i18n.t("djinn.invalid_task"), message(error), agentId);
          }
        } else if (e.type === "report") {
          try {
            const report = validateMissionReport({
              ...d,
              id: text(d.id, `${agentId || "lead"}:${scope || "mission"}`),
              agentId: agentId || undefined,
              stepId: scope || t.activeStepId,
              runId: e.runId,
              updatedAt: e.timestamp,
            });
            next.reports = [
              ...(t.reports || []).filter((item) => item.id !== report.id),
              report,
            ].slice(-200);
            add(
              "note",
              i18n.t("djinn.contribution_report"),
              [
                report.summary,
                report.remaining?.length
                  ? i18n.t("djinn.remaining", {
                      items: report.remaining.join(" ; "),
                    })
                  : "",
                report.nextAction,
              ]
                .filter(Boolean)
                .join("\n"),
              agentId,
            );
          } catch (error) {
            add(
              "error",
              i18n.t("djinn.invalid_report"),
              message(error),
              agentId,
            );
          }
        } else if (e.type === "guidance") {
          const status = text(d.status);
          if (
            ["queued", "transmitted", "consumed", "prevented"].includes(status)
          ) {
            next.instructions = (t.instructions || []).map((i) =>
              i.id === d.id
                ? {
                    ...i,
                    status: status as NonNullable<
                      Task["instructions"]
                    >[number]["status"],
                    reason: text(d.reason) || undefined,
                    ...(status === "consumed"
                      ? { appliedAt: e.timestamp }
                      : {}),
                  }
                : i,
            );
            add(
              "note",
              status === "consumed"
                ? i18n.t("chat.event_instruction_delivered")
                : status === "prevented"
                  ? i18n.t("chat.event_instruction_prevented")
                  : i18n.t("chat.event_instruction_sent"),
              text(d.reason) || text(d.text),
              text(d.agentId, "lead"),
            );
          }
        } else if (e.type === "mission_metadata") {
          next = applyMissionTitle(next, text(d.title), e.timestamp);
        } else if (e.type === "question") {
          let id = text(
            d.id,
            `Q${String(t.questions.length + 1).padStart(2, "0")}`,
          );
          const title = text(d.title, i18n.t("djinn.decision_needed"));
          let existing = t.questions.find((x) => x.id === id);
          if (existing?.answer && existing.title !== title) {
            const usedIds = new Set(t.questions.map((q) => q.id));
            let numericId = t.questions.reduce((max, q) => {
              const number = /^Q(\d+)$/.test(q.id) ? BigInt(q.id.slice(1)) : 0n;
              return number > max ? number : max;
            }, 0n);
            do {
              numericId += 1n;
              id = `Q${String(numericId).padStart(2, "0")}`;
            } while (usedIds.has(id));
            existing = undefined;
          }
          let options = Array.isArray(d.options)
            ? d.options.map((o: unknown, i: number) => {
                if (typeof o === "string")
                  return {
                    id: String.fromCharCode(97 + i),
                    label: o,
                    description: "",
                  };
                const v = record(o);
                return {
                  id: text(v.id, String.fromCharCode(97 + i)),
                  label: text(v.label, text(v.title, `Option ${i + 1}`)),
                  description: text(v.description),
                };
              })
            : [];
          const q: Question = {
            stepId: scope || undefined,
            runId: e.runId,
            id,
            title,
            context: text(d.context, text(d.question, detail)),
            recommendation: text(d.recommendation),
            options,
            blocking: d.blocking !== false,
            blockingScope:
              d.blockingScope === "agent" || d.blockingScope === "mission"
                ? d.blockingScope
                : undefined,
            workItemId: text(d.workItemId) || undefined,
            unlocks: text(d.unlocks),
            agentId: agentId || undefined,
            theme: text(d.theme, i18n.t("common.wish")),
          };
          next.questions = existing
            ? t.questions.map((x) =>
                x.id === id
                  ? { ...q, answer: x.answer, answeredAt: x.answeredAt }
                  : x,
              )
            : [...t.questions, q];
          next.reviewApprovedAt = undefined;
          if (next.questions.some((x) => x.blocking && !x.answer))
            next.status = "waiting";
          add("decision", q.title, q.context, q.agentId);
        } else if (e.type === "agent") {
          const id = text(d.id, agentId || "lead");
          const old = t.agents.find((a) => a.id === id);
          const status = text(
            d.status,
            old?.status || "queued",
          ) as Agent["status"];
          const a: Agent = {
            stepId: scope || old?.stepId,
            runId: text(d.runId, e.runId),
            id,
            name: text(d.name, old?.name || "Djinn"),
            role: text(d.role, old?.role || "Orchestration"),
            origin: d.origin === "codex" ? "codex" : old?.origin,
            live: typeof d.live === "boolean" ? d.live : old?.live,
            provider:
              d.provider === "codex" || d.provider === "claude"
                ? d.provider
                : old?.provider,
            providerThreadId:
              text(d.providerThreadId, old?.providerThreadId) || undefined,
            parentAgentId:
              text(d.parentAgentId, old?.parentAgentId) || undefined,
            activity: text(d.activity, old?.activity) || undefined,
            writeScope: Array.isArray(d.writeScope)
              ? d.writeScope.filter((p): p is string => typeof p === "string")
              : old?.writeScope,
            dependsOn: Array.isArray(d.dependsOn)
              ? d.dependsOn.filter((p): p is string => typeof p === "string")
              : old?.dependsOn,
            readOnly:
              typeof d.readOnly === "boolean" ? d.readOnly : old?.readOnly,
            isolation:
              d.isolation === "shared" || d.isolation === "worktree"
                ? d.isolation
                : old?.isolation,
            resources:
              d.resources && typeof d.resources === "object"
                ? (d.resources as Agent["resources"])
                : old?.resources,
            waitReason:
              typeof d.waitReason === "string"
                ? d.waitReason || undefined
                : old?.waitReason,
            waitingForAgentIds: Array.isArray(d.waitingForAgentIds)
              ? d.waitingForAgentIds.filter(
                  (p): p is string => typeof p === "string",
                )
              : old?.waitingForAgentIds,
            model: text(
              d.model,
              old?.model ||
                (d.origin === "codex" || old?.origin === "codex"
                  ? ""
                  : t.model || t.provider),
            ),
            status: ["queued", "running", "blocked", "done", "error"].includes(
              status,
            )
              ? status
              : "queued",
            summary: text(d.summary, detail || old?.summary || ""),
            progress:
              typeof d.progress === "number" && Number.isFinite(d.progress)
                ? Math.max(0, Math.min(100, d.progress))
                : status === "done"
                  ? 100
                  : old?.progress || 0,
            prompt: text(d.prompt, old?.prompt),
            worktree: text(d.worktree, old?.worktree) || undefined,
            branch: text(d.branch, old?.branch) || undefined,
          };
          next.agents = old
            ? t.agents.map((x) => (x.id === id ? a : x))
            : [...t.agents, a];
          add(
            "agent",
            `${a.name} · ${a.status === "done" ? i18n.t("djinn.agent_done") : a.role}`,
            a.summary,
            a.id,
          );
        } else if (e.type === "artifact") {
          const rawId = text(d.id, uid());
          const id = t.artifacts.some(
            (a) => a.id === rawId && a.stepId !== scope,
          )
            ? `${rawId}:${scope}`
            : rawId;
          const type = text(d.type, "document") as Artifact["type"];
          const a: Artifact = {
            stepId: scope || undefined,
            runId: e.runId,
            revision: 1,
            editedBy: "agent",
            id,
            title: text(d.title, i18n.t("djinn.default_artifact_title")),
            type: [
              "diagram",
              "wireframe",
              "document",
              "code",
              "screenshot",
              "visualization",
            ].includes(type)
              ? type
              : "document",
            content:
              typeof d.content === "string"
                ? d.content
                : JSON.stringify(d.content ?? d),
            updatedAt: now(),
          };
          const existing = t.artifacts.find((x) => x.id === id);
          if (
            existing?.editedBy === "human" &&
            d.baseRevision !== existing.revision
          ) {
            a.id = `${id}:proposal:${uid()}`;
            a.title = i18n.t("djinn.revision_proposal_title", {
              title: a.title,
            });
            add(
              "note",
              i18n.t("djinn.revision_proposed"),
              i18n.t("djinn.your_version_kept"),
              agentId,
            );
            next.artifacts = [...t.artifacts, a];
          } else {
            a.sourceOfTruth = existing?.sourceOfTruth;
            a.revision = (existing?.revision || 0) + 1;
            a.revisions = existing
              ? [
                  ...(existing.revisions || []),
                  {
                    revision: existing.revision || 1,
                    content: existing.content,
                    updatedAt: existing.updatedAt,
                    editedBy: existing.editedBy || "agent",
                  },
                ].slice(-20)
              : [];
            next.artifacts = existing
              ? t.artifacts.map((x) => (x.id === id ? a : x))
              : [...t.artifacts, a];
          }
          next.reviewApprovedAt = undefined;
          add(
            "note",
            i18n.t("chat.event_artifact_available", { title: a.title }),
            "",
            agentId || "lead",
          );
        } else if (e.type === "workflow_amended") {
          if (
            t.workflowMode === "flexible" &&
            t.projectSnapshot?.workflowPolicy !== "enforced" &&
            (!agentId || agentId === "lead") &&
            scope === t.activeStepId
          ) {
            try {
              const proposal =
                Array.isArray(d.steps) && d.steps.length === 0
                  ? { steps: [], reason: text(d.reason) }
                  : validateWorkflowProposal(d);
              next.workflowAmendment = {
                ...proposal,
                stepId: scope,
                runId: e.runId,
              };
              add(
                "note",
                i18n.t("djinn.timeline_adjustment_prepared"),
                proposal.reason,
                "lead",
              );
            } catch {
              /* invalid future suffix remains inert */
            }
          }
        } else if (e.type === "workflow_defined") {
          const initial = t.steps?.find((s) => s.id === t.activeStepId);
          if (
            initial?.type === "discussion" &&
            initial.id === t.steps?.[0]?.id &&
            t.workflowMode === "flexible" &&
            t.workflowOrigin === "agent" &&
            t.projectSnapshot?.workflowPolicy !== "enforced" &&
            (!agentId || agentId === "lead") &&
            scope === initial.id &&
            !t.initialWorkflowProposal
          ) {
            try {
              const proposal = validateWorkflowProposal(
                { ...d, stepId: scope },
                true,
              ) as NonNullable<Task["initialWorkflowProposal"]>;
              next.initialWorkflowProposal = proposal;
              add(
                "note",
                i18n.t("djinn.timeline_defined"),
                proposal.reason,
                "lead",
              );
            } catch {
              // The native runtime validates provider events. Keep this guard
              // for renderer fixtures and stale external bridge events.
            }
          }
        } else if (e.type === "discussion_type") {
          const initial = t.steps?.find((s) => s.id === t.activeStepId);
          if (
            initial?.type === "discussion" &&
            initial.id === t.steps?.[0]?.id &&
            t.workflowMode === "flexible" &&
            t.workflowOrigin !== "agent" &&
            t.projectSnapshot?.workflowPolicy !== "enforced" &&
            (!agentId || agentId === "lead") &&
            scope === initial.id &&
            [
              "exploration",
              "reflection",
              "specification",
              "prototype",
              "implementation",
            ].includes(text(d.type)) &&
            text(d.title).trim() &&
            text(d.reason).trim()
          ) {
            next.initialStepProposal = {
              type: d.type as NonNullable<Task["initialStepProposal"]>["type"],
              title: text(d.title).slice(0, 1000),
              objective: text(d.objective).slice(0, 100000),
              reason: text(d.reason).slice(0, 100000),
              stepId: scope,
            };
            add(
              "note",
              i18n.t("djinn.discussion_chosen"),
              text(d.reason),
              "lead",
            );
          }
        } else if (e.type === "next_step") {
          if (
            t.workflowMode === "flexible" &&
            t.projectSnapshot?.workflowPolicy !== "enforced" &&
            (!agentId || agentId === "lead") &&
            scope === t.activeStepId &&
            STEP_TYPES.includes(d.type as StepType) &&
            text(d.title).trim() &&
            text(d.reason).trim()
          ) {
            next.nextStepProposal = {
              type: d.type as StepType,
              title: text(d.title).slice(0, 1000),
              objective: text(d.objective).slice(0, 100000),
              reason: text(d.reason).slice(0, 100000),
              stepId: scope,
            };
            add(
              "note",
              i18n.t("djinn.next_step_proposed"),
              text(d.reason),
              "lead",
            );
          }
        } else if (e.type === "phase") {
          // Provider progression is informational; only human actions change stages.
          add(
            "phase",
            text(d.title, i18n.t("djinn.plan_progress")),
            detail,
            agentId || undefined,
          );
        } else if (e.type === "status") {
          const status = text(d.status, detail);
          if (!child && status === "running")
            next.activity = {
              lead:
                d.waitingForAgents === true
                  ? d.leadActivity === "responds"
                    ? "responds"
                    : "supervises"
                  : "integrates",
              activeAgents: Array.isArray(d.activeAgents)
                ? d.activeAgents.map((a) => {
                    const r = record(a);
                    return {
                      id: text(r.id),
                      task: text(r.task),
                      runId: text(r.runId),
                    };
                  })
                : [],
              lastActivityAt: e.timestamp,
            };
          if (child) {
            if (agentId)
              next.agents = t.agents.map((a) =>
                a.id === agentId
                  ? {
                      ...a,
                      status:
                        status === "completed"
                          ? d.supervisor === true
                            ? "queued"
                            : "done"
                          : status === "error"
                            ? "error"
                            : status === "cancelled"
                              ? "queued"
                              : a.status,
                      progress:
                        status === "completed" && d.supervisor !== true
                          ? 100
                          : a.progress,
                      summary:
                        d.supervisor === true && status === "completed"
                          ? i18n.t("djinn.lead_waiting_subagents")
                          : a.summary,
                    }
                  : a,
              );
          } else if (status === "running") {
            next.status = t.questions.some(
              (q) =>
                q.blocking && !q.answer && (!q.stepId || q.stepId === scope),
            )
              ? "waiting"
              : "running";
            if (
              !t.events.some(
                (ev) =>
                  ev.runId === e.runId &&
                  ev.agentId === "lead" &&
                  ev.lifecycle === "started",
              )
            ) {
              add("agent", i18n.t("djinn.djinn_starts"), "", "lead");
              next.events[next.events.length - 1].lifecycle = "started";
            }
          } else if (status === "blocked") {
            next.status = "waiting";
          } else if (
            status === "completed" &&
            d.phase !== "provider_completed"
          ) {
            next.runId = undefined;
            const passageStartedAt = t.events.find(
              (ev) => ev.runId === e.runId && ev.lifecycle === "started",
            )?.time;
            const awaitingBusinessInput = t.questions.some(
              (question) =>
                question.blocking &&
                !question.answer &&
                (!scope || !question.stepId || question.stepId === scope),
            );
            const failedContribution = t.agents.some(
              (a) =>
                a.id !== "lead" &&
                (a.status === "error" ||
                  (a.status === "blocked" && !awaitingBusinessInput)) &&
                (!scope || !a.stepId || a.stepId === scope) &&
                (a.runId === e.runId ||
                  (!!passageStartedAt &&
                    t.events.some(
                      (ev) =>
                        ev.agentId === a.id &&
                        ev.runId === a.runId &&
                        ev.time >= passageStartedAt,
                    ))),
            );
            const blocked =
              t.questions.some(
                (q) =>
                  q.blocking && !q.answer && (!q.stepId || q.stepId === scope),
              ) ||
              failedContribution ||
              (t.stepResult?.stepId === scope &&
                t.stepResult?.runId === e.runId &&
                (t.stepResult.status !== "ready" ||
                  t.stepResult.criteria?.some((c) => !c.met)));
            next = finishStepRun(
              { ...next, runId: t.runId },
              scope || t.activeStepId || "",
              failedContribution ? "error" : "completed",
              t.events
                .filter((ev) => ev.agentId === "lead" && ev.type === "note")
                .slice(-3)
                .map((ev) => ev.detail)
                .join("\n")
                .slice(-8000),
            );
            next.runId = undefined;
            next.status = "waiting";
            if (resumeRequested.current.has(t.id) && !blocked) {
              resumeRequested.current.delete(t.id);
              queueMicrotask(() => void startRef.current(undefined, t.id));
            }
            next.agents = t.agents.map((a) =>
              a.id === "lead" ? { ...a, status: "done", progress: 100 } : a,
            );
            if (scope)
              next.agentHistory = {
                ...next.agentHistory,
                [scope]: next.agents,
              };
            add(
              "note",
              i18n.t("chat.event_agent_done"),
              i18n.t("djinn.check_evidence"),
              "lead",
            );
            if (
              !blocked &&
              next.initialStepProposal &&
              t.steps?.find((s) => s.id === scope)?.type === "discussion" &&
              t.workflowOrigin !== "agent"
            ) {
              next = classifyDiscussion(next, next.initialStepProposal);
              // A fresh provider thread receives the newly selected permissions.
              // The routing passage remains in the mission journal.
              next.providerSessions = undefined;
              next.agents = [];
              resumeRequested.current.delete(t.id);
              queueMicrotask(() => void startRef.current(undefined, t.id));
            }
            if (
              !blocked &&
              next.initialWorkflowProposal &&
              t.workflowOrigin === "agent" &&
              t.steps?.find((s) => s.id === scope)?.type === "discussion"
            ) {
              next = applyWorkflowProposal(next, next.initialWorkflowProposal);
              next.providerSessions = undefined;
              next.agents = [];
              resumeRequested.current.delete(t.id);
              queueMicrotask(() => void startRef.current(undefined, t.id));
            }
            if (!blocked && next.workflowAmendment?.stepId === scope) {
              try {
                next.activeStepId = scope;
                next.selectedStepId = scope;
                next = amendWorkflow(next, next.workflowAmendment, e.timestamp);
                next.workflowAmendment = undefined;
              } catch (error) {
                add(
                  "note",
                  i18n.t("djinn.timeline_adjustment_refused"),
                  message(error),
                  "lead",
                );
                next.workflowAmendment = undefined;
              }
            }
            const completedStage = next.steps?.find((s) => s.id === scope);
            if (
              !blocked &&
              completedStage?.validation === "automatic" &&
              completedStage.status === "completed"
            ) {
              const index = next.steps!.findIndex((s) => s.id === scope);
              const following = next.steps![index + 1];
              next.status = following ? "idle" : "done";
              if (following && canStartStep(next, following.id)) {
                next.activeStepId = following.id;
                next.selectedStepId = following.id;
                next.agents = [];
                resumeRequested.current.delete(t.id);
                queueMicrotask(() => void startRef.current(undefined, t.id));
              }
            }
          } else if (status === "cancelled") {
            next = finishStepRun(
              next,
              scope || t.activeStepId || "",
              "cancelled",
            );
            next.runId = undefined;
            next.agents = t.agents.map((a) =>
              a.status === "running"
                ? {
                    ...a,
                    status: "queued",
                    summary: i18n.t("djinn.run_stopped_by_you"),
                  }
                : a,
            );
            add("note", i18n.t("chat.event_wish_paused"));
            if (
              resumeRequested.current.has(t.id) &&
              !t.questions.some((q) => q.blocking && !q.answer?.trim())
            ) {
              resumeRequested.current.delete(t.id);
              queueMicrotask(() => void startRef.current(undefined, t.id));
            }
          } else if (status === "error") {
            next = finishStepRun(next, scope || t.activeStepId || "", "error");
            next.runId = undefined;
            add("error", i18n.t("djinn.run_error"), detail);
          }
        } else if (e.type === "error") {
          if (child)
            next.agents = t.agents.map((a) =>
              a.id === agentId ? { ...a, status: "error", summary: detail } : a,
            );
          else next.status = "error";
          add("error", i18n.t("djinn.agent_error"), detail, agentId || "lead");
        } else if (e.type === "tool") {
          const evidence = [
            detail,
            d.input !== undefined
              ? i18n.t("djinn.tool_input_line", { value: describe(d.input) })
              : "",
            d.command !== undefined
              ? i18n.t("djinn.tool_command_line", {
                  value: describe(d.command),
                })
              : "",
            d.output !== undefined
              ? i18n.t("djinn.tool_output_line", { value: describe(d.output) })
              : "",
            d.exitCode !== undefined
              ? i18n.t("djinn.tool_exit_code_line", {
                  value: describe(d.exitCode),
                })
              : "",
          ]
            .filter(Boolean)
            .join("\n")
            .slice(0, 60000);
          next.reviewApprovedAt = undefined;
          add(
            "tool",
            text(d.title, text(d.name, i18n.t("djinn.agent_action"))),
            evidence,
            agentId || "lead",
          );
        } else if (e.type === "text" || e.type === "note") {
          if (
            d.providerSession === true &&
            typeof d.sessionKey === "string" &&
            typeof d.providerThreadId === "string"
          )
            next.providerSessions = {
              ...t.providerSessions,
              [d.sessionKey]: d.providerThreadId,
            };
          if (
            e.type === "text" &&
            d.streaming === true &&
            typeof d.messageId === "string"
          ) {
            const messageId = `${e.runId}:message:${d.messageId.slice(0, 256)}`;
            const previous = next.events.find(
              (entry) => entry.id === messageId,
            );
            const entry = {
              ...event(
                "note",
                detail.split("\n")[0].slice(0, 140) || i18n.t("djinn.report"),
                detail.slice(0, 60000),
                agentId || "lead",
              ),
              id: messageId,
              time: previous?.time || e.timestamp,
              stepId: scope || undefined,
              runId: e.runId,
            };
            next.events = [
              ...next.events.filter((entry) => entry.id !== messageId),
              ...(detail ? [entry] : []),
            ];
          } else if (detail || d.title)
            add(
              "note",
              text(
                d.title,
                detail.split("\n")[0].slice(0, 140) || i18n.t("djinn.report"),
              ),
              detail.slice(0, 60000),
              agentId || "lead",
            );
        }
        if (next.activity && Array.isArray(d.activeAgents)) {
          next.activity = {
            ...next.activity,
            activeAgents: d.activeAgents.map((a) => {
              const r = record(a);
              return {
                id: text(r.id),
                task: text(r.task),
                runId: text(r.runId),
              };
            }),
          };
        }
        if (
          next.activity &&
          (e.type === "text" || e.type === "tool" || e.type === "note")
        )
          next.activity = { ...next.activity, lastActivityAt: e.timestamp };
        return next;
      });
    };
    eventReceiver.current = receive;
    return window.djinn.onEvent(receive);
  }, [updateTask, notify]);
  const task =
    state.tasks.find((t) => t.id === state.selectedId) || state.tasks[0];
  const select = (id: string) => commit((s) => ({ ...s, selectedId: id }));
  const loadDemo = () => {
    const existing = state.tasks.find((t) => t.demo);
    if (existing) {
      commit((s) => ({ ...enrichDemoState(s), selectedId: existing.id }));
      return existing;
    }
    const demo = initialState().tasks[0];
    commit((s) => ({ ...s, tasks: [...s.tasks, demo], selectedId: demo.id }));
    return demo;
  };
  const addTask = (
    title: string,
    brief: string,
    project: string,
    provider: Task["provider"],
    model: string,
    options?: {
      projectRecord?: Project;
      steps?: MissionStep[];
      concurrency?: number;
      workflowMode?: Task["workflowMode"];
      autoWorkflow?: boolean;
      indication?: string;
    },
  ) => {
    const t = newTask(title, brief, project, provider, model, options);
    commit((s) => ({
      ...s,
      version: 2,
      projects: options?.projectRecord
        ? [
            ...(s.projects || []).filter(
              (p) => p.id !== options.projectRecord!.id,
            ),
            options.projectRecord!,
          ]
        : s.projects || [],
      tasks: [...s.tasks, t],
      selectedId: t.id,
    }));
    if (brief.trim())
      queueMicrotask(() => void startRef.current(undefined, t.id));
    return t;
  };
  const saveProject = async (project: Project) => {
    const checked = validateProject(project);
    const canonical = window.djinn
      ? await window.djinn.validateProject(checked)
      : checked;
    commit((s) => ({
      ...s,
      version: 2,
      projects: [
        ...(s.projects || []).filter((p) => p.id !== canonical.id),
        canonical,
      ],
    }));
    if (window.djinn) await window.djinn.saveState(ref.current);
    return canonical;
  };
  const answer = (id: string, value: string) => {
    if (!value.trim()) return;
    updateTask(task.id, (t) => {
      const q = t.questions.find((x) => x.id === id);
      if (!q) return t;
      const answeredAt = now();
      const questions = t.questions.map((x) =>
        x.id === id ? { ...x, answer: value, answeredAt } : x,
      );
      const blocked = questions.some((x) => x.blocking && !x.answer);
      const artifacts = t.demo
        ? t.artifacts.map((a) => {
            if (a.type !== "wireframe") return a;
            let content: Record<string, unknown>;
            try {
              content = record(JSON.parse(a.content));
            } catch {
              return a;
            }
            if (id === "Q01")
              content.layout =
                value === q.options[1]?.label ? "team" : "status";
            if (id === "Q02")
              content.archive =
                value === q.options[1]?.label ? "filter" : "dedicated";
            return { ...a, content: JSON.stringify(content), updatedAt: now() };
          })
        : t.artifacts;
      return {
        ...t,
        questions,
        artifacts,
        reviewApprovedAt: undefined,
        phase: t.phase,
        status: blocked ? "waiting" : t.runId ? "running" : "idle",
        agents: t.agents.map((a) =>
          a.id === q.agentId
            ? {
                ...a,
                status: questions.some(
                  (x) => x.agentId === a.id && x.blocking && !x.answer,
                )
                  ? "blocked"
                  : "queued",
                summary: i18n.t("djinn.decision_received", { value }),
              }
            : a,
        ),
        events: [
          ...t.events,
          {
            ...event("decision", `${id} · ${value}`, q.unlocks, q.agentId),
            stepId: q.stepId || t.activeStepId,
            time: answeredAt,
            actor: "human",
            interventionId: `answer:${id}:${answeredAt}`,
          },
        ],
      };
    });
    notify(i18n.t("djinn.decision_saved"));
    const latest = ref.current.tasks.find((t) => t.id === task.id)!;
    if (
      !latest.questions.some(
        (q) =>
          q.blocking &&
          !q.answer?.trim() &&
          (!q.stepId || q.stepId === latest.activeStepId),
      )
    ) {
      if (latest.runId) resumeRequested.current.add(latest.id);
      else if (!latest.demo) void startRef.current(undefined, latest.id);
    }
    if (window.djinn && persistenceEnabled)
      void window.djinn.saveState(ref.current).catch((e) => notify(message(e)));
  };
  const reopen = (id: string) =>
    updateTask(task.id, (t) => ({
      ...invalidateDependentSteps(
        t,
        t.questions.find((q) => q.id === id)?.stepId || t.activeStepId || "",
      ),
      questions: t.questions.map((q) =>
        q.id === id ? { ...q, answer: undefined, answeredAt: undefined } : q,
      ),
      reviewApprovedAt: undefined,
      status: "waiting",
      events: [
        ...t.events,
        {
          ...event("decision", i18n.t("djinn.decision_reopened", { id })),
          actor: "human",
        },
      ],
    }));
  const start = async (
    requestedMode?: RunMode,
    missionId = task.id,
    requestedStepId?: string,
  ) => {
    const task = ref.current.tasks.find((t) => t.id === missionId);
    if (!task || startLock.current || task.runId) return;
    const stepId = requestedStepId || task.activeStepId;
    const stage = task.steps?.find((s) => s.id === stepId);
    const mode = stage
      ? stepMode(stage.type)
      : requestedMode || getNextRunMode(task);
    if (!persistenceEnabled) {
      notify(i18n.t("djinn.saving_suspended_agent"));
      return;
    }
    if (stage && !canStartStep(task, stage.id)) {
      notify(i18n.t("djinn.validate_previous_first"));
      return;
    }
    if (task.demo) {
      notify(i18n.t("djinn.demo_wish"));
      return;
    }
    if (!window.djinn) {
      notify(i18n.t("djinn.agents_need_electron"));
      return;
    }
    if (!task.project) {
      notify(i18n.t("djinn.choose_folder"));
      return;
    }
    if (
      task.questions.some(
        (q) => q.blocking && !q.answer && (!q.stepId || q.stepId === stepId),
      )
    ) {
      notify(i18n.t("djinn.answer_blocking_first"));
      return;
    }
    const provider = environment.providers.find((p) => p.id === task.provider);
    if (!provider?.available) {
      notify(
        i18n.t("djinn.provider_not_installed", {
          provider: task.provider === "codex" ? "Codex" : "Claude Code",
        }),
      );
      return;
    }
    if (provider.authenticated === false) {
      notify(i18n.t("djinn.connect_subscription"));
      return;
    }
    startLock.current = true;
    setStarting(true);
    const guidance = initialGuidance(task, stepId);
    const session: PendingRun = {
      mode,
      finished: false,
      sentInstructionIds: guidance.map((i) => i.id),
    };
    pendingRuns.current.set(task.id, session);
    updateTask(task.id, (t) => ({
      ...(stage ? startStep(t, stage.id) : t),
      agentHistory:
        t.activeStepId && t.activeStepId !== stepId
          ? { ...t.agentHistory, [t.activeStepId]: t.agents }
          : t.agentHistory,
      status: "running",
      stepResult: undefined,
      nextStepProposal: undefined,
      initialStepProposal:
        stage?.type === "discussion" ? undefined : t.initialStepProposal,
      initialWorkflowProposal:
        stage?.type === "discussion" ? undefined : t.initialWorkflowProposal,
      instructions: t.instructions?.map((i) =>
        !i.appliedAt &&
        [
          "run_cancelled",
          "blocking_answers_required",
          "provider_not_started",
        ].includes(i.reason || "")
          ? { ...i, status: "queued", reason: undefined }
          : i,
      ),
      agents: (t.activeStepId !== stepId
        ? t.agentHistory?.[stepId || ""] || []
        : t.agents
      ).some((a) => a.id === "lead")
        ? (t.activeStepId !== stepId
            ? t.agentHistory?.[stepId || ""] || []
            : t.agents
          ).map((a) =>
            a.id === "lead"
              ? {
                  ...a,
                  stepId,
                  model: t.model || t.provider,
                  provider: t.provider,
                  status: "running",
                  progress: 0,
                }
              : a,
          )
        : [
            {
              id: "lead",
              stepId,
              name: "Djinn",
              role: "Orchestration",
              model: t.model || t.provider,
              status: "running",
              summary: i18n.t("djinn.run_in_progress"),
              progress: 0,
            },
            ...(t.activeStepId !== stepId
              ? t.agentHistory?.[stepId || ""] || []
              : t.agents),
          ],
      runMode: mode,
      reviewApprovedAt: undefined,
      phase: stage
        ? (
            {
              discussion: "brief",
              exploration: "brief",
              reflection: "brief",
              specification: "brief",
              prototype: "execution",
              implementation: "execution",
              review: "review",
              delivery: "delivery",
            } as const
          )[stage.type]
        : t.phase,
      events: [
        ...t.events,
        {
          ...event(
            "phase",
            mode === "plan"
              ? i18n.t("djinn.framing_started")
              : mode === "review"
                ? i18n.t("djinn.review_started")
                : i18n.t("djinn.execution_started"),
            i18n.t("djinn.events_live"),
            "lead",
          ),
          actor: "human",
          stepId,
        },
      ],
    }));
    const images = selectImages(
      task.artifacts.filter((a) => !a.stepId || a.stepId === stepId),
    );
    const artifacts = missionSupports(
      task.artifacts,
      stage?.id,
      images.map((image) => image.id),
    );
    const providerPrompt = buildMissionPrompt(
      task,
      stage,
      images.map((image) => image.id),
    );
    try {
      await window.djinn.saveState(ref.current);
      const result = await window.djinn.startRun({
        stepId,
        step: stage,
        projectSnapshot: task.projectSnapshot,
        workflowOrigin: task.workflowOrigin,
        initialWorkflowProposal:
          stage?.type === "discussion"
            ? undefined
            : task.initialWorkflowProposal,
        workflowProposal: task.workflowProposal,
        providerSessions: task.providerSessions,
        guidance,
        taskId: task.id,
        provider: task.provider,
        cwd: task.project,
        prompt:
          task.workflowOrigin === "agent" && stage?.type === "discussion"
            ? `${providerPrompt}\nFinal routing contract: emit exactly one workflow_defined event with the complete timeline (1 to 8 stages using exploration, reflection, specification, prototype, implementation, review or delivery) and a non-empty reason. This read-only qualification pass must not emit legacy routing, artifacts, actions, agents, next_step or phase events.`
            : providerPrompt,
        model: task.model || undefined,
        mode,
        concurrency: task.configuration.concurrency,
        images,
        agents: task.agents
          .filter(
            (a) =>
              stage?.type !== "discussion" &&
              a.id !== "lead" &&
              a.origin !== "codex" &&
              a.prompt &&
              a.status === "queued" &&
              (!a.stepId || a.stepId === stepId),
          )
          .slice(0, 16)
          .map((a) => ({
            id: a.id,
            name: a.name,
            role: a.role,
            writeScope: a.writeScope,
            dependsOn: a.dependsOn,
            readOnly: a.readOnly,
            isolation: a.isolation,
            worktree: a.worktree,
            resources: a.resources,
            prompt: `${(a.prompt || "").slice(0, 10000)}\nLatest instructions for your scope: ${JSON.stringify(
              compactRecords(
                (task.instructions || [])
                  .filter((i) => !i.agentId || i.agentId === a.id)
                  .slice()
                  .reverse()
                  .map((i) => ({ id: i.id, text: i.text })),
                4000,
              ),
            )}\nAcquired decisions: ${contextExcerpt(
              task.questions
                .filter((q) => q.answer)
                .map((q) => ({ id: q.id, title: q.title, answer: q.answer }))
                .slice(-16),
              4000,
            )}\nCurrent supports: ${contextExcerpt(artifacts, 8000)}\nGoal: ${task.brief.slice(0, 4000)}. Work in your bounded ownership, respect AGENTS.md, preserve configured provider/model, report evidence and a concrete blocker if needed. Reuse valid checks; recheck affected areas only. Technical approvals use the native provider permission flow. No commits, pushes, publishing, deployment or external messages without explicit human instruction.`,
          })),
      });
      session.runId = result.runId;
      if (!session.finished)
        updateTask(task.id, (t) => ({
          ...t,
          runId: result.runId,
          status: t.questions.some((q) => q.blocking && !q.answer)
            ? "waiting"
            : "running",
          agents: t.agents.some((a) => a.id === "lead")
            ? t.agents.map((a) =>
                a.id === "lead" ? { ...a, status: "running" } : a,
              )
            : [
                {
                  id: "lead",
                  name: "Djinn",
                  role: "Orchestration",
                  model: task.model || task.provider,
                  status: "running",
                  progress: 0,
                  summary: i18n.t("djinn.agent_preparing"),
                },
                ...t.agents,
              ],
        }));
    } catch (error) {
      session.finished = true;
      notify(message(error));
      updateTask(task.id, (t) => ({
        ...(stepId ? finishStepRun(t, stepId, "error", message(error)) : t),
        status: "error",
        runId: undefined,
        agents: t.agents.map((agent) =>
          agent.id === "lead" && agent.status === "running"
            ? { ...agent, status: "error" }
            : agent,
        ),
        events: [
          ...t.events,
          event("error", i18n.t("djinn.start_failed"), message(error)),
        ],
      }));
    } finally {
      startLock.current = false;
      setStarting(false);
    }
  };
  startRef.current = start;
  const switchProvider = async (
    provider: ProviderId,
    model: string,
    resume: boolean,
  ): Promise<boolean> => {
    const latest = ref.current.tasks.find((t) => t.id === task.id);
    if (
      !latest ||
      latest.runId ||
      latest.status === "running" ||
      startLock.current
    ) {
      notify(i18n.t("djinn.pause_before_provider_switch"));
      return false;
    }
    if (!persistenceEnabled) {
      notify(i18n.t("djinn.saving_suspended_provider"));
      return false;
    }
    const target = environment.providers.find((p) => p.id === provider);
    if (!target?.available || target.authenticated === false) {
      notify(i18n.t("djinn.connect_provider_first"));
      return false;
    }
    const changed = provider !== latest.provider || model !== latest.model;
    if (changed)
      updateTask(latest.id, (t) => ({
        ...t,
        provider,
        model,
        providerSessions: undefined,
        agents: t.agents.map((agent) =>
          agent.origin === "codex" || agent.status === "done"
            ? agent
            : {
                ...agent,
                status: "queued",
                model: model || provider,
                provider,
                waitReason: undefined,
                waitingForAgentIds: undefined,
              },
        ),
        events: [
          ...t.events,
          {
            ...event(
              "decision",
              i18n.t("providers.changed"),
              i18n.t("djinn.provider_changed_detail", {
                from: t.provider === "claude" ? "Claude Code" : "Codex",
                to: provider === "claude" ? "Claude Code" : "Codex",
                model: ` · ${model || i18n.t("djinn.default_model")}`,
              }),
              "lead",
            ),
            actor: "human",
            stepId: t.activeStepId,
          },
        ],
      }));
    if (resume) await startRef.current(undefined, latest.id);
    else if (changed) notify(i18n.t("djinn.provider_changed_toast"));
    return true;
  };
  useEffect(() => {
    if (!ready || !window.djinn) return;
    for (const mission of state.tasks) {
      if (!mission.runId) continue;
      for (const instruction of mission.instructions || []) {
        if (
          instruction.status !== "queued" ||
          instruction.appliedAt ||
          instructionTransmissions.current.has(instruction.id) ||
          pendingRuns.current
            .get(mission.id)
            ?.sentInstructionIds?.includes(instruction.id)
        )
          continue;
        instructionTransmissions.current.add(instruction.id);
        void window.djinn
          .steerRun({
            runId: mission.runId,
            id: instruction.id,
            text: instruction.text,
            agentId: instruction.agentId,
          })
          .then((receipt) => {
            updateTask(mission.id, (t) => ({
              ...t,
              instructions: (t.instructions || []).map((i) =>
                i.id === instruction.id && !i.appliedAt
                  ? {
                      ...i,
                      status: receipt.status as typeof i.status,
                      reason: receipt.reason,
                    }
                  : i,
              ),
            }));
            if (receipt.reason === "run_cancelled")
              resumeAfterStoppedRun(mission.id, mission.runId!);
          })
          .catch((error) => {
            const inactive =
              record(error).code === "run_inactive" ||
              /no longer active/.test(message(error));
            updateTask(mission.id, (t) => ({
              ...t,
              instructions: (t.instructions || []).map((i) =>
                i.id === instruction.id
                  ? {
                      ...i,
                      status: inactive ? "queued" : "prevented",
                      reason: message(error),
                    }
                  : i,
              ),
            }));
            if (inactive) resumeAfterStoppedRun(mission.id, mission.runId!);
          });
      }
    }
  }, [state, ready, updateTask, resumeAfterStoppedRun]);
  const pause = async () => {
    if (task.runId && window.djinn) {
      try {
        await window.djinn.cancelRun(task.runId);
      } catch (error) {
        notify(message(error));
        return;
      }
    }
    const session = pendingRuns.current.get(task.id);
    if (session) session.finished = true;
    updateTask(task.id, (t) => ({
      ...t,
      status: "paused",
      runId: t.runId,
      steps: t.steps?.map((s) =>
        s.id === t.activeStepId ? { ...s, status: "paused" } : s,
      ),
      events: [
        ...t.events,
        {
          ...event(
            "note",
            i18n.t("chat.event_wish_paused"),
            i18n.t("djinn.context_kept_for_resume"),
          ),
          actor: "human",
        },
      ],
    }));
    notify(i18n.t("djinn.wish_paused_toast"));
  };
  const exportTask = async () => {
    const payload = {
      format: "djinn-session",
      version: 2,
      projects: state.projects || [],
      exportedAt: now(),
      task: {
        ...task,
        runId: undefined,
        status: task.status === "running" ? "paused" : task.status,
      },
    };
    try {
      if (window.djinn) {
        const result = await window.djinn.exportSession(payload);
        if (result) notify(i18n.t("djinn.wish_exported_file"));
      } else {
        download(
          `${task.title.replace(/\W+/g, "-")}.djinn.json`,
          JSON.stringify(payload, null, 2),
          "application/json",
        );
        notify(i18n.t("djinn.wish_exported"));
      }
    } catch (error) {
      notify(message(error));
    }
  };
  const exportMissionJournal = async () => {
    try {
      const events = await collectMissionJournal(
        task,
        window.djinn?.getMissionJournalPage
          ? (taskId, cursor, limit) =>
              window.djinn!.getMissionJournalPage!(taskId, cursor, limit)
          : undefined,
      );
      download(
        `${task.title.replace(/\W+/g, "-")}.journal.jsonl`,
        serializeMissionJournal(events),
        "application/x-ndjson",
      );
      notify(i18n.t("djinn.journal_exported"));
    } catch (error) {
      notify(message(error));
    }
  };
  const importTask = async (file?: File, given?: unknown) => {
    try {
      let payload: unknown = given;
      if (given !== undefined) {
        // A wish djinn asks to show, already read.
      } else if (file) {
        if (file.size > 20 * 1024 * 1024)
          throw new Error(i18n.t("djinn.file_too_large"));
        payload = JSON.parse(await file.text());
      } else payload = await window.djinn?.importSession();
      if (!payload) return;
      const p = record(payload);
      if (
        ![1, 2].includes(p.version as number) ||
        p.format !== "djinn-session" ||
        !p.task
      )
        throw new Error(i18n.t("djinn.choose_session"));
      const restored = validateState({
        version: p.version,
        tasks: [p.task],
        ...(p.projects ? { projects: p.projects } : {}),
      });
      const imported = restored.tasks[0];
      const t: Task = {
        ...imported,
        providerSessions: undefined,
        id: uid(),
        runId: undefined,
        status: "paused",
        events: [
          ...imported.events,
          event(
            "note",
            i18n.t("djinn.wish_imported"),
            i18n.t("djinn.imported_detail"),
          ),
        ],
      };
      commit((s) => ({
        ...s,
        projects: [
          ...(s.projects || []),
          ...(restored.projects || []).filter(
            (p) => !(s.projects || []).some((existing) => existing.id === p.id),
          ),
        ],
        tasks: [...s.tasks, t],
        selectedId: t.id,
      }));
      notify(i18n.t("djinn.wish_imported_toast"));
    } catch (error) {
      notify(message(error));
    }
  };
  // djinn wish resume shows a wish: select its mission, imported from the wish when the interface lacks it.
  useEffect(() => {
    if (!ready || !window.djinnFocus) return;
    return window.djinnFocus.subscribe((focus) => {
      if (!focus.wishId || !focus.title) return;
      const existing = [...ref.current.tasks]
        .reverse()
        .find((t) => t.title === focus.title);
      if (existing) commit((s) => ({ ...s, selectedId: existing.id }));
      else if (focus.session) void importTask(undefined, focus.session);
    });
  }, [ready]);
  const removeTask = () => {
    if (task.runId) {
      notify(i18n.t("djinn.pause_before_delete"));
      return;
    }
    setState((s) => {
      const tasks = s.tasks.filter((t) => t.id !== task.id);
      if (!tasks.length)
        tasks.push(newTask(i18n.t("djinn.new_wish"), "", "", "codex", ""));
      return { ...s, tasks, selectedId: tasks[0].id };
    });
  };
  const indicate = async (input: string, agentId?: string) => {
    const value = input.trim();
    if (!value || value.length > 12000) return false;
    if (agentId && !task.agents.some((a) => a.id === agentId)) return false;
    const instruction = {
      id: uid(),
      text: value,
      time: now(),
      agentId,
      stepId: task.activeStepId,
      status: "queued" as const,
    };
    updateTask(task.id, (t) => ({
      ...(t.steps?.find((s) => s.id === t.activeStepId)?.status ===
        "completed" && !t.runId
        ? reopenStep(t, t.activeStepId!)
        : t),
      instructions: [...(t.instructions || []), instruction],
      reviewApprovedAt: undefined,
      status: t.status === "done" ? "idle" : t.status,
      phase: t.phase,
      events: [
        ...t.events,
        {
          ...event("note", i18n.t("chat.you"), value, agentId || "lead"),
          stepId: t.activeStepId,
          time: instruction.time,
          actor: "human",
          interventionId: instruction.id,
        },
      ],
      agents: agentId
        ? t.agents.map((a) =>
            a.id === agentId && a.status !== "running"
              ? {
                  ...a,
                  status: "queued",
                  summary: i18n.t("djinn.message_waiting"),
                }
              : a,
          )
        : t.agents,
    }));
    if (task.runId && window.djinn) {
      instructionTransmissions.current.add(instruction.id);
      if (task.status === "paused") resumeRequested.current.add(task.id);
      try {
        const receipt = await window.djinn.steerRun({
          runId: task.runId,
          id: instruction.id,
          text: value,
          agentId,
        });
        updateTask(task.id, (t) => ({
          ...t,
          instructions: (t.instructions || []).map((i) =>
            i.id === instruction.id && !i.appliedAt
              ? {
                  ...i,
                  status: receipt.status as typeof i.status,
                  reason: receipt.reason,
                }
              : i,
          ),
        }));
        if (receipt.reason === "run_cancelled")
          resumeAfterStoppedRun(task.id, task.runId);
        if (receipt.status === "prevented")
          notify(
            i18n.t("djinn.instruction_kept", {
              reason: receipt.reason || i18n.t("djinn.transmission_prevented"),
            }),
          );
      } catch (error) {
        const inactive =
          record(error).code === "run_inactive" ||
          /no longer active/.test(message(error));
        updateTask(task.id, (t) => ({
          ...t,
          instructions: t.instructions?.map((i) =>
            i.id === instruction.id
              ? {
                  ...i,
                  status: inactive ? "queued" : "prevented",
                  reason: message(error),
                }
              : i,
          ),
        }));
        if (inactive) {
          const latest = ref.current.tasks.find((t) => t.id === task.id)!;
          if (latest.runId) resumeRequested.current.add(task.id);
          else if (canResumeAfterHumanInput(latest))
            void startRef.current(undefined, task.id);
        }
        notify(
          i18n.t("djinn.instruction_kept_for_resume", {
            error: message(error),
          }),
        );
      }
    } else if (!task.demo) {
      const latest = ref.current.tasks.find((t) => t.id === task.id)!;
      if (canResumeAfterHumanInput(latest))
        void startRef.current(undefined, latest.id);
      else notify(i18n.t("djinn.instruction_saved"));
    }
    return true;
  };
  const selectStep = (id: string) =>
    updateTask(task.id, (t) =>
      t.steps?.some((s) => s.id === id) ? { ...t, selectedStepId: id } : t,
    );
  const focusStep = (id: string) => {
    try {
      const current = ref.current.tasks.find((t) => t.id === task.id);
      if (!current) return;
      if (current.runId || current.status === "running") {
        notify(i18n.t("djinn.pause_before_step_change"));
        return;
      }
      const focused = focusWorkflowStep(current, id, { humanOverride: true });
      updateTask(task.id, () => ({
        ...focused,
        agentHistory: current.activeStepId
          ? { ...current.agentHistory, [current.activeStepId]: current.agents }
          : current.agentHistory,
        agents: current.agentHistory?.[id] || [],
        events: [
          ...focused.events,
          {
            ...event(
              "note",
              i18n.t("djinn.priority_changed"),
              focused.steps?.find((s) => s.id === id)?.title,
            ),
            stepId: id,
            actor: "human",
          },
        ],
      }));
    } catch (error) {
      notify(message(error));
    }
  };
  const resumeStep = (id: string) => {
    try {
      updateTask(task.id, (t) => reopenStep(t, id));
    } catch (error) {
      notify(message(error));
    }
  };
  const validateStepResult = (id: string) => {
    try {
      updateTask(task.id, (t) => ({
        ...approveStep(t, id, "human"),
        ...(t.steps?.find((s) => s.id === id)?.type === "review"
          ? { reviewApprovedAt: now() }
          : {}),
        events: [
          ...t.events,
          {
            ...event("phase", i18n.t("djinn.result_validated")),
            stepId: id,
            actor: "human",
          },
        ],
      }));
    } catch (error) {
      notify(message(error));
    }
  };
  const addNextStep = (type: StepType, title: string, objective: string) => {
    try {
      const latest = ref.current.tasks.find((t) => t.id === task.id)!;
      const step = {
        ...createDiscussionStep(uid(), type),
        title: title.trim(),
        objective: objective.trim(),
      };
      const updated = appendStep(latest, step);
      updateTask(task.id, () => ({
        ...updated,
        nextStepProposal: undefined,
        events: [
          ...latest.events,
          {
            ...event("phase", i18n.t("djinn.step_added"), step.title),
            stepId: step.id,
            actor: "human",
          },
        ],
      }));
      return true;
    } catch (error) {
      notify(message(error));
      return false;
    }
  };
  const dismissQuestionAlert = (id: string) =>
    setQuestionAlerts((previous) => previous.filter((a) => a.id !== id));
  const dismissActionAlert = (id: string) =>
    setActionAlerts((previous) => previous.filter((a) => a.id !== id));
  const openAction = (taskId: string, actionId: string) => {
    select(taskId);
    dismissActionAlert(`${taskId}:${actionId}`);
    window.dispatchEvent(
      new CustomEvent("djinn:action-focus", { detail: { taskId, actionId } }),
    );
  };
  const performAction = async (
    action: TaskAction,
    operation: "run" | "stop" | "complete" | "open",
  ) => {
    if (actionLocks.current.has(action.id)) return false;
    actionLocks.current.add(action.id);
    setBusyActionIds((current) => [...current, action.id]);
    const taskId = task.id;
    try {
      let result: TaskAction;
      if (typeof window.djinn?.performAction === "function") {
        result = validateAction(
          await window.djinn.performAction({
            taskId,
            cwd: task.project,
            action,
            operation,
          }),
        );
      } else if (operation === "complete" && action.kind === "manual") {
        result = { ...action, status: "done", updatedAt: now() };
      } else if (operation === "open" && action.url && action.kind === "link") {
        const safe = validateAction(action);
        window.open(safe.url, "_blank", "noopener,noreferrer");
        result = { ...action, status: "done", updatedAt: now() };
      } else {
        notify(i18n.t("djinn.open_in_electron_server"));
        return false;
      }
      const time = now();
      updateTask(taskId, (t) => ({
        ...t,
        actions: [
          ...(t.actions || []).filter((a) => a.id !== result.id),
          {
            ...t.actions?.find((a) => a.id === result.id),
            ...result,
            stepId: result.stepId || action.stepId,
            runId: result.runId || action.runId,
            agentId: result.agentId || action.agentId,
            testStartedAt:
              operation === "open" &&
              result.kind === "server" &&
              result.status === "ready"
                ? time
                : ["run", "stop"].includes(operation)
                  ? undefined
                  : t.actions?.find((a) => a.id === result.id)?.testStartedAt,
            testResult:
              operation === "run"
                ? undefined
                : result.testResult ||
                  t.actions?.find((a) => a.id === result.id)?.testResult,
          },
        ],
        events: [
          ...t.events,
          {
            ...event(
              "note",
              operation === "open"
                ? i18n.t("djinn.preview_opened")
                : operation === "stop"
                  ? i18n.t("djinn.server_stopped")
                  : operation === "complete"
                    ? i18n.t("djinn.action_done")
                    : i18n.t("djinn.server_requested"),
              action.title,
            ),
            actor: "human",
            time,
          },
        ],
      }));
      dismissActionAlert(`${taskId}:${action.id}`);
      return result.status !== "error";
    } catch (error) {
      notify(i18n.t("djinn.action_failed", { error: message(error) }));
      return false;
    } finally {
      actionLocks.current.delete(action.id);
      setBusyActionIds((current) => current.filter((id) => id !== action.id));
    }
  };
  const respondPermission = async (
    request: PermissionRequest,
    decision: PermissionDecision,
    answers?: Record<string, string>,
  ) => {
    const current = ref.current.tasks
      .find((t) => t.id === request.taskId)
      ?.permissions?.find((p) => p.id === request.id);
    if (
      !current ||
      current.status !== "pending" ||
      permissionLocks.current.has(request.id)
    )
      return false;
    if (decision === "acceptForSession" && !current.canAcceptForSession)
      return false;
    if (!window.djinn?.respondPermission) {
      notify(i18n.t("djinn.permission_needs_provider"));
      return false;
    }
    permissionLocks.current.add(request.id);
    setBusyPermissionIds((ids) => [...ids, request.id]);
    try {
      const result = await window.djinn.respondPermission({
        taskId: request.taskId,
        requestId: request.id,
        decision,
        answers,
      });
      if (!result.resolved) throw new Error(i18n.t("djinn.request_inactive"));
      updateTask(request.taskId, (t) => ({
        ...t,
        permissions: t.permissions?.map((p) =>
          p.id === request.id && p.status === "pending"
            ? {
                ...p,
                status: decision === "decline" ? "declined" : "accepted",
                updatedAt: now(),
              }
            : p,
        ),
      }));
      return true;
    } catch (error) {
      notify(i18n.t("djinn.permission_not_sent", { error: message(error) }));
      try {
        const live = await window.djinn.getPendingPermissions?.();
        if (live && !live.some((p) => p.id === request.id)) {
          updateTask(request.taskId, (t) => ({
            ...t,
            permissions: t.permissions?.map((p) =>
              p.id === request.id
                ? { ...p, status: "cancelled", updatedAt: now() }
                : p,
            ),
          }));
        }
      } catch {
        /* Keep a failed reply visible and retryable until the native state is known. */
      }
      return false;
    } finally {
      permissionLocks.current.delete(request.id);
      setBusyPermissionIds((ids) => ids.filter((id) => id !== request.id));
    }
  };
  const startTest = async (action: TaskAction) => {
    if (action.kind === "server") return performAction(action, "open");
    if (action.kind === "link" && !(await performAction(action, "open")))
      return false;
    const time = now();
    updateTask(task.id, (t) => ({
      ...t,
      actions: t.actions?.map((a) =>
        a.id === action.id ? { ...a, testStartedAt: time } : a,
      ),
    }));
    openAction(task.id, action.id);
    return true;
  };
  const recordTestResult = async (
    action: TaskAction,
    outcome: "passed" | "problem" | "deferred",
    detail?: string,
  ) => {
    const missionId = task.id;
    const latest = ref.current.tasks
      .find((t) => t.id === missionId)
      ?.actions?.find((a) => a.id === action.id);
    if (
      !latest ||
      latest.runId !== action.runId ||
      (outcome !== "deferred" &&
        latest.kind === "server" &&
        latest.status !== "ready")
    ) {
      notify(i18n.t("djinn.prepare_test_first"));
      return false;
    }
    const feedback = detail?.trim().slice(0, 12000);
    if (outcome === "problem" && !feedback) {
      notify(i18n.t("djinn.describe_problem"));
      return false;
    }
    updateTask(missionId, (t) => ({
      ...t,
      actions: t.actions?.map((a) =>
        a.id === action.id
          ? {
              ...a,
              testStartedAt: undefined,
              testResult: {
                status: outcome,
                detail: feedback,
                recordedAt: now(),
              },
            }
          : a,
      ),
      events: [
        ...t.events,
        {
          ...event(
            "review",
            outcome === "passed"
              ? i18n.t("djinn.test_validated")
              : outcome === "problem"
                ? i18n.t("djinn.problem_reported")
                : i18n.t("djinn.test_postponed"),
            `${action.title}${feedback ? `\n${feedback}` : ""}`,
            "lead",
          ),
          stepId: action.stepId,
          runId: action.runId,
          actor: "human",
        },
      ],
    }));
    if (outcome === "problem") {
      await indicate(
        i18n.t("djinn.test_feedback_prompt", {
          title: action.title,
          target: action.directory || action.url || action.id,
          feedback: feedback || "",
        }),
        "lead",
      );
    }
    return true;
  };
  const openQuestion = (taskId: string, questionId: string) => {
    select(taskId);
    dismissQuestionAlert(`${taskId}:${questionId}`);
    window.dispatchEvent(
      new CustomEvent("djinn:question-focus", {
        detail: { taskId, questionId },
      }),
    );
  };
  const enableNotifications = async () => {
    if (window.djinn) {
      try {
        const result = await window.djinn.notifyQuestion({
          taskId: task.id,
          questionId: task.questions.find((q) => !q.answer)?.id || "test",
          title: "Djinn",
          body: i18n.t("djinn.test_notification_body"),
        });
        setNotificationStatus(
          result.shown
            ? i18n.t("djinn.test_notification_sent")
            : result.message ||
                result.error ||
                i18n.t("djinn.check_macos_notifications"),
        );
      } catch (error) {
        setNotificationStatus(message(error));
      }
      return;
    }
    if (typeof Notification === "undefined") {
      setNotificationStatus(i18n.t("djinn.alerts_in_djinn"));
      return;
    }
    try {
      const permission = await Notification.requestPermission();
      setBrowserPermission(permission);
      setNotificationStatus(
        permission === "granted"
          ? i18n.t("djinn.notifications_enabled")
          : permission === "denied"
            ? i18n.t("djinn.notifications_denied")
            : i18n.t("djinn.alerts_in_djinn"),
      );
    } catch (error) {
      setNotificationStatus(message(error));
    }
  };
  const refreshEnvironment = async () => {
    if (window.djinn)
      try {
        setEnvironment(await window.djinn.getEnvironment());
        notify(i18n.t("djinn.connections_refreshed"));
      } catch (error) {
        notify(message(error));
      }
    else notify(i18n.t("djinn.open_in_electron_cli"));
  };
  return {
    state,
    loadDemo,
    setState,
    task,
    ready,
    persistenceEnabled,
    environment,
    authStatus,
    toast,
    notify,
    select,
    addTask,
    saveProject,
    updateTask,
    selectStep,
    focusStep,
    resumeStep,
    validateStepResult,
    addNextStep,
    answer,
    reopen,
    start,
    switchProvider,
    pause,
    starting,
    exportTask,
    exportMissionJournal,
    importTask,
    removeTask,
    refreshEnvironment,
    indicate,
    questionAlerts,
    notificationStatus,
    browserPermission,
    dismissQuestionAlert,
    openQuestion,
    enableNotifications,
    actionAlerts,
    busyActionIds,
    permissions: task.permissions || [],
    busyPermissionIds,
    respondPermission,
    recordTestResult,
    startTest,
    performAction,
    openAction,
    dismissActionAlert,
  };
}
export function download(name: string, content: string, type = "text/plain") {
  const blob = new Blob([content], { type });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
