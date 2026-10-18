// A task that waited, then started, against the real djinn up --browser (see global-setup.ts): its card says why it
// waits while it does, and nothing once it runs; its page shows the waiting line as past, folded and muted into the
// start that ended it, which says how long it waited and for what (W371). The workers are the fake agent: no model is
// called.
import { expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
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

test("a held task that starts loses its wait line, and its page shows the wait as past", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state?: string }) =>
      w.state !== "WISH_STATE_PAUSED" && w.state !== "WISH_STATE_GRANTED",
  ))
    djinn("wish", "pause", w.id);

  const title = `Wait your turn ${randomUUID().slice(0, 8)}`;
  const made = JSON.parse(djinn("wish", "make", title, "--json")).wish;
  const wishId = made.id as string;
  if (made.state !== "WISH_STATE_ACTIVE") {
    djinn("wish", "activate", wishId);
  }
  const spawn = (taskTitle: string, prompt: string, ...more: string[]) =>
    JSON.parse(
      djinn(
        "task",
        "spawn",
        wishId,
        "--title",
        taskTitle,
        "--provider",
        "fake",
        "--prompt",
        prompt,
        ...more,
        "--json",
      ),
    ).task as { id: string; code: string };
  const first = spawn("Go first", "text working\nwait\ntext done");
  const second = spawn(
    "Go second",
    "text started\nsleep 60s\ntext after",
    "--after",
    first.code,
    "--later",
  );
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    const card = page.locator(`#task-${second.id}`);
    const badge = card.locator(".wish-task-heading .status-badge");
    const waitNote = card.locator(".wish-task-note", {
      hasText: `waits for ${first.code} (running)`,
    });
    await expect(waitNote).toBeVisible();

    // The first ends; the second starts, and its card no longer says it waits.
    djinn("task", "send", first.id, "done");
    await expect(badge).toHaveText("Running", { timeout: 20_000 });
    await expect(waitNote).toHaveCount(0);

    await card.getByRole("button", { name: /Go second/ }).click();
    await card.locator(".wish-task-events-fold summary").click();
    const start = card.locator(".wish-event", { hasText: /started fake/ });
    // The start names the last cause: on a machine that holds one worker (a 4-core CI runner), the second still waits
    // for the first's slot a moment after the first ended.
    await expect(start).toContainText(
      new RegExp(
        `after waiting \\d+ s: (waits for ${first.code} \\(running\\)|1 worker runs, the most this machine holds)`,
      ),
    );
    // The waiting line is past: folded into the start (with the slot wait when one followed), never a line of its own.
    await expect(
      start.locator(".wish-event-waited", {
        hasText: `waiting: waits for ${first.code} (running)`,
      }),
    ).toBeVisible();
    await expect(
      card.locator(".wish-event-text").filter({ hasText: /^waiting: / }),
    ).toHaveCount(0);
    expect(errors).toEqual([]);
  } finally {
    djinn("task", "stop", second.id);
    djinn("wish", "pause", wishId);
  }
});
