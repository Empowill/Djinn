// The dialogs (src/wish-dialogs.tsx): a new project's folder, the global shortcut, a new wish, the sleep dialog. Run
// with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens } from "./screens.mjs";

const { s, h, live } = await screens();

test("the folder of a new project has a folder dialog's button only when the window has one", () => {
  const typed = s.renderToStaticMarkup(
    h(s.FolderField, { value: "/tmp/lamp", onChange() {} }),
  );
  assert.match(typed, /value="\/tmp\/lamp"/);
  assert.doesNotMatch(typed, /<button/);
  assert.doesNotMatch(typed, /Choose a folder…/);

  const native = s.renderToStaticMarkup(
    h(s.FolderField, { value: "", onChange() {}, onChoose() {} }),
  );
  assert.match(native, /<input/);
  assert.match(
    native,
    /<button type="button"[^>]*>.*Choose a folder…<\/button>/,
  );
});

test("the global shortcut is a field of the settings, disabled where djinn cannot take one", () => {
  const set = s.renderToStaticMarkup(
    h(s.ShortcutField, {
      shortcut: {
        chord: "Ctrl+Alt+Space",
        defaultChord: "Ctrl+Alt+Space",
        available: true,
        problem: "",
      },
      onSave: async () => {},
    }),
  );
  assert.match(set, /Global shortcut/);
  assert.match(set, /value="Ctrl\+Alt\+Space"/);
  assert.match(set, /Default: Ctrl\+Alt\+Space/);
  assert.doesNotMatch(set, /disabled/);
  assert.doesNotMatch(set, /Not working/);

  const refused = s.renderToStaticMarkup(
    h(s.ShortcutField, {
      shortcut: {
        chord: "Ctrl+Alt+J",
        defaultChord: "Ctrl+Alt+Space",
        available: true,
        problem: "another application holds it",
      },
      onSave: async () => {},
    }),
  );
  assert.match(refused, /Not working: another application holds it/);

  const browser = s.renderToStaticMarkup(
    h(s.ShortcutField, { shortcut: undefined, onSave: async () => {} }),
  );
  assert.match(browser, /<input[^>]*disabled=""/);
  assert.match(browser, /Only in Djinn&#x27;s window/);
});

test("MakeWish requires an active lead before a wish can be created", async () => {
  let resumed = 0;
  const page = live(s.MakeWish, {
    projects: [],
    active: 0,
    lead: undefined,
    onMade() {},
    onClose() {},
    onResumeLead: async () => {
      resumed++;
    },
  });

  // With no active lead, the creation screen shows the lead onboarding step and command.
  const withoutLeadHtml = page.html();
  assert.match(
    withoutLeadHtml,
    /No Djinn is running to receive this wish: start the Djinn first, or run djinn lead resume\./,
  );
  assert.match(withoutLeadHtml, /djinn lead resume/);
  assert.match(withoutLeadHtml, />Start the Djinn</);

  // Typing a wish keeps submission disabled until a lead is running.
  const findByProp = (pred) => {
    const found = [];
    const walk = (node) => {
      if (Array.isArray(node)) node.forEach(walk);
      else if (node && typeof node === "object" && node.props) {
        if (pred(node)) found.push(node);
        walk(node.props.children);
      }
    };
    walk(page.all("section")[0]);
    return found;
  };
  const composerNode = findByProp(
    (n) => typeof n.props.onChange === "function" && "invalid" in n.props,
  )[0];
  assert.ok(composerNode, "WishComposer mounted");
  composerNode.props.onChange("Build a flying carpet.", true);

  const submitButton = () =>
    findByProp((n) => n.props.type === "submit")[0].props;
  assert.equal(
    submitButton().disabled,
    true,
    "submit stays disabled while no lead is active",
  );

  // Starting the lead from New Wish runs the resume action, clears the warning, and unlocks submission.
  const resumeButton = page
    .all("button")
    .find((b) => b.props.children === "Start the Djinn");
  assert.ok(resumeButton, "Start the Djinn button rendered");
  resumeButton.props.onClick();
  await page.settle();
  assert.equal(resumed, 1);
  assert.doesNotMatch(page.html(), /No Djinn is running to receive this wish/);
  assert.equal(
    submitButton().disabled,
    false,
    "submit is enabled once the lead is started",
  );

  // When a lead is already active, no lead warning is shown and submission is enabled as soon as a prompt is typed.
  const activePage = live(s.MakeWish, {
    projects: [],
    active: 0,
    lead: {
      provider: s.Provider.CLAUDE_CODE,
      sessionId: "lead-session",
      directory: "/workspace/lamp",
    },
    onMade() {},
    onClose() {},
  });
  assert.doesNotMatch(
    activePage.html(),
    /No Djinn is running to receive this wish/,
  );
  assert.doesNotMatch(activePage.html(), /djinn lead resume/);

  // When the lead has exited with an error, MakeWish surfaces the exit trouble and blocks submission.
  const exitedPage = live(s.MakeWish, {
    projects: [],
    active: 0,
    lead: {
      provider: s.Provider.CLAUDE_CODE,
      sessionId: "lead-session",
      directory: "/workspace/lamp",
    },
    leadExit: { code: 1 },
    onMade() {},
    onClose() {},
  });
  assert.match(exitedPage.html(), /The Djinn ended \(code 1\)/);
  assert.match(exitedPage.html(), /djinn lead resume/);
});

test("the sleep dialog offers the wish's tasks not over and the requests not merged, and sends what you pick, all of it", () => {
  const task = (id, code, status, extra = {}) => ({
    id,
    code,
    title: `Task ${code}`,
    status,
    role: s.TaskRole.UNSPECIFIED,
    kind: s.TaskKind.WORK,
    ...extra,
  });
  const tasks = [
    task("t1", "W1", s.TaskStatus.RUNNING),
    task("t2", "W2", s.TaskStatus.DONE),
    task("t3", "W3", s.TaskStatus.CANCELLED),
    task("t4", "T4", s.TaskStatus.DONE, {
      kind: s.TaskKind.AZIMA,
      azima: { state: s.AzimaState.IN_PROGRESS },
    }),
    task("t5", "T5", s.TaskStatus.DONE, {
      kind: s.TaskKind.AZIMA,
      azima: { state: s.AzimaState.DONE },
    }),
    task("t6", "W6", s.TaskStatus.RUNNING, { role: s.TaskRole.INVESTIGATOR }),
    task("t7", "W7", s.TaskStatus.RUNNING, {
      babysit: { url: "https://github.com/acme/lamp/pull/7" },
    }),
    task("t8", "W8", s.TaskStatus.DONE, {
      babysit: {
        url: "https://github.com/acme/lamp/pull/8",
        doneTime: { seconds: 1n, nanos: 0 },
      },
    }),
  ];
  assert.deepEqual(
    s.sleepTasks(tasks).map((t) => t.code),
    ["W1", "T4"],
  );
  assert.deepEqual(
    s.sleepRequests(tasks).map((t) => t.code),
    ["W7"],
  );
  assert.deepEqual(
    s.sleepUntil(["W1", "T4"], ["W7"], "2026-10-20T09:00", " 3d, merged:W9 ,"),
    ["W1", "T4", "merged:W7", "2026-10-20T09:00", "3d", "merged:W9"],
  );
  assert.deepEqual(s.sleepUntil([], [], "", " "), []);
  const html = s.renderToStaticMarkup(
    h(s.SleepQuestionDialog, {
      question: { id: "q1", code: "Q01", text: "Release now?" },
      tasks,
      currentWishId: "w1",
      onClose: () => {},
      onSleep: async () => {},
    }),
  );
  assert.match(html, /Put Q01 to sleep/);
  assert.match(html, /<b>W1<\/b> Task W1/);
  assert.match(html, /<b>T4<\/b> Task T4/);
  assert.doesNotMatch(html, /<b>(?:W2|W3|T5|W6|W8)<\/b>/);
  assert.match(html, /<b>W7<\/b> https:\/\/github.com\/acme\/lamp\/pull\/7/);
  assert.match(html, /type="datetime-local"/);
  // Nothing picked: nothing to send.
  assert.match(
    html,
    /<button type="submit" class="button accent" disabled="">/,
  );
});
