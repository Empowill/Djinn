// The window's terminal follows the running lead of the wish shown; independent shells stay in tabs. The leads are
// the fake claude of global-setup.ts: no model is called.
import { expect, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { openTerminalPanel } from "./terminal-selectors";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
// Sessions of no other spec: two terminals never resume one session.
const first = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d";
const second = "2b3c4d5e-6f7a-4b8c-9d0e-1f2a3b4c5d6e";

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

function wishWithLead(title: string, session: string) {
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "follow-")),
  );
  const id = JSON.parse(djinn("wish", "make", title, "--paused", "--json")).wish
    .id as string;
  djinn("wish", "set-lead", id, session, "--directory", folder);
  djinn("wish", "resume", id);
}

async function expectLead(page: Page, session: string) {
  const terminal = page.getByRole("region", { name: "Terminal" });
  await expect(
    terminal.locator(".lead-terminal-command").first(),
  ).toContainText(`claude --resume ${session}`, { timeout: 10_000 });
  await expect(
    terminal
      .locator(".xterm-rows > div")
      .filter({ hasText: `fake-claude --resume ${session}` }),
  ).toHaveCount(1);
}

test.skip(process.platform === "win32", "the fake claude is a shell script");

test("the terminal shows the lead of the wish shown, and follows the wish", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  wishWithLead("Follow me first", first);
  wishWithLead("Follow me second", second);
  djinn("wish", "make", "No lead here", "--paused");
  await page.goto(process.env.DJINN_URL!);
  const terminal = await openTerminalPanel(page);
  const nav = (title: string) =>
    page.locator(".wish-nav").filter({ hasText: title });
  const bar = terminal.locator(".lead-terminal-bar");

  // The running lead of the wish appears without a separate terminal action.
  await nav("Follow me first").click();
  await expectLead(page, first);
  // Another wish: its own lead.
  await nav("Follow me second").click();
  await expectLead(page, second);
  // Back: the first lead still runs, and the terminal attaches to it.
  await nav("Follow me first").click();
  await expectLead(page, first);

  // An independent shell opens in a new tab, and selecting the lead tab takes us back to the first lead.
  const tabs = terminal.getByRole("tab");
  const tabsBeforeShell = await tabs.count();
  await bar.getByRole("button", { name: /^New terminal/ }).click();
  await expect(tabs).toHaveCount(tabsBeforeShell + 1);
  const shell = terminal.getByRole("tab", { name: /^Shell/ }).last();
  await expect(shell).toHaveAttribute("aria-selected", "true");
  await expect(bar.locator(".lead-terminal-command").first()).not.toContainText(
    "claude",
  );
  await terminal.getByRole("tab", { name: /Lead · Follow me first/ }).click();
  await expectLead(page, first);
  await shell.click();
  await expect(shell).toHaveAttribute("aria-selected", "true");

  // A wish without a lead keeps the independently selected shell and offers Start lead in the wish UI.
  await nav("No lead here").click();
  await expect(bar.locator(".lead-terminal-command").first()).not.toContainText(
    "claude",
  );
  await expect(shell).toHaveAttribute("aria-selected", "true");
  await expect(tabs).toHaveCount(tabsBeforeShell + 1);
  await expect(
    page.locator(".lead-status").getByRole("button", { name: "Start lead" }),
  ).toBeVisible();
  expect(errors).toEqual([]);
});
