// A wish with a worker's report and a long question that names it, the shape of a real one (e2e/report-question.ts):
// - the card reads top-down: what is at stake, its report, the recommendation, then the options, the gestures, the
//   note; the context's first view keeps the options in sight on a laptop, and Read more unfolds the rest in place;
// - a command in an option or in the context is selectable and copies exactly, and copying never answers;
// - the card opens the report it names, on demand; the report itself is a note for agents: no section of its own on
//   the wish, it waits in the For agents tab with the other blocks.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import path from "node:path";
import { portable } from "./portable";
import {
  CONTEXT,
  CONTEXT_FIRST,
  CONTEXT_LATER,
  QUESTION,
  RECOMMENDATION,
  REPORT,
  REPORT_TITLE,
  allowCommand,
  options,
} from "./report-question";

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

test("a long question reads top-down, its commands copy without answering, and the report it names stands out", async ({
  page,
  context,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  await context.grantPermissions(["clipboard-read", "clipboard-write"], {
    origin: new URL(process.env.DJINN_URL!).origin,
  });
  // A laptop's screen.
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();

  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const title = `Dew ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;
  const clipboard = () => page.evaluate(() => navigator.clipboard.readText());

  try {
    djinn(
      "block",
      "put",
      wishId,
      "--kind",
      "report",
      "--title",
      REPORT_TITLE,
      "--content",
      REPORT,
    );
    const [a, b] = options(wishId);
    djinn(
      "question",
      "ask",
      QUESTION,
      wishId,
      "--options",
      a,
      "--options",
      b,
      "--context",
      CONTEXT,
      "--recommendation",
      RECOMMENDATION,
      "--before",
      "before the implementation starts",
      "--move",
    );

    const item = page.locator(".wish-nav").filter({ hasText: title });
    await expect(item).toBeVisible();
    await item.click();

    const card = page.locator(".question-card").filter({ hasText: QUESTION });
    await expect(card).toBeVisible();

    // Top-down: the explanation, its report and the recommendation before the options, the gestures, then the note.
    const top = async (locator: ReturnType<typeof card.locator>) =>
      (await locator.boundingBox())!.y;
    const optionA = card.getByRole("button", { name: /Done: I ran/ });
    const optionB = card.getByRole("button", { name: /No: I run the probe/ });
    const ys = [
      await top(card.locator("h3", { hasText: QUESTION })),
      await top(card.locator(".question-context")),
      await top(card.locator(".question-report-link")),
      await top(card.locator(".recommendation")),
      await top(optionA),
      await top(card.locator(".lamp-rub")),
      await top(card.getByLabel("Note with the answer")),
    ];
    expect(ys).toEqual([...ys].sort((x, y) => x - y));

    // The context's first view: its first section; the options stay within a laptop's screen below the card's top.
    await expect(card.getByText(CONTEXT_FIRST)).toBeVisible();
    await expect(card.getByText(CONTEXT_LATER)).toHaveCount(0);
    const cardTop = await top(card);
    const optionBox = (await optionA.boundingBox())!;
    expect(optionBox.y + optionBox.height - cardTop).toBeLessThan(768 - 60);
    // Read more unfolds the rest in place, and folds it again.
    await card.getByRole("button", { name: "Read more" }).click();
    await expect(card.getByText(CONTEXT_LATER)).toBeVisible();

    // A command of the context copies as it is.
    const inline = card
      .locator(".question-context .command")
      .filter({ hasText: "PAYROLL_CHECK=1 PAYROLL_CLIENT=acme" });
    await inline.getByRole("button", { name: "Copy the command" }).click();
    expect(await clipboard()).toBe("PAYROLL_CHECK=1 PAYROLL_CLIENT=acme");
    await card.getByRole("button", { name: "Read less" }).click();
    await expect(card.getByText(CONTEXT_LATER)).toHaveCount(0);

    // Pick B, then copy the command of A: it copies exactly, says so, and neither picks A nor answers.
    await expect(optionA).toHaveAttribute("aria-pressed", "true");
    await optionB.click();
    await expect(optionB).toHaveAttribute("aria-pressed", "true");
    await optionA.getByRole("button", { name: "Copy the command" }).click();
    expect(await clipboard()).toBe(allowCommand(wishId));
    await expect(optionA.getByRole("status")).toHaveText("Copied");
    await expect(optionB).toHaveAttribute("aria-pressed", "true");
    // Enter on the copy button copies again, and answers nothing.
    await optionA.locator(".copy-command").focus();
    await page.keyboard.press("Enter");
    await expect(card).toBeVisible();

    // The command's text is selectable: a drag over it selects it, and picks nothing.
    const code = optionA.locator("code");
    // The mouse does not scroll: the command comes into sight first.
    await code.evaluate((el) => el.scrollIntoView({ block: "center" }));
    // From its first letter to its last, on whatever lines it wraps.
    const [from, to] = await code.evaluate((el) => {
      const rects = [...el.getClientRects()];
      const first = rects[0];
      const last = rects[rects.length - 1];
      return [
        { x: first.left + 2, y: first.top + first.height / 2 },
        { x: last.right - 2, y: last.top + last.height / 2 },
      ];
    });
    await page.mouse.move(from.x, from.y);
    await page.mouse.down();
    await page.mouse.move(to.x, to.y, { steps: 5 });
    await page.mouse.up();
    expect(
      await page.evaluate(() => window.getSelection()?.toString() ?? ""),
    ).toContain("djinn wish allow");
    await expect(optionB).toHaveAttribute("aria-pressed", "true");
    const [asked] = JSON.parse(
      djinn("question", "list", "--wish-id", wishId, "--json"),
    ).questions;
    expect(asked.answer).toBeUndefined();

    // The card opens the report it names, in place.
    const link = card.getByRole("button", { name: `Report: ${REPORT_TITLE}` });
    await link.click();
    await expect(link).toHaveAttribute("aria-expanded", "true");
    await expect(
      card.locator(".question-report-body").getByText("What is left to know"),
    ).toBeVisible();

    // No section of reports on the wish: the report waits among the blocks for agents.
    await expect(page.locator("#reports")).toHaveCount(0);
    const forAgents = page.getByRole("tab", { name: /^For agents/ });
    await expect(forAgents).toHaveText("For agents1");
    await forAgents.click();
    await expect(page.locator(".agent-block h3")).toHaveText(REPORT_TITLE);

    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "delete", wishId);
  }
});
