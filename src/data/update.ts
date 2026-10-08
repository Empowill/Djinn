// Whether a newer Djinn waits at the path of the running one (UiService.WatchUpdate), and the
// click that restarts on it (UiService.Update). Nothing restarts without that click.
import { type Transport, createClient } from "@connectrpc/connect";

import { UiService } from "../../gen/ts/ui/v1/ui_pb";

export interface UpdateState {
  // Version of the running Djinn.
  current: string;
  // Version of the newer Djinn waiting; empty for none.
  ready: string;
  // The terminals the last restart could not run again, one line each.
  notResumed: string[];
}

export interface DjinnUpdate {
  // Calls callback with the state at once when known, then at each change.
  subscribe(callback: (state: UpdateState) => void): () => void;
  // Restarts Djinn on the newer one. The page loses its server for a moment, then the new one answers.
  update(): Promise<{ version: string; terminals: number }>;
}


export function createUpdate(transport: Transport, retry = 1000): DjinnUpdate {
  const ui = createClient(UiService, transport);
  const listeners = new Set<(state: UpdateState) => void>();
  let last: UpdateState | undefined;
  let started = false;

  async function watch() {
    for (;;) {
      try {
        for await (const res of ui.watchUpdate({})) {
          last = {
            current: res.current,
            ready: res.ready,
            notResumed: [...res.notResumed],
          };
          listeners.forEach((listener) => listener(last!));
        }
      } catch {
        // Djinn restarted, or the link broke: watch again.
      }
      await new Promise((r) => setTimeout(r, retry));
    }
  }

  return Object.freeze({
    subscribe: (callback: (state: UpdateState) => void) => {
      listeners.add(callback);
      if (last) callback(last);
      if (!started) {
        started = true;
        void watch();
      }
      return () => void listeners.delete(callback);
    },
    update: async () => {
      const res = await ui.update({});
      return { version: res.version, terminals: res.terminals };
    },
  });
}
