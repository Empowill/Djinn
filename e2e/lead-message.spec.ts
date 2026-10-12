// Writing to a wish's lead from the window: the centered workspace pill unfolds into a box whose text Djinn types in
// the lead's terminal, as the developer would. Without a lead running, the pill offers an explicit resume control. The lead is
// the fake claude of global-setup.ts, which repeats what it reads: no model is called.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
// Not the session of wish-resume.spec.ts: two terminals never resume one session.
const session = "6f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f";

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

// show asks djinn for a terminal, as the command line asks for the lead's.
async function show(terminal: string) {
  const url = new URL(process.env.DJINN_URL!);
  const res = await fetch(new URL("/ui.v1.UiService/Show", url), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${url.searchParams.get("token")}`,
    },
    body: JSON.stringify({ terminal }),
  });
  expect(res.ok).toBe(true);
}

test.skip(process.platform === "win32", "the fake claude is a shell script");

// The next specs find the window's own terminal again.
test.afterAll(() => show("main"));

test("a word written in the window reaches the lead's terminal", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "tell-")),
  );
  const title = "Talk to the lead";
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  djinn("wish", "set-lead", wishId, session, "--directory", folder);
  try {
    await page.goto(process.env.DJINN_URL!);
    // Folded, the terminal waits to be opened before it resumes the lead of the wish shown.
    await page
      .locator(".lead-terminal-bar button[title='Collapse the terminal']")
      .last()
      .click();
    await page.locator(".wish-nav").filter({ hasText: title }).click();

    // Saving works before the fake lead resumes; Resume remains an explicit action. The shortcut opens the same pill.
    const toggle = page.getByRole("button", { name: "Add an indication" });
    await toggle.focus();
    await page.keyboard.press(
      process.platform === "darwin" ? "Meta+I" : "Control+I",
    );
    const panel = page.getByRole("dialog", { name: "Indication for the lead" });
    await expect(
      panel.getByRole("button", { name: "Resume the lead" }),
    ).toBeVisible();
    const box = panel.getByLabel("An indication for this wish");
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
    await expect(box).toBeEnabled();
    await panel.getByRole("button", { name: "Resume the lead" }).click();
    await expect(box).toBeEnabled({ timeout: 10_000 });
    const rows = page.locator(".lead-terminal .xterm-rows > div");
    await expect(
      rows.filter({ hasText: `fake-claude --resume ${session}` }),
    ).toHaveCount(1);

    // Shift+Enter preserves the full text; the fake lead receives a reference to its durable record.
    // Escape keeps an unfinished draft and restores focus to the whole-pill trigger.
    await box.fill("Draft stays");
    await box.press("Escape");
    await expect(panel).toHaveCount(0);
    await expect(toggle).toBeFocused();
    await toggle.press("Enter");
    await expect(box).toHaveValue("Draft stays");

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
    await expect(panel.getByText("Saved · pending")).toBeVisible();
    await expect(
      rows.filter({ hasText: "Developer instruction I01" }).first(),
    ).toBeVisible({
      timeout: 10_000,
    });

    const saved = JSON.parse(
      djinn("instruction", "list", wishId, "--json"),
    ).instructions;
    expect(saved).toHaveLength(1);
    expect(saved[0].text).toBe("Hello\nlead");

    // Escape folds the box.
    await box.press("Escape");
    await expect(panel).toHaveCount(0);
    await expect(toggle).toBeFocused();

    // Generic reports remain transient and still reach the lead.
    djinn("wish", "tell", wishId, "From the command line");
    await expect(
      rows.filter({ hasText: "From the command line" }).first(),
    ).toBeVisible({ timeout: 10_000 });
    expect(
      JSON.parse(djinn("instruction", "list", wishId, "--json")).instructions,
    ).toHaveLength(1);
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});
