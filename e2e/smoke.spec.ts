import { expect, test, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

// djinnURL is the URL printed by `djinn up --browser`, token included.
function djinnURL(pathname = "/"): string {
  const url = new URL(process.env.DJINN_URL!);
  url.pathname = pathname;
  return url.toString();
}

// collectErrors records the uncaught exceptions and console errors of the page.
function collectErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  page.on("console", (msg) => {
    if (msg.type() === "error") errors.push(msg.text());
  });
  return errors;
}

test("the interface loads from djinn", async ({ page }) => {
  const errors = collectErrors(page);
  await page.goto(djinnURL());
  // The token left the address bar: it is now an HttpOnly cookie.
  expect(new URL(page.url()).search).toBe("");
  await expect(page).toHaveTitle(/Djinn$/);
  await expect(page.locator(".sidebar .new-mission")).toBeVisible();
  // The page follows djinn: the watch stream answers.
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();
  expect(errors).toEqual([]);
});

// The browser has no folder dialog: the path is typed, and the button of the native window is not there.
test("adding a project in the browser types the folder", async ({ page }) => {
  const errors = collectErrors(page);
  await page.goto(djinnURL());
  await page
    .locator(".sidebar")
    .getByRole("button", { name: "Create a project" })
    .click();
  const folder = page.getByRole("textbox", { name: "Folder" });
  await expect(folder).toBeVisible();
  await folder.fill("/tmp/lamp");
  await expect(folder).toHaveValue("/tmp/lamp");
  await expect(
    page.getByRole("button", { name: "Choose a folder…" }),
  ).toHaveCount(0);
  expect(errors).toEqual([]);
});

// The other tests read the interface in English. This one checks the French catalog reaches the page: its texts
// come from locales/fr.json, the only home of French in the repository.
test.describe("in French", () => {
  test.use({ locale: "fr-FR" });
  const fr: Record<string, string> = JSON.parse(
    fs.readFileSync(path.join(__dirname, "../locales/fr.json"), "utf8"),
  );

  test("the interface follows the system's language", async ({ page }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(page.locator("html")).toHaveAttribute("lang", "fr");
    await expect(
      page
        .locator(".sidebar")
        .getByRole("button", { name: fr["app.new_wish"] }),
    ).toBeVisible();
    await expect(
      page.locator(".app-statusbar").getByText(fr["status.live"]),
    ).toBeVisible();
    expect(errors).toEqual([]);
  });
});

test("the demo stream reaches the page value by value", async ({ page }) => {
  const errors = collectErrors(page);
  await page.goto(djinnURL("/e2e/stream.html"));
  await expect(page.locator("#status")).toHaveText("streaming");
  const value = page.locator("#value");
  await expect(value).not.toHaveText("0");
  const first = Number(await value.textContent());
  // The count runs to 600 at one value every 100 ms: seeing it move proves the stream is not buffered.
  await expect
    .poll(async () => Number(await value.textContent()))
    .toBeGreaterThan(first + 3);
  expect(errors).toEqual([]);
});

test("a request without the token is refused", async ({ request }) => {
  const url = new URL(djinnURL());
  url.search = "";
  const res = await request.get(url.toString(), { maxRedirects: 0 });
  expect(res.status()).toBe(401);
});
