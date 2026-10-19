// An instruction sent from the window to a running worker, and a comment on a line of its diff: each is an event of the
// task at once, then "received" once the worker says something after it. The worker is the fake agent: no model is
// called. While it waits, the spec commits in the task's worktree itself, for the diff to show a line.
import { expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { portable } from "./portable";

portable();

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

function git(dir: string, ...args: string[]): string {
  return execFileSync(
    "git",
    ["-c", "user.name=e2e", "-c", "user.email=e2e@example.com", ...args],
    { cwd: dir, encoding: "utf8" },
  );
}

test("an instruction and a comment on a line of a running task's diff go to its worker", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const suffix = randomUUID().slice(0, 8);
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "wick-")),
  );
  git(folder, "init", "--quiet", "-b", "main");
  fs.writeFileSync(path.join(folder, "README.md"), "A candle.\n");
  git(folder, "add", ".");
  git(folder, "commit", "--quiet", "-m", "A candle");
  const projectId = JSON.parse(
    djinn("project", "add", folder, "--name", `wick-${suffix}`, "--json"),
  ).project.id as string;
  const title = `Review a line ${suffix}`;
  const wishId = JSON.parse(
    djinn("wish", "make", title, "--project-ids", projectId, "--json"),
  ).wish.id as string;
  const taskId = JSON.parse(
    djinn(
      "task",
      "spawn",
      wishId,
      "--project-id",
      projectId,
      "--title",
      "Trim the wick",
      "--provider",
      "fake",
      "--prompt",
      "text working\nwait\ntext mid\n" +
        "sleep 300ms\ntext tick\n".repeat(40) +
        "sleep 5s",
      "--json",
    ),
  ).task.id as string;
  try {
    let worktree = "";
    await expect
      .poll(() => {
        const task = JSON.parse(djinn("task", "get", taskId, "--json")).task;
        worktree = task.status === "TASK_STATUS_RUNNING" ? task.worktree : "";
        return worktree;
      })
      .not.toBe("");
    fs.writeFileSync(path.join(worktree, "wick.txt"), "a long wick\n");
    git(worktree, "add", "wick.txt");
    git(worktree, "commit", "--quiet", "-m", "A wick");

    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    await page.getByRole("button", { name: /Trim the wick/ }).click();
    await page.locator(".wish-task-events-fold summary").click();
    await expect(
      page.locator(".wish-event").getByText("working", { exact: true }),
    ).toBeVisible();

    const box = page.getByLabel(
      "An instruction for the worker, while it works",
    );
    await box.fill("Also update the docs");
    await box.press("Enter");
    await expect(box).toHaveValue("", { timeout: 10000 });
    await expect(
      page.locator(".wish-event.kind-11").getByText("Also update the docs"),
    ).toBeVisible();
    await expect(
      page
        .locator(".wish-event.kind-12")
        .getByText("Received: Also update the docs"),
    ).toBeVisible({ timeout: 15_000 });

    await page.locator(".wish-task-diff-button").click();
    const line = page
      .locator(".diff-line-row")
      .filter({ hasText: "a long wick" });
    await line.hover();
    await line.getByRole("button", { name: "Add comment" }).click();
    await page.getByLabel("Comment on line 1").fill("Trim it shorter");
    await page.getByRole("button", { name: "Comment", exact: true }).click();
    await expect(
      page.locator(".diff-comment-text").getByText("Trim it shorter"),
    ).toBeVisible();
    // The diff's review covers the main shell: Escape closes it, back to the task's card.
    await page.keyboard.press("Escape");
    await expect(page.locator(".task-review")).toHaveCount(0);

    await expect(
      page
        .locator(".wish-event.kind-11")
        .getByText("Review comment on wick.txt, line 1:"),
    ).toBeVisible();
    await expect(
      page
        .locator(".wish-event.kind-12")
        .getByText("Received: Review comment on wick.txt, line 1:"),
    ).toBeVisible({ timeout: 15_000 });
    const comments = JSON.parse(djinn("task", "get", taskId, "--json")).task
      .comments;
    expect(comments).toEqual([
      expect.objectContaining({
        file: "wick.txt",
        line: 1,
        excerpt: "a long wick",
        sent_to: taskId,
      }),
    ]);
    expect(errors).toEqual([]);
  } finally {
    djinn("task", "stop", taskId);
    djinn("wish", "pause", wishId);
  }
});
