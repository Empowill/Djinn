// Mark a task done from the window, against the real djinn up --browser (see global-setup.ts): in the Tasks tab, an
// interrupted task, imported with its wish, leaves what moves or waits and tops the finished tasks, saying who closed
// it and why; a cancelled task wears its own badge, which says on hover who cancelled it and why. Every run makes
// fresh identifiers, so --repeat-each works on the same djinn.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
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

test("a task marked done leaves what moves and tops the finished tasks", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const wishId = randomUUID();
  const azimaId = randomUUID();
  const title = `Close old work ${wishId.slice(0, 8)}`;
  const task = (
    code: string,
    taskTitle: string,
    status: string,
    partOf?: string,
    end?: string,
  ) => ({
    id: randomUUID(),
    wish_id: wishId,
    code,
    title: taskTitle,
    status: `TASK_STATUS_${status}`,
    create_time: "2026-10-07T21:00:00Z",
    ...(partOf ? { part_of: partOf } : {}),
    ...(end ? { end_time: end } : {}),
  });
  const file = path.join(
    fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-done-")),
    "wish.json",
  );
  fs.writeFileSync(
    file,
    JSON.stringify({
      version: 1,
      create_time: "2026-10-07T21:10:00Z",
      wish: { id: wishId, title, create_time: "2026-10-07T21:00:00Z" },
      tasks: [
        {
          id: azimaId,
          wish_id: wishId,
          code: "T1",
          title: "First milestone",
          kind: "TASK_KIND_AZIMA",
          status: "TASK_STATUS_PENDING",
          create_time: "2026-10-07T21:00:00Z",
        },
        task("W1", "Merged long ago", "DONE", azimaId, "2026-10-07T21:05:00Z"),
        task("W2", "Failed on a flaky test", "FAILED", azimaId),
        task("W3", "Still planned", "PENDING", azimaId),
      ],
    }),
  );
  djinn("wish", "import", file);
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const tasksTab = page.getByRole("tab", { name: /^Tasks/ });
    await expect(tasksTab).toHaveText("Tasks3");
    await tasksTab.click();
    const moving = page.locator(".tasks-moving");
    await expect(moving.locator("h2")).toHaveText("Moving or waiting0");
    await expect(page.locator(".tasks-finished")).toHaveCount(0);

    const azima = page.locator(".azima-card");
    await expect(azima.locator(".azima-heading strong")).toHaveText(
      "First milestone",
    );
    // Under the azima: moving parts W2 and W3; W1 is folded.
    await expect(azima.locator(".wish-task")).toHaveCount(2);
    await expect(azima.locator(".wish-task").first()).toContainText(
      "Failed on a flaky test",
    );
    await expect(azima.locator(".wish-task").nth(1)).toContainText(
      "Still planned",
    );
    await expect(azima.locator(".fold-line")).toHaveText("Show 1 finished");

    const card = azima.locator(".wish-task").filter({
      hasText: "Failed on a flaky test",
    });
    await card.getByRole("button", { name: "Mark done" }).click();
    await card
      .getByLabel("Why, in a few words (optional)")
      .fill("merged in Git");
    await card.getByRole("button", { name: "Mark done" }).last().click();

    // It leaves moving parts and joins the folded finished parts at the top.
    await expect(azima.locator(".wish-task")).toHaveCount(1);
    await expect(azima.locator(".wish-task")).toContainText("Still planned");
    await expect(azima.locator(".fold-line")).toHaveText("Show the 2 finished");
    await azima.locator(".fold-line").click();

    const allTasks = azima.locator(".wish-task");
    await expect(allTasks).toHaveCount(3);
    const first = allTasks.nth(1);
    await expect(first).toContainText("Failed on a flaky test");
    await expect(first.locator(".wish-task-closed")).toContainText(
      /Closed by you, .+: merged in Git/,
    );
    await expect(allTasks.nth(2)).toContainText("Merged long ago");
    await expect(page.locator(".tasks-finished")).toHaveCount(0);
    const listed = JSON.parse(
      djinn("task", "list", "--wish-id", wishId, "--json"),
    ).tasks as { code: string; status: string; closed?: { actor: string } }[];
    const closed = listed.find((t) => t.code === "W2");
    expect(closed?.status).toBe("TASK_STATUS_DONE");
    expect(closed?.closed?.actor).toBe("CLOSER_DEVELOPER");
    expect(errors).toEqual([]);
  } finally {
    // It may have been imported paused, when three wishes were active.
    try {
      djinn("wish", "pause", wishId);
    } catch {}
  }
});

test("a cancelled task says who cancelled it and why, on hover", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const wishId = randomUUID();
  const azimaId = randomUUID();
  const title = `Cancel old work ${wishId.slice(0, 8)}`;
  const file = path.join(
    fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-cancel-")),
    "wish.json",
  );
  fs.writeFileSync(
    file,
    JSON.stringify({
      version: 1,
      create_time: "2026-10-07T21:10:00Z",
      wish: { id: wishId, title, create_time: "2026-10-07T21:00:00Z" },
      tasks: [
        {
          id: azimaId,
          wish_id: wishId,
          code: "T1",
          title: "First milestone",
          kind: "TASK_KIND_AZIMA",
          status: "TASK_STATUS_PENDING",
          create_time: "2026-10-07T21:00:00Z",
        },
        {
          id: randomUUID(),
          wish_id: wishId,
          code: "W1",
          title: "Dropped on the way",
          status: "TASK_STATUS_CANCELLED",
          create_time: "2026-10-07T21:00:00Z",
          end_time: "2026-10-07T21:05:00Z",
          part_of: azimaId,
          cancelled: {
            actor: "CLOSER_LEAD",
            create_time: "2026-10-07T21:05:00Z",
            note: "superseded",
          },
        },
      ],
    }),
  );
  djinn("wish", "import", file);
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    // Its only part finished, the azima shows folded: opening it is the click.
    const azima = page.locator(".azima-card");
    const heading = azima.locator(".azima-heading");
    await expect(heading).toHaveAttribute("aria-expanded", "false");
    await heading.click();
    const card = azima
      .locator(".wish-task")
      .filter({ hasText: "Dropped on the way" });
    // Its own muted badge, never the done one; who cancelled it and why on hover, compact, with no note under it.
    const badge = card.locator(".status-badge");
    await expect(badge).toHaveText("Cancelled");
    await expect(badge).toHaveClass(/tone-cancelled/);
    await expect(badge).toHaveAttribute("title", "By the lead: superseded");
    await expect(card.locator(".wish-task-closed")).toHaveCount(0);
    expect(errors).toEqual([]);
  } finally {
    try {
      djinn("wish", "pause", wishId);
    } catch {}
  }
});

// W507: the lead made a wish with no project and one research task; its worker ended done while its permissions
// refused 6 calls. The window showed no task, read the wish's rank as 3 questions, and proposed the grant.
test("a task done with problems, in a wish with no project, waits in sight until marked done; then the grant", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const wishId = randomUUID();
  const title = `Install Wispr Flow ${wishId.slice(0, 8)}`;
  const file = path.join(
    fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-problems-")),
    "wish.json",
  );
  fs.writeFileSync(
    file,
    JSON.stringify({
      version: 1,
      create_time: "2026-10-12T19:02:00Z",
      wish: { id: wishId, title, create_time: "2026-10-12T19:00:57Z" },
      tasks: [
        {
          id: randomUUID(),
          wish_id: wishId,
          code: "W1",
          title: "Find a safe way to run Wispr Flow on Linux",
          status: "TASK_STATUS_DONE",
          create_time: "2026-10-12T19:00:58Z",
          end_time: "2026-10-12T19:01:29Z",
          refused: { count: 6, names: ["Read", "Glob"] },
        },
      ],
    }),
  );
  djinn("wish", "import", file);
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(page.locator(".review-head .eyebrow")).toHaveText(
      "No project",
    );
    // No question, so no count of them; no grant: the task is what waits for you, named.
    await expect(page.locator(".review-pills")).not.toContainText("to decide");
    await expect(
      page.getByRole("button", { name: "My wish is granted" }),
    ).toHaveCount(0);
    await expect(page.locator(".attention-bar")).toContainText(
      "W1done with problems: its permissions refused 6 calls",
    );
    const line = page.locator(".action-center .plan-line");
    await expect(line).toContainText(
      "Find a safe way to run Wispr Flow on Linux · done with problems: its permissions refused 6 calls (Read, Glob)",
    );
    await expect(line.locator(".status-badge")).toHaveText(
      "Done with problems",
    );

    // Its code opens its card in the Tasks tab: in sight with what moves or waits, amber.
    await line.getByRole("button", { name: "W1" }).click();
    const tasksTab = page.getByRole("tab", { name: /^Tasks/ });
    await expect(tasksTab).toHaveAttribute("aria-selected", "true");
    await expect(tasksTab).toHaveText("Tasks1");
    const moving = page.locator(".tasks-moving");
    await expect(moving.locator("h2")).toHaveText("Moving or waiting1");
    const card = moving.locator(".wish-task");
    await expect(card).toHaveClass(/tone-interrupted/);
    await expect(card.locator(".status-badge").first()).toHaveText(
      "Done with problems",
    );

    // Marked done, it is seen: a clean done, with the finished work part of no azima, unfolded since the link
    // brought it into sight.
    await card.getByRole("button", { name: "Mark done" }).click();
    await card
      .getByLabel("Why, in a few words (optional)")
      .fill("read it: no install needed");
    await card.getByRole("button", { name: "Mark done" }).last().click();
    await expect(moving.locator("h2")).toHaveText("Moving or waiting0");
    const finished = page.locator(".tasks-finished");
    await expect(finished.locator("h2")).toHaveText("Finished1");
    await expect(finished.locator(".fold-line")).toHaveText(
      "Hide the finished ones",
    );
    await expect(
      finished.locator(".wish-task .status-badge").first(),
    ).toHaveText("Done");
    await expect(finished.locator(".wish-task-closed")).toContainText(
      /Closed by you, .+: read it: no install needed/,
    );
    const listed = JSON.parse(
      djinn("task", "list", "--wish-id", wishId, "--json"),
    ).tasks as { code: string; status: string; closed?: { actor: string } }[];
    expect(listed[0]?.status).toBe("TASK_STATUS_DONE");
    expect(listed[0]?.closed?.actor).toBe("CLOSER_DEVELOPER");

    // Nothing is left to look at: Djinn proposes the grant.
    await page.getByRole("tab", { name: /^Wish/ }).click();
    await expect(
      page.getByRole("button", { name: "My wish is granted" }),
    ).toBeVisible();
    expect(errors).toEqual([]);
  } finally {
    try {
      djinn("wish", "pause", wishId);
    } catch {}
  }
});
