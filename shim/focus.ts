// window.djinnFocus: what djinn asks the window to show (UiService.WatchShow), such as the wish and the lead's
// terminal that `djinn wish resume` takes back. One stream for the page; each part of the interface subscribes to it,
// and one that subscribes late still gets a request of the last minute.
import { type Transport, createClient } from "@connectrpc/connect";

import { WishService } from "../gen/ts/plan/v1/plan_pb";
import { UiService } from "../gen/ts/ui/v1/ui_pb";
import { wishToSession } from "./legacy-bridge";

export interface Focus {
  // The wish to show, and its title; empty for none.
  wishId: string;
  title: string;
  // The wish as the interface imports a mission (legacy-bridge), when it could be read.
  session?: unknown;
  // The terminal to show at the bottom of the window; empty for none.
  terminal: string;
}

export interface DjinnFocus {
  // Calls callback with each request to show something; at once with the last one if it is less than a minute old.
  subscribe(callback: (focus: Focus) => void): () => void;
}

declare global {
  interface Window {
    djinnFocus?: DjinnFocus;
  }
}

const RECENT = 60_000;

export function createFocus(transport: Transport, retry = 1000): DjinnFocus {
  const ui = createClient(UiService, transport);
  const wishes = createClient(WishService, transport);
  const listeners = new Set<(focus: Focus) => void>();
  let last: { focus: Focus; at: number } | undefined;
  let started = false;

  async function receive(wishId: string, terminal: string) {
    const focus: Focus = { wishId, title: "", terminal };
    if (wishId) {
      try {
        const snapshot = await wishes.snapshot({ wishId });
        focus.title = snapshot.export?.wish?.title ?? "";
        focus.session = wishToSession(snapshot);
      } catch {
        // The terminal still shows; the wish stays where it is.
      }
    }
    last = { focus, at: Date.now() };
    listeners.forEach((listener) => listener(focus));
  }

  async function watch() {
    for (;;) {
      try {
        for await (const res of ui.watchShow({}))
          if (res.wishId || res.terminal)
            await receive(res.wishId, res.terminal);
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
  });
}
