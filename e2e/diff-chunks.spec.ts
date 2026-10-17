// A file's diff past 2 000 lines comes by chunks: the window shows the first one, then asks for the next as the reader
// scrolls near its end, never the whole file at once. The task's worker is the fake agent: no model is called. While
// it waits, the spec commits a file of 6 000 lines in the task's worktree: its diff is 6 006 lines, four chunks.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { portable } from "./portable";

portable();

// Budgets in milliseconds, set from first measurements on Linux with margin: the first chunk shows ~600 ms after the
// click (budget 2 000 ms), a next chunk ~500 ms after its row nears the view, the third as fast as the first: rows
// already shown are not rendered again (budget 1 500 ms).
const BUDGET_FIRST_CHUNK_MS = 2_000;
const BUDGET_NEXT_CHUNK_MS = 1_500;

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

test(
  "a diff of 6 000 lines loads by chunks as the reader scrolls, within budget",
  { tag: "@budget" },
  async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));
    // The window's calls of TaskService.Diff (binary protobuf): the list of files, then one per chunk.
    let asked = 0;
    page.on("request", (req) => {
      if (req.url().includes("TaskService/Diff")) asked++;
    });
    const suffix = randomUUID().slice(0, 8);
    const folder = fs.realpathSync(
      fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "scroll-")),
    );
    git(folder, "init", "--quiet", "-b", "main");
    fs.writeFileSync(path.join(folder, "README.md"), "A scroll.\n");
    git(folder, "add", ".");
    git(folder, "commit", "--quiet", "-m", "A scroll");
    const projectId = JSON.parse(
      djinn("project", "add", folder, "--name", `scroll-${suffix}`, "--json"),
    ).project.id as string;
    const title = `Read a long scroll ${suffix}`;
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
        "Unroll the scroll",
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
      const rows = Array.from(
        { length: 6000 },
        (_, i) => `row ${i + 1} of the scroll`,
      );
      fs.writeFileSync(
        path.join(worktree, "scroll.txt"),
        rows.join("\n") + "\n",
      );
      git(worktree, "add", "scroll.txt");
      git(worktree, "commit", "--quiet", "-m", "A long scroll");

      await page.goto(process.env.DJINN_URL!);
      await page.locator(".wish-nav").filter({ hasText: title }).click();
      await page.getByRole("tab", { name: /^Tasks/ }).click();
      await page.getByRole("button", { name: /Unroll the scroll/ }).click();

      const row = (n: number) =>
        page
          .locator(".diff-line-row")
          .filter({ hasText: `row ${n} of the scroll` });
      // Timed in the page, from the click (or the scroll) until the chunk's rows are all there: Playwright's actions
      // snapshot the DOM for their trace, longer than the window takes on 6 000 rows.
      const until = (rows: number, action: "click" | "scroll") =>
        page.evaluate(
          ([rows, action]) =>
            new Promise<number>((resolve) => {
              const started = performance.now();
              if (action === "click")
                document
                  .querySelector<HTMLElement>(".wish-task-diff-button")!
                  .click();
              else document.querySelector(".diff-next-chunk")!.scrollIntoView();
              const check = () => {
                if (document.querySelectorAll(".diff-line-row").length === rows)
                  resolve(performance.now() - started);
                else requestAnimationFrame(check);
              };
              check();
            }),
          [rows, action] as const,
        );
      const first = await until(1994, "click");
      await expect(row(1)).toBeVisible();

      // The first chunk alone: the diff's header and hunk header take 6 of its 2 000 lines.
      const next = page.locator(".diff-next-chunk");
      await expect(next).toContainText("2000 of 6006 lines shown");
      await expect(page.locator(".diff-line-row")).toHaveCount(1994);
      await expect(row(1995)).toHaveCount(0);
      expect(asked).toBe(2);

      // Each next chunk loads as its row nears the view, in the same hunk: one header for the whole file.
      const nexts: number[] = [];
      for (const rows of [3994, 5994, 6000])
        nexts.push(await until(rows, "scroll"));
      await expect(next).toHaveCount(0);
      await expect(page.locator(".diff-line-row")).toHaveCount(6000);
      await expect(page.locator(".diff-hunk-header")).toHaveCount(1);
      await row(6000).scrollIntoViewIfNeeded();
      await expect(row(6000)).toBeVisible();
      expect(asked).toBe(5);

      console.log(
        `diff by chunks: first ${Math.round(first)} ms, next ${nexts.map(Math.round).join(", ")} ms`,
      );
      expect(first).toBeLessThanOrEqual(BUDGET_FIRST_CHUNK_MS);
      for (const ms of nexts)
        expect(ms).toBeLessThanOrEqual(BUDGET_NEXT_CHUNK_MS);
      expect(errors).toEqual([]);
    } finally {
      djinn("task", "stop", taskId);
      djinn("wish", "pause", wishId);
    }
  },
);
