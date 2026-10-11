import { expect, type Locator, type Page } from "@playwright/test";

export const wishHeading = (page: Page): Locator =>
  page.locator(".workspace-thread-head h1");

export const workspaceStage = (page: Page): Locator =>
  page.locator('.wish-workspace[data-wish-workspace="stage"]');

export const workspacePanel = (page: Page, id: string): Locator =>
  workspaceStage(page).locator(`[data-wish-panel="${id}"]`);

export const workspaceSection = (page: Page, id: string): Locator =>
  workspaceStage(page).locator(`[data-wish-section="${id}"]`);

export const workspaceGlobalTrigger = (page: Page): Locator =>
  page.locator(".workspace-action-trigger");

export const workspaceGlobalDialog = (page: Page): Locator =>
  page.getByRole("dialog", { name: /^(Your move|À toi de jouer)$/ });

export async function openWorkspaceSection(
  page: Page,
  id: string,
): Promise<Locator> {
  if (
    await workspaceGlobalDialog(page)
      .isVisible()
      .catch(() => false)
  )
    await closeWorkspaceActions(page);
  const maximized = await workspaceStage(page).getAttribute(
    "data-wish-maximized",
  );
  if (maximized && maximized !== id)
    await workspacePanel(page, maximized)
      .getByTitle(/^(Restore|Restaurer)$/)
      .click();
  const panel = workspacePanel(page, id);
  if (!(await panel.isVisible().catch(() => false)))
    await workspaceSection(page, id).click();
  await expect(panel).toHaveCount(1);
  return panel;
}

export async function openWorkspaceActions(page: Page): Promise<Locator> {
  await workspaceGlobalTrigger(page).click();
  const dialog = workspaceGlobalDialog(page);
  await expect(dialog).toBeVisible();
  return dialog;
}

export async function closeWorkspaceActions(page: Page): Promise<void> {
  await workspaceGlobalDialog(page)
    .getByRole("button", { name: /Close the window|Close window|Fermer/ })
    .click();
  await expect(workspaceGlobalDialog(page)).toHaveCount(0);
}
