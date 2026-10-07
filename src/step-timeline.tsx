import { Check, Play, Plus, RotateCcw, Settings2 } from "lucide-react";
import type { Task, MissionStep, StepType } from "./types";
import { canStartStep, MAX_SUB_AGENTS, STEP_TYPES } from "./workflow";
import { uid } from "./data";
import "./step-timeline.css";
export const stepLabels: Record<StepType, string> = {
  discussion: "Qualification",
  exploration: "Exploration",
  reflection: "Réflexion",
  specification: "Spécification",
  prototype: "Prototype",
  implementation: "Implémentation",
  review: "Review",
  delivery: "Livraison",
};
const labels = {
  pending: "À venir",
  running: "En cours",
  awaiting_human: "Résultat à valider",
  completed: "Validée",
  blocked: "Décision attendue",
  paused: "En pause",
  error: "Erreur",
};
export function StepTimeline({
  task,
  onSelect,
  onStart,
  onReopen,
  onConfigure,
  onAdd,
  starting = false,
}: {
  task: Task;
  onSelect: (id: string) => void;
  onStart: (id: string) => void;
  onReopen: (id: string) => void;
  onConfigure: () => void;
  onAdd?: () => void;
  starting?: boolean;
}) {
  const steps = task.steps || [],
    next = steps.find((s) => s.status !== "completed" || s.needsRevalidation),
    selected = task.selectedStepId || task.activeStepId;
  return (
    <div className="workflow-strip">
      <div className="workflow-timeline-row">
        <nav className="step-timeline" aria-label="Étapes de la mission">
          {steps.map((s, i) => (
            <button
              key={s.id}
              disabled={s.status === "pending"}
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
                  {s.needsRevalidation ? "À revalider" : s.status === "completed" && s.validation === "automatic" ? "Terminée automatiquement" : labels[s.status]}
                  {s.id === task.activeStepId &&
                    task.runId &&
                    " · Exécution active"}
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
          aria-label="Configurer le workflow"
          title="Configurer le workflow"
          onClick={onConfigure}
        >
          <Settings2 size={16} />
        </button>
      </div>
      <div className="workflow-strip-actions">
        {task.workflowMode === "flexible" && !next && onAdd && (
          <button
            className="button secondary small"
            disabled={!!task.runId || starting}
            onClick={onAdd}
          >
            <Plus size={13} /> Ajouter une étape
          </button>
        )}
        {next && next.status === "pending" && (
          <button
            className="button accent small"
            disabled={!canStartStep(task, next.id) || !!task.runId || starting}
            onClick={() => onStart(next.id)}
          >
            <Play size={13} />
            Lancer {next.title}
          </button>
        )}
        {selected !== task.activeStepId && task.activeStepId && (
          <button
            className="button secondary small"
            onClick={() => onSelect(task.activeStepId!)}
          >
            Revenir à l’étape active
          </button>
        )}
        {steps.find((s) => s.id === selected)?.status === "completed" && (
          <button
            className="button secondary small"
            disabled={!!task.runId || starting}
            onClick={() => onReopen(selected!)}
          >
            <RotateCcw size={12} />
            Reprendre cette étape
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
      <p>
        Les étapes commencées gardent leur identité. Chaque résultat attend
        votre validation.
      </p>
      {steps.map((s, i) => (
        <fieldset key={s.id} disabled={disabled || s.status !== "pending"}>
          <legend>
            {i + 1}.{" "}
            {s.status === "pending" ? "Étape future" : "Historique conservé"}
          </legend>
          <div className="workflow-editor-row">
            <select
              aria-label={`Type de l’étape ${i + 1}`}
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
              aria-label={`Titre de l’étape ${i + 1}`}
              value={s.title}
              onChange={(e) => update(s.id, { title: e.target.value })}
            />
            <button
              type="button"
              aria-label={`Monter l’étape ${i + 1}`}
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
              aria-label={`Descendre l’étape ${i + 1}`}
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
              aria-label={`Dupliquer l’étape ${i + 1}`}
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
              aria-label={`Retirer l’étape ${i + 1}`}
              disabled={steps.length === 1}
              onClick={() => onChange(steps.filter((item) => item.id !== s.id))}
            >
              ×
            </button>
          </div>
          <label>
            Objectif
            <textarea
              rows={2}
              value={s.objective}
              onChange={(e) => update(s.id, { objective: e.target.value })}
            />
          </label>
          <label>
            Critères de sortie (un par ligne)
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
            Supports attendus
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
            Skills facultatives
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
        Ajouter une étape
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
      Limite de workers simultanés
      <input
        aria-label="Nombre maximal de sous-agents"
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
      <small>
        Jusqu’à cette limite, les workers aux périmètres d’écriture
        explicitement disjoints peuvent avancer ensemble. Un périmètre absent ou
        vide reste réservé par prudence ; les dépendances et conflits sont
        signalés quand le runtime les rapporte.
      </small>
    </label>
  );
}
