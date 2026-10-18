import { expect, test, type Page } from "./test";
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

test("tooltips expose labels and real keyboard hints", async ({ page }) => {
  await page.goto(djinnURL());

  const tooltip = page.locator('[role="tooltip"]');
  const newWish = page.locator(".sidebar .new-mission");
  await newWish.hover();
  await expect(tooltip).toContainText("New wish");
  await expect(tooltip.locator("kbd")).toHaveText(/N/);
  await expect(newWish).toHaveAttribute("aria-describedby", /\S+/);
  await expect(newWish).toHaveAttribute(
    "aria-keyshortcuts",
    /^(Meta|Control)\+N$/,
  );
  await newWish.focus();
  await expect(tooltip).toBeVisible();

  const toggle = page.locator(".app-toolbar-toggle");
  await toggle.focus();
  await expect(tooltip).toBeVisible();
  const tooltipBox = await tooltip.boundingBox();
  const viewport = page.viewportSize();
  expect(tooltipBox).not.toBeNull();
  expect(tooltipBox!.x).toBeGreaterThanOrEqual(0);
  expect(tooltipBox!.y).toBeGreaterThanOrEqual(0);
  expect(tooltipBox!.x + tooltipBox!.width).toBeLessThanOrEqual(
    viewport!.width,
  );
  expect(tooltipBox!.y + tooltipBox!.height).toBeLessThanOrEqual(
    viewport!.height,
  );
  await expect(tooltip).toContainText("Collapse the navigation");
  await expect(tooltip.locator("kbd")).toHaveText(/(⌘|Ctrl).*\\/);
  await page.keyboard.press("ControlOrMeta+Backslash");
  await expect(page.locator(".wish-app")).toHaveClass(/sidebar-collapsed/);
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await page.keyboard.press("ControlOrMeta+Backslash");
  await expect(page.locator(".wish-app")).not.toHaveClass(/sidebar-collapsed/);
});

// The sidebar spans the whole height of the window, the lead's terminal sits under the main view only, and the creation
// page, which takes the terminal's place until Back, moves nothing of the sidebar. The sidebar is measured once the
// wishes and the projects are read: on the Windows CI they came during the creation, and a paused wish's row moved the
// list (PR #2). A paused wish and late lists reproduce it anywhere.
test("sidebar geometry stays stable while creation hides the terminal", async ({
  page,
}) => {
  const title = `Hold the sidebar ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id;
  try {
    await sidebarStaysStable(page, title);
  } finally {
    djinn("wish", "delete", wishId);
  }
});

async function sidebarStaysStable(page: Page, title: string) {
  for (const list of ["WishService/List", "ProjectService/List"])
    await page.route(`**/plan.v1.${list}`, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 400));
      await route.continue();
    });
  await page.goto(djinnURL());
  await expect(page.locator(".sidebar")).toBeVisible();
  const terminal = page.locator(".lead-terminal");
  await expect(terminal).toBeVisible();
  const importButton = page
    .locator(".sidebar")
    .getByRole("button", { name: "Import a wish" });
  await expect(importButton).toBeDisabled();
  await expect(importButton).toBeEnabled();
  await expect(
    page.locator(".sidebar .wish-list").getByTitle(title),
  ).toBeVisible();

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
  const creation = page.locator(".wish-creation");
  await expect(creation).toBeVisible();
  await expect(terminal).toBeHidden();
  expectSameGeometry(existing, await snapshot());

  await page.locator(".app-toolbar-back").click();
  await expect(creation).toHaveCount(0);
  await expect(terminal).toBeVisible();
  expectSameGeometry(existing, await snapshot());
}

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
    await expect(row.locator(".wish-emblem svg.wish-icon")).toBeVisible();
    await expect(row.locator(".wish-tone")).toHaveCount(0);

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
  const existing = (JSON.parse(djinn("wish", "list", "--json")).wishes ??
    []) as {
    id: string;
    state: string;
  }[];
  for (const w of existing)
    if (w.state === "WISH_STATE_ACTIVE") djinn("wish", "pause", w.id);
  const title = `Mind the strip ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  try {
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
    // The creation page adds Back after the toggle, which stays where it is.
    await page.locator(".sidebar .new-mission").click();
    const back = page.locator(".app-toolbar-back");
    await expect(back).toBeEnabled();
    expect(await toggle.boundingBox()).toEqual(toggleBox);
    expect(await back.boundingBox()).toMatchObject({
      width: 20,
      height: 20,
      y: 6,
    });
    expect((await back.boundingBox())!.x).toBeGreaterThan(
      toggleBox!.x + toggleBox!.width,
    );
    await back.click();
    await expect(back).toHaveCount(0);
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
      const pageHeader = page.locator(".page-header");
      if (await pageHeader.count()) {
        await expect(pageHeader).toHaveCSS("padding-top", "6px");
        expect(breadcrumbsBox!.x).toBeCloseTo(166, 0);
      } else {
        expect(breadcrumbsBox!.x).toBeGreaterThanOrEqual(
          toggleBox!.x + toggleBox!.width,
        );
      }
    }

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
      expect(top, `${name}`).toBeGreaterThanOrEqual(6);
    // The sidebar is a rail here: the topbar starts after the toggle.
    const first = await page
      .locator(".main-shell .topbar > :first-child")
      .boundingBox();
    expect(first!.x).toBeGreaterThanOrEqual(toggleBox!.x + toggleBox!.width);
    await toggle.click();
    await expect(page.locator(".wish-app")).toHaveClass(/sidebar-collapsed/);
    expect(await toggle.boundingBox()).toEqual(toggleBox);
    expect(await toggle.locator("svg").evaluate((svg) => svg.outerHTML)).toBe(
      iconBefore,
    );
  } finally {
    djinn("wish", "delete", wishId);
  }
});

// Without Wails' JavaScript runtime, --wails-draggable does nothing: src/native-window.ts posts Wails' own drag and
// double-click messages for a press in the top 40 px that is not on a control. A fake WebKit bridge records them.
test("native mac drag protocol ignores controls and handles the empty titlebar", async ({
  page,
}) => {
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
    const messages: string[] = [];
    Object.defineProperty(window, "__djinnNativeDragMessages", {
      configurable: true,
      value: messages,
    });
    Object.defineProperty(window, "webkit", {
      configurable: true,
      value: {
        messageHandlers: {
          external: {
            postMessage: (message: string) => messages.push(message),
          },
        },
      },
    });
  });
  await page.setViewportSize({ width: 1100, height: 760 });
  await page.goto(djinnURL());
  await expect(page.locator("html")).toHaveClass(/native-mac/);

  const messages = () =>
    page.evaluate(
      () =>
        (window as unknown as { __djinnNativeDragMessages: string[] })
          .__djinnNativeDragMessages,
    );
  const clearMessages = () =>
    page.evaluate(() => {
      (
        window as unknown as { __djinnNativeDragMessages: string[] }
      ).__djinnNativeDragMessages.length = 0;
    });

  // The topbar centres the operating-load gauge: the strip between the sidebar and the gauge is empty.
  // The gauge shows in the page's header once the wishes are read (wish-app.tsx).
  const gaugeGroup = page.getByRole("group", { name: "Operating load" });
  await expect(gaugeGroup).toBeVisible();
  const main = await page.locator(".main-shell").boundingBox();
  const gauge = await gaugeGroup.boundingBox();
  expect(main).not.toBeNull();
  expect(gauge).not.toBeNull();
  expect(gauge!.y).toBeLessThan(40);
  const x = Math.round((main!.x + gauge!.x) / 2);

  await page.mouse.move(x, 20);
  await page.mouse.down();
  await page.mouse.move(x + 10, 20);
  await page.mouse.up();
  expect(await messages()).toEqual(["wails:drag"]);

  await clearMessages();
  await page.mouse.dblclick(x, 20);
  expect(await messages()).toEqual(["wails:drag:doubleclick"]);

  await clearMessages();
  await page.mouse.move(x + 10, 48);
  await page.mouse.down();
  await page.mouse.move(x + 20, 48);
  await page.mouse.up();
  expect(await messages()).toEqual([]);

  // A control inside the strip keeps its clicks: the toolbar's toggle folds the sidebar, and unfolds it again.
  await clearMessages();
  const toggle = await page.locator(".app-toolbar-toggle").boundingBox();
  expect(toggle).not.toBeNull();
  expect(toggle!.y).toBeLessThan(40);
  await page.mouse.move(toggle!.x + 6, toggle!.y + 10);
  await page.mouse.down();
  await page.mouse.move(toggle!.x + 14, toggle!.y + 10);
  await page.mouse.up();
  await expect(page.locator(".wish-app")).toHaveClass(/sidebar-collapsed/);
  await page.mouse.dblclick(toggle!.x + 10, toggle!.y + 10);
  expect(await messages()).toEqual([]);
  await expect(page.locator(".wish-app")).toHaveClass(/sidebar-collapsed/);
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
