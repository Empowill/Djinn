// The page's copy of what djinn holds, read from the services and kept up to date by WishService.Watch. The watch
// says what changed, and brings the tasks, questions and blocks that did: the store puts those in place of its own,
// keeping the others as they were, and reads the rest again, a few reads at a time, and tells the screens. Nothing
// here is computed from the data: what the lamp computes (a wish's rank, whether it is ready) comes from the lamp.
import {
  create,
  type DescMessage,
  equals,
  type MessageShape,
} from "@bufbuild/protobuf";
import type { Gate, Machine } from "../../gen/ts/machine/v1/machine_pb";
import {
  type Block,
  BlockSchema,
  Change,
  type InboxItem,
  InboxItemSchema,
  type InboxSource,
  InboxSourceSchema,
  type Lead,
  type LeadExit,
  type LeadPrompt,
  LeadPromptSchema,
  LeadSchema,
  type LeadServiceGetResponse,
  LeadServiceGetResponseSchema,
  type Project,
  ProjectSchema,
  type Question,
  QuestionSchema,
  type Task,
  type TaskEvent,
  TaskEventKind,
  TaskSchema,
  type Wish,
  type WishChanges,
  WishChangesSchema,
  WishSchema,
  WishState,
} from "../../gen/ts/plan/v1/plan_pb";
import {
  type Tilasm,
  type TilasmComment,
  TilasmCommentSchema,
  type TilasmHold,
  TilasmHoldSchema,
  TilasmSchema,
  type TilasmSendRead,
  TilasmSendReadSchema,
  type TilasmSends,
  TilasmSendsSchema,
} from "../../gen/ts/plan/v1/tilasm_pb";
import { type Clients, message, notFound, unimplemented } from "./client";

export type ReadState = "reading" | "slow" | "ready" | "failed";

export type ReadKind =
  | Change
  | "wishes"
  | "projects"
  | "tasks"
  | "questions"
  | "blocks"
  | "tilasms"
  | "inbox"
  | "machine"
  | "commands"
  | "review"
  | "project"
  | "terminal";

// What the page shows of one wish, read while a screen shows it.
export interface WishDetail {
  tasks: Task[];
  questions: Question[];
  blocks: Block[];
  // Its tilasms, by code.
  tilasms: Tilasm[];
  // The developer's draft comments on its tilasms, read with them: the window's alone.
  drafts: TilasmComment[];
  // Why some of its tilasms take no send for now, read with the drafts: computed by the server.
  holds: TilasmHold[];
  // The comments sent on its tilasms, with the Djinn's replies, read with the drafts.
  sent: TilasmComment[];
  // The sends on its tilasms the Djinn read, read with the drafts: computed by the server.
  reads: TilasmSendRead[];
  // What an iterate or launch send does for each tilasm linked to an open question, read with the drafts: computed by the server.
  sends: TilasmSends[];
  // Read at least once.
  loaded: boolean;
  // Totals from the server for paginated lists.
  tasksTotal?: number;
  questionsTotal?: number;
  blocksTotal?: number;
  // Next page tokens from the server.
  tasksNextToken?: string;
  questionsNextToken?: string;
  blocksNextToken?: string;
  // The kinds of entities loaded so far for this wish.
  loadedKinds: Set<Change>;
}

export interface State {
  // The watch stream answers: what shows follows djinn as it changes.
  live: boolean;
  // The watch stream answered at least once since start.
  everLive?: boolean;
  // The wishes and the projects were read at least once.
  loaded: boolean;
  // Why the last read failed; empty when it did not.
  error: string;
  projects: Project[];
  lead?: Lead;
  // The choice the lead's agent shows in its terminal, waiting for the developer: an approval, a folder to trust.
  leadPrompt?: LeadPrompt;
  // How the lead's program ended, until Lead starts it again; unset while it runs.
  leadExit?: LeadExit;
  // Why the last start of the lead failed, until one succeeds; empty when none did.
  leadStartError?: string;
  // While Djinn drives the lead: where it runs (seat), the accounts of this machine, and the usage limit it hit, if
  // any (limit); unset when the lead runs in its terminal.
  leadDrive?: Pick<LeadServiceGetResponse, "seat" | "accounts" | "limit">;
  // As WishService.List gives them: the active ones by rank, then the paused ones, then the granted ones.
  wishes: Wish[];
  details: Readonly<Record<string, WishDetail>>;
  // The inbox items that wait for an answer, the newest first.
  inbox: InboxItem[];
  // The sources the projects' skills declare, plugged in or not on this machine.
  sources: InboxSource[];
  // The events of the tasks followed, oldest first.
  events: Readonly<Record<string, TaskEvent[]>>;
  // The first prompt of each task read alone or followed, by task id.
  prompts: Readonly<Record<string, string>>;
  machine?: Machine;
  gates: Gate[];
}

export interface Store {
  readonly clients: Clients;
  getState(): State;
  subscribe(listener: () => void): () => void;
  // Follows djinn until the returned function is called: reads everything, then what changes.
  start(): () => void;
  // Reads a wish's tasks, questions, blocks and tilasms, or the given kinds, and again when they change, until the returned function is called.
  open(wishId: string, kinds?: readonly Change[]): () => void;
  // Loads kinds for a wish that are not yet loaded.
  loadKinds(wishId: string, kinds: readonly Change[]): Promise<void>;
  // Loads the next page for a kind; of that many items at most, the server's default when none.
  loadMore(wishId: string, kind: Change, pageSize?: number): Promise<void>;
  // Follows a task's events until the returned function is called, or the task ends. Once no screen follows it, its
  // events are dropped after a while: a task card closed frees them.
  follow(taskId: string): () => void;
  // Reads a task's prompt alone, without loading its other events. It never fails: a read that fails gives "" and is
  // made again after retry, its prompt in the store's prompts once it answers.
  readPrompt(taskId: string): Promise<string>;
  // Reads again what changed, as a write the page made says it: the watch says it too, a moment later.
  changed(wishId: string, changes: Change[]): Promise<void>;
  // Puts a question a write returned in place of the one shown, at once: the screens show what djinn stored without
  // waiting for a read, which a slow call may hold.
  putQuestion(question: Question): void;
  // Reads the machine and its gates.
  readMachine(): Promise<void>;
  // How far a read is for a kind (and a wish when the kind is a wish's): reading, slow after 2 s, ready, or failed.
  readState(kind: ReadKind, wishId?: string): ReadState;
  // Reads again at once a kind whose read failed.
  retryRead(kind: ReadKind, wishId?: string): void;
  // Follows a screen's own read so readState and retryRead cover it too.
  trackRead<T>(
    kind: ReadKind,
    id: string | undefined,
    run: () => Promise<T>,
  ): Promise<T>;
}

// How long a read of the store waits for djinn's answer before it fails, and is made again.
const READ_TIMEOUT = 30_000;

// The most events kept for a task: the oldest go first.
const MAX_EVENTS = 2000;
const EMPTY_DETAIL: WishDetail = {
  tasks: [],
  questions: [],
  blocks: [],
  tilasms: [],
  drafts: [],
  holds: [],
  sent: [],
  reads: [],
  sends: [],
  loaded: false,
  loadedKinds: new Set<Change>(),
};

export const emptyDetail = EMPTY_DETAIL;

// What a wish shows that a watch message may bring whole.
const CARRIED = new Set([Change.TASK, Change.QUESTION, Change.BLOCK]);
// What a wish shows, read again for it all.
const DETAIL = [Change.TASK, Change.QUESTION, Change.BLOCK, Change.TILASM];

const keyOf = (m: unknown) =>
  m && typeof m === "object"
    ? "id" in m
      ? String((m as { id: unknown }).id)
      : "name" in m
        ? String((m as { name: unknown }).name)
        : ""
    : "";

// reconcile compares incoming entities against prev, reusing previous objects if equal according to the schema.
// If all elements in order match prev, prev itself is returned so the array reference remains stable.
function reconcile<Desc extends DescMessage>(
  schema: Desc,
  prev: readonly MessageShape<Desc>[],
  incoming: readonly MessageShape<Desc>[],
): MessageShape<Desc>[] {
  if (!prev.length && !incoming.length) return prev as MessageShape<Desc>[];
  const oldByKey = new Map(prev.map((m) => [keyOf(m), m]));
  let identical = prev.length === incoming.length;
  const out: MessageShape<Desc>[] = [];
  for (let i = 0; i < incoming.length; i++) {
    const next = incoming[i];
    const key = keyOf(next);
    const old = key ? oldByKey.get(key) : undefined;
    if (old && equals(schema, old, next)) {
      out.push(old);
      if (prev[i] !== old) identical = false;
    } else {
      out.push(next);
      identical = false;
    }
  }
  return identical ? (prev as MessageShape<Desc>[]) : out;
}

// merge puts the entities that changed in place of theirs in list, and takes out those deleted. The others keep their
// place and their reference, and so does one that comes back equal: a screen that shows them draws them again only
// when they changed. A new one goes where order puts it, as the service lists them. Nothing changed: list itself.
// One that behind says was read before the one in list does not take its place.
function merge<Desc extends DescMessage>(
  schema: Desc,
  list: readonly MessageShape<Desc>[],
  changed: readonly MessageShape<Desc>[],
  deleted: ReadonlySet<string>,
  order: (a: MessageShape<Desc>, b: MessageShape<Desc>) => number,
  behind?: (old: MessageShape<Desc>, next: MessageShape<Desc>) => boolean,
): MessageShape<Desc>[] {
  const id = (m: MessageShape<Desc>) => (m as unknown as { id: string }).id;
  const incoming = new Map(changed.map((m) => [id(m), m]));
  let touched = false;
  const out: MessageShape<Desc>[] = [];
  const placed: MessageShape<Desc>[] = [];
  for (const old of list) {
    if (deleted.has(id(old))) {
      touched = true;
      continue;
    }
    const next = incoming.get(id(old));
    incoming.delete(id(old));
    if (!next || equals(schema, old, next) || behind?.(old, next))
      out.push(old);
    else if (order(old, next) === 0) {
      out.push(next);
      touched = true;
    } else {
      // Moved: placed again below.
      placed.push(next);
      touched = true;
    }
  }
  for (const m of [...placed, ...incoming.values()]) {
    const at = out.findIndex((other) => order(other, m) > 0);
    out.splice(at < 0 ? out.length : at, 0, m);
    touched = true;
  }
  return touched ? out : (list as MessageShape<Desc>[]);
}

// The orders the services list them in: tasks and questions by id, a UUIDv7, so the oldest first; blocks by position.
const byID = (a: { id: string }, b: { id: string }) =>
  a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
const byPosition = (a: Block, b: Block) =>
  a.position < b.position ? -1 : a.position > b.position ? 1 : byID(a, b);

// A question only moves on: each request to investigate and each revision adds a round, and an answer or a
// withdrawal closes it for good. A copy with fewer rounds, or open where the one shown is closed, was read before
// it: it never takes the card back, to "Being investigated" after its revision (incident Q274) or open after its
// answer (incident Q253). A read that left before a write, a page loaded meanwhile, a watch message that crossed a
// write's answer: each can bring such a copy.
const stage = (q: Question) =>
  (q.rounds?.length ?? 0) + (q.answer || q.withdrawal ? 1_000_000 : 0);
const questionBehind = (old: Question, next: Question) =>
  stage(next) < stage(old);

// questionsAhead is list, but for the questions shown further along in shown, which stay.
function questionsAhead(
  shown: readonly Question[],
  list: Question[],
): Question[] {
  const byId = new Map(shown.map((q) => [q.id, q]));
  let out = list;
  list.forEach((q, i) => {
    const old = byId.get(q.id);
    if (!old || old === q || !questionBehind(old, q)) return;
    if (out === list) out = list.slice();
    out[i] = old;
  });
  return out;
}

// adjustTotal updates total when incremental changes add or remove items.
function adjustTotal<T extends { id: string }>(
  total: number | undefined,
  oldList: readonly T[],
  newList: readonly T[],
  incoming: readonly T[],
  deleted: ReadonlySet<string>,
): number | undefined {
  if (total === undefined) return undefined;
  const oldIds = new Set(oldList.map((x) => x.id));
  let delta = 0;
  for (const m of incoming) {
    if (!oldIds.has(m.id)) delta++;
  }
  for (const old of oldList) {
    if (deleted.has(old.id)) delta--;
  }
  return Math.max(0, total + delta);
}

// apply puts what a watch message brought of a wish in place of what it shows, but for what was just read: a read
// made after the message is newer. Only kinds that are already loaded are updated; hidden tabs are untouched.
function apply(
  detail: WishDetail,
  changes: WishChanges,
  read: Partial<WishDetail>,
): WishDetail {
  const deleted = new Set(changes.deleted);
  const tasks = !detail.loadedKinds.has(Change.TASK)
    ? detail.tasks
    : read.tasks
      ? detail.tasks
      : merge(TaskSchema, detail.tasks, changes.tasks, deleted, byID);
  const questions = !detail.loadedKinds.has(Change.QUESTION)
    ? detail.questions
    : read.questions
      ? detail.questions
      : merge(
          QuestionSchema,
          detail.questions,
          changes.questions,
          deleted,
          byID,
          questionBehind,
        );
  const blocks = !detail.loadedKinds.has(Change.BLOCK)
    ? detail.blocks
    : read.blocks
      ? detail.blocks
      : merge(BlockSchema, detail.blocks, changes.blocks, deleted, byPosition);
  const tasksTotal = read.tasks
    ? detail.tasksTotal
    : adjustTotal(
        detail.tasksTotal,
        detail.tasks,
        tasks,
        changes.tasks,
        deleted,
      );
  const questionsTotal = read.questions
    ? detail.questionsTotal
    : adjustTotal(
        detail.questionsTotal,
        detail.questions,
        questions,
        changes.questions,
        deleted,
      );
  const blocksTotal = read.blocks
    ? detail.blocksTotal
    : adjustTotal(
        detail.blocksTotal,
        detail.blocks,
        blocks,
        changes.blocks,
        deleted,
      );
  if (
    tasks === detail.tasks &&
    questions === detail.questions &&
    blocks === detail.blocks &&
    tasksTotal === detail.tasksTotal &&
    questionsTotal === detail.questionsTotal &&
    blocksTotal === detail.blocksTotal
  )
    return detail;
  return {
    ...detail,
    tasks,
    questions,
    blocks,
    tasksTotal,
    questionsTotal,
    blocksTotal,
  };
}

export function createStore(
  clients: Clients,
  retry = 1000,
  throttle = Math.min(retry, 200),
  // How long the events of a task no screen shows stay kept.
  forget = 5_000,
  timeout = READ_TIMEOUT,
  slowMs = 2_000,
): Store {
  // A read that gets no answer fails after timeout, and is made again: one call that never ends would hold every
  // read after it, and the screens would show what they had, an answered question still open (incident Q253).
  const call = { timeoutMs: timeout };
  let state: State = {
    live: false,
    everLive: false,
    loaded: false,
    error: "",
    projects: [],
    wishes: [],
    details: {},
    inbox: [],
    sources: [],
    events: {},
    prompts: {},
    gates: [],
  };
  const listeners = new Set<() => void>();
  const set = (patch: Partial<State>) => {
    state = { ...state, ...patch };
    listeners.forEach((listener) => listener());
  };

  // What to read again at the next flush, and what the watch brought, to put in place after it, in order.
  const pending = {
    wishes: false,
    projects: false,
    inbox: false,
    details: new Map<string, Set<Change>>(),
    changes: new Map<string, WishChanges[]>(),
  };
  const opened = new Map<string, number>();
  // The kinds of each wish not shown that changed meanwhile: what it holds of them is read again when it shows.
  const stale = new Map<string, Set<Change>>();
  // Keys whose RPC is currently in flight so duplicate open()/loadKinds() calls at start do not issue a second read.
  const inFlight = new Set<string>();
  const inFlightPromises = new Map<string, Promise<void>>();
  // Requested kinds for each wish across open() and loadKinds() calls, used to set detail.loaded once all are read.
  const requestedKinds = new Map<string, Set<Change>>();
  // ReadState tracking per key ("wishes", "projects", "inbox", "machine", "${wishId}:${kind}", "${kind}:${id}").
  const loadedKeys = new Set<string>();
  const slowKeys = new Set<string>();
  const failedKeys = new Set<string>();
  const slowTimers = new Map<string, ReturnType<typeof setTimeout>>();
  const trackedRunners = new Map<string, () => void>();

  function normalizeKind(kind: ReadKind): ReadKind {
    switch (kind) {
      case "tasks":
        return Change.TASK;
      case "questions":
        return Change.QUESTION;
      case "blocks":
        return Change.BLOCK;
      case "tilasms":
        return Change.TILASM;
      default:
        return kind;
    }
  }

  function keyFor(rawKind: ReadKind, wishId?: string): string {
    const kind = normalizeKind(rawKind);
    if (kind === Change.WISH || kind === "wishes") return "wishes";
    if (kind === Change.PROJECT || kind === "projects") return "projects";
    if (kind === Change.INBOX || kind === "inbox") return "inbox";
    if (kind === "machine") return "machine";
    if (
      kind === Change.TASK ||
      kind === Change.QUESTION ||
      kind === Change.BLOCK ||
      kind === Change.TILASM
    ) {
      return wishId ? `${wishId}:${kind}` : String(kind);
    }
    return wishId ? `${kind}:${wishId}` : String(kind);
  }

  function clearSlowTimer(key: string) {
    const t = slowTimers.get(key);
    if (t !== undefined) {
      clearTimeout(t);
      slowTimers.delete(key);
    }
  }

  function ensureSlowTimer(key: string) {
    if (loadedKeys.has(key) || slowKeys.has(key) || slowTimers.has(key)) return;
    const t = setTimeout(() => {
      slowTimers.delete(key);
      if (stopped || loadedKeys.has(key) || failedKeys.has(key)) return;
      slowKeys.add(key);
      set({});
    }, slowMs);
    (t as unknown as { unref?: () => void }).unref?.();
    slowTimers.set(key, t);
  }

  function startReadKey(key: string) {
    inFlight.add(key);
    inFlightChanges.delete(key);
    if (!loadedKeys.has(key)) {
      ensureSlowTimer(key);
    }
  }

  function finishReadKey(key: string, ok: boolean, notify = false) {
    inFlight.delete(key);
    inFlightPromises.delete(key);
    inFlightChanges.delete(key);
    clearSlowTimer(key);
    if (ok) {
      const changed =
        !loadedKeys.has(key) || slowKeys.has(key) || failedKeys.has(key);
      loadedKeys.add(key);
      slowKeys.delete(key);
      failedKeys.delete(key);
      if (notify && changed) set({});
    } else if (!loadedKeys.has(key)) {
      slowKeys.delete(key);
      const changed = !failedKeys.has(key);
      failedKeys.add(key);
      if (notify && changed) set({});
    }
  }

  function readState(rawKind: ReadKind, wishId?: string): ReadState {
    const kind = normalizeKind(rawKind);
    if (
      !wishId &&
      (kind === Change.TASK ||
        kind === Change.QUESTION ||
        kind === Change.BLOCK ||
        kind === Change.TILASM)
    ) {
      const wState = readState("wishes");
      if (wState !== "ready") return wState;
      const activeWishes = state.wishes.filter(
        (w) =>
          w.state === WishState.ACTIVE || w.state === WishState.UNSPECIFIED,
      );
      let hasSlow = false;
      let hasReading = false;
      for (const w of activeWishes) {
        const s = readState(kind, w.id);
        if (s === "failed") return "failed";
        if (s === "slow") hasSlow = true;
        else if (s !== "ready") hasReading = true;
      }
      if (hasSlow) return "slow";
      if (hasReading) return "reading";
      return "ready";
    }
    const key = keyFor(kind, wishId);
    if (key === "wishes" && (loadedKeys.has("wishes") || state.loaded))
      return "ready";
    if (
      key === "projects" &&
      (loadedKeys.has("projects") ||
        (state.loaded &&
          !pending.projects &&
          !inFlight.has("projects") &&
          !slowKeys.has("projects") &&
          !failedKeys.has("projects")))
    )
      return "ready";
    if (
      key === "inbox" &&
      (loadedKeys.has("inbox") ||
        (state.loaded &&
          !pending.inbox &&
          !inFlight.has("inbox") &&
          !slowKeys.has("inbox") &&
          !failedKeys.has("inbox")))
    )
      return "ready";
    if (
      key === "machine" &&
      (loadedKeys.has("machine") || state.machine !== undefined)
    )
      return "ready";
    if (
      wishId &&
      (kind === Change.TASK ||
        kind === Change.QUESTION ||
        kind === Change.BLOCK ||
        kind === Change.TILASM)
    ) {
      const detail = state.details[wishId];
      if (detail?.loadedKinds.has(kind) || detail?.loaded) return "ready";
      if (
        (loadedKeys.has("wishes") || state.loaded) &&
        !state.wishes.some((w) => w.id === wishId) &&
        !opened.has(wishId) &&
        !requestedKinds.has(wishId) &&
        !pending.details.has(wishId) &&
        !inFlight.has(key) &&
        !failedKeys.has(key) &&
        !slowKeys.has(key)
      )
        return "ready";
    }
    if (loadedKeys.has(key)) return "ready";
    if (failedKeys.has(key)) return "failed";
    if (slowKeys.has(key)) return "slow";
    return "reading";
  }

  const fresh = (wishId: string, kind: Change) =>
    !!state.details[wishId]?.loadedKinds.has(kind) &&
    !stale.get(wishId)?.has(kind);
  const loadingMore = new Set<string>();
  const pendingPrompts = new Map<string, Promise<string>>();
  const lastReadAt = new Map<string, number>();
  // Incremental watch changes that arrived while a "${wishId}:${kind}" read was already in flight, re-applied when
  // that read finishes so an older list snapshot does not overwrite a newer watch update.
  const inFlightChanges = new Map<string, WishChanges[]>();
  // How long a read that failed waits before it is made again, by key: it doubles at each failure, up to a minute.
  const backoff = new Map<string, number>();
  // The store no longer follows djinn: a read that failed is not made again.
  let stopped = false;

  const activeReads = new Set<Promise<void>>();
  let scheduled: Promise<void> | undefined;
  let scheduledResolve: (() => void) | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let timerAt = 0;

  // mark notes what to read again; with changed, the tasks, questions and blocks it brings need no read. A change
  // this page does not know reads everything again. Only loaded kinds of opened wishes are re-read.
  function mark(
    wishId: string,
    changes: readonly Change[],
    changed?: WishChanges,
  ) {
    if (changed && wishId && opened.has(wishId))
      pending.changes.set(wishId, [
        ...(pending.changes.get(wishId) ?? []),
        changed,
      ]);
    for (const change of changes) {
      if (!Change[change] || change === Change.UNSPECIFIED) {
        const base = [Change.WISH, Change.PROJECT, Change.INBOX];
        if (wishId) {
          const loaded = state.details[wishId]?.loadedKinds;
          mark(wishId, [
            ...base,
            ...(loaded && loaded.size > 0 ? [...loaded] : DETAIL),
          ]);
        } else {
          pending.projects = pending.wishes = pending.inbox = true;
          for (const id of opened.keys()) {
            const loaded = state.details[id]?.loadedKinds;
            const kinds = loaded && loaded.size > 0 ? [...loaded] : DETAIL;
            mark(id, kinds);
          }
        }
        continue;
      }
      if (change === Change.PROJECT) pending.projects = true;
      if (change === Change.WISH) pending.wishes = true;
      if (change === Change.INBOX) pending.inbox = true;
      if (!DETAIL.includes(change)) continue;
      for (const id of wishId ? [wishId] : Object.keys(state.details))
        if (!opened.has(id) && state.details[id]?.loadedKinds.has(change))
          stale.set(id, (stale.get(id) ?? new Set<Change>()).add(change));
      if (changed && wishId && CARRIED.has(change)) continue;
      // Without a wish, every wish shown; a wish not shown is read when it is.
      const ids = wishId ? [wishId] : [...opened.keys()];
      for (const id of ids) {
        if (!opened.has(id)) continue;
        const detail = state.details[id];
        // Do not load hidden tabs! Only re-read if already loaded, pending, or in flight.
        const loaded = detail
          ? detail.loadedKinds.has(change)
          : pending.details.get(id)?.has(change) ||
            inFlight.has(`${id}:${change}`);
        if (!loaded) continue;
        const kinds = pending.details.get(id) ?? new Set<Change>();
        kinds.add(change);
        pending.details.set(id, kinds);
      }
    }
  }

  // reconnected follows a watch that answers again, after djinn restarted or the stream broke: what changed meanwhile
  // came in no message. The watch's first message reads again what each wish shown holds; the kinds whose read failed
  // while djinn was away are read again with them, at once, not after their wait: a question revised meanwhile shows
  // revised (incident Q274).
  function reconnected() {
    lastReadAt.clear();
    backoff.clear();
    for (const [wishId, kinds] of stale) {
      if (!opened.has(wishId) || !kinds.size) continue;
      const pend = pending.details.get(wishId) ?? new Set<Change>();
      for (const k of kinds) pend.add(k);
      pending.details.set(wishId, pend);
    }
    set({ live: true, everLive: true });
  }

  function canRead(key: string, now: number): boolean {
    if (inFlight.has(key)) return false;
    const last = lastReadAt.get(key);
    return last === undefined || now - last >= throttle;
  }

  function nextDelay(now: number): number {
    let minWait = Infinity;
    const check = (key: string) => {
      if (inFlight.has(key)) return;
      const last = lastReadAt.get(key);
      if (last === undefined) minWait = 0;
      else {
        const wait = throttle - (now - last);
        if (wait <= 0) minWait = 0;
        else if (wait < minWait) minWait = wait;
      }
    };
    if (pending.wishes) check("wishes");
    if (pending.projects) check("projects");
    if (pending.inbox) check("inbox");
    for (const [wishId, kinds] of pending.details) {
      for (const k of kinds) {
        check(`${wishId}:${k}`);
      }
    }
    if (pending.changes.size > 0) {
      minWait = 0;
    }
    return minWait === Infinity ? -1 : minWait;
  }

  function isWishFullyLoaded(
    wishId: string,
    loadedKinds: Set<Change>,
  ): boolean {
    const req = requestedKinds.get(wishId);
    if (!req || req.size === 0) return loadedKinds.size > 0;
    for (const k of req) {
      if (!loadedKinds.has(k)) return false;
    }
    return true;
  }

  async function read() {
    const now = Date.now();
    const wishes = pending.wishes && canRead("wishes", now);
    const projects = pending.projects && canRead("projects", now);
    const inbox = pending.inbox && canRead("inbox", now);
    if (wishes) {
      pending.wishes = false;
      lastReadAt.set("wishes", now);
      startReadKey("wishes");
    }
    if (projects) {
      pending.projects = false;
      lastReadAt.set("projects", now);
      startReadKey("projects");
    }
    if (inbox) {
      pending.inbox = false;
      lastReadAt.set("inbox", now);
      startReadKey("inbox");
    }

    const details: [string, Set<Change>][] = [];
    for (const [wishId, kinds] of pending.details) {
      const eligible = new Set<Change>();
      for (const k of kinds) {
        const key = `${wishId}:${k}`;
        if (canRead(key, now)) {
          eligible.add(k);
        }
      }
      if (eligible.size > 0) {
        details.push([wishId, eligible]);
        for (const k of eligible) {
          const key = `${wishId}:${k}`;
          kinds.delete(k);
          lastReadAt.set(key, now);
          startReadKey(key);
        }
        if (kinds.size === 0) {
          pending.details.delete(wishId);
        }
      }
    }

    const changes = [...pending.changes];
    pending.changes.clear();

    if (
      !wishes &&
      !projects &&
      !inbox &&
      details.length === 0 &&
      changes.length === 0
    ) {
      return;
    }

    // Apply incremental watch changes immediately for already-loaded kinds that are not being full-read right now.
    if (changes.length > 0) {
      const next = { ...state.details };
      let touched = false;
      for (const [wishId, list] of changes) {
        const eligible = details.find(([id]) => id === wishId)?.[1];
        for (const k of CARRIED) {
          const key = `${wishId}:${k}`;
          if (inFlight.has(key) && !eligible?.has(k)) {
            inFlightChanges.set(key, [
              ...(inFlightChanges.get(key) ?? []),
              ...list,
            ]);
          }
        }
        const detail = next[wishId];
        if (!detail || (!detail.loaded && detail.loadedKinds.size === 0))
          continue;
        const mask: Partial<WishDetail> = {
          ...(eligible?.has(Change.TASK) ? { tasks: detail.tasks } : {}),
          ...(eligible?.has(Change.QUESTION)
            ? { questions: detail.questions }
            : {}),
          ...(eligible?.has(Change.BLOCK) ? { blocks: detail.blocks } : {}),
        };
        next[wishId] = list.reduce((d, c) => apply(d, c, mask), detail);
        touched ||= next[wishId] !== detail;
      }
      if (touched) {
        let prompts = state.prompts;
        for (const [wishId, detail] of Object.entries(next)) {
          const prevTasks = state.details[wishId]?.tasks ?? [];
          if (detail.tasks !== prevTasks) {
            prompts = syncPrompts(prevTasks, detail.tasks, prompts) ?? prompts;
          }
        }
        set({
          details: next,
          ...(prompts !== state.prompts ? { prompts } : {}),
        });
      }
    }

    const reads: Promise<void>[] = [];
    // A read that fails is read again after retry: what it would have brought (a task spawned, a worker started)
    // must not wait for the next change of the same thing, which may come only as it ends.
    const again = (key: string, pend: () => void) => {
      if (stopped) return;
      const wait = backoff.get(key) ?? retry;
      backoff.set(key, Math.min(wait * 2, 60_000));
      pend();
      lastReadAt.set(key, Date.now() + Math.max(0, wait - throttle));
    };
    const attempt = (
      key: string,
      run: () => Promise<void>,
      failed?: () => void,
    ) => {
      const p = run().then(
        () => {
          backoff.delete(key);
          finishReadKey(key, true);
        },
        (error) => {
          if (unimplemented(error)) {
            finishReadKey(key, true);
            return;
          }
          finishReadKey(key, false);
          failed?.();
          set({ error: message(error) });
        },
      );
      inFlightPromises.set(key, p);
      reads.push(p);
    };

    const publishDetail = (
      wishId: string,
      kind: Change,
      update: (cur: WishDetail) => Partial<WishDetail>,
    ) => {
      const key = `${wishId}:${kind}`;
      const queued = inFlightChanges.get(key);
      backoff.delete(key);
      finishReadKey(key, true);
      stale.get(wishId)?.delete(kind);
      const prevDetail = state.details[wishId] ?? EMPTY_DETAIL;
      const got = update(prevDetail);
      let tasks = got.tasks ?? prevDetail.tasks;
      let questions = got.questions
        ? questionsAhead(prevDetail.questions, got.questions)
        : prevDetail.questions;
      let blocks = got.blocks ?? prevDetail.blocks;
      const tilasms = got.tilasms ?? prevDetail.tilasms;
      const drafts = got.drafts ?? prevDetail.drafts;
      const holds = got.holds ?? prevDetail.holds;
      const sent = got.sent ?? prevDetail.sent;
      const readsList = got.reads ?? prevDetail.reads;
      const sends = got.sends ?? prevDetail.sends;
      let tasksTotal =
        got.tasksTotal !== undefined ? got.tasksTotal : prevDetail.tasksTotal;
      let questionsTotal =
        got.questionsTotal !== undefined
          ? got.questionsTotal
          : prevDetail.questionsTotal;
      let blocksTotal =
        got.blocksTotal !== undefined
          ? got.blocksTotal
          : prevDetail.blocksTotal;
      const tasksNextToken =
        got.tasksNextToken !== undefined
          ? got.tasksNextToken
          : prevDetail.tasksNextToken;
      const questionsNextToken =
        got.questionsNextToken !== undefined
          ? got.questionsNextToken
          : prevDetail.questionsNextToken;
      const blocksNextToken =
        got.blocksNextToken !== undefined
          ? got.blocksNextToken
          : prevDetail.blocksNextToken;
      let loadedKinds = prevDetail.loadedKinds;
      let kindsChanged = false;
      if (!loadedKinds.has(kind)) {
        loadedKinds = new Set(loadedKinds);
        loadedKinds.add(kind);
        kindsChanged = true;
      }
      if (queued && queued.length > 0 && CARRIED.has(kind)) {
        const mask: Partial<WishDetail> = {
          ...(kind !== Change.TASK ? { tasks } : {}),
          ...(kind !== Change.QUESTION ? { questions } : {}),
          ...(kind !== Change.BLOCK ? { blocks } : {}),
        };
        const base: WishDetail = {
          ...prevDetail,
          tasks,
          questions,
          blocks,
          tasksTotal,
          questionsTotal,
          blocksTotal,
          loadedKinds,
        };
        const applied = queued.reduce((d, c) => apply(d, c, mask), base);
        if (kind === Change.TASK) {
          tasks = reconcile(TaskSchema, prevDetail.tasks, applied.tasks);
          tasksTotal = applied.tasksTotal;
        } else if (kind === Change.QUESTION) {
          questions = reconcile(
            QuestionSchema,
            prevDetail.questions,
            applied.questions,
          );
          questionsTotal = applied.questionsTotal;
        } else if (kind === Change.BLOCK) {
          blocks = reconcile(BlockSchema, prevDetail.blocks, applied.blocks);
          blocksTotal = applied.blocksTotal;
        }
      }
      const nowLoaded =
        prevDetail.loaded || isWishFullyLoaded(wishId, loadedKinds);
      if (
        prevDetail.loaded === nowLoaded &&
        tasks === prevDetail.tasks &&
        questions === prevDetail.questions &&
        blocks === prevDetail.blocks &&
        tilasms === prevDetail.tilasms &&
        drafts === prevDetail.drafts &&
        holds === prevDetail.holds &&
        sent === prevDetail.sent &&
        readsList === prevDetail.reads &&
        sends === prevDetail.sends &&
        tasksTotal === prevDetail.tasksTotal &&
        questionsTotal === prevDetail.questionsTotal &&
        blocksTotal === prevDetail.blocksTotal &&
        tasksNextToken === prevDetail.tasksNextToken &&
        questionsNextToken === prevDetail.questionsNextToken &&
        blocksNextToken === prevDetail.blocksNextToken &&
        !kindsChanged &&
        !state.error
      ) {
        set({});
        return;
      }
      const nextDetail: WishDetail = {
        tasks,
        questions,
        blocks,
        tilasms,
        drafts,
        holds,
        sent,
        reads: readsList,
        sends,
        loaded: nowLoaded,
        tasksTotal,
        questionsTotal,
        blocksTotal,
        tasksNextToken,
        questionsNextToken,
        blocksNextToken,
        loadedKinds,
      };
      const prompts =
        tasks !== prevDetail.tasks
          ? syncPrompts(prevDetail.tasks, tasks, state.prompts)
          : undefined;
      set({
        details: { ...state.details, [wishId]: nextDetail },
        ...(prompts ? { prompts } : {}),
        ...(state.error ? { error: "" } : {}),
      });
    };

    const attemptKind = (
      wishId: string,
      kind: Change,
      run: () => Promise<(cur: WishDetail) => Partial<WishDetail>>,
    ) => {
      const key = `${wishId}:${kind}`;
      const p = run().then(
        (updater) => publishDetail(wishId, kind, updater),
        (error) => {
          if (unimplemented(error)) {
            publishDetail(wishId, kind, () => ({}));
            return;
          }
          finishReadKey(key, false);
          // A wish no longer shown reads it when it shows again.
          stale.set(wishId, (stale.get(wishId) ?? new Set<Change>()).add(kind));
          if (opened.has(wishId))
            again(key, () =>
              pending.details.set(
                wishId,
                (pending.details.get(wishId) ?? new Set<Change>()).add(kind),
              ),
            );
          set({ error: message(error) });
        },
      );
      inFlightPromises.set(key, p);
      reads.push(p);
    };

    if (wishes)
      attempt(
        "wishes",
        async () => {
          const [wishesRes, leadRes] = await Promise.all([
            clients.wishes.list({}, call),
            clients.lead.get({}, call).catch((error: unknown) => {
              if (unimplemented(error)) return undefined;
              throw error;
            }),
          ]);
          finishReadKey("wishes", true);
          const patch: Partial<State> = {};
          const list = wishesRes.wishes;
          const reconciled = reconcile(WishSchema, state.wishes, list);
          if (reconciled !== state.wishes) patch.wishes = reconciled;
          if (leadRes) {
            const {
              lead,
              prompt,
              exit,
              startError,
              driven,
              seat,
              accounts,
              limit,
            } = leadRes;
            const sameLead =
              (!state.lead && !lead) ||
              (state.lead && lead
                ? equals(LeadSchema, state.lead, lead)
                : false);
            if (!sameLead) patch.lead = lead;
            const samePrompt =
              (!state.leadPrompt && !prompt) ||
              (state.leadPrompt && prompt
                ? equals(LeadPromptSchema, state.leadPrompt, prompt)
                : false);
            if (!samePrompt) patch.leadPrompt = prompt;
            if (
              state.leadExit?.code !== exit?.code ||
              !state.leadExit !== !exit
            )
              patch.leadExit = exit;
            if ((state.leadStartError ?? "") !== startError)
              patch.leadStartError = startError;
            const drive = driven
              ? create(LeadServiceGetResponseSchema, { seat, accounts, limit })
              : undefined;
            const sameDrive =
              (!state.leadDrive && !drive) ||
              (state.leadDrive && drive
                ? equals(
                    LeadServiceGetResponseSchema,
                    create(LeadServiceGetResponseSchema, state.leadDrive),
                    drive,
                  )
                : false);
            if (!sameDrive)
              patch.leadDrive = drive && {
                seat: drive.seat,
                accounts: drive.accounts,
                limit: drive.limit,
              };
          }
          if (loadedKeys.has("projects") && !state.loaded) patch.loaded = true;
          if (state.error) patch.error = "";
          set(patch);
        },
        () => again("wishes", () => (pending.wishes = true)),
      );
    if (projects)
      attempt(
        "projects",
        async () => {
          const list = (await clients.projects.list({}, call)).projects;
          finishReadKey("projects", true);
          const patch: Partial<State> = {};
          const reconciled = reconcile(ProjectSchema, state.projects, list);
          if (reconciled !== state.projects) patch.projects = reconciled;
          if (loadedKeys.has("wishes") && !state.loaded) patch.loaded = true;
          if (state.error) patch.error = "";
          set(patch);
        },
        () => again("projects", () => (pending.projects = true)),
      );
    if (inbox)
      attempt(
        "inbox",
        async () => {
          const [itemsRes, sourcesRes] = await Promise.all([
            clients.inbox.list({}, call),
            clients.inbox.sources({}, call).catch((error: unknown) => {
              if (unimplemented(error)) return undefined;
              throw error;
            }),
          ]);
          finishReadKey("inbox", true);
          const patch: Partial<State> = {};
          const reconciledItems = reconcile(
            InboxItemSchema,
            state.inbox,
            itemsRes.items,
          );
          if (reconciledItems !== state.inbox) patch.inbox = reconciledItems;
          if (sourcesRes) {
            const reconciledSources = reconcile(
              InboxSourceSchema,
              state.sources,
              sourcesRes.sources,
            );
            if (reconciledSources !== state.sources)
              patch.sources = reconciledSources;
          }
          if (state.error) patch.error = "";
          set(patch);
        },
        () => again("inbox", () => (pending.inbox = true)),
      );
    for (const [wishId, kinds] of details) {
      const startDetail = state.details[wishId] ?? EMPTY_DETAIL;
      if (kinds.has(Change.TASK))
        attemptKind(wishId, Change.TASK, async () => {
          const res = await clients.tasks.list({ wishId }, call);
          // Past the first page, the tasks the lists of what runs read come too (live): the glance shows a wish's
          // waiting work from the start, not once the Tasks tab read further (W219).
          let live: Task[] = [];
          // Page after page, but never the same page twice.
          const seen = new Set<string>();
          let token = "";
          while (res.nextPageToken && !seen.has(token)) {
            seen.add(token);
            const page = await clients.tasks.list(
              { wishId, live: true, pageSize: 200, pageToken: token },
              call,
            );
            live = live.concat(page.tasks);
            token = page.nextPageToken;
            if (!token) break;
          }
          const liveIds = new Set(live.map((x) => x.id));
          // The read goes as far as the tasks shown went, as for the questions: after a restart, an enlighten task
          // ended meanwhile past the first page shows ended, not running as it was (incident Q274, W544). The live
          // ones read already are not that far: only those read by a page, or that are live no longer.
          const furthest = startDetail.tasks.reduce(
            (far, x) => (!liveIds.has(x.id) && x.id > far ? x.id : far),
            "",
          );
          let allTasks = res.tasks;
          let next = res.nextPageToken;
          while (next && (allTasks.at(-1)?.id ?? "") < furthest) {
            const page = await clients.tasks.list(
              { wishId, pageSize: 200, pageToken: next },
              call,
            );
            allTasks = allTasks.concat(page.tasks);
            next = page.nextPageToken;
          }
          if (live.length) {
            const paged = new Set(allTasks.map((x) => x.id));
            allTasks = allTasks
              .concat(live.filter((x) => !paged.has(x.id)))
              .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
          }
          return (cur) => ({
            tasks: reconcile(TaskSchema, cur.tasks, allTasks),
            tasksTotal: res.total,
            tasksNextToken: next,
          });
        });
      if (kinds.has(Change.QUESTION))
        attemptKind(wishId, Change.QUESTION, async () => {
          const res = await clients.questions.list({ wishId }, call);
          // The read goes as far as the questions shown went: one answered past the first page stays, answered,
          // instead of leaving the screens with the open ones (incident Q253).
          const furthest = startDetail.questions.at(-1)?.id ?? "";
          let allQuestions = res.questions;
          let next = res.nextPageToken;
          while (next && (allQuestions.at(-1)?.id ?? "") < furthest) {
            const page = await clients.questions.list(
              { wishId, pageSize: 200, pageToken: next },
              call,
            );
            allQuestions = allQuestions.concat(page.questions);
            next = page.nextPageToken;
          }
          if (next) {
            let openList: Question[] = [];
            let token: string | undefined;
            do {
              const page = await clients.questions.list(
                {
                  wishId,
                  open: true,
                  pageSize: 200,
                  pageToken: token,
                },
                call,
              );
              openList = openList.concat(page.questions);
              token = page.nextPageToken;
            } while (token);
            allQuestions = merge(
              QuestionSchema,
              allQuestions,
              openList,
              new Set(),
              byID,
            );
          }
          return (cur) => ({
            questions: reconcile(QuestionSchema, cur.questions, allQuestions),
            questionsTotal: res.total,
            questionsNextToken: next,
          });
        });
      if (kinds.has(Change.BLOCK))
        attemptKind(wishId, Change.BLOCK, async () => {
          const res = await clients.blocks.list({ wishId }, call);
          return (cur) => ({
            blocks: reconcile(BlockSchema, cur.blocks, res.blocks),
            blocksTotal: res.total,
            blocksNextToken: res.nextPageToken,
          });
        });
      if (kinds.has(Change.TILASM))
        attemptKind(wishId, Change.TILASM, async () => {
          const [listRes, draftsRes] = await Promise.all([
            clients.tilasms.list({ wish: wishId }, call),
            clients.tilasms
              .drafts({ wish: wishId }, call)
              .catch((error: unknown) => {
                if (unimplemented(error))
                  return {
                    comments: [],
                    holds: [],
                    sent: [],
                    reads: [],
                    sends: [],
                  };
                throw error;
              }),
          ]);
          return (cur) => {
            const sameHolds =
              cur.holds.length === draftsRes.holds.length &&
              draftsRes.holds.every((h, i) =>
                equals(TilasmHoldSchema, h, cur.holds[i]),
              );
            const sameReads =
              cur.reads.length === draftsRes.reads.length &&
              draftsRes.reads.every((r, i) =>
                equals(TilasmSendReadSchema, r, cur.reads[i]),
              );
            const resSends = draftsRes.sends ?? [];
            const sameSends =
              cur.sends.length === resSends.length &&
              resSends.every((s, i) =>
                equals(TilasmSendsSchema, s, cur.sends[i]),
              );
            return {
              tilasms: reconcile(TilasmSchema, cur.tilasms, listRes.tilasms),
              drafts: reconcile(
                TilasmCommentSchema,
                cur.drafts,
                draftsRes.comments,
              ),
              holds: sameHolds ? cur.holds : draftsRes.holds,
              reads: sameReads ? cur.reads : draftsRes.reads,
              sends: sameSends ? cur.sends : resSends,
              sent: reconcile(TilasmCommentSchema, cur.sent, draftsRes.sent),
            };
          };
        });
    }
    await Promise.all(reads);
  }

  function syncPrompts(
    prevTasks: Task[],
    nextTasks: Task[],
    base = state.prompts,
  ): Record<string, string> | undefined {
    const prevById = new Map(prevTasks.map((t) => [t.id, t.prompt]));
    let next: Record<string, string> | undefined;
    for (const t of nextTasks) {
      if (!t.prompt) continue;
      const cur = (next ?? base)[t.id];
      if (!cur || t.prompt !== prevById.get(t.id)) {
        if (cur !== t.prompt) {
          next ??= { ...base };
          next[t.id] = t.prompt;
        }
      }
    }
    return next;
  }

  function ensureScheduled(): Promise<void> {
    if (!scheduled) {
      scheduled = new Promise<void>((resolve) => {
        scheduledResolve = resolve;
      });
    }
    return scheduled;
  }

  function scheduleFlush(delay: number): Promise<void> {
    const out = ensureScheduled();
    const target = Date.now() + Math.max(0, delay);
    if (timer !== undefined) {
      if (target < timerAt) {
        clearTimeout(timer);
        timer = undefined;
      } else {
        return out;
      }
    }
    timerAt = target;
    const wait = Math.max(0, target - Date.now());
    timer = setTimeout(runScheduled, wait);
    return out;
  }

  function launchRead(): Promise<void> {
    const promise: Promise<void> = read().finally(() => {
      activeReads.delete(promise);
      checkAfterActive();
    });
    activeReads.add(promise);
    return promise;
  }

  async function runScheduled() {
    timer = undefined;
    timerAt = 0;
    const delay = nextDelay(Date.now());
    if (delay > 0) {
      scheduleFlush(delay);
      return;
    }
    if (delay === 0) {
      await launchRead();
    } else {
      checkAfterActive();
    }
  }

  function checkAfterActive() {
    const delay = nextDelay(Date.now());
    if (delay === 0) {
      if (timer !== undefined) {
        clearTimeout(timer);
        timer = undefined;
        timerAt = 0;
      }
      void launchRead();
    } else if (delay > 0) {
      scheduleFlush(delay);
    } else if (activeReads.size === 0 && scheduledResolve) {
      const resolve = scheduledResolve;
      scheduled = undefined;
      scheduledResolve = undefined;
      resolve();
    }
  }

  // flush reads what is pending; a change that comes while it reads is read right after, coalesced and throttled.
  function flush(): Promise<void> {
    const now = Date.now();
    const delay = nextDelay(now);
    if (delay > 0) {
      return scheduleFlush(delay);
    }
    if (delay < 0) {
      if (activeReads.size > 0) return ensureScheduled();
      return Promise.resolve();
    }
    if (timer !== undefined) {
      clearTimeout(timer);
      timer = undefined;
      timerAt = 0;
    }
    const out = ensureScheduled();
    void launchRead();
    return out;
  }

  // How many screens show each task's events; its stream runs while one does.
  const shown = new Map<string, number>();
  const following = new Map<string, AbortController>();
  const forgetting = new Map<string, ReturnType<typeof setTimeout>>();

  async function followEvents(taskId: string, abort: AbortController) {
    let buffer: TaskEvent[] = [];
    let timer: ReturnType<typeof setTimeout> | undefined;
    // Events come in bursts: the screen gets them together, at most every 50 ms.
    const push = () => {
      timer = undefined;
      // A follow stopped as a new status follows again pushes what it held, and the new one reads it again: an
      // event already kept is not kept twice.
      const known = state.events[taskId] ?? [];
      const lastSeq = known.at(-1)?.seq ?? 0n;
      const fresh = buffer.filter((e) => e.seq > lastSeq);
      buffer = [];
      if (!fresh.length) return;
      const promptEvent = fresh
        .filter((e) => e.kind === TaskEventKind.PROMPT)
        .at(-1);
      const patchPrompts =
        promptEvent && state.prompts[taskId] !== promptEvent.text
          ? { prompts: { ...state.prompts, [taskId]: promptEvent.text } }
          : undefined;
      const kept = [...known, ...fresh].slice(-MAX_EVENTS);
      set({
        events: { ...state.events, [taskId]: kept },
        ...(patchPrompts ?? {}),
      });
    };
    for (;;) {
      const known = state.events[taskId] ?? [];
      const afterSeq = buffer.at(-1)?.seq ?? known.at(-1)?.seq ?? 0n;
      try {
        for await (const res of clients.tasks.watch(
          { taskId, afterSeq },
          { signal: abort.signal },
        )) {
          if (!res.event) continue;
          buffer.push(res.event);
          timer ??= setTimeout(push, 50);
        }
        // The task ended: nothing more comes.
        clearTimeout(timer);
        push();
        return;
      } catch (error) {
        clearTimeout(timer);
        push();
        if (abort.signal.aborted || notFound(error)) return;
      }
      await new Promise((r) => setTimeout(r, retry));
      if (abort.signal.aborted) return;
    }
  }

  // readPrompt reads a task's prompt alone (Store.readPrompt).
  async function readPrompt(taskId: string): Promise<string> {
    if (state.prompts[taskId]) return state.prompts[taskId];
    const pending = pendingPrompts.get(taskId);
    if (pending) return pending;
    const read = (async () => {
      const key = `prompt:${taskId}`;
      try {
        const res = await clients.tasks.get({ taskId }, call);
        backoff.delete(key);
        const prompt = res.task?.prompt ?? "";
        if (prompt) {
          set({ prompts: { ...state.prompts, [taskId]: prompt } });
        }
        return prompt;
      } catch (error) {
        // A call the network cut (Chromium aborts every request in flight when an interface comes or goes, W7) or a
        // djinn that restarts: read again, never an error thrown at the page. A task gone has no prompt to read.
        if (!stopped && !notFound(error) && !unimplemented(error)) {
          const wait = backoff.get(key) ?? retry;
          backoff.set(key, Math.min(wait * 2, 60_000));
          setTimeout(() => void readPrompt(taskId), wait);
        }
        return "";
      } finally {
        pendingPrompts.delete(taskId);
      }
    })();
    pendingPrompts.set(taskId, read);
    return read;
  }

  async function readMachine() {
    startReadKey("machine");
    try {
      const [machine, gates] = await Promise.all([
        clients.machine.show({}, call),
        clients.gates.list({}, call),
      ]);
      finishReadKey("machine", true);
      set({ machine: machine.machine, gates: gates.gates });
    } catch (error) {
      finishReadKey("machine", unimplemented(error));
      set({ error: message(error) });
    }
  }

  function retryRead(rawKind: ReadKind, wishId?: string) {
    const kind = normalizeKind(rawKind);
    if (
      !wishId &&
      (kind === Change.TASK ||
        kind === Change.QUESTION ||
        kind === Change.BLOCK ||
        kind === Change.TILASM)
    ) {
      if (readState("wishes") === "failed") retryRead("wishes");
      for (const w of state.wishes) {
        if (
          (w.state === WishState.ACTIVE || w.state === WishState.UNSPECIFIED) &&
          readState(kind, w.id) === "failed"
        ) {
          retryRead(kind, w.id);
        }
      }
      return;
    }
    const key = keyFor(kind, wishId);
    failedKeys.delete(key);
    slowKeys.delete(key);
    backoff.delete(key);
    lastReadAt.delete(key);
    ensureSlowTimer(key);
    set({});
    if (key === "wishes") {
      pending.wishes = true;
      void flush();
    } else if (key === "projects") {
      pending.projects = true;
      void flush();
    } else if (key === "inbox") {
      pending.inbox = true;
      void flush();
    } else if (key === "machine") {
      void readMachine();
    } else if (
      wishId &&
      (kind === Change.TASK ||
        kind === Change.QUESTION ||
        kind === Change.BLOCK ||
        kind === Change.TILASM)
    ) {
      const pendingKinds = pending.details.get(wishId) ?? new Set<Change>();
      pendingKinds.add(kind);
      pending.details.set(wishId, pendingKinds);
      void flush();
    } else {
      trackedRunners.get(key)?.();
    }
  }

  function trackRead<T>(
    kind: ReadKind,
    id: string | undefined,
    run: () => Promise<T>,
  ): Promise<T> {
    const key = keyFor(kind, id);
    trackedRunners.set(key, () => {
      void trackRead(kind, id, run).catch(() => {});
    });
    startReadKey(key);
    return run().then(
      (res) => {
        finishReadKey(key, true, true);
        return res;
      },
      (err) => {
        finishReadKey(key, unimplemented(err), true);
        throw err;
      },
    );
  }

  return {
    clients,
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => void listeners.delete(listener);
    },
    start() {
      stopped = false;
      ensureSlowTimer("wishes");
      ensureSlowTimer("projects");
      const abort = new AbortController();
      void (async () => {
        while (!abort.signal.aborted) {
          try {
            for await (const res of clients.wishes.watch(
              {},
              { signal: abort.signal },
            )) {
              if (!state.live) reconnected();
              mark(res.wishId, res.changes, res.changed);
              void flush();
            }
          } catch (error) {
            if (abort.signal.aborted) return;
            set({ live: false, error: message(error) });
          }
          if (state.live) set({ live: false });
          await new Promise((r) => setTimeout(r, retry));
        }
      })();
      return () => {
        stopped = true;
        abort.abort();
        for (const t of slowTimers.values()) clearTimeout(t);
        slowTimers.clear();
        if (timer !== undefined) {
          clearTimeout(timer);
          timer = undefined;
          timerAt = 0;
        }
      };
    },
    open(wishId, kinds) {
      opened.set(wishId, (opened.get(wishId) ?? 0) + 1);
      const toLoad = kinds && kinds.length > 0 ? kinds : DETAIL;
      const req = requestedKinds.get(wishId) ?? new Set<Change>();
      for (const k of toLoad) req.add(k);
      requestedKinds.set(wishId, req);
      const pendingKinds = pending.details.get(wishId) ?? new Set<Change>();
      let added = false;
      for (const k of toLoad) {
        const key = `${wishId}:${k}`;
        ensureSlowTimer(key);
        if (!fresh(wishId, k) && !inFlight.has(key)) {
          pendingKinds.add(k);
          added = true;
        }
      }
      if (added) {
        pending.details.set(wishId, pendingKinds);
        void flush();
      }
      return () => {
        const count = (opened.get(wishId) ?? 1) - 1;
        if (count > 0) opened.set(wishId, count);
        else opened.delete(wishId);
      };
    },
    async loadKinds(wishId, kinds) {
      const req = requestedKinds.get(wishId) ?? new Set<Change>();
      for (const k of kinds) req.add(k);
      requestedKinds.set(wishId, req);
      const needed = kinds.filter((k) => !fresh(wishId, k));
      if (!needed.length) return;
      const pendingKinds = pending.details.get(wishId) ?? new Set<Change>();
      let added = false;
      for (const k of needed) {
        const key = `${wishId}:${k}`;
        ensureSlowTimer(key);
        if (!inFlight.has(key)) {
          pendingKinds.add(k);
          added = true;
        }
      }
      if (added) {
        pending.details.set(wishId, pendingKinds);
      }
      await flush();
    },
    async loadMore(wishId, kind, pageSize) {
      const key = `${wishId}:${kind}`;
      if (loadingMore.has(key)) return;
      const inflight = inFlightPromises.get(key);
      if (inflight) await inflight;
      const detail = state.details[wishId];
      if (!detail) return;
      loadingMore.add(key);
      try {
        if (kind === Change.QUESTION && detail.questionsNextToken) {
          const res = await clients.questions.list(
            { wishId, pageToken: detail.questionsNextToken },
            call,
          );
          const cur = state.details[wishId];
          if (!cur) return;
          const questions = merge(
            QuestionSchema,
            cur.questions,
            res.questions,
            new Set(),
            byID,
            questionBehind,
          );
          set({
            details: {
              ...state.details,
              [wishId]: {
                ...cur,
                questions,
                questionsTotal: res.total,
                questionsNextToken: res.nextPageToken,
              },
            },
          });
        } else if (kind === Change.TASK && detail.tasksNextToken) {
          const res = await clients.tasks.list(
            { wishId, pageToken: detail.tasksNextToken, pageSize },
            call,
          );
          const cur = state.details[wishId];
          if (!cur) return;
          const tasks = merge(
            TaskSchema,
            cur.tasks,
            res.tasks,
            new Set(),
            byID,
          );
          const prompts = syncPrompts(cur.tasks, res.tasks);
          set({
            details: {
              ...state.details,
              [wishId]: {
                ...cur,
                tasks,
                tasksTotal: res.total,
                tasksNextToken: res.nextPageToken,
              },
            },
            ...(prompts ? { prompts } : {}),
          });
        } else if (kind === Change.BLOCK && detail.blocksNextToken) {
          const res = await clients.blocks.list(
            { wishId, pageToken: detail.blocksNextToken },
            call,
          );
          const cur = state.details[wishId];
          if (!cur) return;
          const blocks = merge(
            BlockSchema,
            cur.blocks,
            res.blocks,
            new Set(),
            byPosition,
          );
          set({
            details: {
              ...state.details,
              [wishId]: {
                ...cur,
                blocks,
                blocksTotal: res.total,
                blocksNextToken: res.nextPageToken,
              },
            },
          });
        }
      } catch (error) {
        set({ error: message(error) });
      } finally {
        loadingMore.delete(key);
      }
    },
    follow(taskId) {
      shown.set(taskId, (shown.get(taskId) ?? 0) + 1);
      clearTimeout(forgetting.get(taskId));
      forgetting.delete(taskId);
      if (!following.has(taskId)) {
        const abort = new AbortController();
        following.set(taskId, abort);
        void followEvents(taskId, abort).finally(() => {
          if (following.get(taskId) === abort) following.delete(taskId);
        });
      }
      let released = false;
      return () => {
        if (released) return;
        released = true;
        const count = (shown.get(taskId) ?? 1) - 1;
        if (count > 0) {
          shown.set(taskId, count);
          return;
        }
        shown.delete(taskId);
        following.get(taskId)?.abort();
        following.delete(taskId);
        // No screen shows the task's events any more: they leave the window's memory after a while, and are read
        // again if it shows them again. A status that follows the task again at once keeps them.
        forgetting.set(
          taskId,
          setTimeout(() => {
            forgetting.delete(taskId);
            if (shown.has(taskId) || !(taskId in state.events)) return;
            const events = { ...state.events };
            delete events[taskId];
            set({ events });
          }, forget),
        );
      };
    },
    readPrompt,
    putQuestion(question) {
      const detail = state.details[question.wishId];
      if (!detail?.loadedKinds.has(Change.QUESTION)) return;
      const changes = create(WishChangesSchema, { questions: [question] });
      const next = apply(detail, changes, {});
      if (next !== detail)
        set({ details: { ...state.details, [question.wishId]: next } });
    },
    changed(wishId, changes) {
      for (const change of changes) {
        if (change === Change.WISH) lastReadAt.delete("wishes");
        else if (change === Change.PROJECT) lastReadAt.delete("projects");
        else if (change === Change.INBOX) lastReadAt.delete("inbox");
        else if (wishId && DETAIL.includes(change))
          lastReadAt.delete(`${wishId}:${change}`);
      }
      if (timer !== undefined) {
        clearTimeout(timer);
        timer = undefined;
        timerAt = 0;
      }
      mark(wishId, changes);
      return flush();
    },
    readMachine,
    readState,
    retryRead,
    trackRead,
  };
}
