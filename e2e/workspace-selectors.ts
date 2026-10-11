import { expect, type Locator, type Page } from "@playwright/test";

export const wishHeading = (page: Page): Locator =>
  page.locator(".workspace-thread-head h1");

export const workspaceStage = (page: Page): Locator =>
  page.locator('.wish-workspace[data-wish-workspace="stage"]');

export const workspacePanel = (page: Page, id: string): Locator =>
  workspaceStage(page).locator(`[data-wish-panel="${id}"]`);

export const workspaceSection = (page: Page, id: string): Locator =>
  workspaceStage(page).locator(`[data-wish-section="${id}"]`);

export async function openWorkspaceSection(
  page: Page,
  id: string,
): Promise<Locator> {
  const maximized = await workspaceStage(page).getAttribute(
    "data-wish-maximized",
  );
  if (maximized && maximized !== id)
    await workspacePanel(page, maximized)
      .getByTitle(/^(Restore|Restaurer)$/)
      .click();
  const panel = workspacePanel(page, id);
  if ((await panel.count()) === 0)
    await workspaceSection(page, id).locator(".workspace-section-card").click();
  await expect(panel).toHaveCount(1);
  return panel;
}
