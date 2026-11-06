// The window's history of views (W491), as a browser keeps its pages: Alt+Left goes back to the view before, Alt+Right
// forward again (window-keys.ts). A view is where the window stands, in layers: the page (a wish, the flying carpet,
// the Hourglass, the Grimoire), the wish's tab, the tilasm open in its Tilasms tab, the tilasm open over the view. Each
// layer is told by the component that holds it (usePlace), which also knows how to go back to a value; a change of any
// layer, once a commit is done, is a new view, and a new view after going back drops the views ahead, as a browser
// does. Going back or forward sets each layer again from the top, a layer at a time, each waiting for the one above
// (a wish's tab is set once its page shows), each once: the views met on the way are not new views. A view the window
// cannot reach again (a wish deleted, a tilasm gone) stops the way where it is, after a short while, and a move of
// yours meanwhile is never undone.
// The questions are not views: the arrows move from one to the next (question-column.tsx), and each would be a view.
import { useEffect, useLayoutEffect, useRef } from "react";

export const LAYERS = ["page", "tab", "tilasm", "over"] as const;
export type Layer = (typeof LAYERS)[number];

// A view: a value per layer the window shows; a layer it does not show is left out.
export type Place = Partial<Record<Layer, string>>;

// What a component tells of its layer: its value, and how to set it again.
interface Part {
  value?: string;
  restore: (value: string | undefined) => void;
}

// How many views the history keeps, the oldest dropped first.
const KEPT = 100;
// How long a way back or forward waits for the window to reach its view, at most.
export const WAY_MS = 3000;

export function samePlace(a: Place, b: Place): boolean {
  return LAYERS.every((layer) => (a[layer] || "") === (b[layer] || ""));
}

export class ViewHistory {
  private entries: Place[] = [];
  private at = -1;
  private parts = new Map<Layer, Part>();
  private queued = false;
  // The view a way back or forward goes to, until the window shows it or the way gives up, and the layers it set
  // already: each once, a layer that does not follow (a tilasm being read) is waited for, never set again.
  private way?: { to: Place; set: Set<Layer> };

  constructor(
    // Wrapped: a browser's queueMicrotask called as a method of another object throws (Illegal invocation).
    private readonly later: (run: () => void) => void = (run) =>
      queueMicrotask(run),
    private readonly wait: (run: () => void, ms: number) => void = (run, ms) =>
      void setTimeout(run, ms),
  ) {}

  // set tells a layer's value; the history reads every layer once the changes of a commit are all told.
  set(layer: Layer, part: Part) {
    this.parts.set(layer, part);
    this.changed();
  }

  // unset tells the layer's holder is gone (its page closed), unless another took its place meanwhile.
  unset(layer: Layer, part: Part) {
    if (this.parts.get(layer) !== part) return;
    this.parts.delete(layer);
    this.changed();
  }

  // place is the view the window shows, by its layers.
  place(): Place {
    const place: Place = {};
    for (const layer of LAYERS) {
      const value = this.parts.get(layer)?.value;
      if (value) place[layer] = value;
    }
    return place;
  }

  // views and index say what the history holds and where it stands (the tests read them).
  views(): readonly Place[] {
    return this.entries;
  }
  index(): number {
    return this.at;
  }
  canBack(): boolean {
    return this.at > 0;
  }
  canForward(): boolean {
    return this.at < this.entries.length - 1;
  }

  // back goes to the view before, forward to the one after; each says whether there was one.
  back(): boolean {
    return this.canBack() && this.go(this.at - 1);
  }
  forward(): boolean {
    return this.canForward() && this.go(this.at + 1);
  }

  private go(at: number): boolean {
    // A change told but not read yet is a view of its own, before the way starts.
    if (this.queued) this.flush();
    this.at = at;
    const way = { to: this.entries[at], set: new Set<Layer>() };
    this.way = way;
    // A view the window cannot reach again: the way ends where the window stands, which is the view there now.
    this.wait(() => {
      if (this.way !== way) return;
      this.way = undefined;
      const place = this.place();
      if (place.page) this.entries[this.at] = place;
    }, WAY_MS);
    this.step();
    return true;
  }

  private changed() {
    if (this.queued) return;
    this.queued = true;
    this.later(() => this.flush());
  }

  // flush reads the view once a commit's changes are told: a new view, or a step on the way back or forward.
  flush() {
    this.queued = false;
    const place = this.place();
    if (this.way) {
      if (samePlace(place, this.way.to)) this.way = undefined;
      else this.step();
      return;
    }
    // No page yet (the wishes being read): no view.
    if (!place.page) return;
    if (this.at >= 0 && samePlace(place, this.entries[this.at])) return;
    this.entries = [...this.entries.slice(0, this.at + 1), place].slice(-KEPT);
    this.at = this.entries.length - 1;
  }

  // step sets the first layer, from the top, that differs from the view the way goes to: the layers below wait for it.
  private step() {
    const way = this.way;
    if (!way) return;
    for (const layer of LAYERS) {
      const part = this.parts.get(layer);
      if ((part?.value || "") === (way.to[layer] || "")) continue;
      // Not shown (a tab with no wish open): the layer above has not taken its place yet, or never will. Set already:
      // it may still come (a tilasm being read).
      if (!part || way.set.has(layer)) return;
      way.set.add(layer);
      part.restore(way.to[layer]);
      return;
    }
  }
}

// The window's history, one per page.
export const viewHistory = new ViewHistory();

// usePlace tells the window's history the value of a layer the component holds, while it is mounted, and how to set it
// again on the way back or forward.
export function usePlace(
  layer: Layer,
  value: string | undefined,
  restore: (value: string | undefined) => void,
  history: ViewHistory = viewHistory,
) {
  const latest = useRef(restore);
  useLayoutEffect(() => {
    latest.current = restore;
  });
  useEffect(() => {
    const part: Part = { value, restore: (v) => latest.current(v) };
    history.set(layer, part);
    return () => history.unset(layer, part);
  }, [history, layer, value]);
}
