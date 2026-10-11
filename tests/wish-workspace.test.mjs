import assert from "node:assert/strict";
import test from "node:test";

import { bundle } from "./bundle.mjs";

const w = await bundle(
  "wish-workspace",
  `export {
  composeWorkspace,
  closeWorkspacePanel,
  maximizeWorkspacePanel,
  moveWorkspacePanel,
  normalizeWorkspaceLayout,
  openWorkspacePanel,
  parseWorkspaceLayout,
  restoreWorkspace,
  serializeWorkspaceLayout,
  workspaceDefaultLayout,
  workspacePanelIds,
  workspaceStorageKey,
} from "@/src/wish-workspace-layout.ts";
export {
  readWishWorkspace,
  writeWishWorkspace,
} from "@/src/wish-workspace-storage.ts";`,
);

const ids = (state) => w.workspacePanelIds(state.root);

test("a default layout keeps the summary open and every other panel closed", () => {
  const state = w.workspaceDefaultLayout(
    ["summary", "tasks", "decisions", "tasks", "rights"],
    "summary",
  );
  assert.deepEqual(ids(state), ["summary"]);
  assert.deepEqual(state.closed, ["tasks", "decisions", "rights"]);
  assert.equal(state.version, 1);
});

test("normalizing storage drops unknown and duplicate panels while preserving valid splits", () => {
  const state = w.normalizeWorkspaceLayout(
    {
      version: 1,
      root: {
        type: "split",
        direction: "horizontal",
        children: [
          { type: "panel", id: "summary" },
          {
            type: "split",
            direction: "vertical",
            children: [
              { type: "panel", id: "tasks" },
              { type: "panel", id: "tasks" },
              { type: "panel", id: "missing" },
            ],
          },
        ],
      },
      closed: ["rights", "rights", "missing"],
      maximized: "missing",
    },
    ["summary", "tasks", "decisions", "rights"],
  );
  assert.deepEqual(ids(state), ["summary", "tasks"]);
  assert.deepEqual(state.closed, ["rights", "decisions"]);
  assert.equal(state.maximized, undefined);
});

test("normalizing a valid tree without the pinned summary restores summary access", () => {
  const state = w.normalizeWorkspaceLayout(
    {
      version: 1,
      root: { type: "panel", id: "tasks" },
      closed: ["summary", "decisions"],
    },
    ["summary", "tasks", "decisions"],
  );
  assert.ok(ids(state).includes("summary"));
  assert.ok(!state.closed.includes("summary"));
  assert.deepEqual(ids(state), ["summary", "tasks"]);
});

test("composing a panel makes the requested horizontal or vertical split and reopens it", () => {
  let state = w.workspaceDefaultLayout([
    "summary",
    "tasks",
    "decisions",
    "rights",
  ]);
  state = w.openWorkspacePanel(state, "tasks");
  assert.deepEqual(ids(state), ["summary", "tasks"]);
  assert.equal(state.maximized, "tasks");

  state = w.composeWorkspace(state, "tasks", "summary", "bottom");
  assert.deepEqual(ids(state), ["summary", "tasks"]);
  assert.deepEqual(state.root, {
    type: "split",
    direction: "vertical",
    children: [
      { type: "panel", id: "summary" },
      { type: "panel", id: "tasks" },
    ],
  });
  assert.equal(state.maximized, undefined);
  assert.deepEqual(state.closed, ["decisions", "rights"]);

  state = w.composeWorkspace(state, "decisions", "tasks", "left");
  assert.deepEqual(ids(state), ["summary", "decisions", "tasks"]);
  assert.deepEqual(state.root, {
    type: "split",
    direction: "vertical",
    children: [
      { type: "panel", id: "summary" },
      {
        type: "split",
        direction: "horizontal",
        children: [
          { type: "panel", id: "decisions" },
          { type: "panel", id: "tasks" },
        ],
      },
    ],
  });
});

test("closing, maximizing and restoring keep closed panels isolated from the tree", () => {
  let state = w.workspaceDefaultLayout(["summary", "tasks", "decisions"]);
  state = w.openWorkspacePanel(state, "tasks");
  state = w.openWorkspacePanel(state, "decisions");
  state = w.maximizeWorkspacePanel(state, "tasks");
  assert.equal(state.maximized, "tasks");

  state = w.closeWorkspacePanel(state, "tasks");
  assert.deepEqual(ids(state), ["summary", "decisions"]);
  assert.deepEqual(state.closed, ["tasks"]);
  assert.equal(state.maximized, undefined);

  state = w.restoreWorkspace(state);
  assert.equal(state.maximized, undefined);
  state = w.closeWorkspacePanel(state, "summary");
  assert.deepEqual(ids(state), ["summary", "decisions"]);
  assert.deepEqual(state.closed, ["tasks"]);
});

test("keyboard movement handles all four sides and refuses an edge move", () => {
  let state = {
    version: 1,
    root: {
      type: "split",
      direction: "horizontal",
      children: [
        { type: "panel", id: "summary" },
        { type: "panel", id: "tasks" },
        { type: "panel", id: "decisions" },
      ],
    },
    closed: [],
  };

  const before = w.serializeWorkspaceLayout(state);
  assert.equal(
    w.serializeWorkspaceLayout(w.moveWorkspacePanel(state, "summary", "left")),
    before,
  );

  state = w.moveWorkspacePanel(state, "summary", "right");
  assert.deepEqual(ids(state), ["tasks", "summary", "decisions"]);
  state = w.moveWorkspacePanel(state, "summary", "bottom");
  assert.deepEqual(ids(state), ["tasks", "decisions", "summary"]);
  state = w.moveWorkspacePanel(state, "summary", "top");
  assert.deepEqual(ids(state), ["tasks", "summary", "decisions"]);
  state = w.moveWorkspacePanel(state, "summary", "left");
  assert.deepEqual(ids(state), ["summary", "tasks", "decisions"]);
});

test("parsing malformed or old storage falls back to a safe default", () => {
  for (const raw of ["{", JSON.stringify({ version: 99 }), "null", ""])
    assert.deepEqual(
      w.parseWorkspaceLayout(raw, ["summary", "tasks"]),
      w.workspaceDefaultLayout(["summary", "tasks"]),
    );
  assert.equal(w.workspaceStorageKey("wish-1"), "djinn.wish.workspace.wish-1");
});

test("workspace storage tolerates denied reads and writes without losing the layout", () => {
  const fallback = w.workspaceDefaultLayout(["summary", "tasks"]);
  const denied = {
    getItem() {
      throw new Error("blocked");
    },
    setItem() {
      throw new Error("quota");
    },
  };
  assert.deepEqual(
    w.readWishWorkspace("wish-1", ["summary", "tasks"], denied),
    fallback,
  );
  assert.doesNotThrow(() => w.writeWishWorkspace("wish-1", fallback, denied));

  const values = new Map();
  const storage = {
    getItem(key) {
      return values.get(key) ?? null;
    },
    setItem(key, value) {
      values.set(key, value);
    },
  };
  const open = w.openWorkspacePanel(fallback, "tasks");
  w.writeWishWorkspace("wish-1", open, storage);
  assert.deepEqual(
    w.readWishWorkspace("wish-1", ["summary", "tasks"], storage),
    open,
  );
  values.set(w.workspaceStorageKey("wish-1"), "not json");
  assert.deepEqual(
    w.readWishWorkspace("wish-1", ["summary", "tasks"], storage),
    fallback,
  );
});
