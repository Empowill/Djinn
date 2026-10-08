import type { Artifact, MissionStep, Task } from "./types";
import { language, t } from "./i18n";

/** Keep current decisions and evidence; provider tool transcripts stay in the journal. */
export function compactRecords<T>(records: T[], budget: number): T[] {
  const result: T[] = [];
  let remaining = budget;
  for (const entry of records) {
    const size = JSON.stringify(entry).length;
    if (size > remaining) continue;
    result.push(entry);
    remaining -= size;
  }
  return result;
}

export function missionSupports(
  artifacts: Artifact[],
  stepId?: string,
  attachedImages: string[] = [],
) {
  const sorted = [...artifacts].sort(
    (a, b) =>
      Number(!!b.sourceOfTruth) - Number(!!a.sourceOfTruth) ||
      Number(b.stepId === stepId) - Number(a.stepId === stepId) ||
      Date.parse(b.updatedAt) - Date.parse(a.updatedAt),
  );
  return compactRecords(
    sorted.map((a) => ({
      id: a.id,
      title: a.title.slice(0, 250),
      type: a.type,
      sourceOfTruth: !!a.sourceOfTruth,
      revision: a.revision,
      editedBy: a.editedBy,
      content:
        a.type === "screenshot"
          ? attachedImages.includes(a.id)
            ? `Attached image ${a.id}; inspect its pixels.`
            : "Image not attached; do not claim visual inspection."
          : a.content.slice(0, a.sourceOfTruth ? 3500 : 1800),
      truncated: a.content.length > (a.sourceOfTruth ? 3500 : 1800),
    })),
    12000,
  );
}

export function buildMissionPrompt(
  task: Task,
  stage?: MissionStep,
  attachedImages: string[] = [],
) {
  const decisions = compactRecords(
    task.questions
      .filter((q) => q.answer)
      .slice()
      .reverse()
      .map((q) => ({
        id: q.id,
        title: q.title.slice(0, 250),
        answer: q.answer?.slice(0, 2000),
      })),
    3000,
  );
  const instructions = compactRecords(
    (task.instructions || [])
      .slice()
      .reverse()
      .map((i) => ({
        id: i.id,
        agentId: i.agentId,
        text: i.text.slice(0, 3000),
        truncated: i.text.length > 3000,
      })),
    4000,
  );
  const supports = compactRecords(
    missionSupports(task.artifacts, stage?.id, attachedImages),
    6500,
  );
  const summaries = compactRecords(
    (task.steps || [])
      .filter((s) => s.summary)
      .slice()
      .reverse()
      .map((s) => ({
        id: s.id,
        title: s.title.slice(0, 250),
        status: s.status,
        summary: s.summary?.slice(0, 1000),
      })),
    2000,
  );
  return [
    `You are Djinn, the persistent mission lead. Speak ${new Intl.DisplayNames(["en"], { type: "language" }).of(language)}. Goal: ${task.title.slice(0, 500)}\n${task.brief.slice(0, 3000)}`,
    `Current discussion: ${stage?.type || "reflection"}. Objective: ${(stage?.objective || task.brief).slice(0, 2000)}.`,
    "Use the shortest useful path to a usable result. Latest human instructions refine the goal and take priority over the proposed timeline. Adapt scope and priorities; keep acquired decisions. Stage names do not require another confirmation for authorized local work. Ask only unresolved business decisions, grouped together. Use native permission requests for technical approvals.",
    "Use bounded subagents for the mission with explicit disjoint ownership, keep independent results testable independently, and integrate their evidence. Preserve the configured model. Serialize shared heavy operations. Reuse valid checks and recheck only affected areas after fixes; scale verification to permissions/data/API risk. Do not repeat a known blocked operation without a relevant change.",
    `Latest human instructions (newest first): ${JSON.stringify(instructions)}`,
    `Acquired decisions (newest first): ${JSON.stringify(decisions)}`,
    `Current supports (canonical first, then current/latest; excerpts marked truncated): ${JSON.stringify(supports)}`,
    `Previous results (latest first): ${JSON.stringify(summaries)}`,
    `Recent runtime incidents and provider changes: ${JSON.stringify(
      compactRecords(
        task.events
          .filter(
            (entry) =>
              entry.type === "error" || entry.title === t("providers.changed"),
          )
          .slice()
          .reverse()
          .map((entry) => ({
            title: entry.title,
            detail: entry.detail.slice(0, 1000),
            stepId: entry.stepId,
          })),
        2000,
      ),
    )}`,
    `Work items: ${JSON.stringify(compactRecords((task.workItems || []).slice().reverse(), 2000))}`,
    `Latest contribution reports: ${JSON.stringify(compactRecords((task.reports || []).slice().reverse(), 2000))}`,
    `Open decisions: ${JSON.stringify(
      compactRecords(
        task.questions
          .filter((q) => !q.answer)
          .map((q) => ({
            id: q.id,
            title: q.title,
            context: q.context.slice(0, 1000),
            agentId: q.agentId,
            blocking: q.blocking,
          })),
        2000,
      ),
    )}`,
    `Human feedback: ${JSON.stringify(compactRecords(task.feedback.slice().reverse(), 1000))}`,
    stage?.type === "specification"
      ? "Produce the specification. Implementation requires a human request; do not infer it from a specification."
      : "",
    "Use the structured Djinn tools for questions, work items, reports, artifacts and test actions. Publish unresolved human questions with full context, options (or a free-text answer), recommendation and the affected work. Never hide a question in prose or a report; nonblocking questions allow independent work to continue. Define a work item for each independently testable ticket or slice of a large task, with a stable id and its owner. Update work items at meaningful status changes only. After human test feedback, route the correction to the owning worker and update that work item to running when correction actually begins; preserve independent sibling recipes and outcomes. Publish an independent test action as soon as a ticket is testable, with its worktree/package directory, recipe and expected result; do not wait for sibling tickets or mission completion. Native readiness must verify the actual environment. Keep reports in the existing stage results; no additional living status document is required. Before ending publish a step report with status ready, blocked or needs_input, summary, completed, remaining, evidence, useful next action and evidence for each configured exit criterion. Workers publish their contribution report for integration by the lead. Never report ready with missing generation, failed required tests or an unavailable required test environment.",
    "Human recipe and delivery approvals remain human decisions. Commits, pushes, publication, deployment and external messages require explicit human authorization. Use the native Djinn event protocol supplied by the runtime.",
  ]
    .filter(Boolean)
    .join("\n\n");
}
