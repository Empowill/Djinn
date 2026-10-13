// The pages by the keyboard (W545, Decision Q254), against the real djinn up --browser (see global-setup.ts): Ctrl+2
// (Cmd+2 on macOS) shows the Hourglass, Ctrl+1 the flying carpet, by the key's place (Digit2, Numpad2); with Shift,
// nothing; from the terminal, nothing; from the text of a new wish, the page. The side panel's entries name the
// shortcut as an AZERTY keyboard writes it, from the layout map the browser gives.
// Run alone with: go tool task e2e -- e2e/page-keys.spec.ts
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
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

test("Ctrl+1 shows the flying carpet and Ctrl+2 the Hourglass, by the key's place", async ({
  page,
}) => {
  // A project: the terminal opens in its folder (with none, it asks for one).
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "keys-")),
  );
  djinn("project", "add", folder, "--name", `keys${randomUUID().slice(0, 6)}`);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  // An AZERTY keyboard: its digit row types & and e acute where QWERTY types 1 and 2.
  await page.addInitScript(() => {
    const layout = new Map([
      ["Digit1", "&"],
      ["Digit2", "\u00e9"],
      ["KeyQ", "a"],
    ]);
    Object.defineProperty(navigator, "keyboard", {
      configurable: true,
      value: { getLayoutMap: () => Promise.resolve(layout) },
    });
  });
  await page.goto(process.env.DJINN_URL!);
  const glass = page.locator(".sidebar .hourglass-nav");
  await expect(glass).toHaveAttribute(
    "title",
    /^Hourglass \((Ctrl|⌘) \u00e9\)$/,
  );
  await expect(glass).toHaveAttribute(
    "aria-keyshortcuts",
    /^(Control|Meta)\+2$/,
  );
  // With no wish, the window opens at home; with some, on the flying carpet or a wish.
  const heading = page.locator(".hero h1");

  await page.keyboard.press("ControlOrMeta+Digit2");
  await expect(page.locator(".hourglass .hero h1")).toHaveText("Hourglass");
  await expect(glass).toHaveClass(/selected/);
  await page.keyboard.press("ControlOrMeta+Digit1");
  await expect(heading).toHaveText("Flying carpet");
  await expect(page.locator(".hourglass")).toHaveCount(0);
  // The numpad's 2 too; with Shift, the key types another sign, and nothing changes.
  await page.keyboard.press("ControlOrMeta+Numpad2");
  await expect(heading).toHaveText("Hourglass");
  await page.keyboard.press("ControlOrMeta+Shift+Digit1");
  await page.waitForTimeout(300);
  await expect(heading).toHaveText("Hourglass");

  // The terminal keeps its Ctrl and digit keys.
  await page
    .getByRole("region", { name: "Terminal" })
    .getByRole("button", { name: "New terminal (Ctrl+Shift+T)" })
    .last()
    .click();
  const terminal = page.locator(".lead-terminal .xterm");
  await expect(terminal).toBeVisible();
  await terminal.click();
  await page.keyboard.press("ControlOrMeta+Digit1");
  await page.waitForTimeout(300);
  await expect(heading).toHaveText("Hourglass");

  // A text field does not: Ctrl and a digit type nothing there.
  await page.locator(".sidebar .new-mission").click();
  const editor = page.getByRole("textbox", {
    name: "What would you like to make happen?",
  });
  await expect(editor).toBeFocused();
  await page.keyboard.press("ControlOrMeta+Digit1");
  await expect(heading).toHaveText("Flying carpet");
  await expect(editor).toHaveCount(0);
  expect(errors).toEqual([]);
});
