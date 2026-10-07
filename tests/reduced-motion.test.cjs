"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");

const source = path.resolve(__dirname, "../src/reduced-motion.ts");
const compiled = ts.transpileModule(fs.readFileSync(source, "utf8"), {
  fileName: source,
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2022,
    esModuleInterop: true,
  },
});
const loaded = new Module(source, module);
loaded.filename = source;
loaded.paths = Module._nodeModulePaths(path.dirname(source));
loaded._compile(compiled.outputText, source);
const { readReducedMotion, subscribeReducedMotion } = loaded.exports;

function environment({ reduced = false, modern = true } = {}) {
  const mediaListeners = new Set();
  const mutationObservers = new Set();
  const media = {
    matches: reduced,
    ...(modern
      ? {
          addEventListener(_type, listener) {
            mediaListeners.add(listener);
          },
          removeEventListener(_type, listener) {
            mediaListeners.delete(listener);
          },
        }
      : {
          addListener(listener) {
            mediaListeners.add(listener);
          },
          removeListener(listener) {
            mediaListeners.delete(listener);
          },
        }),
    emit(value) {
      media.matches = value;
      for (const listener of mediaListeners) listener({ matches: value });
    },
    listenerCount: () => mediaListeners.size,
  };
  class FakeMutationObserver {
    constructor(listener) {
      this.listener = listener;
      mutationObservers.add(this);
    }
    observe() {}
    disconnect() {
      mutationObservers.delete(this);
    }
    emit() {
      this.listener([]);
    }
  }
  const root = { dataset: { motion: "full" } };
  const document = { documentElement: root };
  const window = { matchMedia: () => media };
  return {
    document,
    window,
    media,
    mutationObservers,
    FakeMutationObserver,
    setMotion(value) {
      root.dataset.motion = value;
      for (const observer of mutationObservers) observer.emit();
    },
  };
}

const originalWindow = global.window;
const originalDocument = global.document;
const originalMutationObserver = global.MutationObserver;
test.after(() => {
  global.window = originalWindow;
  global.document = originalDocument;
  global.MutationObserver = originalMutationObserver;
});

test("reduced motion follows document and OS preferences and cleans modern listeners", () => {
  const env = environment({ reduced: false, modern: true });
  global.window = env.window;
  global.document = env.document;
  global.MutationObserver = env.FakeMutationObserver;
  assert.equal(readReducedMotion(), false);
  const values = [];
  const cleanup = subscribeReducedMotion((value) => values.push(value));
  assert.equal(values.at(-1), false);
  env.setMotion("reduced");
  assert.equal(values.at(-1), true);
  env.setMotion("full");
  env.media.emit(true);
  assert.equal(values.at(-1), true);
  assert.equal(env.media.listenerCount(), 1);
  cleanup();
  assert.equal(env.media.listenerCount(), 0);
  assert.equal(env.mutationObservers.size, 0);
});

test("reduced motion supports legacy media listeners", () => {
  const env = environment({ reduced: false, modern: false });
  global.window = env.window;
  global.document = env.document;
  global.MutationObserver = env.FakeMutationObserver;
  const values = [];
  const cleanup = subscribeReducedMotion((value) => values.push(value));
  env.media.emit(true);
  assert.equal(values.at(-1), true);
  cleanup();
  assert.equal(env.media.listenerCount(), 0);
});

