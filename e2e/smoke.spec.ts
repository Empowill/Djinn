import { expect, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

// djinnURL is the URL printed by `djinn up --browser`, token included.
function djinnURL(pathname = "/"): string {
  const url = new URL(process.env.DJINN_URL!);
  url.pathname = pathname;
  return url.toString();
}

// djinn runs the e2e binary on the djinn the specs drive (global-setup.ts).
function djinn(...args: string[]): string {
  const binary = path.resolve(
    __dirname,
    "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
  );
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
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

// Folded, the sidebar keeps a rail of icons with each wish's tone, which says what waits in it (Q03); the toggle stays
// in the toolbar, focused, and the fold is remembered.
test("sidebar collapse glides, keeps focus, and persists", async ({ page }) => {
  const title = `Rail ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  try {
    await page.goto(djinnURL());
    const app = page.locator(".wish-app");
    const sidebar = page.locator(".sidebar");
    const sidebarContent = page.locator(".sidebar-content");
    const toggle = page.locator(".sidebar-toggle");
    const row = sidebar.locator(`.wish-nav[title*="${title}"]`);
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(row).toBeVisible();
    const expandedBrand = await page.locator(".sidebar-brand").boundingBox();
    const expandedContent = await sidebarContent.boundingBox();
    expect(expandedBrand).not.toBeNull();
    expect(expandedContent).not.toBeNull();
    expect(expandedContent!.y).toBeCloseTo(
      expandedBrand!.y + expandedBrand!.height,
      0,
    );
    const toggleBox = await toggle.boundingBox();
    await page.screenshot({
      path: "test-results/e2e/sidebar-rail-expanded.png",
    });

    await toggle.click();
    await expect(app).toHaveClass(/sidebar-collapsed/);
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(toggle).toBeFocused();
    await expect
      .poll(async () => (await sidebar.boundingBox())?.width ?? -1)
      .toBe(67);
    await expect
      .poll(async () => (await page.locator(".main-shell").boundingBox())?.x)
      .toBe(67);
    expect(await toggle.boundingBox()).toEqual(toggleBox);
    expect(toggleBox!.x + toggleBox!.width).toBeLessThanOrEqual(67);
    await expect(sidebarContent).toHaveCSS("opacity", "1");
    expect(await sidebarContent.locator(".new-mission > span").count()).toBe(0);
    await expect(row).toBeVisible();
    await expect(row.locator(".wish-tone")).toBeVisible();
    await page.screenshot({
      path: "test-results/e2e/sidebar-rail-collapsed.png",
    });

    await expect
      .poll(() =>
        page.evaluate(() => localStorage.getItem("djinn.sidebar.collapsed")),
      )
      .toBe("1");
    await page.reload();
    await expect(app).toHaveClass(/sidebar-collapsed/);
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect((await sidebar.boundingBox())?.width).toBe(67);

    await toggle.click();
    await expect(app).not.toHaveClass(/sidebar-collapsed/);
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(toggle).toBeFocused();
    await expect(sidebarContent).toHaveCSS("opacity", "1");
    await expect(page.locator(".sidebar .new-mission")).toBeVisible();
    await expect(row).toBeVisible();
  } finally {
    djinn("wish", "delete", wishId);
  }
});

test("sidebar collapse honors reduced motion", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto(djinnURL());
  const sidebar = page.locator(".sidebar");
  const content = page.locator(".sidebar-content");
  const seconds = (durations: string) =>
    Math.max(
      ...durations.split(",").map((value) => {
        const duration = value.trim();
        const number = Number.parseFloat(duration);
        return duration.endsWith("ms") ? number / 1000 : number;
      }),
    );
  const maxMotionSeconds = async () =>
    Math.max(
      seconds(
        await sidebar.evaluate((el) => getComputedStyle(el).transitionDuration),
      ),
      seconds(
        await content.evaluate((el) => getComputedStyle(el).animationDuration),
      ),
    );
  await expect.poll(maxMotionSeconds).toBeLessThanOrEqual(0.00001);
  await page.locator(".sidebar-toggle").click();
  await expect.poll(maxMotionSeconds).toBeLessThanOrEqual(0.00001);
  await expect(sidebar).toHaveCSS("width", "67px");
});

// In the macOS window the traffic lights sit over the top of the sidebar, and the top 6 px of the window are the
// native drag strip (window.go): the toolbar's toggle starts below it, beside the lights, in the same place folded or
// not, and the topbar's controls keep their row under the toolbar's, a wish's actions included.
test("native mac titlebar reserves only its chrome space", async ({ page }) => {
  const title = `Mind the strip ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
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
  const sidebarBox = await page.locator(".sidebar").boundingBox();
  expect(sidebarBox).toMatchObject({ width: 67 });
  const toggle = page.locator(".app-toolbar-toggle");
  const toolbar = page.locator(".app-toolbar");
  const toggleBox = await toggle.boundingBox();
  expect(toggleBox).toMatchObject({ width: 20, height: 20, x: 92, y: 6 });
  expect(await toolbar.boundingBox()).toMatchObject({
    x: 92,
    y: 6,
    height: 20,
  });
  const iconBefore = await toggle
    .locator("svg")
    .evaluate((svg) => svg.outerHTML);
  expect(await page.locator(".app-toolbar > button").count()).toBe(1);
  await expect(page.locator(".sidebar-brand")).toHaveCSS(
    "--wails-draggable",
    "drag",
  );
  await expect(page.locator(".sidebar .new-mission")).toHaveCSS(
    "--wails-draggable",
    "no-drag",
  );
  const planNav = page.locator(".sidebar .plan-nav");
  if (await planNav.count()) await planNav.click();
  const topbar = page.locator(".main-shell .topbar");
  if (await topbar.count()) {
    const breadcrumbs = page.locator(".main-shell .topbar .breadcrumbs");
    const breadcrumbsBox = await breadcrumbs.boundingBox();
    expect(breadcrumbsBox).not.toBeNull();
    expect(breadcrumbsBox!.x).toBeGreaterThanOrEqual(
      toggleBox!.x + toggleBox!.width,
    );
  }

  try {
    await page.locator(".sidebar").getByRole("button", { name: title }).click();
    await expect(
      page.locator(".main-shell .topbar").getByRole("button", {
        name: "Delete this wish",
      }),
    ).toBeVisible();
    const controls = page.locator(".main-shell .topbar button:visible");
    for (const [name, top] of await controls.evaluateAll((els) =>
      els.map((el) => [
        el.getAttribute("aria-label") ?? el.textContent,
        el.getBoundingClientRect().top,
      ]),
    ))
      expect(top, `${name}`).toBeGreaterThanOrEqual(16);
    // The sidebar is a rail here: the topbar starts after the toggle.
    const first = await page
      .locator(".main-shell .topbar > :first-child")
      .boundingBox();
    expect(first!.x).toBeGreaterThanOrEqual(toggleBox!.x + toggleBox!.width);
  } finally {
    djinn("wish", "delete", wishId);
  }
  await toggle.click();
  await expect(page.locator(".wish-app")).toHaveClass(/sidebar-collapsed/);
  expect(await toggle.boundingBox()).toEqual(toggleBox);
  expect(await toggle.locator("svg").evaluate((svg) => svg.outerHTML)).toBe(
    iconBefore,
  );
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
