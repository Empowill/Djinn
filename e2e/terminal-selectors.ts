import { expect, type Locator, type Page } from "@playwright/test";

// Reloads deliberately fold the terminal while retaining its tabs. Tests that inspect the emulator must open the
// existing panel explicitly, just as a developer would.
export async function openTerminalPanel(page: Page): Promise<Locator> {
  const terminal = page.locator(".lead-terminal");
  const className = await terminal.getAttribute("class");
  if (className?.split(/\s+/).includes("collapsed"))
    await terminal.locator(".lead-terminal-bar > button").last().click();
  await expect(terminal).not.toHaveClass(/collapsed/);
  return terminal;
}

export async function openShellTerminal(page: Page): Promise<Locator> {
  const terminal = await openTerminalPanel(page);
  const existing = terminal
    .getByRole("tab")
    .filter({ hasText: /^(Terminal|Shell(?: \d+)?)$/ })
    .last();
  if (await existing.count()) await existing.click();
  else
    await terminal
      .locator(".lead-terminal-bar")
      .getByRole("button", { name: /^New terminal/ })
      .click();
  return terminal;
}
