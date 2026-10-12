// The first view of a long Markdown text, the commands and links of a line of text, a move's main link, and the report
// a question names. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { bundle } from "./bundle.mjs";

const s = await bundle(
  "text",
  `export {
  foldMarkdown,
  commandParts,
  djinnAutolinks,
  linksOf,
} from "@/src/data/text.ts";
export { linkName, mainLink } from "@/src/data/links.ts";
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

const MR = "https://gitlab.com/empowill/app/-/merge_requests/123";
const TILASM = "djinn://tilasm/01a130b1-398a-7072-be60-ce928cb7241e";

test("a line's links: a web address or a djinn:// link, without the punctuation after it", () => {
  assert.deepEqual(
    s.commandParts(`Review MR !123 and take it out of draft: ${MR}.`),
    [
      { text: "Review MR !123 and take it out of draft: " },
      { text: MR, href: true },
      { text: "." },
    ],
  );
  // A closing bracket it did not open is the sentence's; one it opened is its own.
  assert.deepEqual(
    s.commandParts(
      `See the diagram (${TILASM}), then https://en.wikipedia.org/wiki/Lamp_(object)`,
    ),
    [
      { text: "See the diagram (" },
      { text: TILASM, href: true },
      { text: "), then " },
      { text: "https://en.wikipedia.org/wiki/Lamp_(object)", href: true },
    ],
  );
  // A link inside a command stays in the command, to copy whole.
  assert.deepEqual(s.commandParts(`Run \`gh pr ready ${MR}\``), [
    { text: "Run " },
    { text: `gh pr ready ${MR}`, code: true },
  ]);
  assert.deepEqual(s.commandParts("No link: https:// alone"), [
    { text: "No link: https:// alone" },
  ]);
  assert.deepEqual(s.linksOf(`[!123](${MR}) and <${TILASM}>`), [MR, TILASM]);
});

test("a bare djinn:// link in Markdown becomes a link; one in code or already a link stays", () => {
  assert.equal(
    s.djinnAutolinks(`The diagram: ${TILASM}.`),
    `The diagram: <${TILASM}>.`,
  );
  for (const kept of [
    `[the diagram](${TILASM})`,
    `<${TILASM}>`,
    `\`djinn open ${TILASM}\``,
    "```sh\ndjinn open " + TILASM + "\n```",
    "No link here",
  ])
    assert.equal(s.djinnAutolinks(kept), kept);
});

test("a move's main link: the first of its title, context, recommendation and options, named by what it is", () => {
  assert.equal(
    s.mainLink({
      text: "Review MR !123 and take it out of draft",
      context: `The task opened [!123](${MR}) as a draft; CI: https://gitlab.com/empowill/app/-/pipelines/9`,
      recommendation: "A: mark it ready",
      options: [],
    }),
    MR,
  );
  assert.equal(
    s.mainLink({
      text: "Push the tag",
      context: "",
      recommendation: "",
      options: ["Done: `git push origin v1`", `Later, see ${TILASM}`],
    }),
    TILASM,
  );
  assert.equal(s.mainLink({ text: "Install", options: [] }), undefined);
  assert.deepEqual(s.linkName(MR), { kind: "mr", number: "123" });
  assert.deepEqual(s.linkName("https://github.com/o/r/pull/12/files"), {
    kind: "pr",
    number: "12",
  });
  assert.deepEqual(s.linkName("https://github.com/o/r/issues/5"), {
    kind: "issue",
    number: "5",
  });
  assert.deepEqual(s.linkName("https://gitlab.com/o/r/-/pipelines/9"), {
    kind: "pipeline",
    number: "9",
  });
  assert.deepEqual(s.linkName("https://github.com/o/r/actions/runs/77"), {
    kind: "run",
  });
  assert.deepEqual(s.linkName(TILASM), { kind: "tilasm" });
  assert.deepEqual(s.linkName("https://jira.example.com/browse/EMP-4"), {
    kind: "host",
    host: "jira.example.com",
  });
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
