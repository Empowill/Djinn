// "Rub the lamp" starts a converter: the developer approves a question in the window, Djinn starts "Q01 → tasks", a
// question worker that takes no slot, and it ends with a task spawned from the decision. The project's settings run
// the workers on the fake provider; the question's context scripts the converter, whose prompt holds it: a "djinn"
// line runs this djinn's command line, as an agent would, with $DJINN_TASK_ID. The spec runs a djinn of its own, with
// question workers on (global-setup.ts turns them off for the others), on a data folder of its own, and stops only it,
// by its process.
import { expect, test } from "@playwright/test";
import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

// env is the environment of this spec's djinn: its own data folder, question workers on.
const env = (home: string) => ({
  ...process.env,
  DJINN_HOME: home,
  DJINN_QUESTION_WORKERS: "on",
});

// up starts djinn up --browser on home, and gives its URL once it answers.
async function up(
  home: string,
): Promise<{ url: string; proc: ChildProcess; exited: Promise<unknown> }> {
  const proc = spawn(binary, ["up", "--browser"], {
    stdio: ["ignore", "pipe", "inherit"],
    env: env(home),
  });
  const exited = new Promise((resolve) => proc.once("exit", resolve));
  const url = await new Promise<string>((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error("djinn printed no URL")),
      10_000,
    );
    let out = "";
    proc.stdout!.on("data", (chunk: Buffer) => {
      out += chunk.toString();
      const match = out.match(/http:\/\/127\.0\.0\.1:\d+\/\?token=[0-9a-f]+/);
      if (match) {
        clearTimeout(timer);
        resolve(match[0]);
      }
    });
    proc.once("exit", (code) => reject(new Error(`djinn exited: ${code}`)));
  });
  return { url, proc, exited };
}

// down stops the djinn: SIGINT, as a person does; on Windows it ends at once.
async function down(d: { proc: ChildProcess; exited: Promise<unknown> }) {
  if (d.proc.exitCode !== null) return;
  d.proc.kill(process.platform === "win32" ? undefined : "SIGINT");
  await d.exited;
}

type Task = {
  id: string;
  code: string;
  title: string;
  status: string;
  role?: string;
  question?: string;
  decision?: string;
  access?: string;
};

test("rubbing the lamp starts a converter, which spawns a task from the decision", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-qw-"));
  const project = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-qwp-"));
  fs.mkdirSync(path.join(project, ".agents"));
  fs.writeFileSync(
    path.join(project, ".agents", "settings.txtpb"),
    "provider: PROVIDER_FAKE\n",
  );
  const djinn = (...args: string[]) =>
    execFileSync(binary, args, { env: env(home), encoding: "utf8" });
  const tasks = (wishId: string) =>
    (JSON.parse(djinn("task", "list", "--wish-id", wishId, "--json")).tasks ??
      []) as Task[];
  const title = `Pick a store ${randomUUID().slice(0, 8)}`;
  const running = await up(home);
  try {
    const projectId = JSON.parse(djinn("project", "add", project, "--json"))
      .project.id as string;
    const wishId = JSON.parse(
      djinn("wish", "make", title, "--project-ids", projectId, "--json"),
    ).wish.id as string;
    djinn(
      "question",
      "ask",
      "Which store?",
      wishId,
      "--options",
      "SQLite",
      "--options",
      "Postgres",
      "--recommendation",
      "A: one file, no server.",
      "--context",
      `One writer for now.\ndjinn task spawn ${wishId} --title 'Use SQLite' --prompt 'text stored' --decision Q01`,
    );

    await page.goto(running.url);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(page.locator(".hero h1")).toHaveText(title);
    await page
      .locator(".question-card")
      .filter({ hasText: "Which store?" })
      .getByRole("button", { name: "Rub the lamp" })
      .click();

    // The converter appears, ends, and the task it spawned names the decision.
    await expect
      .poll(() => tasks(wishId).map((t) => `${t.code} ${t.title}`), {
        timeout: 20_000,
      })
      .toEqual(["W1 Q01 → tasks", "W2 Use SQLite"]);
    await expect
      .poll(() => tasks(wishId).map((t) => t.status), { timeout: 20_000 })
      .toEqual(["TASK_STATUS_DONE", "TASK_STATUS_DONE"]);
    const [converter, spawned] = tasks(wishId);
    expect(converter).toMatchObject({
      role: "TASK_ROLE_CONVERTER",
      question: "Q01",
      access: "TASK_ACCESS_DJINN",
    });
    expect(spawned.decision).toBe("Q01");
    expect(spawned.role ?? "").toBe("");

    // The window shows both, and the decision log links the decision to the task.
    const tasksTab = page.getByRole("tab", { name: /^Tasks/ });
    await expect(tasksTab).toHaveText("Tasks2");
    await tasksTab.click();
    const finished = page.locator(".tasks-finished .wish-task");
    await expect(finished).toHaveCount(2);
    await expect(finished.filter({ hasText: "Q01 → tasks" })).toHaveCount(1);
    await expect(finished.filter({ hasText: "Use SQLite" })).toHaveCount(1);
    expect(errors).toEqual([]);
  } finally {
    await down(running);
    for (const dir of [home, project])
      fs.rmSync(dir, {
        recursive: true,
        force: true,
        maxRetries: 20,
        retryDelay: 250,
      });
  }
});
