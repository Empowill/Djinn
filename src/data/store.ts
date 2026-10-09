// The page's copy of what djinn holds, read from the services and kept up to date by WishService.Watch. The watch
// says what changed; the store reads it again, a few reads at a time, and tells the screens. Nothing here is
// computed from the data: what the lamp computes (a wish's rank, whether it is ready) comes from the lamp.
import type { Gate, Machine } from "../../gen/ts/machine/v1/machine_pb";
import {
  type Block,
  Change,
  type InboxItem,
  type Project,
  type Question,
  type Task,
  type TaskEvent,
  type Wish,
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
  // Reads a wish's tasks, questions, blocks and tilasms, and again when they change, until the returned function is called.
  open(wishId: string): () => void;
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
};

export const emptyDetail = EMPTY_DETAIL;

export function createStore(clients: Clients, retry = 1000): Store {
  let state: State = {
    live: false,
    loaded: false,
    error: "",
    projects: [],
    wishes: [],
    details: {},
    inbox: [],
    events: {},
    gates: [],
  };
  const listeners = new Set<() => void>();
  const set = (patch: Partial<State>) => {
    state = { ...state, ...patch };
    listeners.forEach((listener) => listener());
  };

  // What to read again at the next flush.
  const pending = {
    wishes: false,
    projects: false,
    inbox: false,
    details: new Map<string, Set<Change>>(),
  };
  const opened = new Map<string, number>();
  let flushing: Promise<void> | undefined;
  let again = false;

  function mark(wishId: string, changes: readonly Change[]) {
    for (const change of changes) {
      if (change === Change.PROJECT) pending.projects = true;
      if (change === Change.WISH) pending.wishes = true;
      if (change === Change.INBOX) pending.inbox = true;
      if (
        change !== Change.TASK &&
        change !== Change.QUESTION &&
        change !== Change.BLOCK &&
        change !== Change.TILASM
      )
        continue;
      // Without a wish, every wish shown; a wish not shown is read when it is.
      const ids = wishId ? [wishId] : [...opened.keys()];
      for (const id of ids) {
        if (!opened.has(id)) continue;
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
    pending.wishes = pending.projects = pending.inbox = false;
    pending.details.clear();
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
    const read: Record<string, Partial<WishDetail>> = {};
    for (const [wishId, kinds] of details) {
      const into: Partial<WishDetail> = (read[wishId] = {});
      if (kinds.has(Change.TASK))
        attempt(async () => {
          into.tasks = (await clients.tasks.list({ wishId })).tasks;
        });
      if (kinds.has(Change.QUESTION))
        attempt(async () => {
          into.questions = (await clients.questions.list({ wishId })).questions;
        });
      if (kinds.has(Change.BLOCK))
        attempt(async () => {
          into.blocks = (await clients.blocks.list({ wishId })).blocks;
        });
      if (kinds.has(Change.TILASM))
        attempt(async () => {
          into.tilasms = (await clients.tilasms.list({ wish: wishId })).tilasms;
        });
    }
    await Promise.all(reads);
    if (Object.keys(read).length) {
      const next = { ...state.details };
      for (const [wishId, got] of Object.entries(read))
        next[wishId] = {
          ...(next[wishId] ?? EMPTY_DETAIL),
          ...got,
          loaded: true,
        };
      patch.details = next;
    }
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
              mark(res.wishId, res.changes);
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
    open(wishId) {
      opened.set(wishId, (opened.get(wishId) ?? 0) + 1);
      mark(wishId, [Change.TASK, Change.QUESTION, Change.BLOCK, Change.TILASM]);
      void flush();
      return () => {
        const count = (opened.get(wishId) ?? 1) - 1;
        if (count > 0) opened.set(wishId, count);
        else opened.delete(wishId);
      };
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
