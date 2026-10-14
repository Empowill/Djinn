// An option of a question picks, it never answers (W574): a click, a double click, Enter or Space on a focused option,
// Enter on the card around it only select; the answer leaves by Rub the lamp alone, with the option picked last.
import { expect, test } from "@playwright/test";
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

test("an option picks and never answers: only Rub the lamp sends", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();

  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const title = `Option picks ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;

  // answer is the question's answer as djinn has it, once a gesture had time to send one: its button stays down
  // 150 ms before the answer leaves.
  const answer = async () => {
    await page.waitForTimeout(500);
    const [q] = JSON.parse(
      djinn("question", "list", "--wish-id", wishId, "--json"),
    ).questions;
    return q.answer as { choice?: string } | undefined;
  };

  try {
    const item = page.locator(".wish-nav").filter({ hasText: title });
    await expect(item).toBeVisible();
    await item.click();

    djinn(
      "question",
      "ask",
      "Go on or stop?",
      wishId,
      "--options",
      "Go on",
      "--options",
      "Stop",
      "--context",
      "Stopping drops the branch.",
      "--recommendation",
      "**A**: the branch is nearly done.",
    );
    const card = page
      .locator(".question-card")
      .filter({ hasText: "Go on or stop?" });
    await expect(card).toBeVisible();
    const goOn = card.getByRole("button", { name: /Go on/ });
    const stop = card.getByRole("button", { name: /Stop/ });
    await expect(goOn).toHaveAttribute("aria-pressed", "true");

    // A click selects, the card stays open.
    await stop.click();
    await expect(stop).toHaveAttribute("aria-pressed", "true");
    await expect(goOn).toHaveAttribute("aria-pressed", "false");
    expect(await answer()).toBeUndefined();
    await expect(card).toBeVisible();

    // Enter on the option the click focused selects it again, and sends nothing.
    await expect(stop).toBeFocused();
    await page.keyboard.press("Enter");
    expect(await answer()).toBeUndefined();
    await expect(card).toBeVisible();
    await expect(card).not.toHaveClass(/gesture-/);

    // A double click selects too.
    await goOn.dblclick();
    await expect(goOn).toHaveAttribute("aria-pressed", "true");
    expect(await answer()).toBeUndefined();

    // Enter and Space on a focused option pick it.
    await stop.focus();
    await page.keyboard.press("Enter");
    await expect(stop).toHaveAttribute("aria-pressed", "true");
    await goOn.focus();
    await page.keyboard.press(" ");
    await expect(goOn).toHaveAttribute("aria-pressed", "true");
    expect(await answer()).toBeUndefined();

    // A click on the card's text focuses the card: Enter there sends nothing either.
    await card.getByText("Stopping drops the branch.").click();
    await page.keyboard.press("Enter");
    expect(await answer()).toBeUndefined();
    await expect(card).toBeVisible();

    // Rub the lamp sends the option picked last.
    await stop.click();
    await card.getByRole("button", { name: /Rub the lamp/ }).click();
    await expect(card).not.toBeVisible();
    expect((await answer())?.choice).toBe("CHOICE_B");

    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "delete", wishId);
  }
});
