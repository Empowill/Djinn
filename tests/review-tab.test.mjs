// The Review tab (src/review-tab.tsx): merged work and its checks. Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens } from "./screens.mjs";

const { s, h, live } = await screens();

test("the Review tab lists merged work, its merges under the task they serve, checks each, and counts what is left", async () => {
  const at = (seconds) => ({ seconds: BigInt(seconds), nanos: 0 });
  const merged = { state: s.IntegrationState.COMMITTED, sha: "abc" };
  const work = (code, extra) => ({
    id: code,
    code,
    wishId: "w",
    title: `Task ${code}`,
    status: s.TaskStatus.DONE,
    branch: `b-${code}`,
    dependsOn: [],
    createTime: at(1),
    integration: merged,
    ...extra,
  });
  const reviewed = (actor) => ({
    marks: [
      { kind: s.MarkKind.REVIEWED, actor, createTime: at(1_760_000_000) },
    ],
  });
  const tasks = [
    work("W1", {
      comments: [
        {
          id: "c1",
          file: "src/lamp.go",
          line: 3,
          endLine: 5,
          text: "Off by one",
        },
        {
          id: "c2",
          file: "src/lamp.go",
          line: 9,
          endLine: 9,
          text: "Done",
          resolved: true,
        },
      ],
    }),
    // Not merged: its tests failed; still running; an azima; a question worker, without a branch; a 0-commit task without a merge sha.
    work("W2", { integration: { state: s.IntegrationState.RED } }),
    work("W3", { status: s.TaskStatus.RUNNING, integration: undefined }),
    work("W4", { kind: s.TaskKind.AZIMA, branch: "" }),
    work("W5", { branch: "", integration: undefined }),
    // A correction of W1, merged: under W1.
    work("W6", { correction: { failure: { taskIds: ["W1"] }, attempt: 1 } }),
    // Merged and checked by the developer.
    work("W7", reviewed("local")),
    // A push correction serves no task: a row of its own.
    work("W8", { correction: { failure: { taskIds: [] }, attempt: 1 } }),
    work("W9", {
      integration: { state: s.IntegrationState.COMMITTED, sha: "" },
    }),
  ];
  const rows = s.reviewRows(tasks);
  assert.deepEqual(
    rows.map((r) => [r.task.code, r.merges.map((x) => x.code)]),
    [
      ["W1", ["W6"]],
      ["W7", []],
      ["W8", []],
    ],
  );
  assert.equal(s.reviewLeft(rows), 3);
  // W6 checked: W1's row still waits on W1 itself.
  const rowsChecked = s.reviewRows(
    tasks.map((x) => (x.code === "W6" ? { ...x, ...reviewed("local") } : x)),
  );
  assert.equal(s.reviewLeft(rowsChecked), 2);

  const html = s.renderToStaticMarkup(
    h(s.ReviewTab, {
      rows,
      fold: "w-review-test",
      codes: new Map(tasks.map((x) => [x.id, x.code])),
      onReview: async () => {},
    }),
  );
  // What is left first, the merge indented under the task it serves, marked; the comments counted; the checked folded.
  assert.match(
    html,
    /aria-checked="false"[^>]*aria-label="Mark W1 reviewed".*review-code">W1<.*Task W1<.*title="2 comments on its diff".*review-merges.*review-code">W6<\/span><span class="review-merge">Merge<\/span>.*review-code">W8</s,
  );
  assert.doesNotMatch(html, /review-code">W7</);
  assert.match(html, /Show 1 reviewed/);
  for (const code of ["W2", "W3", "W4", "W5", "W9"])
    assert.doesNotMatch(html, new RegExp(`review-code">${code}<`));

  // A click on the box checks the task; checked, it says who and when, and a click takes the mark off.
  const calls = [];
  const opened = [];
  const props = (task, open = false) => ({
    task,
    merge: false,
    open,
    codes: new Map(),
    onOpen: (id) => opened.push(id),
    onReview: async (x, remove) => calls.push([x.code, remove]),
  });
  const line = live(s.ReviewLine.type, props(tasks[0]));
  line.one("review-check").onClick();
  line.one("review-open").onClick();
  await line.settle();
  assert.deepEqual(calls, [["W1", false]]);
  assert.deepEqual(opened, ["W1"]);
  const checked = live(s.ReviewLine.type, props(tasks[6]));
  assert.match(
    checked.html(),
    /aria-checked="true"[^>]*aria-label="W7 reviewed: take the mark off"/,
  );
  assert.match(checked.html(), /class="review-by">you, /);
  checked.one("review-check").onClick();
  await checked.settle();
  assert.deepEqual(calls[1], ["W7", true]);
  // Opened: the comments on its lines, where they are, the resolved one struck; then its diff.
  const open = s.renderToStaticMarkup(h(s.ReviewLine, props(tasks[0], true)));
  assert.match(
    open,
    /review-comment-list"><li class=""><span class="review-where">src\/lamp.go:3-5<\/span><span>Off by one<\/span><\/li><li class="resolved"><span class="review-where">src\/lamp.go:9<\/span>.*task-diff-view/s,
  );

  // The tab sits after Tasks, with what is left to review.
  const tabs = s.renderToStaticMarkup(
    h(s.ViewTabs, {
      view: "review",
      main: "Wish",
      tasks: 8,
      review: 3,
      decisions: 0,
      onView() {},
    }),
  );
  assert.match(
    tabs,
    /id="view-tab-tasks".*id="view-tab-review" aria-selected="true" class="active"><span class="djinn-eye-perch" data-perch="tab:review" aria-hidden="true"><\/span>Review<span class="count">3<\/span>.*id="view-tab-decisions"/,
  );
  // The flying carpet has no Review tab.
  assert.doesNotMatch(
    s.renderToStaticMarkup(
      h(s.ViewTabs, {
        view: "main",
        main: "Flying carpet",
        tasks: 0,
        decisions: 0,
        onView() {},
      }),
    ),
    /view-tab-review/,
  );
});
