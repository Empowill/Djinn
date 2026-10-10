// The README's screenshots, dark and light, into docs/screenshots/readme/: a demonstration wish in English, with
// fictional data, imported into the djinn of global-setup.ts. Run with `go tool task screenshots`.
import { type Page, expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const shots = path.join(__dirname, "../docs/screenshots/readme");

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

// id is a UUIDv7, as djinn makes them: the store lists them in the order they were made, so the shots keep theirs.
let made = 0;
function id(): string {
  const time = (Date.now() + made++).toString(16).padStart(12, "0");
  return `${time.slice(0, 8)}-${time.slice(8)}-7${randomUUID().slice(15)}`;
}

// ago is a time some minutes before now, so that the page reads "2 h ago" whenever the screenshots are taken.
const ago = (minutes: number) =>
  new Date(Date.now() - minutes * 60_000).toISOString();

type TaskRow = {
  code: string;
  title: string;
  status: string;
  provider?: string;
  started?: number;
  ended?: number;
  cost?: number;
  error?: string;
};

// A wish as `djinn wish export` writes it: one project, its tasks, its questions and its notes.
function wishFile(
  title: string,
  created: number,
  tasks: TaskRow[],
  questions: object[] = [],
  blocks: object[] = [],
) {
  const wishId = id();
  const project = { id: id(), name: "field-app", git: true };
  const rows = tasks.map((t) => ({
    id: id(),
    wish_id: wishId,
    project_id: project.id,
    code: t.code,
    title: t.title,
    status: t.status,
    provider: t.provider ?? "PROVIDER_CLAUDE",
    create_time: ago(created),
    ...(t.started ? { start_time: ago(t.started) } : {}),
    ...(t.ended ? { end_time: ago(t.ended) } : {}),
    ...(t.cost
      ? {
          usage: {
            input_tokens: Math.round(t.cost * 90_000),
            output_tokens: Math.round(t.cost * 8_000),
            cost_usd: t.cost,
          },
        }
      : {}),
    ...(t.error ? { error: t.error } : {}),
  }));
  const taskId = (code: string) => rows.find((r) => r.code === code)!.id;
  return {
    version: 1,
    create_time: ago(0),
    wish: {
      id: wishId,
      title,
      project_ids: [project.id],
      create_time: ago(created),
      state: "WISH_STATE_ACTIVE",
    },
    projects: [project],
    tasks: rows,
    questions: questions.map((q) => ({
      id: id(),
      wish_id: wishId,
      ...q,
    })),
    blocks: blocks.map((b, i) => {
      const { task, ...block } = b as { task?: string };
      return {
        id: id(),
        wish_id: wishId,
        position: i + 1,
        media_type: "text/markdown",
        ...(task ? { task_id: taskId(task) } : {}),
        ...block,
      };
    }),
  };
}

const offline = wishFile(
  "Offline mode for the field app",
  300,
  [
    {
      code: "W1",
      title: "Map what the app stores on the device",
      status: "TASK_STATUS_DONE",
      started: 280,
      ended: 262,
      cost: 0.38,
    },
    {
      code: "W2",
      title: "Queue the forms while offline",
      status: "TASK_STATUS_DONE",
      provider: "PROVIDER_CODEX",
      started: 250,
      ended: 196,
      cost: 1.74,
    },
    {
      code: "W3",
      title: "Send the queue when the network returns",
      status: "TASK_STATUS_FAILED",
      started: 190,
      ended: 141,
      cost: 2.12,
      error:
        "two devices saved the same form: no rule says which one wins (see Q01)",
    },
    {
      code: "W4",
      title: "Show what waits to be sent",
      status: "TASK_STATUS_PENDING",
    },
    {
      code: "W5",
      title: "Test on a slow, lossy network",
      status: "TASK_STATUS_PENDING",
    },
  ],
  [
    {
      code: "Q01",
      text: "When two devices edit the same form offline, which version wins?",
      options: [
        "The last save — simple, may lose a change",
        "A merge, field by field — keeps both, more work",
        "Ask the person — never silent, one more screen",
      ],
      recommendation:
        "B: the forms are long and filled in by several people; losing a field costs more than the merge.",
      context: [
        "## What W3 found",
        "",
        "- 14% of the forms are edited on two devices the same day.",
        "- Nine conflicts in ten touch different fields.",
        "",
        "| Rule | Changes lost | Work |",
        "| --- | --- | --- |",
        "| Last save | some | half a day |",
        "| Field by field | none | 2 days |",
        "| Ask the person | none | 3 days |",
      ].join("\n"),
      create_time: ago(140),
    },
    {
      code: "Q02",
      text: "May W4 add a small storage library?",
      recommendation:
        "Yes: it is MIT-licensed, 6 kB, and replaces 200 lines of our own.",
      create_time: ago(95),
    },
    {
      code: "Q00",
      text: "Which forms work offline first?",
      options: ["Inspections only", "Every form"],
      recommendation: "A: they are 80% of what is filled in on site.",
      answer: {
        choice: "CHOICE_A",
        note: "Inspections, then the rest.",
        create_time: ago(285),
      },
      create_time: ago(290),
    },
  ],
  [
    {
      kind: "plan",
      title: "Offline mode, in three steps",
      content: [
        "1. **Keep** every form on the device until the server has it.",
        "2. **Send** the queue as soon as the network returns, oldest first.",
        "3. **Show** what waits, so that nobody fills a form twice.",
        "",
        "Inspections come first (Q00); the other forms follow the same queue.",
      ].join("\n"),
      create_time: ago(286),
      update_time: ago(140),
    },
    {
      kind: "note",
      task: "W1",
      title: "What the app stores today",
      content: [
        "| Data | Where | Size |",
        "| --- | --- | --- |",
        "| Session | memory | 2 kB |",
        "| Draft forms | local storage | up to 5 MB |",
        "| Photos | none: sent at once | — |",
        "",
        "Local storage is full at 5 MB: photos must go to `IndexedDB`.",
      ].join("\n"),
      create_time: ago(262),
      update_time: ago(262),
    },
  ],
);

const search = wishFile("Faster search in the parts catalog", 1500, [
  {
    code: "W1",
    title: "Measure the slow queries",
    status: "TASK_STATUS_DONE",
    started: 1480,
    ended: 1460,
    cost: 0.52,
  },
  {
    code: "W2",
    title: "Index the part numbers",
    status: "TASK_STATUS_PENDING",
  },
]);

const digest = wishFile("A weekly digest of open inspections", 2900, [
  {
    code: "W1",
    title: "Draft the digest",
    status: "TASK_STATUS_PENDING",
  },
]);

// shoot takes the page dark, then light. Hidden: the terminal's command and folder, which name this machine, and the
// machine's load in the status bar, which says how busy it was when the shots were taken.
async function shoot(page: Page, name: string) {
  for (const theme of ["dark", "light"] as const) {
    await page.evaluate((v) => localStorage.setItem("djinn.theme", v), theme);
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    await expect(page.locator(".hero h1")).toHaveText(offline.wish.title);
    for (const card of await page.locator(".question-card").all())
      await expect(card).toHaveCSS("opacity", "1");
    // The question with options first: it shows the lamp's two buttons under its choices.
    await expect(page.locator(".question-card").first()).toContainText(
      "which version wins?",
    );
    if (name === "tasks") {
      // The Tasks tab: what moves or waits, then what is finished, and what it all cost.
      await page.locator("#view-tab-tasks").click();
      await expect(
        page.locator(".tasks-moving .wish-task").first(),
      ).toBeVisible();
    }
    await page.screenshot({
      path: path.join(shots, `${name}-${theme}.png`),
      style: [
        ".lead-terminal-command { visibility: hidden }",
        ".app-statusbar > span:nth-child(2) { visibility: hidden }",
      ].join("\n"),
    });
  }
}

test("the README's screenshots", async ({ page }) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-shots-"));
  // Each import goes last among the active wishes: the offline one leads.
  for (const [i, wish] of [offline, search, digest].entries()) {
    const file = path.join(dir, `wish-${i}.json`);
    fs.writeFileSync(file, JSON.stringify(wish));
    djinn("wish", "import", file);
  }
  fs.rmSync(dir, { recursive: true, force: true });

  await page.goto(process.env.DJINN_URL!);
  await page.evaluate((id) => {
    localStorage.setItem("djinn.wish", id);
    localStorage.setItem("djinn.terminal.collapsed", "1");
  }, offline.wish.id);
  await page.reload();
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();

  await shoot(page, "questions");
  await shoot(page, "tasks");
});
