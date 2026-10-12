// Drafts belong to the connected store, so moving between wishes, or folding the pill, never loses a word.
import { useSyncExternalStore } from "react";

import { type Store } from "./data/store";

export interface InstructionDraft {
  text: string;
  busy: boolean;
  error: string;
  // What became of the last text: gone to the lead, or waiting in its terminal for the developer's own line.
  notice: "" | "sent" | "waiting";
}

const empty: InstructionDraft = {
  text: "",
  busy: false,
  error: "",
  notice: "",
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
    // begin locks the draft and gives the text to send, trimmed; nothing while a send runs or for a blank text.
    begin() {
      const text = state.text.trim();
      if (state.busy || !text) return;
      this.set({ busy: true, error: "", notice: "" });
      return text;
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
