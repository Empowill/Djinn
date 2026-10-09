// The documentation site that djinn serves at /docs/ (docs/site): its concepts, its API tab with every operation of
// docs/openapi.json (TestOpenAPI ties that file to the public methods of the protos), and the way to it from the
// settings. Screenshots of both tabs, dark and light, go to test-results/e2e/docs-*.png.
import { type Page, expect, test } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

const shots = path.join(__dirname, "../test-results/e2e");

function djinnURL(pathname = "/"): string {
  const url = new URL(process.env.DJINN_URL!);
  url.pathname = pathname;
  return url.toString();
}

// The operations of the API document, as RapiDoc names them: post-/plan.v1.QuestionService/Answer.
function operations(): string[] {
  const doc = JSON.parse(
    fs.readFileSync(path.join(__dirname, "../docs/openapi.json"), "utf8"),
  ) as { paths: Record<string, Record<string, unknown>> };
  return Object.entries(doc.paths)
    .flatMap(([p, item]) => Object.keys(item).map((method) => `${method}-${p}`))
    .sort();
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
  test(`the documentation shows its concepts and every operation of the API, ${scheme}`, async ({
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

    await page.getByRole("tab", { name: "API" }).click();
    await expect(page).toHaveURL(/#api$/);
    await expect(page.getByRole("tab", { name: "API" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(page.locator("#concepts")).toBeHidden();
    const want = operations();
    expect(want.length).toBeGreaterThan(50);
    const nav = page.locator("rapi-doc .nav-bar-path");
    await expect(nav).toHaveCount(want.length);
    const shown = (
      await nav.evaluateAll((items) =>
        items.map((i) => i.getAttribute("data-content-id") ?? ""),
      )
    ).sort();
    expect(shown).toEqual(want);
    // Served by djinn, the console may call it.
    await expect(page.locator("rapi-doc")).toHaveAttribute("allow-try", "true");
    await page.screenshot({ path: path.join(shots, `docs-api-${scheme}.png`) });

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
