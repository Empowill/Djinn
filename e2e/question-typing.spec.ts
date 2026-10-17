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

test(
  "a note typed on a question keeps its diagram mounted and every key fast",
  { tag: "@budget" },
  async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));
    await page.goto(process.env.DJINN_URL!);
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const title = `Rich question ${randomUUID().slice(0, 8)}`;
    const wishId = JSON.parse(
      djinn("wish", "make", title, "--paused", "--json"),
    ).wish.id as string;
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
    await box.pressSequentially(note);
    await expect(box).toHaveValue(note);

    // The keys again, timed in the page: Playwright's own round trips for each key took ~90 % of the time measured from
    // the test (2 to 4 s for these keys, past 5 s in a loaded push check), the card's renders the rest. One input
    // event a key, and a task between keys so that each key's render lands before the next, as typing does.
    await box.fill("");
    const elapsed = await box.evaluate(async (el, text) => {
      const area = el as HTMLTextAreaElement;
      const value = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )!.set!;
      const nextTask = () =>
        new Promise<void>((resolve) => {
          const channel = new MessageChannel();
          channel.port1.onmessage = () => resolve();
          channel.port2.postMessage(0);
        });
      const start = performance.now();
      for (let at = 1; at <= text.length; at++) {
        value.call(area, text.slice(0, at));
        area.dispatchEvent(
          new InputEvent("input", {
            bubbles: true,
            inputType: "insertText",
            data: text[at - 1],
          }),
        );
        await nextTask();
      }
      return Math.round(performance.now() - start);
    }, note);
    test.info().annotations.push({
      type: "typing",
      description: `${note.length} keys in ${elapsed} ms`,
    });
    await expect(box).toHaveValue(note);

    await expect(frame).toHaveAttribute("data-kept", "yes");
    await expect(card.locator(".ac-code")).toHaveAttribute("data-kept", "yes");
    // In the page, 288 keys take 0.2 to 0.6 s with the fix (the most after the specs before this one), and 4 to 6 s
    // without it, the diagram and the code block mounted again at every key.
    expect(elapsed).toBeLessThan(2_000);
    expect(errors).toEqual([]);
  },
);
