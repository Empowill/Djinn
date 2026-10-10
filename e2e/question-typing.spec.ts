// Typing an answer note stays fast, however rich the question. Each key re-renders the question card: its Markdown
// (the recommendation, the context and its Mermaid diagram) must not be parsed again nor mounted again, or the box
// gets slower with every key until it cannot be typed in.
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

test("a note typed on a question keeps its diagram mounted and every key fast", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();

  const title = `Rich question ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;
  djinn(
    "question",
    "ask",
    "Which flow first?",
    wishId,
    "--options",
    "The store first",
    "--options",
    "The window first",
    "--recommendation",
    "**A**: the store holds the rest.",
    "--context",
    [
      "The flows:",
      "",
      "```mermaid",
      "flowchart LR",
      "  cli[CLI] --> server[Server] --> store[(Store)]",
      "  window[Window] --> server",
      "```",
      "",
      "```go",
      'fmt.Println("a code block")',
      "```",
    ].join("\n"),
  );
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  const card = page
    .locator(".question-card")
    .filter({ hasText: "Which flow first?" });
  await expect(card).toBeVisible();
  const frame = card.locator("iframe");
  await expect(frame).toHaveCount(1);
  await expect(card.getByText("Preparing the diagram")).toHaveCount(0, {
    timeout: 30_000,
  });

  // A mark on the frame and on the code block: a remount would drop them.
  await frame.evaluate((el) => el.setAttribute("data-kept", "yes"));
  await card
    .locator(".ac-code")
    .evaluate((el) => el.setAttribute("data-kept", "yes"));

  const note =
    "Store first, then the window; keep the diagram as it is, it reads well. ".repeat(
      4,
    );
  const box = card.getByLabel("Note with the answer");
  const start = Date.now();
  await box.pressSequentially(note);
  const elapsed = Date.now() - start;
  test.info().annotations.push({
    type: "typing",
    description: `${note.length} keys in ${elapsed} ms`,
  });
  await expect(box).toHaveValue(note);

  await expect(frame).toHaveAttribute("data-kept", "yes");
  await expect(card.locator(".ac-code")).toHaveAttribute("data-kept", "yes");
  // Playwright sends one key at a time, a few milliseconds each on its own: 290 keys took 1.8 s with this fix and
  // 10 s without it, the diagram and the code block mounted again at every key.
  expect(elapsed).toBeLessThan(5_000);
  expect(errors).toEqual([]);
});
