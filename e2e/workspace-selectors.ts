import { expect, type Locator, type Page } from "@playwright/test";
import * as enModule from "../locales/en.json";
import * as frModule from "../locales/fr.json";

type Catalog = Record<string, string>;
const catalog = (module: object): Catalog =>
  ((module as { default?: Catalog }).default ?? module) as Catalog;
const catalogs = [catalog(enModule), catalog(frModule)];

const escapeRegExp = (value: string): string =>
  value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

const localizedValues = (key: string): string[] =>
  catalogs
    .map((value) => value[key])
    .filter(
      (value, index, values): value is string =>
        !!value && values.indexOf(value) === index,
    );

export const workspaceLocalePattern = (key: string): RegExp =>
  new RegExp(`^(?:${localizedValues(key).map(escapeRegExp).join("|")})$`);

export const workspaceLocalePrefixPattern = (key: string): RegExp =>
  new RegExp(`^(?:${localizedValues(key).map(escapeRegExp).join("|")}) `);

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
  page.getByRole("dialog", {
    name: workspaceLocalePattern("panels.your_move"),
  });

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
      .getByTitle(workspaceLocalePattern("workspace.restore"))
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
    .getByRole("button", {
      name: workspaceLocalePattern("app.close_window"),
    })
    .click();
  await expect(workspaceGlobalDialog(page)).toHaveCount(0);
}
