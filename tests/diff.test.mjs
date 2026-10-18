// The diff views (src/diff.tsx): unified and split, comments, chunks loaded as one scrolls, the uncommitted diff. Run
// with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, setupMockDom } from "./screens.mjs";

const { s, h } = await screens();

test("diff views render unified and split diffs with Chroma syntax tokens and hunk headers", () => {
  const hunks = [
    {
      header: "@@ -1,3 +1,4 @@",
      lines: [
        {
          kind: s.DiffLineKind.CONTEXT,
          oldLine: 1,
          newLine: 1,
          text: "package main",
          tokens: [
            { text: "package", category: "keyword" },
            { text: " main", category: "" },
          ],
        },
        {
          kind: s.DiffLineKind.DELETED,
          oldLine: 2,
          newLine: 0,
          text: "// old line",
          tokens: [{ text: "// old line", category: "comment" }],
        },
        {
          kind: s.DiffLineKind.ADDED,
          oldLine: 0,
          newLine: 2,
          text: "// new line",
          tokens: [{ text: "// new line", category: "comment" }],
        },
      ],
    },
  ];

  // 1. Unified view
  const unified = s.renderToStaticMarkup(h(s.UnifiedView, { hunks }));
  assert.match(unified, /diff-hunk-header/);
  assert.match(unified, /@@ -1,3 \+1,4 @@/);
  assert.match(unified, /line-context/);
  assert.match(unified, /line-deleted/);
  assert.match(unified, /line-added/);
  assert.match(unified, /tok-keyword/);
  assert.match(unified, /tok-comment/);
  assert.match(unified, />package</);
  assert.match(unified, />\+/);
  assert.match(unified, />-</);

  // 2. Split view
  const split = s.renderToStaticMarkup(h(s.SplitView, { hunks }));
  assert.match(split, /diff-split-table/);
  assert.match(split, /diff-split-row/);
  assert.match(split, /diff-split-side left/);
  assert.match(split, /diff-split-side right/);
  assert.match(split, /tok-keyword/);
  assert.match(split, /tok-comment/);
});

test("diff views render inline comments and a comment box; an ended task card offers to turn its waiting comments into work", () => {
  const hunks = [
    {
      header: "@@ -1,3 +1,4 @@",
      lines: [
        {
          kind: s.DiffLineKind.CONTEXT,
          oldLine: 1,
          newLine: 1,
          text: "package main",
        },
        {
          kind: s.DiffLineKind.ADDED,
          oldLine: 0,
          newLine: 2,
          text: '\tprintln("review this")',
        },
      ],
    },
  ];

  const comments = [
    {
      id: "c-1",
      file: "main.go",
      line: 2,
      endLine: 2,
      commit: "abcdef123456",
      text: "Please add a comment explaining this line.",
      resolved: false,
    },
    {
      id: "c-2",
      file: "main.go",
      line: 1,
      endLine: 2,
      commit: "abcdef123456",
      text: "Looks good overall.",
      resolved: true,
    },
  ];

  // 1. Unified view with comments
  const unified = s.renderToStaticMarkup(
    h(s.UnifiedView, { hunks, comments, filePath: "main.go" }),
  );
  assert.match(unified, /diff-comment-row/);
  assert.match(unified, /Please add a comment explaining this line\./);
  assert.match(unified, /Looks good overall\./);
  assert.match(unified, /diff-comment-resolved-badge/);
  assert.match(unified, /Resolved/);
  assert.match(unified, /diff-add-comment-btn/);

  // 2. CommentBox standalone
  const box = s.renderToStaticMarkup(
    h(s.CommentBox, {
      startLine: 2,
      endLine: 5,
      filePath: "main.go",
      onCancel() {},
    }),
  );
  assert.match(box, /diff-comment-box/);
  assert.match(box, /Comment on lines 2–5/);
  assert.match(box, /diff-comment-textarea/);
  assert.match(box, /diff-comment-btn-cancel/);
  assert.match(box, /diff-comment-btn-submit/);

  // A deleted line is in no version of the file a comment names: no comment button on it.
  const deleted = s.renderToStaticMarkup(
    h(s.UnifiedView, {
      hunks: [
        {
          header: "@@ -1 +0,0 @@",
          lines: [
            {
              kind: s.DiffLineKind.DELETED,
              oldLine: 1,
              newLine: 0,
              text: "gone",
            },
          ],
        },
      ],
      comments: [],
      filePath: "main.go",
    }),
  );
  assert.doesNotMatch(deleted, /diff-add-comment-btn/);

  // 3. The card of an ended task: the comments show in its diff; the card only offers to turn the waiting ones into
  // work, those that went to no worker and are not resolved.
  const card = (status, comments, onTurnComments = async () => {}) =>
    s.renderToStaticMarkup(
      h(s.WishTask, {
        task: {
          id: "t1",
          code: "W1",
          title: "Trim the wick",
          status,
          comments,
        },
        focused: true,
        onStop() {},
        onTurnComments,
      }),
    );
  const taskCard = card(s.TaskStatus.DONE, comments);
  assert.match(taskCard, /wish-task-turn-comments-button/);
  assert.match(taskCard, /Turn comments into work/);
  assert.doesNotMatch(taskCard, /Please add a comment explaining this line\./);
  const sent = comments.map((c) => ({ ...c, sentTo: "t2" }));
  assert.doesNotMatch(
    card(s.TaskStatus.DONE, sent),
    /wish-task-turn-comments-button/,
  );
  assert.doesNotMatch(
    card(s.TaskStatus.RUNNING, comments),
    /wish-task-turn-comments-button/,
  );
  assert.doesNotMatch(
    card(s.TaskStatus.CANCELLED, comments),
    /wish-task-turn-comments-button/,
  );
  assert.doesNotMatch(
    card(s.TaskStatus.DONE, comments, null),
    /wish-task-turn-comments-button/,
  );
});

test("a long diff loads its next chunk as the reader scrolls near its end, a hunk cut between chunks joined back", async () => {
  const line = (kind, n, text) => ({
    kind,
    oldLine: kind === s.DiffLineKind.ADDED ? 0 : n,
    newLine: kind === s.DiffLineKind.DELETED ? 0 : n,
    text,
    tokens: [],
  });
  const first = {
    path: "big.txt",
    totalLines: 9,
    offset: 0,
    nextOffset: 5,
    hunks: [
      {
        header: "@@ -1,4 +1,4 @@",
        lines: [
          line(s.DiffLineKind.CONTEXT, 1, "line one"),
          line(s.DiffLineKind.ADDED, 2, "line two"),
          line(s.DiffLineKind.ADDED, 3, "line three"),
        ],
      },
    ],
  };
  const second = {
    path: "big.txt",
    totalLines: 9,
    offset: 5,
    nextOffset: 0,
    hunks: [
      {
        header: "@@ -1,4 +1,4 @@",
        continued: true,
        lines: [line(s.DiffLineKind.ADDED, 4, "line four")],
      },
      {
        header: "@@ -20,1 +20,1 @@",
        lines: [line(s.DiffLineKind.ADDED, 20, "line twenty")],
      },
    ],
  };

  // appendChunk joins the hunk the second chunk carries on to the first chunk's last one.
  const joined = s.appendChunk(s.appendChunk(null, first), second);
  assert.deepEqual(
    joined.hunks.map((hk) => [hk.header, hk.lines.map((l) => l.text)]),
    [
      ["@@ -1,4 +1,4 @@", ["line one", "line two", "line three", "line four"]],
      ["@@ -20,1 +20,1 @@", ["line twenty"]],
    ],
  );
  assert.equal(joined.nextOffset, 0);

  const container = setupMockDom();
  const observers = [];
  const savedObserver = globalThis.IntersectionObserver;
  globalThis.IntersectionObserver = class {
    constructor(callback) {
      this.callback = callback;
      this.live = true;
      observers.push(this);
    }
    observe() {}
    disconnect() {
      this.live = false;
    }
  };
  const asked = [];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.TaskService, {
      diff: (req) => {
        if (req.file === "") {
          return { files: [{ path: "big.txt", added: 5 }], head: "abc" };
        }
        asked.push(req.offset);
        return { file: req.offset === 0 ? first : second, head: "abc" };
      },
    });
  });
  const djinn = s.createDjinn(transport, 10);
  // React sets a lone text child as textContent, and clears a root's: children first.
  const text = (node) =>
    node.childNodes.length > 0
      ? node.childNodes.map(text).join("")
      : (node.nodeValue ?? node.textContent ?? "");
  const count = (node, cls) =>
    (node.attributes?.class?.split(" ").includes(cls) ? 1 : 0) +
    node.childNodes.reduce((n, c) => n + count(c, cls), 0);
  const settle = async () => {
    for (let i = 0; i < 5; i++) {
      await s.act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });
    }
  };

  const root = s.createRoot(container);
  try {
    s.act(() => {
      root.render(
        h(
          s.DjinnProvider,
          { djinn },
          h(s.TaskDiffView, { task: { id: "t1", comments: [] } }),
        ),
      );
    });
    await settle();
    assert.deepEqual(asked, [0], "the first chunk alone at first");
    let shown = text(container);
    assert.match(shown, /line three/);
    assert.doesNotMatch(shown, /line four/);
    assert.match(shown, /5 of 9 lines shown/);
    assert.equal(count(container, "diff-next-chunk"), 1);

    // The end of the diff nears the view: the window asks for the next chunk, once.
    const live = observers.filter((o) => o.live);
    assert.equal(live.length, 1);
    await s.act(async () => {
      live[0].callback([{ isIntersecting: true }]);
      live[0].callback([{ isIntersecting: true }]);
    });
    await settle();
    assert.deepEqual(asked, [0, 5]);
    shown = text(container);
    assert.match(shown, /line four/);
    assert.match(shown, /line twenty/);
    assert.equal(count(container, "diff-hunk-header"), 2);
    assert.equal(
      count(container, "diff-next-chunk"),
      0,
      "nothing more to load",
    );
  } finally {
    s.act(() => root.unmount());
    globalThis.IntersectionObserver = savedObserver;
    for (const handle of process._getActiveHandles()) {
      if (handle?.constructor?.name === "MessagePort") handle.unref();
    }
  }
});

test("the uncommitted diff asks for the worktree's changes, read only, and says when the worktree is clean or gone", async () => {
  const container = setupMockDom();
  const asked = [];
  let answer = {};
  const transport = s.createRouterTransport(({ service }) => {
    service(s.TaskService, {
      diff: (req) => {
        asked.push([req.file, req.uncommitted]);
        if (req.file !== "") {
          return {
            file: {
              path: "notes.md",
              totalLines: 6,
              hunks: [
                {
                  header: "@@ -0,0 +1 @@",
                  lines: [
                    {
                      kind: s.DiffLineKind.ADDED,
                      newLine: 1,
                      text: "a draft",
                      tokens: [],
                    },
                  ],
                },
              ],
            },
            base: "abc",
          };
        }
        return answer;
      },
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const text = (node) =>
    node.childNodes.length > 0
      ? node.childNodes.map(text).join("")
      : (node.nodeValue ?? node.textContent ?? "");
  const count = (node, cls) =>
    (node.attributes?.class?.split(" ").includes(cls) ? 1 : 0) +
    node.childNodes.reduce((n, c) => n + count(c, cls), 0);
  const settle = async () => {
    for (let i = 0; i < 5; i++) {
      await s.act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });
    }
  };
  const root = s.createRoot(container);
  const show = async (key) => {
    s.act(() => {
      root.render(
        h(
          s.DjinnProvider,
          { djinn },
          h(s.TaskDiffView, {
            key,
            task: { id: "t1", comments: [] },
            uncommitted: true,
          }),
        ),
      );
    });
    await settle();
    return text(container);
  };
  try {
    answer = {
      files: [{ path: "notes.md", added: 1, status: s.DiffStatus.ADDED }],
      base: "abc",
    };
    const shown = await show("changed");
    assert.deepEqual(asked, [
      ["", true],
      ["notes.md", true],
    ]);
    assert.match(shown, /a draft/);
    assert.equal(count(container, "diff-line-row"), 1);
    // Work not committed has no commit a comment could name: no comment on it.
    assert.equal(count(container, "diff-add-comment-btn"), 0);

    answer = { files: [], base: "abc" };
    assert.match(
      await show("clean"),
      /The worktree is clean: nothing left uncommitted\./,
    );
    answer = { files: [], noWorktree: true };
    assert.match(await show("gone"), /This task has no worktree any more/);
  } finally {
    s.act(() => root.unmount());
    for (const handle of process._getActiveHandles()) {
      if (handle?.constructor?.name === "MessagePort") handle.unref();
    }
  }
});
