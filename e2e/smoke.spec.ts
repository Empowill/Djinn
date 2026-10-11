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
  await expect(
    page.locator(".sidebar").getByRole("button", { name: "New wish" }),
  ).toBeVisible();
  // The page follows djinn: the watch stream answers.
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();
  expect(errors).toEqual([]);
});

// The sidebar spans the whole height of the window, the lead's terminal sits under the main view only, and opening
// the new-wish dialog moves nothing of the sidebar.
test("sidebar geometry stays stable while the new-wish dialog opens", async ({
  page,
}) => {
  await page.goto(djinnURL());
  await expect(page.locator(".sidebar")).toBeVisible();
  const terminal = page.locator(".lead-terminal");
  await expect(terminal).toBeVisible();

  const viewport = page.viewportSize()!;
  const sidebar = (await page.locator(".sidebar").boundingBox())!;
  expect(sidebar.y).toBe(0);
  expect(Math.abs(sidebar.height - viewport.height)).toBeLessThan(0.5);
  const lead = (await terminal.boundingBox())!;
  expect(lead.x).toBeGreaterThanOrEqual(sidebar.x + sidebar.width - 0.5);
  const shell = (await page.locator(".main-shell").boundingBox())!;
  expect(Math.abs(shell.y + shell.height - lead.y)).toBeLessThan(0.5);

  const landmarks = [
    ".sidebar",
    ".sidebar-brand",
    ".sidebar .new-mission",
    ".sidebar .wish-list",
    ".sidebar .project-list",
    ".import-nav",
    ".sidebar-bottom",
  ];
  const snapshot = async () =>
    Promise.all(
      landmarks.map(async (selector) => {
        const box = await page.locator(selector).boundingBox();
        expect(box, selector).not.toBeNull();
        return box!;
      }),
    );
  const expectSameGeometry = (
    before: Awaited<ReturnType<typeof snapshot>>,
    after: Awaited<ReturnType<typeof snapshot>>,
  ) => {
    expect(after).toHaveLength(before.length);
    for (let index = 0; index < before.length; index++) {
      for (const key of ["x", "y", "width", "height"] as const) {
        expect(
          Math.abs(after[index][key] - before[index][key]),
          `${landmarks[index]} ${key}`,
        ).toBeLessThan(0.5);
      }
    }
  };

  const existing = await snapshot();
  await page.locator(".sidebar .new-mission").click();
  const dialog = page.getByRole("dialog", { name: "Make a wish" });
  await expect(dialog).toBeVisible();
  expectSameGeometry(existing, await snapshot());

  await dialog.getByRole("button", { name: "Close the window" }).click();
  await expect(dialog).toHaveCount(0);
  expectSameGeometry(existing, await snapshot());
});

// In the macOS window the traffic lights sit over the top of the sidebar, and the top 16 px of the window are the
// native drag strip (window.go): no control of the main view may start inside it.
test("native mac titlebar reserves only its chrome space", async ({ page }) => {
  await page.addInitScript(() => {
    const userAgent = navigator.userAgent;
    Object.defineProperty(navigator, "userAgent", {
      configurable: true,
      get: () => `${userAgent} wails.io`,
    });
    Object.defineProperty(navigator, "platform", {
      configurable: true,
      get: () => "MacIntel",
    });
  });
  await page.setViewportSize({ width: 760, height: 720 });
  await page.goto(djinnURL());

  await expect(page.locator("html")).toHaveClass(/native-mac/);
  await expect(page.locator(".sidebar")).toHaveCSS("padding-top", "28px");
  expect(await page.locator(".sidebar").boundingBox()).toMatchObject({
    width: 67,
  });
  await expect(page.locator(".sidebar-brand")).toHaveCSS(
    "--wails-draggable",
    "drag",
  );
  await expect(page.locator(".sidebar .new-mission")).toHaveCSS(
    "--wails-draggable",
    "no-drag",
  );

  const controls = page.locator(".main-shell .topbar button:visible");
  await expect(controls.first()).toBeVisible();
  for (const box of await controls.evaluateAll((els) =>
    els.map((el) => el.getBoundingClientRect().top),
  ))
    expect(box).toBeGreaterThanOrEqual(16);
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
