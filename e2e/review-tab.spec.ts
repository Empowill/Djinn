// A wish's Review tab lists its merged work: a task whose fake worker committed a file, integrated by Djinn into the
// wish's branch (a project check that always passes), with a comment on a line of its diff. The tab counts it, opens
// its diff, checks it reviewed, and the mark is kept: the command line lists it, and a reload shows it still. No
// model is called. Screenshots, dark and light, go to test-results/e2e/.
import { type Page, expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const shots = path.join(__dirname, "../test-results/e2e");

// djinn runs the command line, in cwd when given: a worker runs it in its worktree.
function djinn(...args: string[]): string {
  return djinnIn(undefined, ...args);
}

function djinnIn(cwd: string | undefined, ...args: string[]): string {
  return execFileSync(binary, args, {
    cwd,
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

async function theme(page: Page, value: "" | "light") {
  await page.evaluate((v) => {
    if (v) localStorage.setItem("djinn.theme", v);
    else localStorage.removeItem("djinn.theme");
  }, value);
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme",
    value || "dark",
  );
}

test("the Review tab lists a merged task, opens its diff with its comment, and keeps it reviewed", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const suffix = randomUUID().slice(0, 8);
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lamp-")),
  );
  git(folder, "init", "--quiet", "-b", "main");
  // The merge Djinn commits takes the repository's identity: a CI runner has none of its own (e2e/global-setup.ts).
  git(folder, "config", "user.name", "e2e");
  git(folder, "config", "user.email", "e2e@example.com");
  fs.writeFileSync(path.join(folder, "README.md"), "A lamp.\n");
  // A check names the project's work as integrated by Djinn: one that always passes.
  fs.mkdirSync(path.join(folder, ".agents"));
  fs.writeFileSync(
    path.join(folder, ".agents", "settings.txtpb"),
    'checks { name: "lint" command: "git --version" when: CHECK_WHEN_COMMIT }\n',
  );
  git(folder, "add", ".");
  git(folder, "commit", "--quiet", "-m", "A lamp");
  const projectId = JSON.parse(
    djinn("project", "add", folder, "--name", `lamp-${suffix}`, "--json"),
  ).project.id as string;
  const title = `Review the lamp ${suffix}`;
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
      "Polish the lamp",
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
    fs.writeFileSync(path.join(worktree, "lamp.txt"), "brass\npolished\n");
    git(worktree, "add", "lamp.txt");
    git(worktree, "commit", "--quiet", "-m", "Polish the lamp");
    // The comment, from the worktree as a worker would, goes to the running worker, which ends on it.
    djinnIn(
      worktree,
      "task",
      "comment",
      taskId,
      "Shine it more",
      "--file",
      "lamp.txt",
      "--line",
      "2",
    );
    // The whole integration, so that a failure shows its reason.
    await expect
      .poll(
        () =>
          JSON.parse(djinn("task", "get", taskId, "--json")).task.integration,
        { timeout: 60_000 },
      )
      .toMatchObject({ state: "INTEGRATION_STATE_COMMITTED" });

    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const tab = page.getByRole("tab", { name: /^Review/ });
    await expect(tab.locator(".count")).toHaveText("1");
    await tab.click();
    const item = page
      .locator(".review-item")
      .filter({ hasText: "Polish the lamp" });
    await expect(item.locator(".review-comments")).toHaveText("1");
    // Its diff opens in place, with the comment on its line.
    await item.locator(".review-open").click();
    await expect(item.locator(".review-comment-list")).toContainText(
      "lamp.txt:2Shine it more",
    );
    await expect(
      item.locator(".diff-line-row").filter({ hasText: "polished" }),
    ).toBeVisible({ timeout: 15_000 });
    await expect(item.locator(".diff-comment-item")).toContainText(
      "Shine it more",
    );
    if (process.env.REVIEW_SHOTS) {
      fs.mkdirSync(shots, { recursive: true });
      await page.screenshot({ path: path.join(shots, "review-tab-dark.png") });
    }

    // Checked: the count falls to 0, the line folds with who checked it; the lamp keeps it.
    const check = item.getByRole("checkbox");
    await expect(check).toHaveAttribute("aria-checked", "false");
    await check.click();
    await expect(tab.locator(".count")).toHaveText("0");
    await expect(
      page.getByRole("button", { name: "Show 1 reviewed" }),
    ).toBeVisible();
    const marks = JSON.parse(djinn("mark", "list", wishId, "--json")).marks;
    expect(marks).toHaveLength(1);
    expect(marks[0].task_id).toBe(taskId);
    expect(marks[0].mark.kind).toBe("MARK_KIND_REVIEWED");
    expect(marks[0].mark.actor).toBe("local");

    if (process.env.REVIEW_SHOTS) await theme(page, "light");
    else await page.reload();
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await page.getByRole("tab", { name: /^Review/ }).click();
    await page.getByRole("button", { name: "Show 1 reviewed" }).click();
    const kept = page
      .locator(".review-item")
      .filter({ hasText: "Polish the lamp" });
    await expect(kept.getByRole("checkbox")).toHaveAttribute(
      "aria-checked",
      "true",
    );
    await expect(kept.locator(".review-by")).toContainText("you, ");
    if (process.env.REVIEW_SHOTS) {
      await page.screenshot({ path: path.join(shots, "review-tab-light.png") });
      await theme(page, "");
    }
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});
