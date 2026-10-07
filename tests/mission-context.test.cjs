"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const Module = require("node:module");
const ts = require("typescript");
const filename = path.resolve(__dirname, "../src/mission-context.ts");
const loaded = new Module(filename, module);
loaded._compile(
  ts.transpileModule(fs.readFileSync(filename, "utf8"), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText,
  filename,
);
const { buildMissionPrompt, missionSupports } = loaded.exports;
const artifact = (id, content, updatedAt, extra = {}) => ({
  id,
  content,
  updatedAt,
  title: id,
  type: "document",
  ...extra,
});

test("canonical and latest current supports survive a long mission without replaying tool transcripts", () => {
  const artifacts = Array.from({ length: 150 }, (_, i) =>
    artifact(`old-${i}`, "x".repeat(30000), "2026-10-06T08:00:00Z"),
  );
  artifacts.push(
    artifact(
      "canonical",
      "Keep the acquired business rule",
      "2026-10-05T08:00:00Z",
      { sourceOfTruth: true },
    ),
  );
  artifacts.push(
    artifact(
      "latest",
      "Latest blocker: server is unavailable",
      "2026-10-07T08:00:00Z",
      { stepId: "active" },
    ),
  );
  const task = {
    title: "Three tickets",
    brief: "Implement the selected tickets",
    artifacts,
    instructions: [{ id: "i", text: "Test the icons first" }],
    questions: [{ id: "q", title: "Copy levels?", answer: "Yes" }],
    steps: [
      {
        id: "old",
        title: "Prepared",
        status: "completed",
        summary: "Acquired decision",
      },
    ],
    feedback: [],
    events: [{ type: "tool", detail: "SECRET_TRANSCRIPT_SHOULD_NOT_REPLAY" }],
  };
  const prompt = buildMissionPrompt(task, {
    id: "active",
    type: "review",
    objective: "Test icons",
  });
  assert.ok(prompt.length < 28000, `Prompt length: ${prompt.length}`);
  assert.match(prompt, /Latest blocker: server is unavailable/);
  assert.match(prompt, /Keep the acquired business rule/);
  assert.match(prompt, /Test the icons first/);
  assert.match(prompt, /Copy levels/);
  assert.doesNotMatch(prompt, /SECRET_TRANSCRIPT_SHOULD_NOT_REPLAY/);
  assert.equal(missionSupports(artifacts, "active")[0].id, "canonical");
});

test("image references never pretend un-attached screenshots were inspected", () => {
  const image = artifact(
    "image",
    "data:image/png;base64,AAAA",
    "2026-10-07T08:00:00Z",
    { type: "screenshot" },
  );
  assert.match(missionSupports([image])[0].content, /not attached/);
  assert.match(
    missionSupports([image], undefined, ["image"])[0].content,
    /inspect its pixels/,
  );
});
