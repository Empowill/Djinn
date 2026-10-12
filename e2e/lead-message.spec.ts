// Writing to the lead from a wish: the centred workspace pill unfolds into a box whose text Djinn types in the one
// lead's terminal (LeadService.Tell), about the wish, as the developer would. Refused while the lead does not run, the
// pill keeps the text and offers to start the lead. The lead is the fake claude of global-setup.ts, which repeats what
// it reads: no model is called.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import path from "node:path";

import { dropLead, leadFolder, leadRows as rows, setLead } from "./lead";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
// Not the session of another spec: one session runs in one terminal.
const session = "6f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f";

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

test.skip(process.platform === "win32", "the fake claude is a shell script");

test.afterEach(dropLead);

test("a word written in the pill reaches the lead's terminal", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);
  const folder = leadFolder("tell-");
  const title = "Talk to the lead";
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  setLead(session, folder);
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();

    // The shortcut opens the pill into its box, as wide as the pill.
    const toggle = page.getByRole("button", { name: "Write to the lead" });
    await expect(toggle).toBeVisible();
    await page.screenshot({ path: "test-results/e2e/lead-message-pill.png" });
    await toggle.focus();
    await page.keyboard.press(
      process.platform === "darwin" ? "Meta+I" : "Control+I",
    );
    const panel = page.getByRole("dialog", { name: "To the lead" });
    const box = panel.getByLabel(
      "A word for the lead about this wish, as if typed in its terminal",
    );
    await expect(box).toBeFocused();
    const action = page.locator(".workspace-action-lead");
    await expect
      .poll(async () => {
        const actionBox = await action.boundingBox();
        const shellBox = await panel.boundingBox();
        return actionBox && shellBox
          ? Math.abs(shellBox.width - actionBox.width)
          : Number.POSITIVE_INFINITY;
      })
      .toBeLessThan(2);
    await expect(panel).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
    await expect(box).toHaveCSS("resize", "none");
    await expect(box).toHaveCSS("font-size", "12px");
    await expect
      .poll(() =>
        box.evaluate((element) => getComputedStyle(element).fontFamily),
      )
      .toMatch(/DM Sans|sans-serif/);

    // The lead does not run: the text is refused and stays, and the pill offers to start the lead.
    await box.fill("Too early");
    await box.press("Enter");
    await expect(panel.getByRole("alert")).toContainText("djinn lead resume");
    await expect(box).toHaveValue("Too early");
    const start = panel.getByRole("button", { name: "Start the lead" });
    await start.click();
    await expect(
      rows(page, `fake-claude --resume ${session} in ${folder}`),
    ).toHaveCount(1, { timeout: 10_000 });
    await expect(panel.getByRole("alert")).toHaveCount(0);

    // Escape keeps an unfinished draft and gives the focus back to the pill.
    await box.fill("Draft stays");
    await box.press("Escape");
    await expect(panel).toHaveCount(0);
    await expect(toggle).toBeFocused();
    await toggle.press("Enter");
    await expect(box).toHaveValue("Draft stays");

    // Shift+Enter breaks the line and the box grows; Enter sends the whole text, and the box shrinks back.
    await box.fill("Hello");
    const oneLineHeight = await box.evaluate(
      (element) => element.getBoundingClientRect().height,
    );
    await box.press("Shift+Enter");
    await box.pressSequentially("lead");
    await expect(box).toHaveValue("Hello\nlead");
    const multilineHeight = await box.evaluate(
      (element) => element.getBoundingClientRect().height,
    );
    expect(multilineHeight).toBeGreaterThan(oneLineHeight);
    await box.press("Enter");
    await expect(box).toHaveValue("");
    const clearedHeight = await box.evaluate(
      (element) => element.getBoundingClientRect().height,
    );
    expect(clearedHeight).toBeLessThan(multilineHeight);
    await expect(panel.getByRole("status")).toContainText("Sent");
    await page.screenshot({ path: "test-results/e2e/lead-message-box.png" });
    // The fake lead takes no paste: the lines come joined by a space, after the wish they are about.
    await expect(rows(page, "Hello lead").first()).toBeVisible({
      timeout: 10_000,
    });
    await expect(rows(page, `About wish ${title}`).first()).toBeVisible();

    // Escape folds the box.
    await box.press("Escape");
    await expect(panel).toHaveCount(0);
    await expect(toggle).toBeFocused();
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "delete", wishId);
  }
});
