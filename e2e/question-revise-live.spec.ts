// Incident Q274: a question showed "Being investigated" in the window although its investigator had revised it, and
// only a reload showed it waiting for the developer again. A revision reaches the card at once, by the investigator
// or by the lead from the command line, in the wish and in the flying carpet. The store's own regression tests
// (tests/data-store.test.mjs) replay the reads that crossed the revision, and a watch back after a restart.
import { type Page, expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
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

type Task = { id: string; role?: string };

// investigator waits for the worker Djinn started on the wish's request to investigate.
async function investigator(wishId: string): Promise<string> {
  let id = "";
  await expect
    .poll(() => {
      const tasks: Task[] =
        JSON.parse(djinn("task", "list", "--wish-id", wishId, "--json"))
          .tasks ?? [];
      id = tasks.find((t) => t.role === "TASK_ROLE_INVESTIGATOR")?.id ?? "";
      return id;
    })
    .not.toBe("");
  return id;
}

const card = (page: Page, text: string) =>
  page.locator(".question-card").filter({ hasText: text });

test("a revision reaches the card under investigation without a reload", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const title = `Revised live ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  try {
    const text = "Which wick for the lamp?";
    djinn(
      "question",
      "ask",
      text,
      wishId,
      "--options",
      "Cotton",
      "--options",
      "Linen",
      "--recommendation",
      "A: it burns evenly.",
    );
    await page.goto(process.env.DJINN_URL!);
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const question = card(page, text);
    await expect(question).toBeVisible();

    // Enlightened from the window: Djinn starts its investigator, a fake claude that does nothing.
    await question.getByLabel("Note with the answer").fill("Which lasts?");
    await question.getByRole("button", { name: "Enlighten me" }).click();
    await expect(question).toHaveClass(/investigating/);
    const worker = await investigator(wishId);

    // The investigator revises, as its command does: the card waits for the developer again, revised.
    djinn(
      "question",
      "revise",
      "Q01",
      "--wish-id",
      wishId,
      "--task-id",
      worker,
      "--context",
      "## What the worker found\n\nLinen burns twice as long.",
      "--options",
      "Cotton",
      "--options",
      "Linen, twice as long",
      "--recommendation",
      "B: it lasts.",
    );
    await expect(question).not.toHaveClass(/investigating/);
    await expect(question).toHaveClass(/open/);
    await expect(question.locator(".revised-badge")).toHaveText("Revised");
    // The whole context: what the worker found, then the history of its versions folded under Previous versions,
    // closed, never cut (mockup L04 v2, W69).
    const headings = question.locator(".question-context h2");
    await expect(headings).toHaveCount(1);
    await expect(headings.first()).toHaveText("What the worker found");
    const history = question.locator(".question-history");
    await expect(history.locator("summary")).toHaveText("Previous versions");
    await expect(history).not.toHaveAttribute("open");
    await expect(history.locator("h3")).toHaveText("Tour 0 (initial)");
    await expect(
      question.getByRole("button", { name: /Linen, twice as long/ }),
    ).toHaveAttribute("aria-pressed", "true");

    // Enlightened again from the command line, then revised by the lead, while the window shows the flying carpet.
    await page.keyboard.press("Control+1");
    await expect(page.locator(".hero h1")).toHaveText("Flying carpet");
    djinn(
      "question",
      "enlighten",
      "Q01",
      "--wish-id",
      wishId,
      "--note",
      "And the price?",
    );
    const digging = page
      .locator(".investigating-section .question-card")
      .filter({ hasText: text });
    await expect(digging).toBeVisible();
    djinn(
      "question",
      "revise",
      "Q01",
      "--wish-id",
      wishId,
      "--context",
      "## The price\n\nLinen costs as much.",
    );
    await expect(digging).toHaveCount(0);
    const waiting = card(page, text);
    await expect(waiting).toHaveClass(/open/);
    await expect(waiting.locator(".question-context h2").first()).toHaveText(
      "The price",
    );
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "delete", wishId);
  }
});
