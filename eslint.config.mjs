// ESLint flat config for the React interface (src/), its tests (tests/, e2e/) and the root configs. `go tool task
// lint` runs it; `go tool task format` applies the fixes it knows. No formatting rule: Prettier owns the layout.
// .mjs: package.json declares no "type", and the config is an ES module.
import { createRequire } from "node:module";
import path from "node:path";
import react from "@eslint-react/eslint-plugin";
import js from "@eslint/js";
import { defineConfig } from "eslint/config";
import reactHooks from "eslint-plugin-react-hooks";
import globals from "globals";
import tseslint from "typescript-eslint";

// Key ESLint's cache by path relative to the worktree root so one cache file outside node_modules is shared across
// worktrees of the same project (`file-entry-cache` resolves relative keys against `this._cwd`, yet `createFileKey`
// leaves absolute paths untouched unless normalized here).
const { FileEntryCache } = createRequire(import.meta.url)("file-entry-cache");
FileEntryCache.prototype.createFileKey = function (filePath) {
  const rel = path.isAbsolute(filePath)
    ? path.relative(this._cwd, filePath)
    : filePath;
  return rel.replaceAll("\\", "/");
};

export default defineConfig(
  // Generated, built or vendored: not ours to lint.
  {
    ignores: [
      "dist/",
      "gen/",
      "bin/",
      "node_modules/",
      "src/vendor/",
      "test-results/",
      "playwright-report/",
    ],
  },
  {
    files: ["src/**/*.{ts,tsx}", "e2e/**/*.ts"],
    // The rules that need no type information: tsc -b already checks the types, and typed linting would double
    // the time of the lint task.
    extends: [js.configs.recommended, tseslint.configs.recommended],
    languageOptions: { globals: globals.browser },
    rules: {
      // `catch {}` says a failure is fine; a comment above it says why.
      "no-empty": ["error", { allowEmptyCatch: true }],
      // `_` marks a value left out on purpose, as in Go.
      "@typescript-eslint/no-unused-vars": [
        "error",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
          ignoreRestSiblings: true,
        },
      ],
    },
  },
  {
    files: ["src/**/*.tsx"],
    // @eslint-react rather than eslint-plugin-react, which does not run on ESLint 10. Its TypeScript preset leaves
    // out what tsc checks already; it needs no type information.
    extends: [react.configs["recommended-typescript"]],
    plugins: { "react-hooks": reactHooks },
    rules: {
      // The two rules of hooks. The plugin's other rules check code for the React Compiler, which Djinn does not use.
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "error",
      // A ref is named for what it holds (host, frame, ended); useRef at its declaration says it is one.
      "@eslint-react/naming-convention-ref-name": "off",
      // The lists keyed by their index never reorder: a question's options (the index is the choice), its rounds,
      // the routes of an inbox item, the wordmark's letters.
      "@eslint-react/no-array-index-key": "off",
      // Each effect that sets state does it on an outside event (a focus asked for, a new diagram, a new Djinn's
      // answer); derived during render, each would need a ref to know what changed.
      "@eslint-react/set-state-in-effect": "off",
    },
  },
  {
    // The interface's Node tests and the root configs: plain JavaScript, or TypeScript for Vite.
    files: ["tests/*.{mjs,cjs}", "*.{mjs,ts}"],
    extends: [js.configs.recommended, tseslint.configs.recommended],
    languageOptions: { globals: globals.node },
  },
  {
    files: ["tests/*.cjs"],
    // CommonJS, run by node --test: require() is how it loads.
    rules: { "@typescript-eslint/no-require-imports": "off" },
  },
  {
    // The end-to-end tests and their configs run in Node, next to the pages they drive.
    files: ["e2e/**/*.ts"],
    languageOptions: { globals: { ...globals.node, ...globals.browser } },
  },
  {
    // A spec takes its test from e2e/test.ts, whose fixture fails a test that leaves the place dirty (W306).
    files: ["e2e/*.spec.ts"],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          paths: [
            {
              name: "@playwright/test",
              importNames: ["test", "expect"],
              message:
                "import test and expect from ./test: its fixture fails a test that leaves an active wish or an open question (e2e/test.ts).",
            },
          ],
        },
      ],
    },
  },
);
