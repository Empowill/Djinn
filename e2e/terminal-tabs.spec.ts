// The terminal's tabs, against the real djinn up --browser (see global-setup.ts): a terminal opened at a project's
// root from the side panel, another one with Ctrl+Shift+T, and a tab closed with its ×, its program hung up.
import { expect, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";

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

function line(page: Page, text: string) {
  return page
    .locator(".lead-terminal .xterm-rows > div")
    .filter({ hasText: new RegExp(`^\\s*${text}\\s*$`) });
}

// call calls a method of djinn's services, as the window does.
async function call(method: string, body: object) {
  const url = new URL(process.env.DJINN_URL!);
  const res = await fetch(new URL(method, url), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${url.searchParams.get("token")}`,
    },
    body: JSON.stringify(body),
  });
  expect(res.ok).toBe(true);
  return res.json();
}
const show = (terminal: string) => call("/ui.v1.UiService/Show", { terminal });

test.skip(
  process.platform === "win32",
  "the terminal runs PowerShell on Windows; this test drives a POSIX shell",
);
// The next specs find the window's own terminal again.
test.afterAll(() => show("main"));

test("a terminal opens at a project's root, another with Ctrl+Shift+T, and a tab closes", async ({
  page,
}) => {
  const name = `tabs${randomUUID().slice(0, 6)}`;
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "tabs-")),
  );
  djinn("project", "add", folder, "--name", name);
  await page.goto(process.env.DJINN_URL!);
  const terminal = page.getByRole("region", { name: "Terminal" });
  const tabs = terminal.getByRole("tab");

  // The project's button opens a tab at its root, shown at once.
  const row = page.locator(".project-nav").filter({ hasText: name });
  await row.hover();
  await row
    .getByRole("button", { name: `Open a terminal at the root of ${name}` })
    .click();
  const projectTab = tabs.filter({ hasText: name });
  await expect(projectTab).toHaveAttribute("aria-selected", "true");
  await page.locator(".lead-terminal .xterm").click();
  await page.keyboard.type("pwd");
  await page.keyboard.press("Enter");
  await expect(line(page, folder)).toHaveCount(1);

  // Ctrl+Shift+T opens another one.
  const before = await tabs.count();
  await page.keyboard.press("Control+Shift+T");
  await expect(tabs).toHaveCount(before + 1);
  await expect(tabs.last()).toHaveAttribute("aria-selected", "true");

  // The project's tab closes: its program hangs up, the tab goes.
  await terminal
    .locator(".lead-terminal-tab")
    .filter({ hasText: name })
    .getByRole("button", { name: "Close this terminal" })
    .click();
  await expect(projectTab).toHaveCount(0);
  await expect
    .poll(async () =>
      JSON.stringify(await call("/terminal.v1.TerminalService/List", {})),
    )
    .not.toContain("project-");
});
