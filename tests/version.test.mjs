// The running Djinn's version and where it comes from (src/version.ts, L10, W61). Run with `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens } from "./screens.mjs";

const { s } = await screens();

test("a local install's version reads as harness.LocalBuild reads it", () => {
  assert.deepEqual(s.localBuild("local-30a0568"), {
    commit: "30a0568",
    dirty: false,
  });
  assert.deepEqual(s.localBuild("local-v0.1.0-3-g1a2b3c4-dirty"), {
    commit: "1a2b3c4",
    dirty: true,
  });
  // As internal/harness/compare.go: a commit of fewer than 4 digits, or not hexadecimal, names none.
  assert.equal(s.localBuild("local-abc"), undefined);
  assert.equal(s.localBuild("local-v0.1.0"), undefined);
  assert.equal(s.localBuild("v0.1.0"), undefined);
});

test("the tooltip names the version, then where it comes from", () => {
  assert.deepEqual(s.versionLines("v0.1.0"), [
    "Djinn v0.1.0",
    "release · tag v0.1.0",
  ]);
  assert.deepEqual(s.versionLines("local-30a0568"), [
    "Djinn local-30a0568",
    "local install · commit 30a0568",
  ]);
  assert.deepEqual(s.versionLines("local-v0.1.0-3-g1a2b3c4-dirty"), [
    "Djinn local-v0.1.0-3-g1a2b3c4-dirty",
    "local install · commit 1a2b3c4, modified",
  ]);
  assert.deepEqual(s.versionLines("local-v0.1.0"), [
    "Djinn local-v0.1.0",
    "local install",
  ]);
  assert.deepEqual(s.versionLines("dev"), ["Djinn dev", "development build"]);
  // CI's Go pseudo-version: the commit it names, not a release.
  assert.deepEqual(s.versionLines("v0.0.0-20261012122000-97dd90d8abcd"), [
    "Djinn v0.0.0-20261012122000-97dd90d8abcd",
    "build · commit 97dd90d",
  ]);
  assert.deepEqual(s.versionLines("something"), ["Djinn something"]);
  // Before the update stream says it, nothing.
  assert.deepEqual(s.versionLines(""), []);
});
