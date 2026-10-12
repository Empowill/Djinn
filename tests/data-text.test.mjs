// The first view of a long Markdown text, the commands of a line of text, and the report a question names. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const s = await bundle(
  "text",
  `export { foldMarkdown, commandParts } from "@/src/data/text.ts";
export { isReport, reportOf, reportsOf } from "@/src/data/reports.ts";
export * as dew from "@/e2e/report-question.ts";`,
);

test("a long context shows its first section, and says more is left", () => {
  const fold = s.foldMarkdown(s.dew.CONTEXT);
  assert.equal(fold.more, true);
  assert.match(fold.head, /^## Where we stand\n- \*\*Study W1/);
  assert.match(fold.head, /that was a mistake\.$/);
  assert.doesNotMatch(fold.head, /The report in short|What starts/);
  // The report too: its first section.
  const report = s.foldMarkdown(s.dew.REPORT);
  assert.equal(report.more, true);
  assert.match(
    report.head,
    /13 fields in one request, the base salary among them\.$/,
  );
});

test("a short text stays whole; two short lines more are not worth a click", () => {
  assert.deepEqual(s.foldMarkdown("One line."), {
    head: "One line.",
    more: false,
  });
  const lines = Array.from({ length: 9 }, (_, i) => `- line ${i + 1}`);
  assert.equal(s.foldMarkdown(lines.join("\n")).more, false);
  const many = Array.from({ length: 14 }, (_, i) => `- line ${i + 1}`);
  const fold = s.foldMarkdown(many.join("\n"));
  assert.equal(fold.more, true);
  assert.equal(fold.head, many.slice(0, 8).join("\n"));
});

test("a single long paragraph is cut at the end of a sentence, its marks paired", () => {
  const sentence = "The probe reads the **secret** as the script does. ";
  const text = sentence.repeat(30).trim();
  const fold = s.foldMarkdown(text);
  assert.equal(fold.more, true);
  assert.ok(fold.head.length <= 700, `${fold.head.length} characters`);
  assert.match(fold.head, /does\.$/);
  assert.equal(fold.head.split("**").length % 2, 1);
});

test("a code block is never cut: the first view stops before a long one", () => {
  const code = Array.from({ length: 30 }, (_, i) => `echo ${i}`).join("\n");
  const fold = s.foldMarkdown(
    `Run this:\n\n\`\`\`sh\n${code}\n\`\`\`\n\nThen wait.`,
  );
  assert.equal(fold.more, true);
  assert.equal(fold.head, "Run this:");
  const short = s.foldMarkdown("```sh\ndjinn wish allow x --mode auto\n```");
  assert.equal(short.more, false);
});

test("an option's commands: between backticks, or a djinn command up to a comma or the end", () => {
  const id = "01a130b1-398a-7072-be60-ce928cb7241e";
  assert.deepEqual(s.commandParts(s.dew.options(id)[0]), [
    { text: "Done: I ran " },
    { text: `djinn wish allow ${id} --mode auto`, code: true },
  ]);
  assert.deepEqual(
    s.commandParts(
      `Give the workers auto mode: djinn wish allow ${id} --mode auto, then I run the study again`,
    ),
    [
      { text: "Give the workers auto mode: " },
      { text: `djinn wish allow ${id} --mode auto`, code: true },
      { text: ", then I run the study again" },
    ],
  );
  assert.deepEqual(s.commandParts("Push the tag: `git push origin v1.2.0`."), [
    { text: "Push the tag: " },
    { text: "git push origin v1.2.0", code: true },
    { text: "." },
  ]);
  // The word djinn alone is no command.
  assert.deepEqual(s.commandParts("Let djinn decide later"), [
    { text: "Let djinn decide later" },
  ]);
  assert.deepEqual(s.commandParts("Olive"), [{ text: "Olive" }]);
});

test("a question links the report its context names, by id or by title", () => {
  const report = {
    id: "01a130d9-0000-7000-8000-000000000001",
    kind: "Report",
    title: s.dew.REPORT_TITLE,
    content: s.dew.REPORT,
  };
  const other = { id: "b2", kind: "report", title: "Notes", content: "" };
  const reports = s.reportsOf([
    { id: "b0", kind: "log", title: "", content: "" },
    report,
    other,
  ]);
  assert.deepEqual(
    reports.map((b) => b.id),
    ["b2", report.id],
  );
  assert.equal(s.isReport(report), true);
  assert.equal(
    s.reportOf(
      { text: s.dew.QUESTION, context: s.dew.CONTEXT, recommendation: "" },
      reports,
    ),
    report,
  );
  assert.equal(
    s.reportOf(
      { text: "Go on?", context: `See ${report.id}.`, recommendation: "" },
      reports,
    ),
    report,
  );
  // A title too short to be told from a common word links nothing.
  assert.equal(
    s.reportOf(
      { text: "Notes on the oil?", context: "", recommendation: "" },
      reports,
    ),
    undefined,
  );
});
