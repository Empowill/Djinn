// The Grimoire, the documentation that djinn serves at /docs/ (docs/site): its splash page, then the book, a navigation
// on the left with Introduction by default, a page per concept, and the Command line, filled in by djinn from its own
// commands (TestCommands checks that every command has its entry); the navigation in the window's language; and the
// way to it from the side panel's Grimoire, unfolded and folded (W89). Screenshots, dark and light, go to
// test-results/e2e/docs-*.png.
import { type Page, expect, test } from "@playwright/test";
import path from "node:path";
import { portable } from "./portable";

portable();

const shots = path.join(__dirname, "../test-results/e2e");

function djinnURL(pathname = "/", params: Record<string, string> = {}): string {
  const url = new URL(process.env.DJINN_URL!);
  url.pathname = pathname;
  for (const [name, value] of Object.entries(params))
    url.searchParams.set(name, value);
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
  test(`the Grimoire opens on its splash, then its book, its concepts and every command, ${scheme}`, async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    // The first visit trades the token for the cookie that /docs/ needs too.
    await page.goto(djinnURL());
    await page.goto(djinnURL("/docs/"));
    await expect(page).toHaveTitle("Grimoire · Djinn documentation");
    await expect(page.locator("html")).toHaveAttribute("data-theme", scheme);

    // The splash: the page the documentation was, titled Grimoire.
    const splash = page.locator("#welcome");
    await expect(
      splash.getByRole("heading", { level: 1, name: "Grimoire" }),
    ).toBeVisible();
    for (const name of [
      "The lamp and the smoke",
      "Wish for anything, except what no djinn can grant",
      "The life of a wish",
      "What you will meet",
    ])
      await expect(
        splash.getByRole("heading", { level: 2, name }),
      ).toBeVisible();
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
    await expect(page.locator(".book")).toBeHidden();
    await page.screenshot({
      path: path.join(shots, `docs-splash-${scheme}.png`),
    });

    // Into the book: Introduction first, the navigation on the left.
    await splash
      .getByRole("link", { name: "Read the Grimoire" })
      .first()
      .click();
    await expect(page).toHaveURL(/#introduction$/);
    await expect(splash).toBeHidden();
    const nav = page.getByRole("navigation", { name: "Grimoire" });
    await expect(
      nav.getByRole("link", { name: "Introduction" }),
    ).toHaveAttribute("aria-current", "page");
    await expect(
      page.getByRole("heading", { level: 1, name: "Introduction" }),
    ).toBeVisible();
    for (const name of [
      "Getting started",
      "Wish",
      "Azima",
      "Task and worker",
      "Watcher",
      "Question and move",
      "Decision",
      "Block",
      "Tilasm",
      "Project and settings",
      "Integration branch",
      "Gates",
      "Push strategy",
      "Lead notes",
      "Skills and templates",
      "Ifrit",
      "Command line",
    ])
      await expect(nav.getByRole("link", { name, exact: true })).toBeVisible();
    await page.screenshot({
      path: path.join(shots, `docs-introduction-${scheme}.png`),
    });

    // A concept's page: what is on it leads to its sections, and the next page follows.
    await nav.getByRole("link", { name: "Wish", exact: true }).click();
    await expect(page).toHaveURL(/#wish$/);
    const wish = page.locator("#wish");
    await expect(
      wish.getByRole("heading", { level: 1, name: "Wish" }),
    ).toBeVisible();
    await expect(page.locator("#introduction")).toBeHidden();
    await wish
      .getByRole("navigation", { name: "On this page" })
      .getByRole("link", { name: "Granting a wish" })
      .click();
    await expect(page.locator("#wish-granting-a-wish")).toBeInViewport();
    await wish.getByRole("link", { name: /^Next/ }).click();
    await expect(page).toHaveURL(/#azima$/);
    await expect(
      page.getByRole("heading", { level: 1, name: "Azima" }),
    ).toBeVisible();
    await page.screenshot({
      path: path.join(shots, `docs-azima-${scheme}.png`),
    });

    // An address that names nothing opens the book on Introduction.
    await page.goto(djinnURL("/docs/") + "#no-such-page");
    await expect(
      page.getByRole("heading", { level: 1, name: "Introduction" }),
    ).toBeVisible();

    await nav.getByRole("link", { name: "Command line" }).click();
    await expect(page).toHaveURL(/#commands$/);
    await expect(page.locator("#introduction")).toBeHidden();
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
    // A word no command holds: plain words like "such" turn up in the help of new commands.
    await page.getByRole("searchbox").fill("xyzzy");
    await expect(page.getByText("No command matches.")).toBeVisible();
    await page.getByRole("searchbox").fill("");
    await expect(page.locator("#group-wish")).toBeVisible();

    // The brand leads back to the splash.
    await page.locator(".brand").click();
    await expect(splash).toBeVisible();
    await expect(page.locator(".book")).toBeHidden();
    expect(errors).toEqual([]);
  });
}

// The navigation speaks the window's language; the pages stay in English.
test("the Grimoire's navigation is in the language asked", async ({ page }) => {
  const errors = collectErrors(page);
  await page.goto(djinnURL());
  await page.goto(djinnURL("/docs/", { lang: "fr" }) + "#wish");
  const nav = page.getByRole("navigation", { name: "Le Grimoire" });
  await expect(
    nav.getByRole("link", { name: "Souhait", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  await expect(nav.getByRole("link", { name: "Prise en main" })).toBeVisible();
  await expect(
    page.getByRole("heading", { level: 1, name: "Wish" }),
  ).toBeVisible();
  await expect(
    page.locator("#wish").getByRole("link", { name: /^Suivant/ }),
  ).toBeVisible();
  expect(errors).toEqual([]);
});

// The documentation was so slow to scroll in the window that it seemed to reload: a blur as wide as the window under
// smoke that drifted, drawn again on every frame (W144). The frame is never reloaded by a scroll or a click, and
// nothing blurs what moves behind it; that it scrolls at the pace of the screen is the next test's budget.
test("the Grimoire opens the documentation over the window, in its theme and language, and a scroll keeps it loaded", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto(djinnURL());
  // The settings lead to it no more: the side panel's Grimoire does, just under the Hourglass.
  await page.getByTitle("Connections & preferences").click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.getByRole("dialog").getByText("Documentation")).toHaveCount(
    0,
  );
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toBeHidden();
  const grimoire = page.locator(".sidebar .grimoire-nav");
  await expect(grimoire).toHaveText("Grimoire");
  await expect(grimoire).toHaveAttribute("title", "Documentation");
  await expect(
    page.locator(".sidebar .hourglass-nav + .grimoire-nav"),
  ).toHaveCount(1);
  await page.getByRole("button", { name: "Grimoire", exact: true }).click();
  const docs = page.getByRole("dialog", { name: "Grimoire" });
  await expect(docs).toBeVisible();
  await expect(docs.locator("iframe")).toHaveAttribute(
    "src",
    /[?&]lang=en(&|$)/,
  );
  const frame = page.frameLocator('iframe[title="Grimoire"]');
  // It opens on the splash.
  await expect(
    frame.getByRole("heading", { level: 1, name: "Grimoire" }),
  ).toBeVisible();
  await expect(frame.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(frame.locator("html")).toHaveClass(/embedded/);

  // A mark set on the loaded page: a reload would lose it.
  const site = frame.locator("html");
  await site.evaluate(() => {
    (window as unknown as { loaded: number }).loaded = 1;
  });
  // Nothing blurs what is behind it: neither the window under the frame nor anything in the site.
  expect(
    await page
      .locator(".docs-backdrop")
      .evaluate((e) => getComputedStyle(e).backdropFilter),
  ).toBe("none");
  expect(
    await site.evaluate(() =>
      Array.from(document.styleSheets)
        .flatMap((sheet) => Array.from(sheet.cssRules))
        .filter((rule) => (rule as CSSStyleRule).style?.backdropFilter)
        .map((rule) => (rule as CSSStyleRule).selectorText),
    ),
  ).toEqual([]);
  // The sky, blurred over the whole page, stays still.
  expect(
    await site.evaluate(
      () =>
        document
          .getAnimations()
          .filter((a) =>
            document
              .querySelector(".sky")!
              .contains((a.effect as KeyframeEffect).target as Element),
          ).length,
    ),
  ).toBe(0);
  // A scroll, as the reader's.
  await site.evaluate(() => window.scrollBy(0, 1_200));

  // The book, a page and a command move in the page: the page stays the one loaded.
  await frame.getByRole("link", { name: "Open the Grimoire" }).click();
  await expect(
    frame.getByRole("heading", { level: 1, name: "Introduction" }),
  ).toBeVisible();
  const nav = frame.getByRole("navigation", { name: "Grimoire" });
  await nav.getByRole("link", { name: "Command line" }).click();
  await frame
    .locator(".cli-toc")
    .getByRole("link", { name: "answer", exact: true })
    .click();
  await expect(frame.locator("#cmd-question-answer")).toBeInViewport();
  await nav.getByRole("link", { name: "Question and move" }).click();
  await expect(
    frame.getByRole("heading", { level: 1, name: "Question and move" }),
  ).toBeVisible();
  expect(
    await site.evaluate(
      () => (window as unknown as { loaded?: number }).loaded,
    ),
  ).toBe(1);

  await docs.getByRole("button", { name: "Close the window" }).click();
  await expect(docs).toBeHidden();

  // Folded, the side panel keeps the Grimoire: its book, its name and its tooltip; it opens the Grimoire on its splash.
  await page.getByRole("button", { name: "Collapse the navigation" }).click();
  await expect(page.locator(".app.sidebar-collapsed")).toBeVisible();
  await expect(grimoire.locator("span")).toHaveCount(0);
  await expect(grimoire.locator("svg.lucide-book-marked")).toBeVisible();
  await expect(grimoire).toHaveAttribute("title", "Documentation");
  await page.getByRole("button", { name: "Grimoire", exact: true }).click();
  await expect(docs).toBeVisible();
  await expect(frame.locator("#welcome")).toBeVisible();
  await docs.getByRole("button", { name: "Close the window" }).click();
  await expect(docs).toBeHidden();
  await page.getByRole("button", { name: "Expand the navigation" }).click();
  await expect(page.locator(".app.sidebar-collapsed")).toHaveCount(0);
});

// The frame scrolls at the pace of the screen (W144): a time budget, run on demand (playwright.config.ts).
test(
  "the Grimoire scrolls without stalling",
  { tag: "@budget" },
  async ({ page }) => {
    await page.goto(djinnURL());
    await page.getByRole("button", { name: "Grimoire", exact: true }).click();
    const frame = page.frameLocator('iframe[title="Grimoire"]');
    await expect(
      frame.getByRole("heading", { level: 1, name: "Grimoire" }),
    ).toBeVisible();
    const site = frame.locator("html");
    // A dozen frames of scrolling: their median stays far under the quarter of a second each took. A stalling page
    // stalls every try; other workers' gates load the machine for a while only (W177, W75): the best of five tries.
    const median = await site.evaluate(async () => {
      const tryOnce = () =>
        new Promise<number>((resolve) => {
          window.scrollTo(0, 0);
          const gaps: number[] = [];
          let last = performance.now();
          const step = (now: number) => {
            gaps.push(now - last);
            last = now;
            window.scrollBy(0, 120);
            if (gaps.length < 12) requestAnimationFrame(step);
            else resolve(gaps.sort((a, b) => a - b)[6]);
          };
          requestAnimationFrame(step);
        });
      let best = Infinity;
      for (let i = 0; i < 5 && best >= 100; i++)
        best = Math.min(best, await tryOnce());
      return best;
    });
    expect(median).toBeLessThan(100);
  },
);
