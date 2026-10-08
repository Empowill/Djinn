import { Check, Play, Plus, RotateCcw, Settings2 } from "lucide-react";
import type { Task, MissionStep, StepType } from "./types";
import { canStartStep, MAX_SUB_AGENTS, STEP_TYPES } from "./workflow";
import { uid } from "./data";
import "./step-timeline.css";
import { t } from "./i18n";
export const stepLabels: Record<StepType, string> = {
  discussion: t("steps.type_discussion"),
  exploration: t("steps.type_exploration"),
  reflection: t("steps.type_reflection"),
  specification: t("steps.type_specification"),
  prototype: t("steps.type_prototype"),
  implementation: t("steps.type_implementation"),
  review: t("steps.type_review"),
  delivery: t("steps.type_delivery"),
};
const labels = {
  pending: t("steps.status_pending"),
  running: t("steps.status_running"),
  awaiting_human: t("steps.status_awaiting_human"),
  completed: t("steps.status_completed"),
  blocked: t("steps.status_blocked"),
  paused: t("steps.status_paused"),
  error: t("steps.status_error"),
};
export function StepTimeline({
  task,
  onSelect,
  onStart,
  onReopen,
  onFocus,
  onConfigure,
  onAdd,
  starting = false,
}: {
  task: Task;
  onSelect: (id: string) => void;
  onStart: (id: string) => void;
  onReopen: (id: string) => void;
  onFocus?: (id: string) => void;
  onConfigure: () => void;
  onAdd?: () => void;
  starting?: boolean;
}) {
  const steps = task.steps || [],
    next =
      steps.find(
        (s) =>
          s.id === task.activeStepId &&
          (s.status !== "completed" || s.needsRevalidation),
      ) || steps.find((s) => s.status !== "completed" || s.needsRevalidation),
    selected = task.selectedStepId || task.activeStepId;
  return (
    <div className="workflow-strip">
      <div className="workflow-timeline-row">
        <nav className="step-timeline" aria-label={t("steps.nav_label")}>
          {steps.map((s, i) => (
            <button
              key={s.id}
              aria-current={selected === s.id ? "step" : undefined}
              className={`step-item phase-item ${selected === s.id ? "selected" : ""} ${s.status} ${s.status === "completed" ? "complete" : ""} ${s.id === task.activeStepId ? "current" : ""}`}
              onClick={() => onSelect(s.id)}
            >
              <span className="step-number phase-circle">
                {s.status === "completed" ? (
                  <Check size={14} aria-hidden="true" />
                ) : s.id === task.activeStepId && s.status === "running" ? (
                  <span className="phase-spin" aria-hidden="true" />
                ) : (
                  String(i + 1).padStart(2, "0")
                )}
              </span>
              <span className="phase-copy">
                <strong>{s.title}</strong>
                <small>
                  {s.needsRevalidation
                    ? t("steps.needs_revalidation")
                    : s.status === "completed" && s.validation === "automatic"
                      ? t("steps.completed_automatically")
                      : labels[s.status]}
                  {s.id === task.activeStepId &&
                    task.runId &&
                    (task.permissions?.some(
                      (permission) => permission.status === "pending",
                    )
                      ? t("steps.permission_awaited")
                      : t("steps.run_active"))}
                </small>
              </span>
              {i < steps.length - 1 && (
                <span className="phase-line" aria-hidden="true" />
              )}
            </button>
          ))}
        </nav>
        <button
          type="button"
          className="icon-button workflow-config"
          aria-label={t("steps.configure")}
          title={t("steps.configure")}
          onClick={onConfigure}
        >
          <Settings2 size={16} />
        </button>
      </div>
      <div className="workflow-strip-actions">
        {selected !== task.activeStepId &&
          steps.find((s) => s.id === selected)?.status !== "completed" &&
          onFocus && (
            <button
              className="button secondary small"
              disabled={!!task.runId || starting}
              title={
                task.runId
                  ? t("steps.focus_pause_first")
                  : t("steps.focus_hint")
              }
              onClick={() => onFocus(selected!)}
            >
              {t("steps.focus")}
            </button>
          )}
        {task.workflowMode === "flexible" && !next && onAdd && (
          <button
            className="button secondary small"
            disabled={!!task.runId || starting}
            onClick={onAdd}
          >
            <Plus size={13} /> {t("steps.add")}
          </button>
        )}
        {next && next.status === "pending" && (
          <button
            className="button accent small"
            disabled={!canStartStep(task, next.id) || !!task.runId || starting}
            onClick={() => onStart(next.id)}
          >
            <Play size={13} />
            {t("steps.start", { title: next.title })}
          </button>
        )}
        {selected !== task.activeStepId && task.activeStepId && (
          <button
            className="button secondary small"
            onClick={() => onSelect(task.activeStepId!)}
          >
            {t("steps.back_to_active")}
          </button>
        )}
        {steps.find((s) => s.id === selected)?.status === "completed" && (
          <button
            className="button secondary small"
            disabled={!!task.runId || starting}
            onClick={() => onReopen(selected!)}
          >
            <RotateCcw size={12} />
            {t("steps.reopen")}
          </button>
        )}
      </div>
    </div>
  );
}
export function WorkflowEditor({
  steps,
  onChange,
  disabled = false,
  allowAdd = true,
}: {
  steps: MissionStep[];
  onChange: (s: MissionStep[]) => void;
  disabled?: boolean;
  allowAdd?: boolean;
}) {
  const update = (id: string, patch: Partial<MissionStep>) =>
    onChange(steps.map((s) => (s.id === id ? { ...s, ...patch } : s)));
  const add = (type: StepType = "reflection") =>
    onChange([
      ...steps,
      {
        id: uid(),
        type,
        title: stepLabels[type],
        objective: "",
        status: "pending",
        exitCriteria: [],
        expectedArtifacts: [],
        skills: [],
      },
    ]);
  return (
    <section className="workflow-editor">
      <h3>Workflow</h3>
      <p>{t("steps.editor_intro")}</p>
      {steps.map((s, i) => (
        <fieldset key={s.id} disabled={disabled || s.status !== "pending"}>
          <legend>
            {i + 1}.{" "}
            {s.status === "pending"
              ? t("steps.future_step")
              : t("steps.kept_history")}
          </legend>
          <div className="workflow-editor-row">
            <select
              aria-label={t("steps.step_type", { number: i + 1 })}
              value={s.type}
              onChange={(e) =>
                update(s.id, {
                  type: e.target.value as StepType,
                  title:
                    s.title === stepLabels[s.type]
                      ? stepLabels[e.target.value as StepType]
                      : s.title,
                })
              }
            >
              {STEP_TYPES.filter((t) => t !== "discussion").map((t) => (
                <option key={t} value={t}>
                  {stepLabels[t]}
                </option>
              ))}
            </select>
            <input
              aria-label={t("steps.step_title", { number: i + 1 })}
              value={s.title}
              onChange={(e) => update(s.id, { title: e.target.value })}
            />
            <button
              type="button"
              aria-label={t("steps.move_up", { number: i + 1 })}
              disabled={i === 0 || steps[i - 1].status !== "pending"}
              onClick={() => {
                const next = [...steps];
                [next[i - 1], next[i]] = [next[i], next[i - 1]];
                onChange(next);
              }}
            >
              ↑
            </button>
            <button
              type="button"
              aria-label={t("steps.move_down", { number: i + 1 })}
              disabled={
                i === steps.length - 1 || steps[i + 1].status !== "pending"
              }
              onClick={() => {
                const next = [...steps];
                [next[i + 1], next[i]] = [next[i], next[i + 1]];
                onChange(next);
              }}
            >
              ↓
            </button>
            <button
              type="button"
              aria-label={t("steps.duplicate", { number: i + 1 })}
              disabled={steps.length >= 100}
              onClick={() => {
                const next = [...steps];
                next.splice(i + 1, 0, { ...structuredClone(s), id: uid() });
                onChange(next);
              }}
            >
              +
            </button>
            <button
              type="button"
              aria-label={t("steps.remove", { number: i + 1 })}
              disabled={steps.length === 1}
              onClick={() => onChange(steps.filter((item) => item.id !== s.id))}
            >
              ×
            </button>
          </div>
          <label>
            {t("steps.objective")}
            <textarea
              rows={2}
              value={s.objective}
              onChange={(e) => update(s.id, { objective: e.target.value })}
            />
          </label>
          <label>
            {t("steps.exit_criteria")}
            <textarea
              rows={2}
              value={s.exitCriteria.join("\n")}
              onChange={(e) =>
                update(s.id, {
                  exitCriteria: e.target.value.split("\n"),
                })
              }
            />
          </label>
          <label>
            {t("steps.expected_artifacts")}
            <input
              value={s.expectedArtifacts.join(", ")}
              onChange={(e) =>
                update(s.id, {
                  expectedArtifacts: e.target.value
                    .split(",")
                    .map((v) => v.trim()),
                })
              }
            />
          </label>
          <label>
            {t("steps.optional_skills")}
            <input
              placeholder="grill-me"
              value={s.skills.join(", ")}
              onChange={(e) =>
                update(s.id, {
                  skills: e.target.value.split(",").map((v) => v.trim()),
                })
              }
            />
          </label>
        </fieldset>
      ))}
      <button
        type="button"
        className="button secondary small"
        disabled={disabled || !allowAdd || steps.length >= 100}
        onClick={() => add()}
      >
        {t("steps.add")}
      </button>
    </section>
  );
}
export function ConcurrencyField({
  value,
  onChange,
}: {
  value: number;
  onChange: (n: number) => void;
}) {
  return (
    <label>
      {t("steps.concurrency")}
      <input
        aria-label={t("steps.concurrency_max")}
        type="number"
        min={1}
        max={MAX_SUB_AGENTS}
        value={value}
        onChange={(e) =>
          onChange(
            Math.max(
              1,
              Math.min(MAX_SUB_AGENTS, Math.trunc(Number(e.target.value) || 1)),
            ),
          )
        }
      />
      <small>{t("steps.concurrency_hint")}</small>
    </label>
  );
}
