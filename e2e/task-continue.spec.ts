// Continue an interrupted task from the command line: the same task runs again and ends, and the Tasks tab shows it
// once, finished. To leave a task interrupted, the spec runs a djinn of its own, on a data folder of its own, and
// stops it while the fake worker runs; its wish granted meanwhile, the next start does not resume the task by itself.
// The spec stops only the djinn it started, by its process.
import { expect, test } from "@playwright/test";
import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { openWorkspaceSection } from "./workspace-selectors";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

// up starts djinn up --browser on home, and gives its URL once it answers.
async function up(
  home: string,
): Promise<{ url: string; proc: ChildProcess; exited: Promise<unknown> }> {
  const proc = spawn(binary, ["up", "--browser"], {
    stdio: ["ignore", "pipe", "inherit"],
    env: { ...process.env, DJINN_HOME: home },
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

// down stops the djinn: SIGINT, as a person does; on Windows it ends at once, as after a crash.
async function down(d: { proc: ChildProcess; exited: Promise<unknown> }) {
  if (d.proc.exitCode !== null) return;
  d.proc.kill(process.platform === "win32" ? undefined : "SIGINT");
  await d.exited;
}

type Task = {
  id: string;
  code: string;
  status: string;
  session_id?: string;
  machine_wait?: boolean;
};

test("an interrupted task continued from the command line stays one task", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-continue-"));
  const djinn = (...args: string[]) =>
    execFileSync(binary, args, {
      env: { ...process.env, DJINN_HOME: home },
      encoding: "utf8",
    });
  const task = (id: string) =>
    JSON.parse(djinn("task", "get", id, "--json")).task as Task;
  const title = `Continue in place ${randomUUID().slice(0, 8)}`;
  let running = await up(home);
  try {
    const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
      .id as string;
    const id = (
      JSON.parse(
        djinn(
          "task",
          "spawn",
          wishId,
          "--title",
          "Work cut short",
          "--provider",
          "fake",
          "--prompt",
          "text first\nsleep 1h",
          "--json",
        ),
      ).task as Task
    ).id;
    // The scheduler normally starts a fake worker immediately. On a busy host it can hold the planned task for
    // machine memory; starting a task explicitly is the same user action exposed by its Planned card and keeps this
    // lifecycle proof independent of the host's free memory.
    await expect
      .poll(
        () => {
          const current = task(id);
          return (
            current.session_id || (current.machine_wait ? "machine-wait" : "")
          );
        },
        { timeout: 10_000 },
      )
      .not.toBe("");
    if (task(id).machine_wait) djinn("task", "start", id);
    await expect
      .poll(() => task(id).session_id ?? "", { timeout: 10_000 })
      .not.toBe("");
    djinn("wish", "grant", wishId);
    await down(running);

    // Started again: the task stays cut short, its wish granted.
    running = await up(home);
    expect(task(id).status).toBe("TASK_STATUS_INTERRUPTED");
    djinn("wish", "activate", wishId);
    const continued = JSON.parse(
      djinn("task", "continue", id, "--prompt", "text continued", "--json"),
    ).task as Task;
    expect(continued.id).toBe(id);
    expect(continued.code).toBe("W1");
    // A resumed fake worker can be held by host memory too. Starting it explicitly is the same action exposed by its
    // Planned card; the completion assertion below still proves the continuation ran to the end.
    await expect
      .poll(
        () => {
          const current = task(id);
          return current.machine_wait ? "machine-wait" : current.status;
        },
        { timeout: 10_000 },
      )
      .toMatch(/^(machine-wait|TASK_STATUS_(RUNNING|DONE))$/);
    if (task(id).machine_wait) djinn("task", "start", id);
    await expect
      .poll(() => task(id).status, { timeout: 10_000 })
      .toBe("TASK_STATUS_DONE");

    await page.goto(running.url);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await openWorkspaceSection(page, "tasks");
    const tasksSection = page.locator('[data-wish-section="tasks"]');
    await expect(tasksSection.locator("strong")).toHaveText("Tasks");
    await expect(tasksSection.locator(".count")).toHaveText("1");
    // Empty workspace subgroups are omitted; the finished subgroup still
    // carries the single continued task below.
    await expect(page.locator(".tasks-free-moving")).toHaveCount(0);
    const finished = page.locator(".tasks-free-finished .wish-task");
    await expect(finished).toHaveCount(1);
    await expect(finished).toContainText("Work cut short");
    await finished.getByRole("button", { name: /Work cut short/ }).click();
    // A finished task folds its events.
    const drawer = page.locator("dialog.task-drawer-shell");
    await expect(drawer).toBeVisible();
    await drawer.locator(".wish-task-events-fold summary").click();
    await expect(
      drawer
        .locator(".wish-event")
        .getByText(/continued by the lead, was interrupted .*: text continued/),
    ).toBeVisible();
    await expect(
      drawer.locator(".wish-event").getByText("continued", { exact: true }),
    ).toBeVisible();
    const listed = JSON.parse(
      djinn("task", "list", "--wish-id", wishId, "--json"),
    ).tasks as Task[];
    expect(listed.map((t) => t.code)).toEqual(["W1"]);
    expect(errors).toEqual([]);
  } finally {
    await down(running);
    fs.rmSync(home, {
      recursive: true,
      force: true,
      maxRetries: 20,
      retryDelay: 250,
    });
  }
});
