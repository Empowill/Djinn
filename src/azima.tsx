// An azima of the plan in the Tasks tab: a task no worker runs, with the work part of it under it. Its heading says
// where it stands (open, in progress, awaiting its proof, done: the lamp's, Task.azima), what it waits for (its after
// codes), and its progress. Opened, what its proof needs, box by box, then its parts, as the Tasks tab shows any task:
// under work that still moves, the finished parts fold behind "show the N finished" (src/task-tabs.tsx).
// An azima never waits for the person as work: its proof is no task.
import { BadgeCheck, ChevronDown } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";

import {
  AzimaState,
  type ProofNeed,
  Prover,
  type Task,
  TaskStatus,
} from "../gen/ts/plan/v1/plan_pb";
import { compareCodes, finishedTask } from "./data/flight";
import type { Tone } from "./data/format";
import { t, type TextKey } from "./i18n";
import { StatusBadge, StatusIndicator } from "./status";

// azimaTone and azimaLabel say where an azima stands: done in green, under way in blue while a worker runs on one of
// its parts, its work done and its proof awaited in olive, planned grey otherwise. Never orange: an azima waits for no
// one's answer.
function azimaState(azima: Task): AzimaState {
  if (azima.status === TaskStatus.DONE) return AzimaState.DONE;
  return azima.azima?.state ?? AzimaState.OPEN;
}

// azimaFinished tells a done azima: the Tasks tab folds it.
export function azimaFinished(azima: Task): boolean {
  return azimaState(azima) === AzimaState.DONE;
}

function azimaTone(azima: Task): Tone {
  switch (azimaState(azima)) {
    case AzimaState.DONE:
      return "done";
    case AzimaState.IN_PROGRESS:
      return azima.azima?.partsRunning ? "running" : "planned";
    case AzimaState.AWAITING_PROOF:
      return "proof";
  }
  return "planned";
}

const labels: Record<AzimaState, TextKey> = {
  [AzimaState.UNSPECIFIED]: "azima.state_open",
  [AzimaState.OPEN]: "azima.state_open",
  [AzimaState.IN_PROGRESS]: "azima.state_in_progress",
  [AzimaState.DONE]: "azima.state_done",
  [AzimaState.AWAITING_PROOF]: "azima.state_awaiting_proof",
};

const provers: Record<Prover, TextKey | null> = {
  [Prover.UNSPECIFIED]: null,
  [Prover.MAC]: "prover.mac",
  [Prover.WINDOWS]: "prover.windows",
  [Prover.REVIEW]: "prover.review",
  [Prover.RELEASE]: "prover.release",
  [Prover.REAL_MODEL]: "prover.real_model",
  [Prover.PERSON]: "prover.person",
  [Prover.OTHER]: null,
};

// proverWord names who or what gives a proof: "a Mac", "Clément's review"; the needs' own words for none of them.
function proverWord(prover: Prover, need: ProofNeed): string {
  if (prover === Prover.REVIEW && need.reviewer)
    return t("prover.review_by", { name: need.reviewer });
  const key = provers[prover];
  return key ? t(key) : need.needs;
}

// proofWords says who or what gives an azima's proofs, each once, in the lamp's order (internal/plan.NeedsWords).
export function proofWords(needs: readonly ProofNeed[]): string {
  const words: [Prover, string][] = [];
  for (const need of needs)
    for (const prover of need.provers) {
      const word = proverWord(prover, need);
      if (!words.some(([p, w]) => p === prover && w === word))
        words.push([prover, word]);
    }
  return words
    .sort(([a], [b]) => a - b)
    .map(([, word]) => word)
    .join(", ");
}

export function AzimaCard({
  azima,
  parts,
  tasks,
  origin,
  render,
  focus = "",
  compact = false,
  workspace = false,
  onValidate,
}: {
  azima: Task;
  // The work part of it, in the order to show.
  parts: readonly Task[];
  // The tasks of its wish, by id: what it waits for.
  tasks: ReadonlyMap<string, Task>;
  // Where it comes from, in the flight plan of several wishes: its wish.
  origin?: ReactNode;
  render: (task: Task) => ReactNode;
  // What a link brings into sight: the azima, or one of its parts, opens on it.
  focus?: string;
  // The wish workspace uses a dense icon-only state column; other azima lists keep the full badge.
  compact?: boolean;
  // Workspace azimas start folded; an explicit focus/deep link still opens its matching azima.
  workspace?: boolean;
  // Validates the azima once its work is done: it is marked done, by you.
  onValidate?: () => void;
}) {
  const state = azimaState(azima);
  const holds = focus === azima.id || parts.some((x) => x.id === focus);
  const [open, setOpen] = useState(
    holds ||
      (!workspace &&
        state !== AzimaState.DONE &&
        parts.some((x) => !finishedTask(x))),
  );
  useEffect(() => {
    if (holds) setOpen(true);
  }, [holds]);
  const moving = parts.filter((x) => !finishedTask(x));
  const finished = parts.filter((x) => finishedTask(x));
  const deps = azima.dependsOn
    .map((id) => tasks.get(id))
    .filter((x): x is Task => !!x);
  const after = deps.map((x) => x.code).sort(compareCodes);
  const waits = deps
    .filter((x) => x.status !== TaskStatus.DONE)
    .map((x) => x.code)
    .sort(compareCodes);
  const done = parts.filter((x) => x.status === TaskStatus.DONE).length;
  const proof = state === AzimaState.AWAITING_PROOF;
  const statusLabel = t(labels[state]);
  // On hover, what validating it takes: each box left and what it needs, else that every task is finished.
  const needs = proof
    ? [
        t("azima.to_validate_title"),
        ...(azima.proofNeeds.length
          ? azima.proofNeeds.map(
              (n) => `• ${n.box} ${t("azima.needs", { needs: n.needs })}`,
            )
          : [t("azima.no_boxes")]),
        t("azima.how_to_validate"),
      ].join("\n")
    : undefined;
  const toggleOpen = () => setOpen((value) => !value);
  return (
    <section
      className={`azima-card tone-${azimaTone(azima)} ${compact ? "compact" : ""} ${open ? "open" : ""}`}
      id={`task-${azima.id}`}
      aria-label={`${azima.code} ${azima.title}`}
    >
      <div className="azima-heading-row">
        <button
          className="azima-heading"
          onClick={toggleOpen}
          aria-expanded={open}
        >
          {compact ? (
            <StatusIndicator
              tone={azimaTone(azima)}
              label={statusLabel}
              title={needs ?? statusLabel}
            />
          ) : (
            <StatusBadge
              tone={azimaTone(azima)}
              label={statusLabel}
              title={needs}
            />
          )}
          <span className="agent-code">{azima.code}</span>
          <strong title={azima.title}>{azima.title}</strong>
          <span className="azima-meta">
            <span className="azima-secondary-meta">
              {origin}
              {proof && (
                <span className="azima-needs" title={needs}>
                  {proofWords(azima.proofNeeds)}
                </span>
              )}
              {state !== AzimaState.DONE && waits.length > 0 && (
                <span
                  className="azima-waits"
                  title={t("page.after", { tasks: after.join(", ") })}
                >
                  {t("azima.waits", { tasks: waits.join(", ") })}
                </span>
              )}
              {state !== AzimaState.DONE &&
                waits.length === 0 &&
                after.length > 0 && (
                  <span
                    className="azima-after"
                    title={t("page.after", { tasks: after.join(", ") })}
                  >
                    {t("page.after", { tasks: after.join(", ") })}
                  </span>
                )}
            </span>
          </span>
        </button>
        {proof && onValidate && (
          <button
            type="button"
            className="button azima-validate"
            onClick={onValidate}
            title={needs}
          >
            <BadgeCheck size={12} aria-hidden="true" />
            {t("azima.validate")}
          </button>
        )}
        <button
          type="button"
          className="azima-heading-tail"
          onClick={toggleOpen}
          aria-expanded={open}
          aria-label={`${azima.code} ${azima.title}`}
        >
          {parts.length > 0 && (
            <span
              className="azima-progress"
              title={t("azima.progress", { done, count: parts.length })}
            >
              <span className="azima-bar" aria-hidden="true">
                <span style={{ width: `${(100 * done) / parts.length}%` }} />
              </span>
              {done}/{parts.length}
            </span>
          )}
          <ChevronDown size={14} className={open ? "rotated" : ""} />
        </button>
      </div>
      {open && (
        <div className="azima-parts">
          {parts.length === 0 && (
            <p className="muted-text">{t("azima.no_parts")}</p>
          )}
          {/* Every part is visible inside the one azima accordion; only done azimas fold globally. */}
          {moving.map(render)}
          {finished.map(render)}
        </div>
      )}
    </section>
  );
}
