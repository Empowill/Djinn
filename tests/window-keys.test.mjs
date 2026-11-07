// The window's moves by the keyboard (src/window-keys.ts) and its history of views (src/view-history.ts), W491. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, wish } from "./screens.mjs";

const { s } = await screens();

const key = (k, mods = {}) => ({
  key: k,
  code: "",
  ctrlKey: false,
  metaKey: false,
  altKey: false,
  shiftKey: false,
  ...mods,
});

// Alt+Left and Alt+Right go back and forward, as in Chrome, on every system; on macOS Cmd+[ and Cmd+], Cmd+Left and
// Cmd+Right too. Anything else, Shift among them, moves nowhere.
test("the history's keys: Alt+arrows everywhere, Cmd+brackets and Cmd+arrows on macOS", () => {
  for (const mac of [false, true]) {
    assert.equal(s.historyStep(key("ArrowLeft", { altKey: true }), mac), -1);
    assert.equal(s.historyStep(key("ArrowRight", { altKey: true }), mac), 1);
    assert.equal(s.historyStep(key("ArrowLeft"), mac), 0);
    assert.equal(
      s.historyStep(key("ArrowLeft", { altKey: true, shiftKey: true }), mac),
      0,
    );
    assert.equal(
      s.historyStep(key("ArrowLeft", { altKey: true, ctrlKey: true }), mac),
      0,
    );
    assert.equal(s.historyStep(key("ArrowUp", { altKey: true }), mac), 0);
  }
  const cmd = (k, code = "") => key(k, { metaKey: true, code });
  assert.equal(s.historyStep(cmd("["), true), -1);
  assert.equal(s.historyStep(cmd("]"), true), 1);
  // A layout whose bracket is elsewhere: the key's place.
  assert.equal(s.historyStep(cmd("^", "BracketLeft"), true), -1);
  assert.equal(s.historyStep(cmd("$", "BracketRight"), true), 1);
  assert.equal(s.historyStep(cmd("ArrowLeft"), true), -1);
  assert.equal(s.historyStep(cmd("ArrowRight"), true), 1);
  assert.equal(
    s.historyStep(key("[", { metaKey: true, shiftKey: true }), true),
    0,
  );
  // Out of macOS, the Meta key moves nowhere: Ctrl+[ neither.
  assert.equal(s.historyStep(cmd("["), false), 0);
  assert.equal(s.historyStep(cmd("ArrowLeft"), false), 0);
  assert.equal(s.historyStep(key("[", { ctrlKey: true }), false), 0);
});

// Ctrl+Up and Ctrl+Down go to the wish above or below in the side panel's order: the active ones by rank, then the
// paused ones, the granted ones skipped; nothing past either end, and from no wish of the list the first or the last.
test("Ctrl+Up and Ctrl+Down go through the side panel's wishes, the granted ones skipped", () => {
  assert.equal(s.wishStep(key("ArrowUp", { ctrlKey: true })), -1);
  assert.equal(s.wishStep(key("ArrowDown", { ctrlKey: true })), 1);
  assert.equal(s.wishStep(key("ArrowDown")), 0);
  assert.equal(s.wishStep(key("ArrowDown", { altKey: true })), 0);
  assert.equal(
    s.wishStep(key("ArrowDown", { ctrlKey: true, shiftKey: true })),
    0,
  );
  assert.equal(
    s.wishStep(key("ArrowDown", { ctrlKey: true, metaKey: true })),
    0,
  );
  assert.equal(s.wishStep(key("ArrowLeft", { ctrlKey: true })), 0);

  const wishes = [
    wish("a1", "Ship the lamp", s.WishState.ACTIVE, 1),
    wish("g1", "Polish the brass", s.WishState.GRANTED, 0),
    wish("a2", "Trim the wick", s.WishState.ACTIVE, 2),
    wish("p1", "Fill the oil", s.WishState.PAUSED, 0),
    wish("g2", "Light the room", s.WishState.GRANTED, 0),
    wish("p2", "Clean the glass", s.WishState.PAUSED, 0),
  ];
  const order = s.wishOrder(wishes);
  assert.deepEqual(order, ["a1", "a2", "p1", "p2"]);
  assert.equal(s.nextWish(order, "a1", 1), "a2");
  assert.equal(
    s.nextWish(order, "a2", 1),
    "p1",
    "from the active to the paused",
  );
  assert.equal(s.nextWish(order, "p2", 1), undefined, "nothing past the last");
  assert.equal(
    s.nextWish(order, "a1", -1),
    undefined,
    "nothing before the first",
  );
  assert.equal(s.nextWish(order, "p1", -1), "a2");
  // A granted wish shown, or a page (the flying carpet): the first for Down, the last for Up.
  assert.equal(s.nextWish(order, "g1", 1), "a1");
  assert.equal(s.nextWish(order, "", -1), "p2");
  assert.equal(s.nextWish([], "", 1), undefined);
  // Out of a page (no DOM), nothing is a field.
  assert.equal(s.typingIn(null), false);
});

// A layer as a component holds it: its value, set again by the history on the way back or forward, as React would
// once the commit is done.
function layer(history, name, value) {
  const part = {
    value,
    restores: [],
    restore(v) {
      part.restores.push(v);
      part.set(v);
    },
    set(v) {
      history.unset(name, holder);
      holder = { value: v, restore: (x) => part.restore(x) };
      part.value = v;
      history.set(name, holder);
    },
    drop() {
      history.unset(name, holder);
    },
  };
  let holder = { value, restore: (x) => part.restore(x) };
  history.set(name, holder);
  return part;
}

// A history whose commits are flushed by hand, its way's time out run by hand too.
function history() {
  const queue = [];
  const timers = [];
  const h = new s.ViewHistory(
    (run) => queue.push(run),
    (run, ms) => timers.push({ run, ms }),
  );
  return {
    h,
    flush() {
      while (queue.length) queue.shift()();
    },
    // The ways' time outs come.
    timeout() {
      for (const { run, ms } of timers.splice(0)) {
        assert.equal(ms, s.WAY_MS);
        run();
      }
    },
  };
}

test("the history of views: a view per change, back and forward set each layer from the top, a new view drops the ones ahead", () => {
  const { h, flush } = history();
  // The wishes being read: no page, no view.
  const page = layer(h, "page", undefined);
  flush();
  assert.deepEqual(h.views(), []);
  assert.equal(h.back(), false);
  page.set("plan");
  const tab = layer(h, "tab", "main");
  flush();
  // One commit, one view, whatever the number of layers told.
  assert.deepEqual(h.views(), [{ page: "plan", tab: "main" }]);
  // A wish opened: its page, its tab told by its view mounting in the same commit.
  tab.drop();
  page.set("w1");
  const wishTab = layer(h, "tab", "main");
  flush();
  wishTab.set("tilasms");
  const tilasm = layer(h, "tilasm", "");
  flush();
  tilasm.set("t1");
  flush();
  assert.deepEqual(h.views(), [
    { page: "plan", tab: "main" },
    { page: "w1", tab: "main" },
    { page: "w1", tab: "tilasms" },
    { page: "w1", tab: "tilasms", tilasm: "t1" },
  ]);
  assert.equal(h.index(), 3);
  assert.equal(h.canForward(), false);
  assert.equal(h.forward(), false);

  // Back: the tilasm closes, the only layer that differs; no new view.
  assert.ok(h.back());
  assert.deepEqual(tilasm.restores, [undefined]);
  flush();
  assert.equal(h.index(), 2);
  assert.equal(h.views().length, 4);
  // Back twice more: the tab, then the page; the layers below wait for the one above.
  assert.ok(h.back());
  assert.deepEqual(wishTab.restores, ["main"]);
  tilasm.drop();
  flush();
  assert.ok(h.back());
  assert.deepEqual(page.restores, ["plan"]);
  wishTab.drop();
  flush();
  assert.deepEqual(h.place(), { page: "plan" });
  // The flying carpet's tabs mount on the commit after: its tab is set to the view's, and the way ends there.
  const planTab = layer(h, "tab", "decisions");
  flush();
  assert.deepEqual(planTab.restores, ["main"]);
  flush();
  assert.equal(h.index(), 0);
  assert.equal(h.canBack(), false);
  assert.equal(h.back(), false);

  // Forward again: the wish, then its tab, then its tilasm, a layer at a time.
  assert.ok(h.forward());
  assert.deepEqual(page.restores, ["plan", "w1"]);
  planTab.drop();
  const again = layer(h, "tab", "main");
  flush();
  assert.equal(h.index(), 1);
  assert.ok(h.forward());
  assert.deepEqual(again.restores, ["tilasms"]);
  const reopened = layer(h, "tilasm", "");
  flush();
  assert.equal(h.index(), 2);
  assert.ok(h.forward());
  assert.deepEqual(reopened.restores, ["t1"]);
  flush();
  assert.equal(h.index(), 3);

  // Back, then a view of your own: the views ahead are dropped, as in a browser.
  assert.ok(h.back());
  flush();
  again.set("tasks");
  reopened.drop();
  flush();
  assert.deepEqual(h.views().at(-1), { page: "w1", tab: "tasks" });
  assert.equal(h.views().length, 4);
  assert.equal(h.canForward(), false);

  assert.ok(s.samePlace({ page: "a", tab: "" }, { page: "a" }));
  assert.ok(!s.samePlace({ page: "a" }, { page: "a", over: "t" }));
  assert.deepEqual([...s.LAYERS], ["page", "tab", "tilasm", "over"]);
});

test("a view the window cannot reach again: the way sets each layer once, then stops where the window stands", () => {
  const { h, flush, timeout } = history();
  layer(h, "page", "w1");
  layer(h, "tab", "tilasms");
  const tilasm = layer(h, "tilasm", "t1");
  flush();
  tilasm.set("t2");
  flush();
  // Its first tilasm deleted since: setting it again changes nothing.
  tilasm.restore = (v) => tilasm.restores.push(v);
  assert.ok(h.back());
  assert.deepEqual(tilasm.restores, ["t1"]);
  // A commit meanwhile (the list read again): the layer is not set twice, and nothing is a view of its own.
  tilasm.set("t2");
  flush();
  assert.deepEqual(tilasm.restores, ["t1"]);
  assert.equal(h.views().length, 2);
  // Time out: the view there is the one the history stands on, and the next move is a view of its own.
  timeout();
  assert.equal(h.index(), 0);
  assert.deepEqual(h.views(), [
    { page: "w1", tab: "tilasms", tilasm: "t2" },
    { page: "w1", tab: "tilasms", tilasm: "t2" },
  ]);
  tilasm.set("t3");
  flush();
  assert.equal(h.index(), 1);
  assert.deepEqual(h.views().at(-1), {
    page: "w1",
    tab: "tilasms",
    tilasm: "t3",
  });
  // A way that arrives is not ended by its time out.
  assert.ok(h.back());
  tilasm.set("t2");
  flush();
  timeout();
  assert.equal(h.index(), 0);
  assert.equal(h.views().length, 2);
});

test("the history over a view: a tilasm opened over the page is a view, back closes it, forward opens it", () => {
  const { h, flush } = history();
  const page = layer(h, "page", "w1");
  const tab = layer(h, "tab", "main");
  const over = layer(h, "over", undefined);
  flush();
  over.set("t9");
  flush();
  assert.deepEqual(h.views(), [
    { page: "w1", tab: "main" },
    { page: "w1", tab: "main", over: "t9" },
  ]);
  assert.ok(h.back());
  assert.deepEqual(over.restores, [undefined]);
  flush();
  assert.ok(h.forward());
  assert.deepEqual(over.restores, [undefined, "t9"]);
  flush();
  assert.equal(h.index(), 1);
  assert.deepEqual(page.restores, []);
  assert.deepEqual(tab.restores, []);
});
