// Drafts belong to the connected store, so moving between a card and its wish never loses a word.
// The request ID survives failed sends: a retry of a response lost in transit is the same write.
import { useSyncExternalStore } from "react";

import { type Store } from "./data/store";

export interface InstructionDraft {
  text: string;
  busy: boolean;
  error: string;
  notice: "" | "saved";
  requestId: string;
  requestText: string;
}

const empty: InstructionDraft = {
  text: "",
  busy: false,
  error: "",
  notice: "",
  requestId: "",
  requestText: "",
};
const drafts = new WeakMap<
  Store,
  Map<string, ReturnType<typeof createDraft>>
>();

export function createDraft() {
  let state = empty;
  const listeners = new Set<() => void>();
  return {
    get: () => state,
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    set(patch: Partial<InstructionDraft>) {
      state = { ...state, ...patch };
      listeners.forEach((listener) => listener());
    },
    begin() {
      const text = state.text.trim();
      if (state.busy || !text) return;
      const requestId =
        state.requestText === text && state.requestId
          ? state.requestId
          : crypto.randomUUID();
      this.set({
        busy: true,
        error: "",
        notice: "",
        requestText: text,
        requestId,
      });
      return { text, requestId };
    },
  };
}

export function useInstructionDraft(store: Store, wishId: string) {
  let wishes = drafts.get(store);
  if (!wishes) drafts.set(store, (wishes = new Map()));
  let draft = wishes.get(wishId);
  if (!draft) wishes.set(wishId, (draft = createDraft()));
  const state = useSyncExternalStore(draft.subscribe, draft.get, draft.get);
  return { draft, state };
}
