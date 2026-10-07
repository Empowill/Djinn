"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { projectHistory } = require("../electron/project-history.cjs");

test("project setup history stays within the selected repository, without native transcripts or support contents", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-history-"));
  const other = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-other-"));
  try {
    const task = {
      project: root,
      title: "Prototype",
      brief: "User intent",
      instructions: [{ text: "Utiliser le prototype" }],
      steps: [{ type: "prototype", title: "Prototype", status: "completed" }],
      questions: [{ title: "Choix", answer: "Oui" }],
      artifacts: [
        {
          title: "Canonical",
          type: "document",
          sourceOfTruth: true,
          content: "PRIVATE_BODY",
        },
      ],
      providerSessions: { lead: "PRIVATE_THREAD" },
      events: [{ detail: "RAW_TRANSCRIPT" }],
    };
    const history = projectHistory(
      { tasks: [task, { ...task, project: other, title: "Other" }] },
      root,
    );
    assert.equal(history.length, 1);
    assert.equal(history[0].title, "Prototype");
    assert.equal(history[0].supports[0].sourceOfTruth, true);
    assert.doesNotMatch(
      JSON.stringify(history),
      /PRIVATE_BODY|PRIVATE_THREAD|RAW_TRANSCRIPT|Other/,
    );
    assert.deepEqual(projectHistory(null, root), []);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
    fs.rmSync(other, { recursive: true, force: true });
  }
});
test("project history is bounded and prioritizes the latest human context", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-history-large-"));
  try {
    const tasks = Array.from({ length: 40 }, (_, n) => ({
      project: root,
      title: "Mission " + n,
      brief: "x".repeat(10000),
      instructions: Array.from({ length: 20 }, () => ({
        text: "y".repeat(10000),
      })),
      questions: [],
      artifacts: [],
    }));
    const history = projectHistory({ tasks }, root);
    assert.ok(history.length <= 12);
    assert.ok(JSON.stringify(history).length <= 16030);
    assert.equal(history.at(-1).title, "Mission 39");
    assert.equal(history.at(-1).brief.length, 1200);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
