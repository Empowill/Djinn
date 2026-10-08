// What the page reaches of djinn, made once and handed to the screens through React context: the store of the plan,
// the terminals, what djinn asks the window to show, and the update.
import type { Transport } from "@connectrpc/connect";
import {
  type ReactNode,
  createContext,
  useContext,
  useEffect,
  useSyncExternalStore,
} from "react";

import { type Clients, createClients } from "./client";
import { type DjinnFocus, createFocus } from "./focus";
import {
  type State,
  type Store,
  type WishDetail,
  createStore,
  emptyDetail,
} from "./store";
import { type DjinnTerminal, createTerminal } from "./terminal";
import { type DjinnUpdate, createUpdate } from "./update";

export interface Djinn {
  store: Store;
  clients: Clients;
  terminal: DjinnTerminal;
  focus: DjinnFocus;
  update: DjinnUpdate;
}

export function createDjinn(transport: Transport, retry = 1000): Djinn {
  const clients = createClients(transport);
  return {
    store: createStore(clients, retry),
    clients,
    terminal: createTerminal(transport),
    focus: createFocus(transport, retry),
    update: createUpdate(transport, retry),
  };
}

const DjinnContext = createContext<Djinn | null>(null);

// DjinnProvider gives the screens djinn, and follows it while it is mounted.
export function DjinnProvider({
  djinn,
  children,
}: {
  djinn: Djinn;
  children: ReactNode;
}) {
  useEffect(() => djinn.store.start(), [djinn]);
  return (
    <DjinnContext.Provider value={djinn}>{children}</DjinnContext.Provider>
  );
}

// useDjinn is djinn, or null outside a DjinnProvider: a screen rendered alone, in a test.
export function useDjinn(): Djinn | null {
  return useContext(DjinnContext);
}

function useRequiredDjinn(): Djinn {
  const djinn = useContext(DjinnContext);
  if (!djinn) throw new Error("useDjinn: no DjinnProvider");
  return djinn;
}

// useData selects a part of the store's state; the screen renders again when that part changes.
export function useData<T>(select: (state: State) => T): T {
  const { store } = useRequiredDjinn();
  const read = () => select(store.getState());
  // The same snapshot on a server render, for the tests that render a screen to text.
  return useSyncExternalStore(store.subscribe, read, read);
}

// useWishDetail reads a wish's tasks, questions and blocks while the screen shows it.
export function useWishDetail(wishId: string): WishDetail {
  const { store } = useRequiredDjinn();
  useEffect(() => (wishId ? store.open(wishId) : undefined), [store, wishId]);
  return useData((state) => state.details[wishId] ?? emptyDetail);
}

const NO_EVENTS: never[] = [];

// useTaskEvents follows a task's events while the screen shows them. A new status follows it again: a worker
// started again sends more.
export function useTaskEvents(taskId: string, status: number, follow = true) {
  const { store } = useRequiredDjinn();
  useEffect(
    () => (follow ? store.follow(taskId) : undefined),
    [store, taskId, status, follow],
  );
  return useData((state) => state.events[taskId] ?? NO_EVENTS);
}

// useMachine reads the machine and its gates every few seconds while the screen shows them.
export function useMachine(every = 5000) {
  const { store } = useRequiredDjinn();
  useEffect(() => {
    void store.readMachine();
    const timer = setInterval(() => void store.readMachine(), every);
    return () => clearInterval(timer);
  }, [store, every]);
  return useData((state) => state.machine);
}

export function useClients(): Clients {
  return useRequiredDjinn().clients;
}

export function useStore(): Store {
  return useRequiredDjinn().store;
}
