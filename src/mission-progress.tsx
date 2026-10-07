import {
  Check,
  CheckCircle2,
  ChevronDown,
  CircleAlert,
  CircleDot,
  Clock3,
  FileCheck2,
  ListChecks,
} from "lucide-react";
import type { Agent, MissionStep, StepResult, Task } from "./types";
import { MarkdownBody } from "./markdown-body";
import { agentDisplayState } from "./agent-state";
import "./mission-progress.css";

const resultStatusLabels: Record<StepResult["status"], string> = {
  ready: "Prêt pour la suite",
  blocked: "Bloqué",
  needs_input: "Réponse attendue",
};

const resultStatusIcons: Record<StepResult["status"], typeof CheckCircle2> = {
  ready: CheckCircle2,
  blocked: CircleAlert,
  needs_input: CircleDot,
};

function nonEmpty(values: string[] | undefined): string[] {
  return (values || []).map((value) => value.trim()).filter(Boolean);
}

function ReportList({
  title,
  values,
  kind,
}: {
  title: string;
  values: string[] | undefined;
  kind: "completed" | "remaining" | "evidence";
}) {
  const items = nonEmpty(values);
  if (!items.length) return null;
  return (
    <section className={`stage-report-list is-${kind}`}>
      <h4>
        {kind === "completed" ? (
          <Check size={14} />
        ) : kind === "remaining" ? (
          <Clock3 size={14} />
        ) : (
          <FileCheck2 size={14} />
        )}
        {title}
        <span>{items.length}</span>
      </h4>
      <ul>
        {items.map((item, index) => (
          <li key={`${kind}-${index}-${item}`}>
            <span aria-hidden="true" />
            <span>{item}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function Criteria({ criteria }: { criteria: StepResult["criteria"] }) {
  if (!criteria?.length) return null;
  return (
    <details className="stage-report-details">
      <summary>
        <span>
          <ListChecks size={14} />
          Critères de sortie
        </span>
        <ChevronDown size={14} />
      </summary>
      <ul className="stage-report-criteria">
        {criteria.map((criterion, index) => (
          <li
            className={criterion.met ? "is-met" : "is-open"}
            key={`${criterion.criterion}-${index}`}
          >
            {criterion.met ? <Check size={13} /> : <CircleDot size={13} />}
            <span>
              <strong>{criterion.criterion}</strong>
              {criterion.evidence && <small>{criterion.evidence}</small>}
            </span>
          </li>
        ))}
      </ul>
    </details>
  );
}

/**
 * A compact, human-readable report for the selected mission stage.
 *
 * The report is driven by MissionStep.report, with the legacy step summary as
 * a narrow fallback. It does not infer completion from agent progress or the
 * event journal.
 */
export function StageReport({ step }: { step: MissionStep }) {
  const summary = step.report?.summary?.trim() || step.summary?.trim();
  if (!summary) return null;
  const fallbackStatus: StepResult["status"] =
    step.status === "completed"
      ? "ready"
      : step.status === "blocked" || step.status === "error"
        ? "blocked"
        : "needs_input";
  const report: StepResult = step.report
    ? { ...step.report, summary }
    : { status: fallbackStatus, summary };
  const StatusIcon = resultStatusIcons[report.status];
  const completed = nonEmpty(report.completed);
  const remaining = nonEmpty(report.remaining);
  const evidence = nonEmpty(report.evidence);
  return (
    <section
      className={`stage-report is-${report.status}`}
      aria-label={`Compte rendu de l’étape ${step.title}`}
    >
      <div className="stage-report-heading">
        <div className="stage-report-status" role="status">
          <StatusIcon size={16} />
          <span>{resultStatusLabels[report.status]}</span>
        </div>
        <span className="stage-report-step">Compte rendu · {step.title}</span>
      </div>
      <MarkdownBody text={report.summary} />
      {report.reason && <p className="stage-report-reason">{report.reason}</p>}
      {(completed.length > 0 || remaining.length > 0) && (
        <div className="stage-report-columns">
          <ReportList title="Fait" values={completed} kind="completed" />
          <ReportList title="Reste" values={remaining} kind="remaining" />
        </div>
      )}
      {report.nextAction && (
        <div className="stage-report-next">
          <span>Prochaine action</span>
          <p>{report.nextAction}</p>
        </div>
      )}
      {(report.criteria?.length || evidence.length > 0) && (
        <div className="stage-report-folds">
          <Criteria criteria={report.criteria} />
          {evidence.length > 0 && (
            <details className="stage-report-details">
              <summary>
                <span>
                  <FileCheck2 size={14} />
                  Éléments de preuve
                </span>
                <ChevronDown size={14} />
              </summary>
              <ul className="stage-report-evidence">
                {evidence.map((item, index) => (
                  <li key={`evidence-${index}-${item}`}>{item}</li>
                ))}
              </ul>
            </details>
          )}
        </div>
      )}
    </section>
  );
}

/**
 * Returns the human-facing attention state without changing the underlying
 * agent status. Permission and question blocks are overlays on a real state.
 */
export function agentAttentionLabel(
  task: Task,
  agent?: Agent,
): string | undefined {
  if (!agent) return undefined;
  const state = agentDisplayState(task, agent);
  if (state.permission) return "En attente d’autorisation";
  if (state.question) return "Blocage · question à traiter";
  return undefined;
}

/** Useful for small status labels in other mission surfaces. */
export function agentStatusLabel(task: Task, agent?: Agent): string {
  const attention = agentAttentionLabel(task, agent);
  if (attention) return attention;
  if (!agent) return "Activité enregistrée";
  return agentDisplayState(task, agent).label;
}

export default StageReport;
