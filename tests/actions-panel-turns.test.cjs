"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const Module = require("node:module");
const test = require("node:test");
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");
const typescript = require("typescript");

const root = require("node:path").resolve(__dirname, "../src");
const compile = (module, filename) => {
  const originalRequire = module.require.bind(module);
  module.require = (id) => {
    if (id === "motion/react")
      return {
        AnimatePresence: ({ children }) => children,
        motion: {
          article: ({ layout, initial, animate, exit, transition, ...props }) =>
            React.createElement("article", props),
        },
      };
    if (id === "lucide-react")
      return new Proxy(
        {},
        {
          get: (_, name) => (props) =>
            React.createElement("span", props, String(name)),
        },
      );
    return originalRequire(id);
  };
  module._compile(
    typescript.transpileModule(fs.readFileSync(filename, "utf8"), {
      fileName: filename,
      compilerOptions: {
        module: typescript.ModuleKind.CommonJS,
        target: typescript.ScriptTarget.ES2022,
        jsx: typescript.JsxEmit.ReactJSX,
        esModuleInterop: true,
      },
    }).outputText,
    filename,
  );
};
Module._extensions[".tsx"] = compile;
Module._extensions[".ts"] = compile;
Module._extensions[".css"] = () => {};

const { ActionsPanel, LEGACY_TURN_KEY, groupTurnItems } = require(
  `${root}/actions-panel.tsx`,
);

const action = (id, runId, createdAt, extra = {}) => ({
  id,
  runId,
  kind: "manual",
  title: `Action ${id}`,
  detail: "À vérifier",
  status: "pending",
  createdAt,
  updatedAt: extra.updatedAt || createdAt,
  ...extra,
});

const artifact = (id, runId, updatedAt, extra = {}) => ({
  id,
  runId,
  title: `Support ${id}`,
  type: "document",
  content: "# Résultat",
  updatedAt,
  revision: extra.revision || 1,
  editedBy: "agent",
  ...extra,
});

function installFakeDom() {
  class FakeNode {
    constructor(ownerDocument, nodeType = 1) {
      this.ownerDocument = ownerDocument;
      this.nodeType = nodeType;
      this.parentNode = null;
      this.childNodes = [];
      this.style = {};
      this._listeners = {};
    }
    appendChild(node) {
      if (node.parentNode) node.parentNode.removeChild(node);
      node.parentNode = this;
      this.childNodes.push(node);
      return node;
    }
    insertBefore(node, reference) {
      if (node.parentNode) node.parentNode.removeChild(node);
      node.parentNode = this;
      const index = this.childNodes.indexOf(reference);
      if (index < 0) this.childNodes.push(node);
      else this.childNodes.splice(index, 0, node);
      return node;
    }
    removeChild(node) {
      const index = this.childNodes.indexOf(node);
      if (index >= 0) this.childNodes.splice(index, 1);
      node.parentNode = null;
      return node;
    }
    addEventListener(type, handler) {
      (this._listeners[type] ||= []).push(handler);
    }
    removeEventListener() {}
    setAttribute(name, value) {
      this[name] = String(value);
    }
    setAttributeNS(namespace, name, value) {
      this.setAttribute(name, value);
    }
    removeAttribute(name) {
      delete this[name];
    }
    removeAttributeNS(namespace, name) {
      this.removeAttribute(name);
    }
    getAttribute(name) {
      return this[name] === undefined ? null : this[name];
    }
  }
  class FakeElement extends FakeNode {
    constructor(ownerDocument, tagName) {
      super(ownerDocument, 1);
      this.tagName = tagName.toUpperCase();
      this.nodeName = this.tagName;
      this.namespaceURI = "http://www.w3.org/1999/xhtml";
      this.className = "";
      this.textContent = "";
    }
    get firstChild() {
      return this.childNodes[0] || null;
    }
    get nextSibling() {
      if (!this.parentNode) return null;
      const index = this.parentNode.childNodes.indexOf(this);
      return this.parentNode.childNodes[index + 1] || null;
    }
  }
  class FakeText extends FakeNode {
    constructor(ownerDocument, value) {
      super(ownerDocument, 3);
      this.nodeName = "#text";
      this.nodeValue = value;
      this.data = value;
    }
  }
  const document = {
    createElement: (tagName) => new FakeElement(document, tagName),
    createElementNS: (namespace, tagName) => new FakeElement(document, tagName),
    createTextNode: (value) => new FakeText(document, String(value)),
    documentElement: null,
    body: null,
    activeElement: null,
    addEventListener() {},
    removeEventListener() {},
  };
  document.documentElement = new FakeElement(document, "html");
  document.body = new FakeElement(document, "body");
  document.documentElement.appendChild(document.body);
  document.activeElement = document.body;
  const window = {
    document,
    addEventListener() {},
    removeEventListener() {},
    event: undefined,
    navigator: { userAgent: "node" },
    HTMLIFrameElement: FakeElement,
  };
  document.defaultView = window;
  const previous = {
    document: global.document,
    window: global.window,
    Node: global.Node,
    Element: global.Element,
    HTMLElement: global.HTMLElement,
    SVGElement: global.SVGElement,
    actEnvironment: global.IS_REACT_ACT_ENVIRONMENT,
  };
  global.document = document;
  global.window = window;
  global.Node = FakeNode;
  global.Element = FakeElement;
  global.HTMLElement = FakeElement;
  global.SVGElement = FakeElement;
  global.IS_REACT_ACT_ENVIRONMENT = true;
  const container = new FakeElement(document, "div");
  document.body.appendChild(container);
  return {
    container,
    restore() {
      for (const [key, value] of Object.entries(previous)) {
        if (value === undefined) delete global[key];
        else global[key] = value;
      }
    },
  };
}

function detailsIn(container) {
  const result = [];
  const visit = (node) => {
    if (node?.tagName === "DETAILS") result.push(node);
    for (const child of node?.childNodes || []) visit(child);
  };
  visit(container);
  return result;
}

function reactProps(node) {
  const key = Object.keys(node).find((name) => name.startsWith("__reactProps$"));
  assert.ok(key, "the rendered details keeps its React props");
  return node[key];
}

test("regroupe actions et supports par tour, récent d'abord, sans inventer de run legacy", () => {
  const groups = groupTurnItems(
    [
      action("old-action", "run-old", "2026-10-06T10:00:00.000Z"),
      action("new-action", "run-new", "2026-10-06T11:00:00.000Z"),
      action("legacy-action", undefined, "2026-10-06T12:00:00.000Z"),
    ],
    [artifact("old-support", "run-old", "2026-10-06T10:05:00.000Z")],
    ["run-old", "run-new"],
  );

  assert.deepEqual(
    groups.map((group) => group.key),
    ["turn:run-new", "turn:run-old", LEGACY_TURN_KEY],
  );
  assert.equal(groups[1].actions[0].id, "old-action");
  assert.equal(groups[1].artifacts[0].id, "old-support");
  assert.equal(groups[2].runId, undefined);
  assert.equal(groups[2].actions[0].runId, undefined);
});

test("une révision et la mise à jour d'une ancienne action ne remontent pas leur ancien tour", () => {
  const groups = groupTurnItems(
    [
      action("old-action", "run-old", "2026-10-06T10:00:00.000Z", {
        updatedAt: "2026-10-06T15:00:00.000Z",
      }),
      action("new-action", "run-new", "2026-10-06T11:00:00.000Z"),
    ],
    [
      artifact("old-support", "run-old", "2026-10-06T15:00:00.000Z", {
        revision: 2,
        revisions: [
          {
            revision: 1,
            content: "# Initial",
            updatedAt: "2026-10-06T10:05:00.000Z",
            editedBy: "agent",
          },
        ],
      }),
    ],
    ["run-old", "run-new"],
  );

  assert.deepEqual(
    groups.map((group) => group.runId),
    ["run-new", "run-old"],
  );
  assert.equal(groups[1].artifacts[0].revision, 2);
});

test("rend une restitution par accordéon et affiche les supports avec leur tour", () => {
  const markup = renderToStaticMarkup(
    React.createElement(ActionsPanel, {
      actions: [
        action("old", "run-old", "2026-10-06T10:00:00.000Z"),
        action("new", "run-new", "2026-10-06T11:00:00.000Z"),
      ],
      artifacts: [artifact("brief", "run-new", "2026-10-06T11:05:00.000Z")],
      runOrder: ["run-old", "run-new"],
      onAction: async () => true,
      onOpenArtifact: () => {},
    }),
  );

  assert.equal((markup.match(/class="action-turn /g) || []).length, 2);
  assert.ok(markup.indexOf("Action new") < markup.indexOf("Action old"));
  assert.ok(markup.indexOf("Support brief") < markup.indexOf("Action new"));
  assert.match(markup, /Ouvrir le support/);
  const newestStart = markup.indexOf('data-turn-key="turn:run-new"');
  const oldStart = markup.indexOf('data-turn-key="turn:run-old"');
  const newestTag = markup.slice(markup.lastIndexOf("<details", newestStart), markup.indexOf(">", newestStart) + 1);
  const oldTag = markup.slice(markup.lastIndexOf("<details", oldStart), markup.indexOf(">", oldStart) + 1);
  assert.ok(newestStart >= 0 && oldStart > newestStart);
  assert.match(newestTag, /open=""/);
  assert.doesNotMatch(oldTag, /open=""/);
});

test("activeRunId ferme l'ancien tour et expose le nouveau même sans action", () => {
  const markup = renderToStaticMarkup(
    React.createElement(ActionsPanel, {
      actions: [action("old", "run-old", "2026-10-06T10:00:00.000Z")],
      runOrder: ["run-old"],
      activeRunId: "run-current",
      onAction: async () => true,
    }),
  );

  const currentStart = markup.indexOf('data-turn-key="turn:run-current"');
  const oldStart = markup.indexOf('data-turn-key="turn:run-old"');
  const currentTag = markup.slice(markup.lastIndexOf("<details", currentStart), markup.indexOf(">", currentStart) + 1);
  const oldTag = markup.slice(markup.lastIndexOf("<details", oldStart), markup.indexOf(">", oldStart) + 1);
  assert.ok(currentStart >= 0 && oldStart > currentStart);
  assert.match(markup.slice(currentStart, oldStart), /Tour en cours/);
  assert.match(markup.slice(currentStart, oldStart), /Aucune action ni support/);
  assert.match(currentTag, /open=""/);
  assert.doesNotMatch(oldTag, /open=""/);
});

test("un toggle fermé tardif de l'ancien tour ne referme pas le nouveau", async () => {
  const { act } = React;
  const dom = installFakeDom();
  const { createRoot } = require("react-dom/client");
  const actionValue = action("old", "run-old", "2026-10-06T10:00:00.000Z");
  const props = (activeRunId) => ({
    actions: [actionValue],
    runOrder: ["run-old"],
    activeRunId,
    onAction: async () => true,
  });
  const rootNode = createRoot(dom.container);
  try {
    await act(async () => {
      rootNode.render(React.createElement(ActionsPanel, props(undefined)));
    });
    let turns = detailsIn(dom.container);
    assert.equal(turns.length, 1);
    assert.equal(turns[0].open, "");

    await act(async () => {
      rootNode.render(React.createElement(ActionsPanel, props("run-new")));
    });
    turns = detailsIn(dom.container);
    const oldTurn = turns.find(
      (turn) => turn.getAttribute("data-turn-key") === "turn:run-old",
    );
    const newTurn = turns.find(
      (turn) => turn.getAttribute("data-turn-key") === "turn:run-new",
    );
    assert.equal(newTurn.open, "");
    assert.equal(oldTurn.open, undefined);

    // Chromium can deliver the old close event after the effect opened the
    // new turn. Invoke the real React handler retained on the old details.
    await act(async () => {
      reactProps(oldTurn).onToggle({ currentTarget: { open: false } });
    });
    turns = detailsIn(dom.container);
    assert.equal(
      turns.find((turn) => turn.getAttribute("data-turn-key") === "turn:run-new").open,
      "",
    );
  } finally {
    await act(async () => {
      rootNode.unmount();
    });
    dom.restore();
  }
});
