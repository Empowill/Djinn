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

test("the default content tree is empty while every section stays closed", () => {
  const state = w.workspaceDefaultLayout(
    ["summary", "tasks", "decisions", "tasks", "rights"],
    "summary",
  );
  assert.deepEqual(ids(state), []);
  assert.deepEqual(state.closed, ["tasks", "decisions", "rights"]);
  assert.equal(state.version, 1);
});

test("normalizing storage removes the old summary leaf and keeps valid content splits", () => {
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
      closed: ["rights", "rights", "missing", "summary"],
      maximized: "missing",
    },
    ["summary", "tasks", "decisions", "rights"],
  );
  assert.deepEqual(ids(state), ["tasks"]);
  assert.deepEqual(state.closed, ["rights", "decisions"]);
  assert.equal(state.maximized, undefined);
  assert.equal(JSON.stringify(state).includes('"summary"'), false);
});

test("legacy composed storage aliases activity before the single-panel view reads it", () => {
  const state = w.normalizeWorkspaceLayout(
    {
      version: 1,
      root: {
        type: "split",
        direction: "horizontal",
        children: [
          { type: "panel", id: "tasks" },
          { type: "panel", id: "decisions" },
          { type: "panel", id: "journal" },
          { type: "panel", id: "instructions" },
        ],
      },
      closed: ["tilasms", "agents"],
      maximized: "journal",
    },
    ["summary", "tasks", "tilasms", "activity"],
  );
  assert.deepEqual(ids(state), ["tasks", "activity"]);
  assert.deepEqual(state.closed, ["tilasms"]);
  assert.equal(state.maximized, "activity");
  assert.equal(JSON.stringify(state).includes("decisions"), false);
  assert.equal(JSON.stringify(state).includes("journal"), false);
  assert.equal(JSON.stringify(state).includes("instructions"), false);
});

test("closing panels returns the tree to an empty content root", () => {
  let state = w.workspaceDefaultLayout(["summary", "tasks", "decisions"]);
  state = w.openWorkspacePanel(state, "tasks");
  state = w.closeWorkspacePanel(state, "tasks");
  assert.deepEqual(ids(state), []);
  assert.deepEqual(state.closed, ["decisions", "tasks"]);
  assert.equal(state.maximized, undefined);
});

test("keyboard movement handles all four sides and refuses an edge move", () => {
  let state = {
    version: 1,
    root: {
      type: "split",
      direction: "horizontal",
      children: [
        { type: "panel", id: "tasks" },
        { type: "panel", id: "decisions" },
        { type: "panel", id: "rights" },
      ],
    },
    closed: [],
  };

  const before = w.serializeWorkspaceLayout(state);
  assert.equal(
    w.serializeWorkspaceLayout(w.moveWorkspacePanel(state, "tasks", "left")),
    before,
  );

  state = w.moveWorkspacePanel(state, "tasks", "right");
  assert.deepEqual(ids(state), ["decisions", "tasks", "rights"]);
  state = w.moveWorkspacePanel(state, "tasks", "bottom");
  assert.deepEqual(ids(state), ["decisions", "rights", "tasks"]);
  state = w.moveWorkspacePanel(state, "tasks", "top");
  assert.deepEqual(ids(state), ["decisions", "tasks", "rights"]);
  state = w.moveWorkspacePanel(state, "tasks", "left");
  assert.deepEqual(ids(state), ["tasks", "decisions", "rights"]);
});

test("parsing malformed, old and empty storage falls back to a content-only default", () => {
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
