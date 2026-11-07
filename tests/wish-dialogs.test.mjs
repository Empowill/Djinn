// The dialogs (src/wish-dialogs.tsx): a new project's folder, the global shortcut, a new wish, the sleep dialog. Run
// with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, setupMockDom } from "./screens.mjs";

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
    (n) => typeof n.props.onText === "function" && "invalid" in n.props,
  )[0];
  assert.ok(composerNode, "WishComposer mounted");
  composerNode.props.onText("Build a flying carpet.");

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

// W567: the new wish kept its request in its state, and rendered the whole page at each key. The request is the
// field's own: a key renders the page only when the request turns sendable, or too long; the wish made is what is typed.
test("a key typed in a new wish renders nothing of the page; the wish is made with the last text typed", async () => {
  const container = setupMockDom();
  const proto = Object.getPrototypeOf(container);
  proto.focus = proto.focus ?? function () {};
  // What the page asks of the document beyond the mock: the field it focuses, the reduced-motion query.
  globalThis.document.getElementById = () => null;
  globalThis.matchMedia = () => ({
    matches: true,
    addEventListener() {},
    removeEventListener() {},
  });
  const below = (el, out = []) => {
    for (const child of el.childNodes) {
      out.push(child);
      below(child, out);
    }
    return out;
  };
  const props = (el) =>
    el[Object.keys(el).find((k) => k.startsWith("__reactProps$"))];
  const settle = async () => {
    for (let i = 0; i < 4; i++)
      await s.act(() => new Promise((r) => setTimeout(r, 0)));
  };
  const made = [];
  const wish = { id: "w1", title: "Carpet" };
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, {
      make: (req) => {
        made.push(req.prompt);
        return { wish };
      },
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const done = [];
  let renders = 0;
  const root = s.createRoot(container);
  try {
    await s.act(async () =>
      root.render(
        h(
          s.DjinnProvider,
          { djinn },
          h(
            s.Profiler,
            { id: "page", onRender: () => renders++ },
            h(s.MakeWish, {
              projects: [],
              active: 0,
              lead: {
                provider: s.Provider.CLAUDE_CODE,
                sessionId: "lead-session",
                directory: "/workspace/lamp",
              },
              onMade: (w) => done.push(w.id),
              onClose() {},
            }),
          ),
        ),
      ),
    );
    await settle();
    const box = below(container).find((el) => el.nodeName === "TEXTAREA");
    assert.ok(box, "the request is open");
    const submit = () =>
      below(container).find(
        (el) => el.nodeName === "BUTTON" && el.attributes.type === "submit",
      );
    assert.equal(props(submit()).disabled, true, "nothing typed, nothing sent");
    // Typed as the browser does: the box holds the text, then React hears the change.
    const type = (value) =>
      s.act(() => {
        box.value = value;
        props(box).onChange({ target: box });
      });
    await type("B");
    await settle();
    assert.equal(props(submit()).disabled, false, "the first letter sends");
    const before = renders;
    for (const value of ["Bu", "Build", "Build a", "  Build a flying carpet. "])
      await type(value);
    await settle();
    assert.equal(renders, before, "the keys after the first render nothing");
    await s.act(async () =>
      props(below(container).find((el) => el.nodeName === "FORM")).onSubmit({
        preventDefault() {},
      }),
    );
    await settle();
    assert.deepEqual(made, ["Build a flying carpet."]);
    // The shape the page renders on: blank (spaces, format marks), to send, or over the API's limit.
    assert.equal(s.promptShape(" \n\u200b"), s.PromptShape.EMPTY);
    assert.equal(s.promptShape(" A carpet"), s.PromptShape.TEXT);
    assert.equal(s.promptShape("a".repeat(1_000_001)), s.PromptShape.TOO_LONG);
    assert.deepEqual(done, ["w1"]);
  } finally {
    s.act(() => root.unmount());
    delete globalThis.matchMedia;
    await new Promise((r) => setTimeout(r, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
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
