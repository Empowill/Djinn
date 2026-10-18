// W486: the wish Dew, made with djinn wish make --prompt while the window was open, held two tasks the lead made from
// the command line: T1, an azima with no plan file, and W1, a worker part of it, started a moment after its spawn. The
// window showed W1 nowhere and counted no running worker until W1 ended. The spec makes the same wish with the fake
// agent: W1 shows in T1's card, opened by itself, and counts as running in the side panel, the wish's head and the
// status bar. Then the window opens again while djinn fails to give the wish's tasks once: W1 shows and counts all
// the same, without waiting for W1 to change, which the fake worker does not while it runs.
import { type Page, expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

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

type Task = { id: string; code: string; status: string };

const task = (id: string) =>
  JSON.parse(djinn("task", "get", id, "--json")).task as Task;

// shown checks that W1 shows, and counts as running everywhere the window counts a running worker.
async function shown(page: Page, title: string) {
  const nav = page.locator(".wish-nav").filter({ hasText: title });
  await expect(nav.getByRole("img", { name: "1 running" })).toBeVisible();
  await expect(page.locator(".app-statusbar")).toContainText("Workers 1/", {
    timeout: 10_000,
  });

  await nav.click();
  await expect(
    page.locator(".review-pills").getByRole("img", { name: "1 running" }),
  ).toBeVisible();
  await page.getByRole("tab", { name: /^Tasks/ }).click();
  // Part of T1, W1 is no loose task: it shows in T1's card, opened by itself while its part runs.
  await expect(page.locator(".tasks-moving .wish-task")).toHaveCount(0);
  const card = page.locator(".azima-card").filter({ hasText: "Silae" });
  await expect(card.locator(".azima-heading")).toHaveAttribute(
    "aria-expanded",
    "true",
  );
  const part = card.locator(".wish-task");
  await expect(part).toHaveCount(1);
  await expect(part).toContainText("Silae feasibility study");
  await expect(part).toContainText("Running");
}

test("a worker spawned from the command line part of a new azima shows and counts while it runs", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const suffix = randomUUID().slice(0, 8);
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "dolmen-")),
  );
  git(folder, "init", "--quiet", "-b", "main");
  fs.writeFileSync(path.join(folder, "README.md"), "A dolmen.\n");
  git(folder, "add", ".");
  git(folder, "commit", "--quiet", "-m", "A dolmen");
  const projectId = JSON.parse(
    djinn("project", "add", folder, "--name", `dolmen-${suffix}`, "--json"),
  ).project.id as string;

  // The window is open before the wish is made.
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();

  const wishId = JSON.parse(
    djinn(
      "wish",
      "make",
      "--project-ids",
      projectId,
      "--prompt",
      "Can the file be made without the salaries?",
      "--json",
    ),
  ).wish.id as string;
  // The lead names the wish once it starts.
  const title = `Dew ${suffix}`;
  djinn("wish", "rename", wishId, "--title", title);
  try {
    // Open the wish's Tasks tab while T1 is created and W1 is grouped into it from the command line.
    const nav = page.locator(".wish-nav").filter({ hasText: title });
    await nav.click();
    await page.getByRole("tab", { name: /^Tasks/ }).click();

    // T1: an azima made from the command line, with no plan file in the project.
    const t1 = JSON.parse(
      djinn(
        "task",
        "spawn",
        wishId,
        "--kind",
        "azima",
        "--title",
        "Silae",
        "--description",
        "Can the file be made without the salaries?",
        "--project-id",
        projectId,
        "--json",
      ),
    ).task as Task;
    expect(t1.code).toBe("T1");
    expect(fs.existsSync(path.join(folder, "plan"))).toBe(false);
    const card = page.locator(".azima-card").filter({ hasText: "Silae" });
    await expect(card).toBeVisible();
    await expect(card.locator(".azima-heading")).toHaveAttribute(
      "aria-expanded",
      "false",
    );

    // W1 starts a moment after its spawn, as the real one did: it waits for a task that sleeps.
    const before = JSON.parse(
      djinn(
        "task",
        "spawn",
        wishId,
        "--title",
        "Before",
        "--provider",
        "fake",
        "--prompt",
        "sleep 1s",
        "--project-id",
        projectId,
        "--json",
      ),
    ).task as Task;
    const w1 = JSON.parse(
      djinn(
        "task",
        "spawn",
        wishId,
        "--title",
        "Silae feasibility study",
        "--provider",
        "fake",
        "--prompt",
        "wait",
        "--after",
        before.code,
        "--project-id",
        projectId,
        "--json",
      ),
    ).task as Task;
    expect(w1.status).not.toBe("TASK_STATUS_RUNNING");
    await expect(
      page
        .locator(".tasks-moving .wish-task")
        .filter({ hasText: "Silae feasibility study" }),
    ).toBeVisible();

    // Grouping W1 into T1 from the command line moves it out of loose tasks and opens T1's card without a reload.
    djinn("task", "update", w1.id, "--part-of", "T1");
    await expect(
      page
        .locator(".tasks-moving .wish-task")
        .filter({ hasText: "Silae feasibility study" }),
    ).toHaveCount(0);
    await expect(card.locator(".azima-heading")).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    await expect(
      card.locator(".wish-task").filter({ hasText: "Silae feasibility study" }),
    ).toBeVisible();

    await expect
      .poll(() => task(w1.id).status, { timeout: 20_000 })
      .toBe("TASK_STATUS_RUNNING");

    await shown(page, title);

    // The window opens again, and djinn fails once to give the wish's tasks: they are read again.
    let failed = 0;
    await page.route("**/plan.v1.TaskService/List", async (route) => {
      const body = route.request().postDataBuffer()?.toString("latin1") ?? "";
      if (failed === 0 && body.includes(wishId)) {
        failed++;
        await route.abort("failed");
        return;
      }
      await route.continue();
    });
    await page.reload();
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();
    await expect.poll(() => failed).toBe(1);
    await shown(page, title);
    expect(task(w1.id).status).toBe("TASK_STATUS_RUNNING");
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});
