// What djinn asks the window to show (UiService.WatchShow), such as the wish and the lead's terminal that `djinn wish
// resume` takes back, or the tilasm a djinn:// link names. One stream for the page; each part of the interface subscribes to it, and one that subscribes
// late still gets a request of the last minute.
import { type Transport, createClient } from "@connectrpc/connect";

import { UiService } from "../../gen/ts/ui/v1/ui_pb";

export interface Focus {
  // The wish to show; empty for none.
  wishId: string;
  // The terminal to show at the bottom of the window; empty for none.
  terminal: string;
  // The tilasm to show, in the wish's Tilasms tab; empty for none.
  tilasmId: string;
  // A link the window was asked to open and does not know, or whose tilasm or wish is not on this machine; empty for
  // none.
  unknownLink: string;
}

export interface DjinnFocus {
  // Calls callback with each request to show something; at once with the last one if it is less than a minute old.
  subscribe(callback: (focus: Focus) => void): () => void;
  // Asks the parts of the page to show something, as djinn would: a djinn:// link clicked in the page.
  show(focus: Partial<Focus>): void;
}

const none: Focus = { wishId: "", terminal: "", tilasmId: "", unknownLink: "" };

const RECENT = 60_000;

export function createFocus(transport: Transport, retry = 1000): DjinnFocus {
  const ui = createClient(UiService, transport);
  const listeners = new Set<(focus: Focus) => void>();
  let last: { focus: Focus; at: number } | undefined;
  let started = false;

  async function watch() {
    for (;;) {
      try {
        for await (const res of ui.watchShow({})) {
          if (!res.wishId && !res.terminal && !res.unknownLink) continue;
          const focus = {
            wishId: res.wishId,
            terminal: res.terminal,
            tilasmId: res.tilasmId,
            unknownLink: res.unknownLink,
          };
          last = { focus, at: Date.now() };
          listeners.forEach((listener) => listener(focus));
        }
      } catch {
        // djinn up restarted, or the link broke: watch again.
      }
      await new Promise((r) => setTimeout(r, retry));
    }
  }

  return Object.freeze({
    subscribe: (callback: (focus: Focus) => void) => {
      listeners.add(callback);
      if (last && Date.now() - last.at < RECENT) callback(last.focus);
      if (!started) {
        started = true;
        void watch();
      }
      return () => void listeners.delete(callback);
    },
    show: (focus: Partial<Focus>) => {
      const full = { ...none, ...focus };
      listeners.forEach((listener) => listener(full));
    },
  });
}
