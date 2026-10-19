// The tree of the files a task's diff changes (src/data/diff-tree.ts): grouped by folder, single-child chains
// compacted, each folder with its counts. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const f = await bundle("diff-tree", `export * from "@/src/data/diff-tree.ts";`);

const file = (path, added = 0, deleted = 0) => ({ path, added, deleted });
// shape is the tree in words: a folder as "name/ files +added -deleted" with its children, a file as its name.
const shape = (nodes) =>
  nodes.map((n) =>
    n.kind === "folder"
      ? [`${n.name}/ ${n.files} +${n.added} -${n.deleted}`, shape(n.children)]
      : n.name,
  );

test("files group by folder, folders first then files, each by name", () => {
  const tree = f.diffTree([
    file("src/b.ts", 1),
    file("README.md", 2),
    file("src/a.ts", 3),
    file("docs/guide.md", 4),
    file("src/data/x.ts", 5),
  ]);
  assert.deepEqual(shape(tree), [
    ["docs/ 1 +4 -0", ["guide.md"]],
    ["src/ 3 +9 -0", [["data/ 1 +5 -0", ["x.ts"]], "a.ts", "b.ts"]],
    "README.md",
  ]);
  // The diff shows the files in the tree's order.
  assert.deepEqual(
    f.diffFiles(tree).map((x) => x.path),
    ["docs/guide.md", "src/data/x.ts", "src/a.ts", "src/b.ts", "README.md"],
  );
});

test("a chain of folders that each hold one folder and no file reads as one row", () => {
  const tree = f.diffTree([
    file("internal/plan/store/sqlite.go", 10, 2),
    file("internal/plan/store/sqlite_test.go", 20),
    file("web/src/app/deep/one.ts", 1, 1),
  ]);
  assert.deepEqual(shape(tree), [
    ["internal/plan/store/ 2 +30 -2", ["sqlite.go", "sqlite_test.go"]],
    ["web/src/app/deep/ 1 +1 -1", ["one.ts"]],
  ]);
  // A compacted folder answers to its last folder's path.
  assert.deepEqual(
    tree.map((n) => n.path),
    ["internal/plan/store", "web/src/app/deep"],
  );

  // A folder that holds a file as well as a folder stops the chain.
  const split = f.diffTree([file("a/b/c/one.ts", 1), file("a/b/two.ts", 2)]);
  assert.deepEqual(shape(split), [
    ["a/b/ 2 +3 -0", [["c/ 1 +1 -0", ["one.ts"]], "two.ts"]],
  ]);
  // So do two folders.
  const fork = f.diffTree([file("a/x/one.ts"), file("a/y/two.ts")]);
  assert.deepEqual(shape(fork), [
    [
      "a/ 2 +0 -0",
      [
        ["x/ 1 +0 -0", ["one.ts"]],
        ["y/ 1 +0 -0", ["two.ts"]],
      ],
    ],
  ]);
});

test("a folder counts every file under it, at any depth, and their lines added and removed", () => {
  const tree = f.diffTree([
    file("src/a.ts", 3, 1),
    file("src/data/b.ts", 4, 2),
    file("src/data/deep/c.ts", 5, 0),
    { path: "src/logo.png", added: 0, deleted: 0, binary: true },
  ]);
  const [src] = tree;
  assert.equal(src.files, 4);
  assert.equal(src.added, 12);
  assert.equal(src.deleted, 3);
  const data = src.children[0];
  assert.equal(data.name, "data");
  assert.deepEqual([data.files, data.added, data.deleted], [2, 9, 2]);
});

test("the rows show what open folders hold; the row of a file read is its own, or its closed folder's", () => {
  const tree = f.diffTree([
    file("src/data/b.ts"),
    file("src/a.ts"),
    file("go.mod"),
  ]);
  const all = f.diffRows(tree, () => true);
  assert.deepEqual(
    all.map((r) => [r.node.name, r.depth, r.parent]),
    [
      ["src", 0, ""],
      ["data", 1, "src"],
      ["b.ts", 2, "src/data"],
      ["a.ts", 1, "src"],
      ["go.mod", 0, ""],
    ],
  );
  assert.equal(f.rowShowing(all, "src/data/b.ts"), 2);

  // data closed: its file shows on its row.
  const shut = f.diffRows(tree, (folder) => folder.path !== "src/data");
  assert.deepEqual(
    shut.map((r) => r.node.name),
    ["src", "data", "a.ts", "go.mod"],
  );
  assert.equal(f.rowShowing(shut, "src/data/b.ts"), 1);
  assert.equal(f.rowShowing(shut, "go.mod"), 3);
  // A folder whose name starts like another's holds none of its files.
  assert.equal(f.rowShowing(shut, "srcs/x.ts"), -1);

  // All closed: the top level is the overview.
  const top = f.diffRows(tree, () => false);
  assert.deepEqual(
    top.map((r) => r.node.name),
    ["src", "go.mod"],
  );
  assert.equal(f.rowShowing(top, "src/data/b.ts"), 0);
});
