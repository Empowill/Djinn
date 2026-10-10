// The documentation site that djinn serves at /docs/ (docs/site): its concepts, its Command line tab, filled in by
// djinn from its own commands (TestCommands checks that every command has its entry), and the way to it from the
// settings. Screenshots of both tabs, dark and light, go to test-results/e2e/docs-*.png.
import { type Page, expect, test } from "@playwright/test";
import path from "node:path";

const shots = path.join(__dirname, "../test-results/e2e");

function djinnURL(pathname = "/"): string {
  const url = new URL(process.env.DJINN_URL!);
  url.pathname = pathname;
  return url.toString();
}

function collectErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  page.on("console", (msg) => {
    if (msg.type() === "error") errors.push(msg.text());
  });
  return errors;
}

for (const scheme of ["dark", "light"] as const) {
  test(`the documentation shows its concepts and every command, ${scheme}`, async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    // The first visit trades the token for the cookie that /docs/ needs too.
    await page.goto(djinnURL());
    await page.goto(djinnURL("/docs/"));
    await expect(page).toHaveTitle("Djinn · Documentation");
    await expect(page.locator("html")).toHaveAttribute("data-theme", scheme);

    await expect(
      page.getByRole("heading", { level: 1, name: "Djinn" }),
    ).toBeVisible();
    for (const name of [
      "The lamp and the smoke",
      "Wish for anything, except what no djinn can grant",
      "The life of a wish",
      "What you will meet",
    ])
      await expect(page.getByRole("heading", { level: 2, name })).toBeVisible();
    for (const name of [
      "Azimas",
      "Workers",
      "Questions, and the lamp's rub",
      "Gates",
      "The machine",
      "Tilasms",
    ])
      await expect(
        page
          .locator("#words")
          .getByRole("heading", { level: 3, name: new RegExp(`^${name}`) }),
      ).toBeVisible();
    await page.screenshot({
      path: path.join(shots, `docs-concepts-${scheme}.png`),
    });

    await page.getByRole("tab", { name: "Command line" }).click();
    await expect(page).toHaveURL(/#commands$/);
    await expect(
      page.getByRole("tab", { name: "Command line" }),
    ).toHaveAttribute("aria-selected", "true");
    await expect(page.locator("#concepts")).toBeHidden();
    const commands = page.locator("#commands .cli-cmd");
    expect(await commands.count()).toBeGreaterThan(50);
    for (const name of ["djinn wish make", "djinn gate run", "djinn up"])
      await expect(
        page.getByRole("heading", { level: 4, name, exact: true }),
      ).toBeAttached();
    await page.screenshot({
      path: path.join(shots, `docs-commands-${scheme}.png`),
    });

    // The table of contents leads to a command.
    await page
      .locator(".cli-toc")
      .getByRole("link", { name: "answer", exact: true })
      .click();
    await expect(page).toHaveURL(/#cmd-question-answer$/);
    await expect(page.locator("#cmd-question-answer")).toBeInViewport();

    // The search keeps what matches: an alias finds its group.
    await page.getByRole("searchbox").fill("talisman");
    await expect(page.locator("#group-tilasm")).toBeVisible();
    await expect(page.locator("#group-wish")).toBeHidden();
    await expect(page.locator("#cmd-tilasm-put")).toBeVisible();
    await page.getByRole("searchbox").fill("no such command");
    await expect(page.getByText("No command matches.")).toBeVisible();
    await page.getByRole("searchbox").fill("");
    await expect(page.locator("#group-wish")).toBeVisible();

    await page.getByRole("tab", { name: "Concepts" }).click();
    await expect(page.locator("#concepts")).toBeVisible();
    expect(errors).toEqual([]);
  });
}

test("the settings open the documentation over the window, in its theme", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto(djinnURL());
  await page.getByTitle("Connections & preferences").click();
  await page.getByRole("dialog").getByRole("button", { name: "Open" }).click();
  const docs = page.getByRole("dialog", { name: "Documentation" });
  await expect(docs).toBeVisible();
  const frame = page.frameLocator('iframe[title="Documentation"]');
  await expect(
    frame.getByRole("heading", { level: 1, name: "Djinn" }),
  ).toBeVisible();
  await expect(frame.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(frame.locator("html")).toHaveClass(/embedded/);
  await docs.getByRole("button", { name: "Close the window" }).click();
  await expect(docs).toBeHidden();
});
