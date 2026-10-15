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

// Activity tabs live in the compact panel header, where icon-only tabs expose
// their current count as part of the accessible name.
const workspaceActivityTabPattern = (key: string): RegExp =>
  new RegExp(
    `^(?:${localizedValues(key).map(escapeRegExp).join("|")})(?: · \\d+)?$`,
  );

export const wishHeading = (page: Page): Locator =>
  page.locator(".workspace-thread-head h1");

export const workspaceStage = (page: Page): Locator =>
  page.locator('.wish-workspace[data-wish-workspace="stage"]');

export const workspacePanel = (page: Page, id: string): Locator =>
  workspaceStage(page).locator(`[data-wish-panel="${workspacePanelId(id)}"]`);

export const workspaceSection = (page: Page, id: string): Locator =>
  workspaceStage(page).locator(`[data-wish-section="${workspacePanelId(id)}"]`);

const workspacePanelId = (id: string): string =>
  id === "decisions" || id === "journal" || id === "instructions"
    ? "activity"
    : id;

export const workspaceGlobalTrigger = (page: Page): Locator =>
  page.locator(".workspace-action-attention-pending");

export const workspaceGlobalDialog = (page: Page): Locator =>
  page.locator('[data-wish-action-dock="true"]');

export async function openWorkspaceSection(
  page: Page,
  id: string,
): Promise<Locator> {
  const actionDock = workspaceGlobalDialog(page);
  if (
    (await actionDock.count()) > 0 &&
    !((await actionDock.getAttribute("class")) ?? "").includes("is-collapsed")
  )
    await closeWorkspaceActions(page);
  const panelId = workspacePanelId(id);
  const panel = workspacePanel(page, panelId);
  if (!(await panel.isVisible().catch(() => false)))
    await workspaceSection(page, panelId)
      .locator(".workspace-section-toggle")
      .click();
  if (id === "decisions" || id === "journal" || id === "instructions") {
    const tabKey =
      id === "decisions"
        ? "tabs.decisions"
        : id === "journal"
          ? "page.journal"
          : "instruction.title";
    await panel
      .getByRole("tab", { name: workspaceActivityTabPattern(tabKey) })
      .click();
  }
  await expect(panel).toHaveCount(1);
  return panel;
}

export async function openWorkspaceActions(page: Page): Promise<Locator> {
  const dock = workspaceGlobalDialog(page);
  if (!((await dock.getAttribute("class")) ?? "").includes("is-collapsed"))
    return dock;
  await workspaceGlobalTrigger(page).click();
  await expect(dock).not.toHaveClass(/is-collapsed/);
  return dock;
}

export async function closeWorkspaceActions(page: Page): Promise<void> {
  const dock = workspaceGlobalDialog(page);
  if (
    await dock
      .getByRole("button", { name: workspaceLocalePattern("attention.close") })
      .isVisible()
      .catch(() => false)
  ) {
    await dock
      .getByRole("button", { name: workspaceLocalePattern("attention.close") })
      .click();
  }
  await expect(dock).toHaveClass(/is-collapsed/);
}
