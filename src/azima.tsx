// An azima of the plan in the Tasks tab: a task no worker runs, with the work part of it under it. Its heading says
// where it stands (open, in progress, awaiting its proof, done: the lamp's, Task.azima), what it waits for (its after
// codes), and its progress. Opened, what its proof needs, box by box, then its parts, as the Tasks tab shows any task:
// under work that still moves, the finished parts fold behind "show the N finished" (src/task-tabs.tsx).
// An azima never waits for the person as work: its proof is no task.
import { BadgeCheck, ChevronDown } from "lucide-react";
import {
  type FormEvent,
  type ReactNode,
  memo,
  useEffect,
  useState,
} from "react";

import {
  AzimaState,
  type ProofNeed,
  Prover,
  type Task,
  TaskStatus,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import { compareCodes, finishedTask, isAzima } from "./data/flight";
import { azimaTime, type Tone, useNow } from "./data/format";
import { ModalFrame } from "./frame";
import { t, type TextKey } from "./i18n";
import { StatusBadge } from "./status";
import { Fold } from "./task-tabs";

// azimaTone and azimaLabel say where an azima stands: done in green, under way in blue while a worker runs on one of
// its parts, its work done and its proof awaited in olive, planned grey otherwise. Never orange: an azima waits for no
// one's answer.
function azimaState(azima: Task): AzimaState {
  if (azima.draft || azima.azima?.state === AzimaState.DRAFT)
    return AzimaState.DRAFT;
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
    case AzimaState.DRAFT:
      return "later";
  }
  return "planned";
}

const labels: Record<AzimaState, TextKey> = {
  [AzimaState.UNSPECIFIED]: "azima.state_open",
  [AzimaState.OPEN]: "azima.state_open",
  [AzimaState.IN_PROGRESS]: "azima.state_in_progress",
  [AzimaState.DONE]: "azima.state_done",
  [AzimaState.AWAITING_PROOF]: "azima.state_awaiting_proof",
  [AzimaState.DRAFT]: "azima.state_draft",
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

export const AzimaCard = memo(function AzimaCard({
  azima,
  parts,
  tasks,
  origin,
  render,
  focus = "",
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
  // Validates the azima once its work is done: it is marked done, by you.
  onValidate?: (azima: Task) => void;
}) {
  const state = azimaState(azima);
  const holds = focus === azima.id || parts.some((x) => x.id === focus);
  const [open, setOpen] = useState(
    holds || (state !== AzimaState.DONE && parts.some((x) => !finishedTask(x))),
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
    .filter((x) =>
      isAzima(x)
        ? x.azima?.state !== AzimaState.DONE
        : x.status !== TaskStatus.DONE,
    )
    .map((x) => x.code)
    .sort(compareCodes);
  const done = parts.filter((x) =>
    isAzima(x)
      ? x.azima?.state === AzimaState.DONE
      : x.status === TaskStatus.DONE,
  ).length;
  const running = parts.filter(
    (x) => x.status === TaskStatus.RUNNING || x.status === TaskStatus.RESUMING,
  ).length;
  const waiting = Math.max(0, parts.length - done - running);
  const isRunning =
    state === AzimaState.IN_PROGRESS ||
    (azima.azima?.partsRunning ?? 0) > 0 ||
    running > 0;
  const now = useNow(isRunning);
  const time = azimaTime(azima, parts, now);
  const proof = state === AzimaState.AWAITING_PROOF;
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
  return (
    <section
      className={`azima-card tone-${azimaTone(azima)} ${open ? "open" : ""}`}
      id={`task-${azima.id}`}
      aria-label={`${azima.code} ${azima.title}`}
    >
      <button
        className="azima-heading"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        <StatusBadge
          tone={azimaTone(azima)}
          label={t(labels[state])}
          title={needs}
        />
        <span className="agent-code">{azima.code}</span>
        <strong title={azima.title}>{azima.title}</strong>
        <span className="azima-meta">
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
              <span className="azima-after">
                {t("page.after", { tasks: after.join(", ") })}
              </span>
            )}
          {parts.length > 0 && (
            <span
              className="azima-progress"
              title={t("azima.progress", { done, count: parts.length })}
            >
              <span className="azima-bar" aria-hidden="true">
                <span
                  className="azima-bar-done"
                  style={{ width: `${(100 * done) / parts.length}%` }}
                />
                <span
                  className="azima-bar-running"
                  style={{ width: `${(100 * running) / parts.length}%` }}
                />
              </span>
              <span className="azima-progress-counts">
                {done}/{parts.length}
              </span>
            </span>
          )}
          {parts.length > 0 && (
            <span
              className="azima-progress-parts"
              title={t("azima.progress_parts", { done, running, waiting })}
            >
              {t("azima.progress_parts", { done, running, waiting })}
            </span>
          )}
          {time.text && (
            <span className="task-time" title={time.title}>
              {time.text}
            </span>
          )}
        </span>
        <ChevronDown size={14} className={open ? "rotated" : ""} />
      </button>
      {open && (
        <div className="azima-parts">
          {proof && (
            <div className="azima-proof">
              <p>{t("azima.to_validate_title")}</p>
              {azima.proofNeeds.length ? (
                <ul>
                  {azima.proofNeeds.map((need, i) => (
                    <li key={i}>
                      {need.box}{" "}
                      <span className="muted-text">
                        {t("azima.needs", { needs: need.needs })}
                      </span>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="muted-text">{t("azima.no_boxes")}</p>
              )}
              <p className="muted-text">{t("azima.how_to_validate")}</p>
              {onValidate && (
                <button
                  className="button accent small"
                  onClick={() => onValidate(azima)}
                >
                  <BadgeCheck size={14} />
                  {t("azima.validate")}
                </button>
              )}
            </div>
          )}
          {parts.length === 0 && (
            <p className="muted-text">{t("azima.no_parts")}</p>
          )}
          {/* Every part finished, opening the azima was the click: they show. */}
          {moving.length === 0 ? (
            <div className="card-grid task-grid">{finished.map(render)}</div>
          ) : (
            <>
              <div className="card-grid task-grid">{moving.map(render)}</div>
              <Fold
                id={`parts:${azima.id}`}
                count={finished.length}
                open={finished.some((x) => x.id === focus)}
              >
                <div className="card-grid task-grid">
                  {finished.map(render)}
                </div>
              </Fold>
            </>
          )}
        </div>
      )}
    </section>
  );
});

export function DraftAzimaCard({
  azima,
  tasks,
  origin,
  focus = "",
  onOpen,
  onMove,
}: {
  azima: Task;
  tasks?: ReadonlyMap<string, Task>;
  origin?: ReactNode;
  focus?: string;
  onOpen?: () => void;
  onMove?: () => void;
}) {
  const holds = focus === azima.id;
  const [open, setOpen] = useState(holds);
  useEffect(() => {
    if (holds) setOpen(true);
  }, [holds]);
  const deps = (azima.dependsOn ?? [])
    .map((id) => tasks?.get(id))
    .filter((x): x is Task => !!x);
  const after = deps.map((x) => x.code).sort(compareCodes);
  const waits = deps
    .filter((x) => x.status !== TaskStatus.DONE)
    .map((x) => x.code)
    .sort(compareCodes);

  return (
    <section
      className={`azima-card tone-later ${open ? "open" : ""}`}
      id={`task-${azima.id}`}
      aria-label={`${azima.code} ${azima.title}`}
    >
      <button
        type="button"
        className="azima-heading"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        <StatusBadge tone="later" label={t("azima.state_draft")} />
        <span className="agent-code">{azima.code}</span>
        <strong title={azima.title}>{azima.title}</strong>
        <span className="azima-meta">
          {origin}
          {waits.length > 0 && (
            <span
              className="azima-waits"
              title={t("page.after", { tasks: after.join(", ") })}
            >
              {t("azima.waits", { tasks: waits.join(", ") })}
            </span>
          )}
          {waits.length === 0 && after.length > 0 && (
            <span className="azima-after">
              {t("page.after", { tasks: after.join(", ") })}
            </span>
          )}
        </span>
        <ChevronDown size={14} className={open ? "rotated" : ""} />
      </button>
      {open && (
        <div className="azima-parts draft-azima-body">
          <div className="draft-azima-content">
            {azima.description ? (
              <div className="draft-azima-description">
                <p className="draft-description-text">{azima.description}</p>
              </div>
            ) : (
              <p className="muted-text">{t("azima.no_description")}</p>
            )}
          </div>
          <div className="draft-azima-actions">
            {onOpen && (
              <button
                type="button"
                className="button accent small"
                onClick={onOpen}
              >
                {t("azima.open_button")}
              </button>
            )}
            {onMove && (
              <button
                type="button"
                className="button secondary small"
                onClick={onMove}
              >
                {t("azima.move_button")}
              </button>
            )}
          </div>
        </div>
      )}
    </section>
  );
}

export function MoveAzimaDialog({
  azima,
  wishes,
  currentWishId,
  onClose,
  onMove,
}: {
  azima: Task;
  wishes: readonly Wish[];
  currentWishId: string;
  onClose: () => void;
  onMove: (destWishId: string) => void;
}) {
  const otherWishes = wishes.filter((w) => w.id !== currentWishId);
  const [destWishId, setDestWishId] = useState(otherWishes[0]?.id ?? "");

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!destWishId) return;
    onMove(destWishId);
  };

  return (
    <ModalFrame
      eyebrow={azima.code}
      title={t("azima.move_dialog_title", { code: azima.code })}
      onClose={onClose}
    >
      <form onSubmit={submit} className="form-grid">
        <label>
          <span>{t("azima.move_select_wish")}</span>
          <select
            value={destWishId}
            onChange={(e) => setDestWishId(e.target.value)}
            disabled={otherWishes.length === 0}
          >
            {otherWishes.map((w) => (
              <option key={w.id} value={w.id}>
                {w.title}
              </option>
            ))}
          </select>
        </label>
        <div className="modal-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button
            type="submit"
            className="button accent"
            disabled={!destWishId}
          >
            {t("azima.move_confirm")}
          </button>
        </div>
      </form>
    </ModalFrame>
  );
}
