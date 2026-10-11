// Keyboard shortcuts on a question card:
// - Arrows or 1–4 pick an option when focus is on the card and not in the note.
// - Shift+Enter inserts a new line in the note field.
// - Enter in the note (or on the card) rubs the lamp and answers.
// - Ctrl+Enter (Cmd+Enter on macOS) rubs the lamp.
// - Ctrl+Shift+Enter triggers Enlighten me from anywhere on the card.
// - Enter without an option selected shows an alert and selects nothing.
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

test("question card keyboard shortcuts: arrows, 1-4, Shift+Enter, Enter, Ctrl+Shift+Enter, unpicked warning", async ({
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

  const title = `Shortcuts ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;

  try {
    const item = page.locator(".wish-nav").filter({ hasText: title });
    await expect(item).toBeVisible();
    await item.click();

    // 1. Question with 3 options and recommendation for A: test arrows, numbers, Shift+Enter, Enter.
    djinn(
      "question",
      "ask",
      "Pick an engine",
      wishId,
      "--options",
      "Steam",
      "--options",
      "Diesel",
      "--options",
      "Electric",
      "--recommendation",
      "**A**: Steam is classic.",
    );

    const card1 = page
      .locator(".question-card")
      .filter({ hasText: "Pick an engine" });
    await expect(card1).toBeVisible();

    const steam = card1.getByRole("button", { name: /Steam/ });
    const diesel = card1.getByRole("button", { name: /Diesel/ });
    const electric = card1.getByRole("button", { name: /Electric/ });

    await expect(steam).toHaveAttribute("aria-pressed", "true");
    await expect(diesel).toHaveAttribute("aria-pressed", "false");
    await expect(electric).toHaveAttribute("aria-pressed", "false");

    // Focus card and navigate with ArrowDown / ArrowUp.
    await card1.focus();
    await page.keyboard.press("ArrowDown");
    await expect(diesel).toHaveAttribute("aria-pressed", "true");
    await expect(steam).toHaveAttribute("aria-pressed", "false");

    await page.keyboard.press("ArrowDown");
    await expect(electric).toHaveAttribute("aria-pressed", "true");

    await page.keyboard.press("ArrowUp");
    await expect(diesel).toHaveAttribute("aria-pressed", "true");

    // Number keys pick directly.
    await page.keyboard.press("1");
    await expect(steam).toHaveAttribute("aria-pressed", "true");

    await page.keyboard.press("3");
    await expect(electric).toHaveAttribute("aria-pressed", "true");

    // In textarea: Shift+Enter inserts newline, does not answer.
    const note1 = card1.getByLabel("Note with the answer");
    await note1.focus();
    await page.keyboard.type("Line one");
    await page.keyboard.press("Shift+Enter");
    await page.keyboard.type("Line two");
    await expect(note1).toHaveValue("Line one\nLine two");
    await expect(card1).toBeVisible();

    // Enter inside textarea answers (Rub the lamp).
    await page.keyboard.press("Enter");
    await expect(page.getByRole("tab", { name: /^Decisions/ })).toHaveText(
      "Decisions1",
    );

    const [q1Answered] = JSON.parse(
      djinn("question", "list", "--wish-id", wishId, "--json"),
    ).questions;
    expect(q1Answered.answer.choice).toBe("CHOICE_C");
    expect(q1Answered.answer.note).toBe("Line one\nLine two");

    // 2. Question testing Ctrl+Shift+Enter for Enlighten me.
    djinn(
      "question",
      "ask",
      "Where to build?",
      wishId,
      "--options",
      "Coast",
      "--options",
      "Inland",
      "--recommendation",
      "**A**: Coast is near water.",
    );

    const card2 = page
      .locator(".question-card")
      .filter({ hasText: "Where to build?" });
    await expect(card2).toBeVisible();

    const note2 = card2.getByLabel("Note with the answer");
    await note2.focus();
    await page.keyboard.type("Check seismic activity first");
    await page.keyboard.press("Control+Shift+Enter");

    const digging = page
      .locator(".investigating-section .question-card")
      .filter({ hasText: "Where to build?" });
    await expect(digging).toBeVisible();
    await expect(digging).toHaveClass(/folded/);

    const brief = djinn("wish", "brief", wishId);
    expect(brief).toContain("Check seismic activity first");

    // 3. Question without recommendation: pressing Enter shows unpicked alert; picking clears it and Ctrl+Enter submits.
    djinn(
      "question",
      "ask",
      "Choose a color",
      wishId,
      "--options",
      "Red",
      "--options",
      "Blue",
    );

    const card3 = page
      .locator(".question-card")
      .filter({ hasText: "Choose a color" });
    await expect(card3).toBeVisible();

    await card3.focus();
    await page.keyboard.press("Enter");
    await expect(card3.locator(".question-hint.unpicked")).toBeVisible();

    // Press 2 to pick Blue: clears unpicked alert.
    await page.keyboard.press("2");
    await expect(card3.locator(".question-hint.unpicked")).toHaveCount(0);
    await expect(card3.getByRole("button", { name: /Blue/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    // Ctrl+Enter answers.
    await page.keyboard.press("Control+Enter");
    await expect(card3).not.toBeVisible();

    const list = JSON.parse(
      djinn("question", "list", "--wish-id", wishId, "--json"),
    ).questions as Array<{
      text?: string;
      answer?: { choice?: string; note?: string };
    }>;
    const q3Answered = list.find((q) => q.text === "Choose a color");
    expect(q3Answered?.answer?.choice).toBe("CHOICE_B");

    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "delete", wishId);
  }
});
