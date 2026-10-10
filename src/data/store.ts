// The page's copy of what djinn holds, read from the services and kept up to date by WishService.Watch. The watch
// says what changed, and brings the tasks, questions and blocks that did: the store puts those in place of its own,
// keeping the others as they were, and reads the rest again, a few reads at a time, and tells the screens. Nothing
// here is computed from the data: what the lamp computes (a wish's rank, whether it is ready) comes from the lamp.
import {
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
  type InboxSource,
  type Project,
  type Question,
  QuestionSchema,
  type Task,
  type TaskEvent,
  TaskSchema,
  type Wish,
  type WishChanges,
} from "../../gen/ts/plan/v1/plan_pb";
import type { Tilasm } from "../../gen/ts/plan/v1/tilasm_pb";
import { type Clients, message, notFound } from "./client";

// What the page shows of one wish, read while a screen shows it.
export interface WishDetail {
  tasks: Task[];
  questions: Question[];
  blocks: Block[];
  // Its tilasms, by code.
  tilasms: Tilasm[];
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
  // The wishes and the projects were read at least once.
  loaded: boolean;
  // Why the last read failed; empty when it did not.
  error: string;
  projects: Project[];
  // As WishService.List gives them: the active ones by rank, then the paused ones, then the granted ones.
  wishes: Wish[];
  details: Readonly<Record<string, WishDetail>>;
  // The inbox items that wait for an answer, the newest first.
  inbox: InboxItem[];
  // The sources the projects' skills declare, plugged in or not on this machine.
  sources: InboxSource[];
  // The events of the tasks followed, oldest first.
  events: Readonly<Record<string, TaskEvent[]>>;
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
  // Loads the next page for a kind.
  loadMore(wishId: string, kind: Change): Promise<void>;
  // Follows a task's events until the returned function is called, or the task ends.
  follow(taskId: string): () => void;
  // Reads again what changed, as a write the page made says it: the watch says it too, a moment later.
  changed(wishId: string, changes: Change[]): Promise<void>;
  // Reads the machine and its gates.
  readMachine(): Promise<void>;
}

// The most events kept for a task: the oldest go first.
const MAX_EVENTS = 2000;
const EMPTY_DETAIL: WishDetail = {
  tasks: [],
  questions: [],
  blocks: [],
  tilasms: [],
  loaded: false,
  loadedKinds: new Set<Change>(),
};

export const emptyDetail = EMPTY_DETAIL;

// What a wish shows that a watch message may bring whole.
const CARRIED = new Set([Change.TASK, Change.QUESTION, Change.BLOCK]);
// What a wish shows, read again for it all.
const DETAIL = [Change.TASK, Change.QUESTION, Change.BLOCK, Change.TILASM];

// merge puts the entities that changed in place of theirs in list, and takes out those deleted. The others keep their
// place and their reference, and so does one that comes back equal: a screen that shows them draws them again only
// when they changed. A new one goes where order puts it, as the service lists them. Nothing changed: list itself.
function merge<Desc extends DescMessage>(
  schema: Desc,
  list: readonly MessageShape<Desc>[],
  changed: readonly MessageShape<Desc>[],
  deleted: ReadonlySet<string>,
  order: (a: MessageShape<Desc>, b: MessageShape<Desc>) => number,
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
    if (!next || equals(schema, old, next)) out.push(old);
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

export function createStore(clients: Clients, retry = 1000): Store {
  let state: State = {
    live: false,
    loaded: false,
    error: "",
    projects: [],
    wishes: [],
    details: {},
    inbox: [],
    sources: [],
    events: {},
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
  const loadingMore = new Set<string>();
  let flushing: Promise<void> | undefined;
  let again = false;

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
      if (changed && wishId && CARRIED.has(change)) continue;
      // Without a wish, every wish shown; a wish not shown is read when it is.
      const ids = wishId ? [wishId] : [...opened.keys()];
      for (const id of ids) {
        if (!opened.has(id)) continue;
        const detail = state.details[id];
        // Do not load hidden tabs! Only re-read if already loaded.
        const loaded = detail
          ? detail.loadedKinds.has(change)
          : pending.details.get(id)?.has(change);
        if (!loaded) continue;
        const kinds = pending.details.get(id) ?? new Set<Change>();
        kinds.add(change);
        pending.details.set(id, kinds);
      }
    }
  }

  async function read() {
    const wishes = pending.wishes;
    const projects = pending.projects;
    const inbox = pending.inbox;
    const details = [...pending.details];
    const changes = [...pending.changes];
    pending.wishes = pending.projects = pending.inbox = false;
    pending.details.clear();
    pending.changes.clear();
    const patch: Partial<State> = {};
    const errors: string[] = [];
    const reads: Promise<void>[] = [];
    const attempt = (run: () => Promise<void>) =>
      reads.push(run().catch((error) => void errors.push(message(error))));
    if (wishes)
      attempt(async () => {
        patch.wishes = (await clients.wishes.list({})).wishes;
      });
    if (projects)
      attempt(async () => {
        patch.projects = (await clients.projects.list({})).projects;
      });
    if (inbox)
      attempt(async () => {
        patch.inbox = (await clients.inbox.list({})).items;
      });
    if (inbox)
      attempt(async () => {
        patch.sources = (await clients.inbox.sources({})).sources;
      });
    const read: Record<string, Partial<WishDetail>> = {};
    for (const [wishId, kinds] of details) {
      const into: Partial<WishDetail> = (read[wishId] = {});
      if (kinds.has(Change.TASK))
        attempt(async () => {
          const res = await clients.tasks.list({ wishId });
          into.tasks = res.tasks;
          into.tasksTotal = res.total;
          into.tasksNextToken = res.nextPageToken;
        });
      if (kinds.has(Change.QUESTION))
        attempt(async () => {
          const res = await clients.questions.list({ wishId });
          into.questions = res.questions;
          into.questionsTotal = res.total;
          into.questionsNextToken = res.nextPageToken;
        });
      if (kinds.has(Change.BLOCK))
        attempt(async () => {
          const res = await clients.blocks.list({ wishId });
          into.blocks = res.blocks;
          into.blocksTotal = res.total;
          into.blocksNextToken = res.nextPageToken;
        });
      if (kinds.has(Change.TILASM))
        attempt(async () => {
          into.tilasms = (await clients.tilasms.list({ wish: wishId })).tilasms;
        });
    }
    await Promise.all(reads);
    const next = { ...state.details };
    let touched = false;
    for (const [wishId, got] of Object.entries(read)) {
      const prev = next[wishId] ?? EMPTY_DETAIL;
      const loadedKinds = new Set(prev.loadedKinds);
      const requested = details.find(([id]) => id === wishId)?.[1];
      if (requested) {
        for (const k of requested) loadedKinds.add(k);
      }
      next[wishId] = {
        ...prev,
        ...got,
        loaded: true,
        loadedKinds,
      };
      touched = true;
    }
    // What the watch brought goes on what was read: a wish not read yet has nothing to put it on, and reads it all.
    for (const [wishId, list] of changes) {
      const detail = next[wishId];
      if (!detail?.loaded) continue;
      const got = read[wishId] ?? {};
      next[wishId] = list.reduce((d, c) => apply(d, c, got), detail);
      touched ||= next[wishId] !== detail;
    }
    if (touched) patch.details = next;
    if (wishes && projects && !errors.length) patch.loaded = true;
    patch.error = errors[0] ?? "";
    set(patch);
  }

  // flush reads what is pending; a change that comes while it reads is read right after.
  function flush(): Promise<void> {
    if (flushing) {
      again = true;
      return flushing;
    }
    flushing = (async () => {
      try {
        do {
          again = false;
          await read();
        } while (again);
      } finally {
        flushing = undefined;
      }
    })();
    return flushing;
  }

  const following = new Map<
    string,
    { count: number; abort: AbortController }
  >();

  async function followEvents(taskId: string, abort: AbortController) {
    let buffer: TaskEvent[] = [];
    let timer: ReturnType<typeof setTimeout> | undefined;
    // Events come in bursts: the screen gets them together, at most every 50 ms.
    const push = () => {
      timer = undefined;
      if (!buffer.length) return;
      const kept = [...(state.events[taskId] ?? []), ...buffer].slice(
        -MAX_EVENTS,
      );
      buffer = [];
      set({ events: { ...state.events, [taskId]: kept } });
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

  return {
    clients,
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => void listeners.delete(listener);
    },
    start() {
      const abort = new AbortController();
      void (async () => {
        while (!abort.signal.aborted) {
          try {
            for await (const res of clients.wishes.watch(
              {},
              { signal: abort.signal },
            )) {
              if (!state.live) set({ live: true });
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
      return () => abort.abort();
    },
    open(wishId, kinds) {
      opened.set(wishId, (opened.get(wishId) ?? 0) + 1);
      const toLoad = kinds && kinds.length > 0 ? kinds : DETAIL;
      const detail = state.details[wishId];
      const pendingKinds = pending.details.get(wishId) ?? new Set<Change>();
      let added = false;
      for (const k of toLoad) {
        if (!detail?.loadedKinds.has(k)) {
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
      const detail = state.details[wishId];
      const needed = kinds.filter((k) => !detail?.loadedKinds.has(k));
      if (!needed.length) return;
      const pendingKinds = pending.details.get(wishId) ?? new Set<Change>();
      for (const k of needed) pendingKinds.add(k);
      pending.details.set(wishId, pendingKinds);
      await flush();
    },
    async loadMore(wishId, kind) {
      const key = `${wishId}:${kind}`;
      if (loadingMore.has(key)) return;
      const detail = state.details[wishId];
      if (!detail) return;
      loadingMore.add(key);
      try {
        if (kind === Change.QUESTION && detail.questionsNextToken) {
          const res = await clients.questions.list({
            wishId,
            pageToken: detail.questionsNextToken,
          });
          const cur = state.details[wishId];
          if (!cur) return;
          const questions = merge(
            QuestionSchema,
            cur.questions,
            res.questions,
            new Set(),
            byID,
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
          const res = await clients.tasks.list({
            wishId,
            pageToken: detail.tasksNextToken,
          });
          const cur = state.details[wishId];
          if (!cur) return;
          const tasks = merge(
            TaskSchema,
            cur.tasks,
            res.tasks,
            new Set(),
            byID,
          );
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
          });
        } else if (kind === Change.BLOCK && detail.blocksNextToken) {
          const res = await clients.blocks.list({
            wishId,
            pageToken: detail.blocksNextToken,
          });
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
      const current = following.get(taskId);
      if (current) current.count++;
      else {
        const entry = { count: 1, abort: new AbortController() };
        following.set(taskId, entry);
        void followEvents(taskId, entry.abort).finally(() => {
          if (following.get(taskId) === entry) following.delete(taskId);
        });
      }
      return () => {
        const entry = following.get(taskId);
        if (!entry) return;
        if (--entry.count > 0) return;
        entry.abort.abort();
        following.delete(taskId);
      };
    },
    changed(wishId, changes) {
      mark(wishId, changes);
      return flush();
    },
    async readMachine() {
      try {
        const [machine, gates] = await Promise.all([
          clients.machine.show({}),
          clients.gates.list({}),
        ]);
        set({ machine: machine.machine, gates: gates.gates });
      } catch (error) {
        set({ error: message(error) });
      }
    },
  };
}
