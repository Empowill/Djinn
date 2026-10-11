// An azima of the plan in the Tasks tab: a task no worker runs, with the work part of it under it. Its heading, one line,
// says where it stands (open, in progress, awaiting its proof, done: the lamp's, Task.azima), what it still waits for,
// and its progress; the rest is on hover. Opened, its description and what its proof needs, box by box, on the left,
// its parts on the right, as the Tasks tab shows any task: under work that still moves, the finished parts fold behind
// "show the N finished" (src/task-tabs.tsx).
// An azima never waits for the person as work: its proof is no task.
import { BadgeCheck, ChevronDown, ExternalLink } from "lucide-react";
import {
  type FormEvent,
  type ReactNode,
  type RefObject,
  memo,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import {
  AzimaPrState,
  AzimaState,
  type ProofNeed,
  Prover,
  type Task,
  TaskStatus,
  type Wish,
} from "../gen/ts/plan/v1/plan_pb";
import {
  compareCodes,
  isAzima,
  type MergeNest,
  mergeNest,
  settledTask,
} from "./data/flight";
import {
  azimaBranchName,
  azimaTime,
  syncDescription,
  type Tone,
  useNow,
} from "./data/format";
import { ModalFrame } from "./frame";
import { t, type TextKey } from "./i18n";
import { MarkdownBody } from "./markdown-body";
import { StatusBadge } from "./status";
import { Fold } from "./task-tabs";

// azimaTone and azimaLabel say where an azima stands: done in green, under way in blue while a worker runs on one of
// its parts, its work done and its proof awaited in olive, planned grey otherwise. Never orange: an azima waits for no
// one's answer.
function azimaState(azima: Task, _parts?: readonly Task[]): AzimaState {
  if (azima.draft || azima.azima?.state === AzimaState.DRAFT)
    return AzimaState.DRAFT;
  return azima.azima?.state ?? AzimaState.OPEN;
}

// azimaFinished tells a done azima: the Tasks tab folds it.
export function azimaFinished(azima: Task, _parts?: readonly Task[]): boolean {
  return azimaState(azima, _parts) === AzimaState.DONE;
}

function azimaTone(azima: Task, _parts?: readonly Task[]): Tone {
  const state = azimaState(azima, _parts);
  switch (state) {
    case AzimaState.DONE:
      return "done";
    case AzimaState.IN_PROGRESS: {
      const running = (azima.azima?.partsRunning ?? 0) > 0;
      return running ? "running" : "planned";
    }
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

// useStickyRoom tells what stays in sight in a page that scrolls, an opened azima's left column, where it can stay:
// below the bar of what waits for you, which stays at the top of the page (--attention-height), and within the page's
// height (--scroll-height). bar says whether that bar shows.
export function useStickyRoom(
  scrollRef: RefObject<HTMLElement | null>,
  bar: boolean,
) {
  useLayoutEffect(() => {
    const scroll = scrollRef.current;
    if (!scroll) return;
    const attention = scroll.querySelector<HTMLElement>(
      ":scope > .attention-bar",
    );
    const measure = () => {
      scroll.style.setProperty(
        "--attention-height",
        `${attention?.offsetHeight ?? 0}px`,
      );
      scroll.style.setProperty("--scroll-height", `${scroll.clientHeight}px`);
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(scroll);
    if (attention) observer.observe(attention);
    return () => observer.disconnect();
  }, [scrollRef, bar]);
}

const noNest = mergeNest([]);

export const AzimaCard = memo(function AzimaCard({
  azima,
  parts,
  tasks,
  origin,
  render,
  focus = "",
  isPerAzima = false,
  onPush,
  onOpenPr,
  onValidate,
  nest = noNest,
}: {
  azima: Task;
  // The work part of it, in the order to show: merges nested under the task they serve left out.
  parts: readonly Task[];
  // The tasks of its wish, by id: what it waits for.
  tasks: ReadonlyMap<string, Task>;
  // Where it comes from, in the flight plan of several wishes: its wish.
  origin?: ReactNode;
  render: (task: Task) => ReactNode;
  // What a link brings into sight: the azima, or one of its parts, opens on it.
  focus?: string;
  // Whether the wish integrates per azima.
  isPerAzima?: boolean;
  // Pushes the azima's integration branch.
  onPush?: (azima: Task) => Promise<unknown> | void;
  // Opens the pull request in the external browser.
  onOpenPr?: (url: string) => void;
  // Validates the azima once its work is done: it is marked done, by you.
  onValidate?: (azima: Task) => void;
  // Where the merges of its wish show: a part whose merge still runs moves with it.
  nest?: MergeNest;
}) {
  const [pushing, setPushing] = useState(false);
  const state = azimaState(azima, parts);
  // The part in sight, or the one a merge in sight sits under.
  const inSight = nest.host.get(focus) ?? focus;
  const holds = focus === azima.id || parts.some((x) => x.id === inSight);
  const settled = (x: Task) => settledTask(x, nest);
  const [open, setOpen] = useState(
    holds || (state !== AzimaState.DONE && parts.some((x) => !settled(x))),
  );
  useEffect(() => {
    if (holds) setOpen(true);
  }, [holds]);
  const moving = parts.filter((x) => !settled(x));
  const finished = parts.filter((x) => settled(x));
  const prevMoving = useRef(moving.length);
  useEffect(() => {
    if (prevMoving.current === 0 && moving.length > 0) {
      setOpen(true);
    }
    prevMoving.current = moving.length;
  }, [moving.length]);
  const deps = (azima.dependsOn ?? [])
    .map((id) => tasks.get(id))
    .filter((x): x is Task => !!x);
  const after = deps.map((x) => x.code).sort(compareCodes);
  const waits = deps
    .filter((x) =>
      isAzima(x)
        ? x.azima?.state !== AzimaState.DONE
        : // Djinn counts a cancelled dependency as met, as a done one.
          x.status !== TaskStatus.DONE && x.status !== TaskStatus.CANCELLED,
    )
    .map((x) => x.code)
    .sort(compareCodes);
  // A cancelled part is no work left: listed among the finished, out of the total and of what waits.
  const total =
    azima.azima?.parts ??
    parts.filter((x) => x.status !== TaskStatus.CANCELLED || isAzima(x)).length;
  const done =
    azima.azima?.partsDone ??
    parts.filter((x) =>
      isAzima(x) ? azimaFinished(x) : x.status === TaskStatus.DONE,
    ).length;
  const running =
    azima.azima?.partsRunning ??
    parts.filter(
      (x) =>
        x.status === TaskStatus.RUNNING || x.status === TaskStatus.RESUMING,
    ).length;
  const waiting = Math.max(0, total - done - running);
  const isRunning =
    state === AzimaState.IN_PROGRESS ||
    (azima.azima?.partsRunning ?? 0) > 0 ||
    running > 0;
  const now = useNow(isRunning);
  const time = azimaTime(azima, parts, now);
  const proof = state === AzimaState.AWAITING_PROOF;
  const side = Boolean(azima.description) || proof;
  // On hover, what validating it takes: each box left and what it needs, else that every task is finished.
  const proofNeeds = azima.proofNeeds ?? [];
  const needs = proof
    ? [
        t("azima.to_validate_title"),
        ...(proofNeeds.length
          ? proofNeeds.map(
              (n) => `• ${n.box} ${t("azima.needs", { needs: n.needs })}`,
            )
          : [t("azima.no_boxes")]),
        t("azima.how_to_validate"),
      ].join("\n")
    : undefined;

  const branch = azima.azima?.branch || azimaBranchName(azima);
  const baseBranch = azima.azima?.baseBranch;
  const sync = azima.azima?.sync;
  const isOutOfSync = Boolean(sync && (sync.ahead > 0 || sync.behind > 0));
  const isPushed = Boolean(
    sync &&
    sync.ahead === 0 &&
    sync.behind === 0 &&
    (sync.target || sync.remote || sync.branch),
  );
  const syncText = syncDescription(sync);
  const canPush = isOutOfSync && Boolean(onPush);

  const pr = azima.azima?.pr;
  const prUrl = pr?.url;
  const prText = pr
    ? pr.state === AzimaPrState.PROPOSED
      ? t("azima.pr_proposed")
      : pr.state === AzimaPrState.MERGED
        ? t("azima.pr_merged")
        : t("azima.pr_open")
    : null;

  const handlePush = async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!onPush || pushing) return;
    setPushing(true);
    try {
      await onPush(azima);
    } catch {
      // Toast surfaced by caller
    } finally {
      setPushing(false);
    }
  };

  const hasActions = isOutOfSync || canPush || isPushed || Boolean(prText);
  // The heading reads at a glance: its state, code, title, what it waits for, its bar. The rest is on hover: its
  // branches and what comes before it on its title; its parts, counted by state, and its time on its bar; what its
  // proof needs on its badge, and in the card opened.
  const facts = [
    azima.title,
    isPerAzima && branch,
    isPerAzima && baseBranch && t("azima.lands_in", { branch: baseBranch }),
    after.length > 0 && t("page.after", { tasks: after.join(", ") }),
  ]
    .filter(Boolean)
    .join("\n");
  const progress = [
    t("azima.progress", { done, count: total }),
    t("azima.progress_parts", { done, running, waiting }),
    time.text && [time.text, time.title].filter(Boolean).join(" · "),
  ]
    .filter(Boolean)
    .join("\n");

  return (
    <section
      className={`azima-card tone-${azimaTone(azima, parts)} ${open ? "open" : ""}`}
      id={`task-${azima.id}`}
      aria-label={`${azima.code} ${azima.title}`}
    >
      <div className="azima-row">
        <button
          type="button"
          className="azima-heading"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
        >
          <StatusBadge
            tone={azimaTone(azima, parts)}
            label={t(labels[state])}
            title={needs}
          />
          <span className="agent-code">{azima.code}</span>
          <strong title={facts}>{azima.title}</strong>
          <span className="azima-meta">
            {origin}
            {state !== AzimaState.DONE && waits.length > 0 && (
              <span
                className="azima-waits"
                title={t("page.after", { tasks: after.join(", ") })}
              >
                {t("azima.waits", { tasks: waits.join(", ") })}
              </span>
            )}
            {total > 0 && (
              <span className="azima-progress" title={progress}>
                <span className="azima-bar" aria-hidden="true">
                  <span
                    className="azima-bar-done"
                    style={{ width: `${(100 * done) / total}%` }}
                  />
                  <span
                    className="azima-bar-running"
                    style={{ width: `${(100 * running) / total}%` }}
                  />
                </span>
                <span className="azima-progress-counts">
                  {done}/{total}
                </span>
              </span>
            )}
          </span>
          <ChevronDown size={14} className={open ? "rotated" : ""} />
        </button>
        {isPerAzima && hasActions && (
          <div className="azima-actions">
            {isOutOfSync && syncText && (
              <span className="azima-sync-status" title={syncText}>
                {syncText}
              </span>
            )}
            {canPush && (
              <button
                type="button"
                className="button accent small"
                disabled={pushing}
                onClick={handlePush}
              >
                {pushing ? t("project.pushing") : t("project.push")}
              </button>
            )}
            {isPushed && !isOutOfSync && (
              <span className="azima-pushed-status">{t("azima.pushed")}</span>
            )}
            {prUrl ? (
              <a
                href={prUrl}
                className="azima-pr-link"
                target="_blank"
                rel="noopener noreferrer"
                onClick={(e) => {
                  e.stopPropagation();
                  if (onOpenPr) {
                    e.preventDefault();
                    onOpenPr(prUrl);
                  }
                }}
              >
                <ExternalLink size={12} />
                <span>{prText}</span>
              </a>
            ) : prText ? (
              <span className="azima-pr-badge">{prText}</span>
            ) : null}
          </div>
        )}
      </div>
      {/* Opened: its description, then what its proof needs, on the left, in sight while its parts scroll on the
          right, one per row; on a narrow card, one above the other. With neither, its parts take the width. */}
      {open && (
        <div className={`azima-body${side ? " with-side" : ""}`}>
          {side && (
            <div className="azima-side">
              {azima.description && (
                <div className="azima-description">
                  <MarkdownBody text={azima.description} />
                </div>
              )}
              {proof && (
                <div className="azima-proof">
                  <p>{t("azima.to_validate_title")}</p>
                  {proofNeeds.length ? (
                    <ul>
                      {proofNeeds.map((need, i) => (
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
            </div>
          )}
          <div className="azima-parts">
            {parts.length === 0 && (
              <p className="muted-text">{t("azima.no_parts")}</p>
            )}
            {/* Every part finished, opening the azima was the click: they show. */}
            {moving.length === 0 ? (
              <div className="azima-part-list">{finished.map(render)}</div>
            ) : (
              <>
                <div className="azima-part-list">{moving.map(render)}</div>
                <Fold
                  id={`parts:${azima.id}`}
                  count={finished.length}
                  open={finished.some((x) => x.id === inSight)}
                >
                  <div className="azima-part-list">{finished.map(render)}</div>
                </Fold>
              </>
            )}
          </div>
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
    .filter(
      (x) => x.status !== TaskStatus.DONE && x.status !== TaskStatus.CANCELLED,
    )
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
        <strong
          title={[
            azima.title,
            after.length > 0 && t("page.after", { tasks: after.join(", ") }),
          ]
            .filter(Boolean)
            .join("\n")}
        >
          {azima.title}
        </strong>
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
        </span>
        <ChevronDown size={14} className={open ? "rotated" : ""} />
      </button>
      {open && (
        <div className="azima-parts draft-azima-body">
          <div className="draft-azima-content">
            {azima.description ? (
              <div className="draft-azima-description">
                <MarkdownBody text={azima.description} />
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
