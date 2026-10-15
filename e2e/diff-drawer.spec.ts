// A task's diffs open in a drawer from the right of the window: its committed work on one tab, what its worktree holds
// not committed on the other, read only. The worker is the fake agent: no model is called. While it waits, the spec
// commits a file in the task's worktree, then leaves a change and a new file there without committing them.
import { expect, test } from "@playwright/test";
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

test("a task's committed and uncommitted diffs open in a drawer from the right", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const suffix = randomUUID().slice(0, 8);
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "drawer-")),
  );
  git(folder, "init", "--quiet", "-b", "main");
  fs.writeFileSync(path.join(folder, "lamp.txt"), "a brass lamp\n");
  git(folder, "add", ".");
  git(folder, "commit", "--quiet", "-m", "A lamp");
  const projectId = JSON.parse(
    djinn("project", "add", folder, "--name", `drawer-${suffix}`, "--json"),
  ).project.id as string;
  const title = `Polish the lamp ${suffix}`;
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
      "Rub the lamp",
      "--provider",
      "fake",
      "--prompt",
      "text working\nwait",
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
    fs.writeFileSync(path.join(worktree, "wick.txt"), "a committed wick\n");
    git(worktree, "add", "wick.txt");
    git(worktree, "commit", "--quiet", "-m", "A wick");
    fs.writeFileSync(path.join(worktree, "lamp.txt"), "a shining lamp\n");
    fs.writeFileSync(path.join(worktree, "smoke.md"), "a wisp of smoke\n");

    await page.setViewportSize({ width: 1600, height: 1000 });
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    await page.getByRole("button", { name: /Rub the lamp/ }).click();

    // The committed diff: the drawer stands against the right edge, most of the window wide.
    await page.getByRole("button", { name: "See the diff" }).click();
    const drawer = page.getByRole("dialog", { name: /^Diff of / });
    await expect(drawer).toBeVisible();
    // Measured once it has slid in.
    await expect
      .poll(async () => {
        const box = (await drawer.boundingBox())!;
        return Math.round(box.x + box.width);
      })
      .toBe(1600);
    const box = (await drawer.boundingBox())!;
    expect(box.width).toBeGreaterThan(1600 * 0.8);
    expect(box.height).toBe(1000);
    const committed = drawer.getByRole("tab", {
      name: "Committed",
      exact: true,
    });
    await expect(committed).toHaveAttribute("aria-selected", "true");
    const files = drawer.locator(".diff-file-btn");
    await expect(files).toHaveText(["wick.txt+1"]);
    await expect(
      drawer.locator(".diff-line-row").filter({ hasText: "a committed wick" }),
    ).toBeVisible();

    // The worktree's tab: the change and the new file, not the commit, and no comment on them.
    await drawer.getByRole("tab", { name: "Not committed" }).click();
    await expect(files).toHaveText(["lamp.txt+1-1", "smoke.md+1"]);
    await expect(
      drawer.locator(".diff-line-row").filter({ hasText: "a shining lamp" }),
    ).toBeVisible();
    await files.filter({ hasText: "smoke.md" }).click();
    const smoke = drawer
      .locator(".diff-line-row")
      .filter({ hasText: "a wisp of smoke" });
    await expect(smoke).toBeVisible();
    await smoke.hover();
    await expect(drawer.locator(".diff-add-comment-btn")).toHaveCount(0);

    // A click beside the drawer closes it.
    await page.mouse.click(20, 500);
    await expect(drawer).toHaveCount(0);

    // The card's second button opens the worktree's tab; once the worktree is clean, refresh says so.
    await page.getByRole("button", { name: "See uncommitted files" }).click();
    await expect(
      drawer.getByRole("tab", { name: "Not committed" }),
    ).toHaveAttribute("aria-selected", "true");
    await expect(files).toHaveCount(2);
    git(worktree, "checkout", "--", "lamp.txt");
    fs.rmSync(path.join(worktree, "smoke.md"));
    await drawer.getByRole("button", { name: "Read the diff again" }).click();
    await expect(drawer.locator(".diff-empty")).toHaveText(
      "The worktree is clean: nothing left uncommitted.",
    );

    // Escape closes it, the focus back on the button that opened it.
    await page.keyboard.press("Escape");
    await expect(drawer).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "See uncommitted files" }),
    ).toBeFocused();
    expect(errors).toEqual([]);
  } finally {
    djinn("task", "stop", taskId);
    djinn("wish", "pause", wishId);
  }
});
