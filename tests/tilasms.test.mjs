// The tilasms (src/tilasms.tsx, src/tilasm-over.tsx): the tab, the frame, opened over a view, dropped files. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, setupMockDom, stamp, tilasm, wish } from "./screens.mjs";

const { s, h, live } = await screens();

test("the Tilasms tab lists each tilasm newest first with its code, title, author, date and what it cites, and opens one in a sandboxed frame", () => {
  const model = tilasm(
    "01a1223a-ae45-728f-8c37-c005eee91edb",
    "L01",
    "The objects in the database",
    {
      cites: ["t29", "gone-1234-5678"],
      author: "W12",
      versions: [
        {
          number: 1,
          author: "W12",
          files: 3,
          size: 2048n,
          restoredFrom: 0,
          createTime: stamp("2026-10-09T10:00:00Z"),
        },
        {
          number: 2,
          author: "lead",
          files: 1,
          size: 512n,
          restoredFrom: 0,
          createTime: stamp("2026-10-09T11:00:00Z"),
        },
        {
          number: 3,
          author: "developer",
          files: 3,
          size: 2048n,
          restoredFrom: 1,
          createTime: stamp("2026-10-09T12:00:00Z"),
        },
      ],
    },
  );
  const flows = tilasm("01a1223a-ae45-728f-8c37-c005eee91edc", "L02", "Flows");
  const recent = tilasm(
    "01a1223a-ae45-728f-8c37-c005eee91edd",
    "L03",
    "Latest sketch",
    {
      updateTime: stamp("2026-10-09T13:00:00Z"),
    },
  );
  const props = {
    tilasms: [flows, model, recent],
    count: 3,
    codes: new Map([["t29", "T29"]]),
    search: "",
    onSearch() {},
    onOpen() {},
    history: "",
    onHistory() {},
    onRestore() {},
    onExport() {},
    onDrop() {},
  };
  const html = s.renderToStaticMarkup(h(s.TilasmList, props));
  assert.match(html, /role="tabpanel" aria-labelledby="view-tab-tilasms"/);
  assert.match(
    html,
    /placeholder="Search the tilasms \(talismans\) of this wish"/,
  );
  assert.match(
    html,
    /class="tilasm-drop ".*Drop an \.html file, a folder with an index\.html, or a \.zip — or click to choose a file/,
  );
  assert.match(
    html,
    /<input type="file" accept="\.html,\.htm,\.zip,text\/html,application\/zip"/,
  );
  // Sorted from newest to oldest: L03 (13:00), then L01 (v3 at 12:00), then L02 (10:00).
  assert.ok(html.indexOf("L03") < html.indexOf("L01"));
  assert.ok(html.indexOf("L01") < html.indexOf("L02"));
  assert.match(
    html,
    /tilasm-title"[^>]*>The objects in the database<\/button>/,
  );
  assert.match(html, /W12 · /);
  assert.match(
    html,
    /Explains <span class="tilasm-code">T29<\/span><span class="tilasm-code">gone-123<\/span>/,
  );
  // Nothing open: no frame; the history folded.
  assert.doesNotMatch(html, /<iframe/);
  assert.doesNotMatch(html, /Restore this version/);

  // The drop zone reacts on dragover and forwards files picked from the file input.
  const picked = [];
  const view = live(s.TilasmList, {
    ...props,
    onDrop: (entries) => picked.push(entries),
  });
  view.one("tilasms").onDragOver({
    dataTransfer: { types: ["Files"] },
    preventDefault() {},
  });
  assert.match(view.html(), /class="tilasm-drop dropping"/);
  view.one("tilasms").onDragLeave({
    currentTarget: { contains: () => false },
    relatedTarget: null,
  });
  assert.doesNotMatch(view.html(), /dropping/);
  const input = view
    .all("input")
    .find((node) => node.props.type === "file").props;
  input.onChange({
    target: {
      files: [new File(["<h1>Lamp</h1>"], "lamp.html", { type: "text/html" })],
      value: "lamp.html",
    },
  });
  assert.equal(picked.length, 1);
  assert.equal(picked[0][0].name, "lamp.html");

  // Open: a frame served by Djinn at the tilasm's address, scripts only, never Djinn's origin.
  const open = s.renderToStaticMarkup(
    h(s.TilasmList, { ...props, open: model, history: model.id }),
  );
  const frame = open.match(/<iframe[^>]*>/)[0];
  assert.match(frame, /src="\/tilasm\/01a1223a-ae45-728f-8c37-c005eee91edb\/"/);
  assert.match(frame, /sandbox="allow-scripts"/);
  assert.doesNotMatch(frame, /allow-same-origin/);
  assert.match(frame, /title="L01 · The objects in the database"/);
  assert.match(
    open,
    /<section class="tilasm-view" aria-label="The objects in the database">.*v3/,
  );
  assert.match(open, /tilasm-row open/);
  // The history: the latest first, each with who and when, its files and size; an earlier one restores.
  const versions = open.match(/<ol class="tilasm-history"[\s\S]*?<\/ol>/)[0];
  assert.ok(versions.indexOf("v3") < versions.indexOf("v2"));
  assert.match(versions, /developer · .* · 3 files · 2 kB · restores v1/);
  assert.match(versions, /1 file · 512 byte/);
  assert.equal(versions.match(/Restore this version/g).length, 2);

  // A search that finds nothing says so; a wish without tilasms says how to make one.
  assert.match(
    s.renderToStaticMarkup(
      h(s.TilasmList, { ...props, tilasms: [], search: "lamp" }),
    ),
    /No tilasm holds “lamp”/,
  );
  assert.match(
    s.renderToStaticMarkup(
      h(s.TilasmList, { ...props, tilasms: [], count: 0 }),
    ),
    /<p class="empty-state-text">No tilasm yet\. Drop an \.html file or a folder with an index\.html here; I can put one too\.<\/p><p class="empty-state-text">From the command line: djinn tilasm put\.<\/p>/,
  );
});

test("a tilasm's draft comments are counted in gold on its head, its row and the Tilasms tab", () => {
  const model = tilasm("01a1223a-ae45-728f-8c37-c005eee91edb", "L01", "Model");
  const flows = tilasm("01a1223a-ae45-728f-8c37-c005eee91edc", "L02", "Flows");
  const draft = (id, tilasmId, code, state = 1) => ({
    id,
    tilasmId,
    wishId: "w1",
    code,
    text: `Text of ${code}`,
    state,
  });
  const props = {
    tilasms: [model, flows],
    drafts: [
      draft("01a14600-0000-7000-8000-000000000001", model.id, "C1"),
      draft("01a14600-0000-7000-8000-000000000002", model.id, "C2"),
      // Sent: no longer waits for the developer.
      draft("01a14600-0000-7000-8000-000000000003", model.id, "C3", 2),
    ],
    comments: { put() {}, remove() {}, putBack() {} },
    count: 2,
    codes: new Map(),
    search: "",
    onSearch() {},
    open: model,
    onOpen() {},
    history: "",
    onHistory() {},
    onRestore() {},
    onExport() {},
    onDrop() {},
  };
  const html = s.renderToStaticMarkup(h(s.TilasmList, props));
  // The head: a button that opens the column, with the count; the column stays closed until it is pressed.
  assert.match(
    html,
    /<button type="button" class="icon-button tilasm-comments-toggle[^"]*" aria-pressed="false" title="Comments · 2 draft comments"[^>]*>.*<span class="draft-count">2<\/span><\/button>/,
  );
  assert.doesNotMatch(html, /class="tilasm-comments"/);
  // The row of the tilasm with drafts has its gold dot; the other has none.
  const rows = html.match(/<li id="tilasm-[^"]*"[\s\S]*?<\/li>/g);
  const row = (id) => rows.find((r) => r.includes(`tilasm-${id}`));
  assert.match(
    row(model.id),
    /<span class="draft-dot" title="2 draft comments" aria-label="2 draft comments">2<\/span>/,
  );
  assert.doesNotMatch(row(flows.id), /draft-dot/);
  // Without what writes them, as over a view, no comments show.
  assert.doesNotMatch(
    s.renderToStaticMarkup(h(s.TilasmList, { ...props, comments: undefined })),
    /tilasm-comments-toggle/,
  );
  // The tab: the tilasms' count, then the drafts' gold dot; none without drafts.
  const tabs = (drafts) =>
    s.renderToStaticMarkup(
      h(s.ViewTabs, {
        view: "main",
        main: "Wish",
        tasks: 0,
        decisions: 0,
        tilasms: 2,
        drafts,
        onView() {},
      }),
    );
  assert.match(
    tabs(2),
    /id="view-tab-tilasms"[^>]*><span class="djinn-eye-perch" data-perch="tab:tilasms" aria-hidden="true"><\/span>Tilasms<span class="count">2<\/span><span class="draft-dot" title="2 draft comments" aria-label="2 draft comments">2<\/span><\/button>/,
  );
  assert.doesNotMatch(tabs(0), /draft-dot/);
});

test("the open tilasm goes full size and back, the same tilasm open, its frame never made again", () => {
  const model = tilasm("01a1223a-ae45-728f-8c37-c005eee91edb", "L01", "Model");
  const flows = tilasm("01a1223a-ae45-728f-8c37-c005eee91edc", "L02", "Flows");
  const opened = [];
  const view = live(s.TilasmList, {
    tilasms: [model, flows],
    count: 2,
    codes: new Map(),
    search: "",
    onSearch() {},
    open: model,
    onOpen: (id) => opened.push(id),
    history: "",
    onHistory() {},
    onRestore() {},
    onExport() {},
    onDrop() {},
  });
  const shown = () => view.one(s.TilasmFrame);
  // The frame as it renders, from what the tab gives it.
  const frame = (props = shown()) => live(s.TilasmFrame, props);
  const before = frame().all("iframe")[0];
  // Not full size: the button that enters it says so in words, with no tooltip; nothing leaves it.
  assert.equal(frame().one("tilasm-full-enter").title, undefined);
  assert.match(
    view.html(),
    /class="text-button tilasm-full-enter">.*Full size<\/button>/,
  );
  assert.match(view.html(), /<section class="tilasm-view"/);
  assert.doesNotMatch(view.html(), /tilasm-full-leave/);

  // The enter button asks the tab for full size.
  let asked;
  frame({ ...shown(), onFull: (on) => (asked = on) })
    .one("tilasm-full-enter")
    .onClick();
  assert.equal(asked, true);
  shown().onFull(true);
  const html = view.html();
  assert.match(html, /<section class="tilasm-view full"/);
  // The same frame, at the same place: React keeps it, so its page does not load again.
  const iframe = frame().all("iframe")[0];
  assert.equal(iframe.key, before.key);
  assert.equal(iframe.props.className, "tilasm-frame");
  assert.equal(iframe.props.src, before.props.src);
  assert.equal(iframe.props.sandbox, "allow-scripts");
  assert.doesNotMatch(html.match(/<iframe[^>]*>/)[0], /allow-same-origin/);
  // The room it left keeps the page's place; a small button leaves, named, with Escape as its key, and no tooltip.
  assert.match(
    html,
    /<\/iframe><div class="tilasm-frame-room"><\/div><button type="button" class="icon-button tilasm-full-leave" aria-label="Leave full size" aria-keyshortcuts="Escape">/,
  );
  assert.equal(frame().one("tilasm-full-leave").title, undefined);
  assert.match(html, /tilasm-row open/);

  frame({ ...shown(), onFull: (on) => (asked = on) })
    .one("tilasm-full-leave")
    .onClick();
  assert.equal(asked, false);
  shown().onFull(false);
  const back = view.html();
  assert.match(back, /<section class="tilasm-view"/);
  assert.doesNotMatch(back, /tilasm-full-leave/);
  assert.equal(frame().all("iframe")[0].key, before.key);
  // The same tilasm open, the same row marked: leaving full size opens and closes nothing.
  assert.deepEqual(opened, []);
  assert.deepEqual(
    view
      .all("tilasm-row")
      .filter((row) => /\bopen\b/.test(row.props.className))
      .map((row) => row.key),
    [model.id],
  );

  // Closing the tilasm while full ends full size: the next one opens in the page, and so does this one again.
  shown().onFull(true);
  frame()
    .all("icon-button")
    .find((button) => button.props["aria-label"] === "Close the tilasm")
    .props.onClick();
  assert.deepEqual(opened, [""]);
  view.update({ open: flows });
  assert.match(view.html(), /<section class="tilasm-view"/);
  view.update({ open: model });
  assert.match(view.html(), /<section class="tilasm-view"/);
});

test("a tilasm over a view is the same frame, full size from the start, with no head and a button that closes it", () => {
  const model = tilasm("01a1223a-ae45-728f-8c37-c005eee91edb", "L01", "Model");
  let closed = 0;
  const props = {
    tilasm: model,
    full: true,
    over: true,
    onFull() {},
    onClose: () => closed++,
  };
  const html = s.renderToStaticMarkup(h(s.TilasmFrame, props));
  // Laid over the view, it takes no room in it: no head, no room left in the page.
  assert.match(
    html,
    /^<section class="tilasm-view over full-over" aria-label="Model"><iframe/,
  );
  assert.doesNotMatch(
    html,
    /tilasm-view-head|tilasm-frame-room|tilasm-full-enter/,
  );
  // The tab's frame: scripts only, at the tilasm's address.
  const frame = html.match(/<iframe[^>]*>/)[0];
  assert.match(frame, /class="tilasm-frame"/);
  assert.match(frame, /src="\/tilasm\/01a1223a-ae45-728f-8c37-c005eee91edb\/"/);
  assert.match(frame, /sandbox="allow-scripts"/);
  assert.doesNotMatch(frame, /allow-same-origin/);
  assert.match(
    html,
    /<button type="button" class="icon-button tilasm-full-leave" aria-label="Close the tilasm" aria-keyshortcuts="Escape"><svg[^>]*lucide-x/,
  );
  live(s.TilasmFrame, props).one("tilasm-full-leave").onClick();
  assert.equal(closed, 1);
});

test("a tilasm link in a question opens over the view; elsewhere it opens its wish's tab", () => {
  const href = "djinn://tilasm/01a1223a-ae45-728f-8c37-c005eee91edb";
  const asked = [];
  const view = live(s.Link, { href, children: "the diagram" });
  const click = (inQuestion) => {
    const anchor = {
      closest: (sel) => (sel === ".question-card" && inQuestion ? {} : null),
    };
    let stopped = false;
    view.one("djinn-link").onClick({
      preventDefault() {},
      stopPropagation: () => (stopped = true),
      currentTarget: anchor,
    });
    return { anchor, stopped };
  };
  // Without the window around (no TilasmOver), nothing opens over.
  assert.equal(click(true).stopped, true);
  s.TilasmOverContext._currentValue = (link, from) => asked.push([link, from]);
  try {
    const { anchor, stopped } = click(true);
    assert.equal(stopped, true);
    assert.deepEqual(asked, [[href, anchor]]);
    // Out of a question, the link does as before: nothing opens over.
    click(false);
    assert.equal(asked.length, 1);
  } finally {
    s.TilasmOverContext._currentValue = undefined;
  }
});

test("closing gives the focus back to the link, else the same link drawn again in its question, else the question, never scrolling", () => {
  const href = "djinn://tilasm/01a1223a-ae45-728f-8c37-c005eee91edb";
  const focused = [];
  const el = (name, extra = {}) => ({
    name,
    isConnected: true,
    focus: (options) => focused.push([name, options]),
    ...extra,
  });
  const link = el("link");
  const again = el("again", { getAttribute: () => href });
  const other = el("other", { getAttribute: () => "djinn://wish/x" });
  let links = [other, again];
  const question = el("question", { querySelectorAll: () => links });
  const doc = {
    getElementById: (id) => (id === "question-q1" ? question : null),
  };
  const back = { link, href, question: "question-q1" };
  assert.equal(s.giveBack(back, doc), link);
  // The link drawn again: the same one in its question takes the focus; none there, the question.
  link.isConnected = false;
  assert.equal(s.giveBack(back, doc), again);
  links = [other];
  assert.equal(s.giveBack(back, doc), question);
  // The question gone too: nothing takes it.
  assert.equal(s.giveBack({ ...back, question: "question-q2" }, doc), null);
  assert.deepEqual(focused, [
    ["link", { preventScroll: true }],
    ["again", { preventScroll: true }],
    ["question", { preventScroll: true }],
  ]);
});

test("a tilasm opened from a question covers the view full size, which stays mounted; closing lands back on the link, focus and scroll kept; a click on the wish in the side panel closes it", async () => {
  const container = setupMockDom();
  const doc = globalThis.document;
  // What the frame, its cover and the focus need of a DOM, on the test's own.
  const proto = Object.getPrototypeOf(container);
  const classes = (el) => String(el.attributes?.class ?? "").split(" ");
  const matches = (el, selectors) =>
    el.nodeType === 1 &&
    selectors.split(",").some((one) => {
      const m =
        /^([a-z]*)((?:\.[\w-]+)*)(?:\[([\w-]+)(?:="([^"]*)")?\])?$/i.exec(
          one.trim(),
        );
      if (!m) throw new Error(`selector ${one}`);
      const [, tag, cls, attr, value] = m;
      if (tag && el.nodeName !== tag.toUpperCase()) return false;
      if (
        cls &&
        !cls
          .slice(1)
          .split(".")
          .every((c) => classes(el).includes(c))
      )
        return false;
      if (attr === "inert") return el.inert === true;
      if (attr)
        return value === undefined
          ? attr in el.attributes
          : el.attributes[attr] === value;
      return true;
    });
  const below = (el, out = []) => {
    for (const child of el.childNodes) {
      out.push(child);
      below(child, out);
    }
    return out;
  };
  const focused = [];
  Object.defineProperties(proto, {
    parentElement: {
      configurable: true,
      get() {
        return this.parentNode?.nodeType === 1 ? this.parentNode : null;
      },
    },
    isConnected: {
      configurable: true,
      get() {
        return doc.documentElement.contains(this);
      },
    },
    id: {
      configurable: true,
      get() {
        return this.attributes.id ?? "";
      },
    },
  });
  Object.assign(proto, {
    matches(selectors) {
      return matches(this, selectors);
    },
    closest(selectors) {
      if (matches(this, selectors)) return this;
      return this.parentNode?.nodeType === 1
        ? this.parentNode.closest(selectors)
        : null;
    },
    contains(node) {
      for (let at = node; at; at = at.parentNode) if (at === this) return true;
      return false;
    },
    querySelectorAll(selectors) {
      return below(this).filter((el) => matches(el, selectors));
    },
    getAttribute(name) {
      return this.attributes[name] ?? null;
    },
    focus(options) {
      if (this.closest("[inert]")) return;
      doc.activeElement = this;
      focused.push(options);
    },
  });
  doc.documentElement.appendChild(doc.body);
  doc.body.appendChild(container);
  doc.getElementById = (id) =>
    below(doc.documentElement).find((el) => el.id === id) ?? null;
  const keys = [];
  doc.addEventListener = (type, listener) =>
    type === "keydown" && keys.push(listener);
  doc.removeEventListener = (type, listener) =>
    keys.includes(listener) && keys.splice(keys.indexOf(listener), 1);
  const press = (key, target) => {
    const event = {
      key,
      target,
      defaultPrevented: false,
      preventDefault() {
        this.defaultPrevented = true;
      },
    };
    [...keys].forEach((listener) => listener(event));
    return event;
  };
  const props = (el) =>
    el[Object.keys(el).find((k) => k.startsWith("__reactProps$"))];
  const settle = async () => {
    for (let i = 0; i < 5; i++)
      await s.act(() => new Promise((r) => setTimeout(r, 0)));
  };

  const id = "01a1223a-ae45-728f-8c37-c005eee91edb";
  const href = `djinn://tilasm/${id}`;
  const shown = [];
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, {
      list: () => ({ wishes: [] }),
      watch: async function* () {},
    });
    service(s.TaskService, { list: () => ({ tasks: [] }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
    service(s.TilasmService, {
      get: (req) => ({
        tilasm:
          req.tilasm.ref.value === id ? tilasm(id, "L01", "Model") : undefined,
      }),
    });
  });
  // What the window is asked to show is kept here.
  const djinn = {
    ...s.createDjinn(transport, 10),
    focus: { subscribe: () => () => {}, show: (focus) => shown.push(focus) },
  };

  // The window's main shell, the wish clicked last in the side panel given (landing).
  const page = (landing) =>
    h(
      s.DjinnProvider,
      { djinn },
      h(
        "main",
        { className: "main-shell" },
        h(
          s.TilasmOver,
          { view: "plan", landing },
          h(
            "div",
            { className: "wish-view" },
            h(
              "div",
              { className: "mission-scroll" },
              h(
                "article",
                {
                  id: "question-q1",
                  className: "question-card question-row open current",
                  tabIndex: 0,
                },
                h("p", null, "Read it first: ", h(s.Link, { href }, "L01")),
                h("button", { type: "button" }, "Yes"),
              ),
            ),
          ),
          h("footer", { className: "app-statusbar" }),
        ),
      ),
    );
  const root = s.createRoot(container);
  try {
    s.act(() => root.render(page(undefined)));
    const main = container.children[0];
    const [view, footer] = main.children;
    const scroller = view.children[0];
    const question = doc.getElementById("question-q1");
    const link = question.querySelectorAll("a.djinn-link")[0];
    assert.ok(link);
    scroller.scrollTop = 240;
    question.focus({ preventScroll: true });
    focused.length = 0;

    for (const how of ["Escape", "button"]) {
      // The click on the link: it opens nothing in the page, the frame comes over the view.
      s.act(() =>
        props(link).onClick({
          preventDefault() {},
          stopPropagation() {},
          currentTarget: link,
        }),
      );
      await settle();
      const over = main.children.at(-1);
      assert.equal(over.nodeName, "SECTION");
      assert.equal(over.attributes.class, "tilasm-view over full-over");
      const frame = over.children[0];
      assert.equal(frame.nodeName, "IFRAME");
      assert.equal(frame.attributes.src, `/tilasm/${id}/`);
      assert.equal(frame.attributes.sandbox, "allow-scripts");
      const leave = over.querySelectorAll(".tilasm-full-leave")[0];
      // The view underneath stays mounted, out of the keyboard's reach, and unscrolled; the button that closes has the
      // focus.
      assert.equal(doc.getElementById("question-q1"), question);
      assert.equal(main.children[0], view);
      assert.equal(view.inert, true);
      assert.equal(footer.inert, true);
      assert.equal(over.inert, undefined);
      assert.equal(scroller.scrollTop, 240);
      assert.equal(doc.activeElement, leave);
      assert.deepEqual(shown, []);

      if (how === "Escape") {
        // Escape in a dialog is the dialog's.
        const dialog = doc.createElement("dialog");
        const inside = dialog.appendChild(doc.createElement("button"));
        assert.equal(press("Escape", inside).defaultPrevented, false);
        s.act(() => press("Escape", leave));
      } else s.act(() => props(leave).onClick());
      await settle();
      // Gone, the cover lifted, the focus on the link again, the page where it was.
      assert.equal(main.children.length, 2);
      assert.equal(over.isConnected, false);
      assert.equal(view.inert, false);
      assert.equal(footer.inert, false);
      assert.equal(doc.activeElement, link);
      assert.equal(doc.getElementById("question-q1"), question);
      assert.equal(scroller.scrollTop, 240);
      assert.deepEqual(keys, []);
    }
    // Every focus moved never scrolled.
    assert.ok(focused.length >= 4);
    assert.ok(focused.every((options) => options?.preventScroll === true));

    // The wish clicked in the side panel, even the one under the tilasm (a new landing): it closes, the cover lifted.
    s.act(() =>
      props(link).onClick({
        preventDefault() {},
        stopPropagation() {},
        currentTarget: link,
      }),
    );
    await settle();
    assert.equal(main.children.length, 3);
    assert.equal(view.inert, true);
    s.act(() => root.render(page({ wishId: "w1" })));
    await settle();
    assert.equal(main.children.length, 2);
    assert.equal(view.inert, false);
    assert.equal(footer.inert, false);
    // Opened again after it, it stays until the next click.
    s.act(() =>
      props(link).onClick({
        preventDefault() {},
        stopPropagation() {},
        currentTarget: link,
      }),
    );
    await settle();
    assert.equal(main.children.length, 3);
    s.act(() =>
      props(
        main.children.at(-1).querySelectorAll(".tilasm-full-leave")[0],
      ).onClick(),
    );
    await settle();
    assert.equal(main.children.length, 2);

    // A tilasm not on this machine: the window says so, and nothing covers the view.
    const gone = `djinn://tilasm/${"0".repeat(8)}-0000-7000-8000-${"0".repeat(12)}`;
    s.act(() =>
      root.render(
        h(
          s.DjinnProvider,
          { djinn },
          h(
            "main",
            { className: "main-shell" },
            h(
              s.TilasmOver,
              { view: "plan" },
              h(
                "div",
                { className: "wish-view" },
                h(
                  "article",
                  { id: "question-q2", className: "question-card" },
                  h(s.Link, { href: gone }, "gone"),
                ),
              ),
            ),
          ),
        ),
      ),
    );
    const lost = doc.getElementById("question-q2").querySelectorAll("a")[0];
    s.act(() =>
      props(lost).onClick({
        preventDefault() {},
        stopPropagation() {},
        currentTarget: lost,
      }),
    );
    await settle();
    assert.deepEqual(
      shown.map((f) => f.unknownLink),
      [gone],
    );
    assert.equal(container.children[0].querySelectorAll("section").length, 0);
  } finally {
    s.act(() => root.unmount());
    // React's act keeps a port open: it holds the test's process no more.
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});

test("a wish's view has a Tilasms tab beside Tasks and Decisions, with its count", async () => {
  const wishId = "01a11833-a440-7479-a067-52615c91da71";
  const lamp = wish(wishId, "Ship the lamp", s.WishState.ACTIVE, 1);
  const transport = s.createRouterTransport(({ service }) => {
    service(s.WishService, { list: () => ({ wishes: [lamp] }) });
    service(s.TaskService, { list: () => ({ tasks: [] }) });
    service(s.QuestionService, { list: () => ({ questions: [] }) });
    service(s.BlockService, { list: () => ({ blocks: [] }) });
    service(s.TilasmService, {
      list: (req) => ({
        tilasms: req.wish === wishId ? [tilasm("x1", "L01", "Model")] : [],
      }),
    });
  });
  const djinn = s.createDjinn(transport, 10);
  const close = djinn.store.open(wishId);
  await djinn.store.changed(wishId, [s.Change.TILASM]);
  close();
  assert.equal(djinn.store.getState().error, "");
  const html = s.renderToStaticMarkup(
    h(s.DjinnProvider, { djinn }, h(s.WishView, { wish: lamp, onToast() {} })),
  );
  assert.match(
    html,
    /id="view-tab-decisions".*id="view-tab-tilasms" aria-selected="false" class=""><span class="djinn-eye-perch" data-perch="tab:tilasms" aria-hidden="true"><\/span>Tilasms<span class="count">1<\/span>/,
  );
});

test("a folder, an .html file or a .zip dropped on the Tilasms tab is read with its files, hidden files left out", async () => {
  const file = (name, text) => ({
    name,
    isFile: true,
    isDirectory: false,
    file: (ok) => ok(new Blob([text])),
  });
  const folder = (name, children) => {
    let read = false;
    return {
      name,
      isFile: false,
      isDirectory: true,
      // readEntries gives a batch, then an empty one.
      createReader: () => ({
        readEntries: (ok) => ok(read ? [] : ((read = true), children)),
      }),
    };
  };
  const dropped = await s.readDrop([
    folder("Data model", [
      file("index.html", "<p>model</p>"),
      file(".DS_Store", "x"),
      folder("css", [file("a.css", "p{}")]),
    ]),
  ]);
  assert.equal(dropped.name, "Data model");
  assert.deepEqual(
    dropped.files.map((f) => [f.path, new TextDecoder().decode(f.content)]),
    [
      ["index.html", "<p>model</p>"],
      ["css/a.css", "p{}"],
    ],
  );
  const htmlFile = await s.readDrop([
    file("diagram.html", "<title>Lamp</title><p>diagram</p>"),
  ]);
  assert.equal(htmlFile.name, "diagram");
  assert.deepEqual(
    htmlFile.files.map((f) => [f.path, new TextDecoder().decode(f.content)]),
    [["index.html", "<title>Lamp</title><p>diagram</p>"]],
  );
  const htmFile = await s.readDrop(
    s.entriesOf({
      items: [],
      files: [new File(["<p>notes</p>"], "notes.HTM", { type: "text/html" })],
    }),
  );
  assert.equal(htmFile.name, "notes");
  assert.deepEqual(
    htmFile.files.map((f) => [f.path, new TextDecoder().decode(f.content)]),
    [["index.html", "<p>notes</p>"]],
  );
  const zip = await s.readDrop([file("model.zip", "PK")]);
  assert.equal(zip.name, "model.zip");
  assert.deepEqual(
    zip.files.map((f) => f.path),
    ["model.zip"],
  );
});

test("the tilasm comments foot sends Iterate or Launch in one click in its kind tint, names what the question's options start, and shows what left", async () => {
  const sent = [];
  const send = async (choice, note) => {
    sent.push([choice, note]);
  };
  // Without an open question's mockup option, Iterate goes to the lead in gold; Launch builds in green.
  const leadFoot = live(s.SendBar, { drafts: 2, send });
  const leadHtml = leadFoot.html();
  assert.doesNotMatch(leadHtml, /tilasm-send-confirm/);
  assert.match(
    leadHtml,
    /<textarea rows="2" maxLength="8000" aria-label="A comment on the whole tilasm, sent with the others"/,
  );
  assert.match(
    leadHtml,
    /<button type="button" class="button secondary small kind-lead" title="Ask the Djinn for the next version of this tilasm, with your comments\."><svg[^>]*lucide-lamp[\s\S]*?>Iterate · lead<\/button>/,
  );
  assert.match(
    leadHtml,
    /<button type="button" class="button accent small kind-work" title="Launch the work this tilasm explains, with your comments\. Without a comment: yes to the question that links it, with its recommended option\."><svg[^>]*lucide-hammer[\s\S]*?>Launch<\/button>/,
  );
  leadFoot.one("kind-lead").onClick();
  await leadFoot.settle();
  assert.deepEqual(sent, [[s.SendChoice.ITERATE, ""]]);
  assert.match(
    leadFoot.html(),
    /<p class="tilasm-send-hold">Send 1 left for the Djinn: it makes the next version\.<\/p>/,
  );

  // With TilasmSends from a question that cites the tilasm, Iterate is purple (kind-mock) and both tooltips name the option and question.
  const sends = {
    tilasmId: "l25",
    question: "Q25",
    iterateOption: "Iterate on the mockup",
    round: 1,
    rounds: 3,
    launchOption: "Proposal A",
    launchKind: "work",
    launchTitle: "Build the answer buttons",
  };
  const mockFoot = live(s.SendBar, { drafts: 1, sends, send });
  const mockHtml = mockFoot.html();
  assert.match(
    mockHtml,
    /<button type="button" class="button secondary small kind-mock" title="Starts the mockup worker on Iterate on the mockup, with your comments; puts the next version and revises Q25\."><svg[^>]*lucide-pencil-ruler[\s\S]*?>Iterate<\/button>/,
  );
  assert.match(
    mockHtml,
    /<button type="button" class="button accent small kind-work" title="Starts the work task \(Build the answer buttons\) on Proposal A of Q25, with your comments\."><svg[^>]*lucide-hammer[\s\S]*?>Launch<\/button>/,
  );
  mockFoot.one("textarea").onChange({
    target: { value: "Keep 4.5:1 contrast" },
    currentTarget: { style: {}, scrollHeight: 48 },
  });
  mockFoot.one("kind-work").onClick();
  await mockFoot.settle();
  assert.deepEqual(sent.at(-1), [s.SendChoice.LAUNCH, "Keep 4.5:1 contrast"]);
  assert.match(
    mockFoot.html(),
    /<p class="tilasm-send-hold">Send 1 left for the Djinn: it launches the work\.<\/p>/,
  );
});

test("the comments column gives the open creation form its own class and disables Edit, Delete, Reattach and On the whole tilasm while a save is in flight", async () => {
  const container = setupMockDom();
  const proto = Object.getPrototypeOf(container);
  proto.focus = proto.focus ?? function () {};
  const below = (el, out = []) => {
    for (const child of el.childNodes) {
      out.push(child);
      below(child, out);
    }
    return out;
  };
  const text = (node) =>
    node.childNodes.length > 0
      ? node.childNodes.map(text).join("")
      : (node.nodeValue ?? node.textContent ?? "");
  const props = (el) =>
    el[Object.keys(el).find((k) => k.startsWith("__reactProps$"))];

  const l01 = "01a1223a-ae45-728f-8c37-c005eee91edb";
  const c1 = {
    id: "01a14600-0000-7000-8000-000000000001",
    tilasmId: l01,
    code: "C1",
    text: "First note.",
    state: s.CommentState.DRAFT,
    anchor: {
      page: "index.html",
      exact: "old quote",
      status: s.AnchorStatus.LOST,
    },
  };
  let finishPut;
  const writes = {
    put: () =>
      new Promise((resolve) => {
        finishPut = resolve;
      }),
    remove: async () => {},
    send: async () => {},
  };
  const root = s.createRoot(container);
  try {
    await s.act(async () => {
      root.render(
        h(s.CommentsColumn, {
          drafts: [c1],
          sent: [],
          reads: [],
          tasks: [],
          placed: [],
          active: "",
          reattaching: "",
          onActive() {},
          onReattach() {},
          onWriting() {},
          onClose() {},
          onError() {},
          writes,
        }),
      );
    });

    // Open the new-comment form via "On the whole tilasm": it uses .tilasm-comment-new, not .tilasm-comment.
    const wholeBtn = below(container).find(
      (el) =>
        el.nodeName === "BUTTON" && text(el).includes("On the whole tilasm"),
    );
    await s.act(async () => {
      props(wholeBtn).onClick();
    });
    const lis = below(container).filter((el) => el.nodeName === "LI");
    assert.equal(
      lis.filter((el) => el.attributes.class === "tilasm-comment-new").length,
      1,
    );
    assert.equal(
      lis.filter((el) =>
        String(el.attributes.class ?? "")
          .split(" ")
          .includes("tilasm-comment"),
      ).length,
      1,
      "only saved/draft cards have class tilasm-comment",
    );

    // Type and click Save: while writes.put is in flight (busy), Edit C1, Delete C1, Reattach and On the whole tilasm are disabled.
    const textarea = below(container).find((el) => el.nodeName === "TEXTAREA");
    await s.act(async () => {
      props(textarea).onChange({
        target: { value: "Second note." },
        currentTarget: { style: {}, scrollHeight: 48 },
      });
    });
    const saveBtn = below(container).find(
      (el) => el.nodeName === "BUTTON" && text(el).includes("Save"),
    );
    let saving;
    await s.act(async () => {
      saving = props(saveBtn).onClick();
    });
    const buttonDisabled = (label) => {
      const btn = below(container).find(
        (el) =>
          el.nodeName === "BUTTON" &&
          (el.attributes["aria-label"] === label || text(el).includes(label)),
      );
      return Boolean(props(btn)?.disabled);
    };
    assert.equal(buttonDisabled("Edit C1"), true);
    assert.equal(buttonDisabled("Delete C1"), true);
    assert.equal(buttonDisabled("Reattach"), true);
    assert.equal(buttonDisabled("On the whole tilasm"), true);

    await s.act(async () => {
      finishPut({
        id: "01a14600-0000-7000-8000-000000000002",
        tilasmId: l01,
        code: "C2",
        text: "Second note.",
        state: s.CommentState.DRAFT,
      });
      await saving;
    });
    assert.equal(
      below(container).filter(
        (el) =>
          el.nodeName === "LI" && el.attributes.class === "tilasm-comment-new",
      ).length,
      0,
    );
    assert.equal(buttonDisabled("Edit C1"), false);
  } finally {
    s.act(() => root.unmount());
    await new Promise((r) => setTimeout(r, 30));
    for (const handle of process._getActiveHandles())
      if (handle?.constructor?.name === "MessagePort") handle.unref();
  }
});

test("a row publishes its tilasm on claude.ai when the machine can, asks first the first time, and links it once published", () => {
  const two = [
    {
      number: 1,
      author: "lead",
      files: 1,
      size: 512n,
      restoredFrom: 0,
      createTime: stamp("2026-10-09T10:00:00Z"),
    },
    {
      number: 2,
      author: "lead",
      files: 1,
      size: 512n,
      restoredFrom: 0,
      createTime: stamp("2026-10-09T11:00:00Z"),
    },
  ];
  const fresh = tilasm(
    "01a1223a-ae45-728f-8c37-c005eee91ee1",
    "L01",
    "Never published",
  );
  const current = tilasm(
    "01a1223a-ae45-728f-8c37-c005eee91ee2",
    "L02",
    "Up to date",
    {
      versions: two,
      artifact: {
        url: "https://claude.ai/artifact/D1gnn",
        version: 2,
        artifactVersion: 3,
      },
    },
  );
  const behind = tilasm(
    "01a1223a-ae45-728f-8c37-c005eee91ee3",
    "L03",
    "A version behind",
    {
      versions: two,
      artifact: {
        url: "https://claude.ai/artifact/EG3S4",
        version: 1,
        artifactVersion: 1,
      },
    },
  );
  const props = {
    tilasms: [fresh, current, behind],
    count: 3,
    codes: new Map(),
    search: "",
    onSearch() {},
    onOpen() {},
    history: "",
    onHistory() {},
    onRestore() {},
    onExport() {},
    onDrop() {},
  };
  const rows = (html) =>
    Object.fromEntries(
      [...html.matchAll(/<li id="tilasm-([^"]+)"[^>]*>(.*?)<\/li>/g)].map(
        (m) => [m[1], m[2]],
      ),
    );

  // The machine cannot publish: no button, but a published tilasm keeps its link and its state.
  let html = rows(s.renderToStaticMarkup(h(s.TilasmList, props)));
  assert.doesNotMatch(html[fresh.id], /Publish on claude\.ai/);
  assert.match(
    html[current.id],
    /href="https:\/\/claude\.ai\/artifact\/D1gnn"[^>]*>.*On claude\.ai/,
  );
  assert.match(html[current.id], /class="tilasm-artifact-state ">Up to date</);
  assert.match(
    html[behind.id],
    /class="tilasm-artifact-state waiting">v1 published, v2 waiting</,
  );
  assert.doesNotMatch(
    html[behind.id],
    /aria-label="Publish the latest version/,
  );

  // It can: the tilasm never published offers to publish, the one behind to publish again, the one up to date nothing.
  html = rows(
    s.renderToStaticMarkup(h(s.TilasmList, { ...props, canPublish: true })),
  );
  assert.match(html[fresh.id], /aria-label="Publish on claude\.ai"/);
  assert.doesNotMatch(html[current.id], /aria-label="Publish/);
  assert.match(
    html[behind.id],
    /aria-label="Publish the latest version on claude\.ai, at the same address"/,
  );

  const published = [];
  live(s.PublishButton, {
    tilasm: fresh,
    canPublish: true,
    publishing: false,
    onPublish: (t) => published.push(t.code),
  })
    .one("button")
    .onClick();
  assert.deepEqual(published, ["L01"]);
  // Asked once per tilasm: one with an address or the go-ahead publishes at once.
  assert.equal(s.needsConfirm(fresh), true);
  assert.equal(s.needsConfirm(behind), false);
  assert.equal(
    s.needsConfirm({
      ...fresh,
      artifact: { url: "", confirmTime: stamp("2026-10-19T20:00:00Z") },
    }),
    false,
  );

  // Its first publish asks: the page leaves for claude.ai, the developer checks it for a client's personal data.
  const confirmed = [];
  let cancelled = 0;
  html = rows(
    s.renderToStaticMarkup(
      h(s.TilasmList, { ...props, canPublish: true, confirming: fresh.id }),
    ),
  );
  assert.match(
    html[fresh.id],
    /role="group" aria-label="Publish L01 on claude\.ai"/,
  );
  assert.match(
    html[fresh.id],
    /leaves this machine for claude\.ai, as a private artifact of your Claude account/,
  );
  assert.match(
    html[fresh.id],
    /<strong>Check that it holds no personal data of a client, its screenshots included\.<\/strong>/,
  );
  assert.doesNotMatch(html[behind.id], /personal data/);
  const asking = live(s.PublishConfirm, {
    tilasm: fresh,
    onConfirm: () => confirmed.push(fresh.code),
    onCancel: () => cancelled++,
  });
  const button = (text) =>
    asking.all("button").find((b) => [b.props.children].flat().includes(text));
  button("Checked: publish").props.onClick();
  button("Cancel").props.onClick();
  assert.deepEqual(confirmed, ["L01"]);
  assert.equal(cancelled, 1);

  // While it goes: no button, the row says so.
  html = rows(
    s.renderToStaticMarkup(
      h(s.TilasmList, { ...props, canPublish: true, publishing: behind.id }),
    ),
  );
  assert.match(html[behind.id], /role="status">.*Publishing…/);
  assert.doesNotMatch(html[behind.id], /aria-label="Publish/);
});
