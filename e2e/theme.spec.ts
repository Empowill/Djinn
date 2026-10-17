// The light theme everywhere, against the real djinn up --browser (see global-setup.ts): the system's theme by
// default, then dark and light chosen in the settings, on the dialogs, the lead's blocks (Markdown, code, Mermaid) and
// the terminal. Screenshots of each, dark and light, go to test-results/e2e/theme-*.png.
import { type Page, expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { disableWishSmokeWebGL } from "./wish-smoke-fixture";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const shots = path.join(__dirname, "../test-results/e2e");

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

type Wish = { id: string; state: string };
const wishes = (): Wish[] =>
  JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];

const at = "2026-10-09T09:10:00Z";

// shownTheme is the theme Djinn keeps as the one the window shows, from settings.json of its data folder.
function shownTheme(): string {
  try {
    const file = path.join(process.env.DJINN_E2E_HOME!, "settings.json");
    return JSON.parse(fs.readFileSync(file, "utf8")).shownTheme ?? "";
  } catch {
    return "";
  }
}

async function choose(page: Page, value: "" | "dark" | "light") {
  await page.evaluate((v) => {
    if (v) localStorage.setItem("djinn.theme", v);
    else localStorage.removeItem("djinn.theme");
  }, value);
  await page.reload();
}

const background = (page: Page, selector: string) =>
  page
    .locator(selector)
    .first()
    .evaluate((e) => {
      const style = getComputedStyle(e);
      return `${style.backgroundColor} ${style.backgroundImage}`;
    });

// The system's theme by default: light under a light system, dark under a dark one.
test("the window follows the system's theme by default, and tells Djinn the one it shows", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto(process.env.DJINN_URL!);
  await choose(page, "");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  // The page tells Djinn the theme it shows, the system's resolved: the Djinn's tone follows it (W156).
  await expect.poll(shownTheme).toBe("SHOWN_THEME_LIGHT");
  await page.emulateMedia({ colorScheme: "dark" });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect.poll(shownTheme).toBe("SHOWN_THEME_DARK");
});

const looks = {
  dark: { page: "rgb(17, 17, 17)", card: "rgb(25, 25, 25)" },
  light: { page: "rgb(246, 246, 248)", card: "rgb(255, 255, 255)" },
};

// pauseActive pauses the active wishes, which other specs may have left: the page shows the one a test makes.
function pauseActive() {
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);
}

// open shows the window in the theme chosen in the settings, kept before its first load: one load, not a load and a
// reload, which a loaded machine took seconds each (W249). A later choose changes it.
async function open(page: Page, theme: "dark" | "light") {
  await page.addInitScript((v) => {
    if (sessionStorage.getItem("djinn.e2e.theme")) return;
    sessionStorage.setItem("djinn.e2e.theme", v);
    localStorage.setItem("djinn.theme", v);
  }, theme);
  await page.setViewportSize({ width: 1440, height: 1300 });
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
}

// Four tests per theme, the terminal, the lead's blocks, the settings, the creation page: each has its 30 s, which one
// test for all took whole on a loaded machine, with or without the trace (W249).
for (const theme of ["dark", "light"] as const) {
  const look = looks[theme];

  test(`the terminal follows the theme chosen, ${theme}`, async ({ page }) => {
    test.skip(process.platform === "win32", "the terminal runs a POSIX shell");
    pauseActive();
    await open(page, theme);
    // Every ANSI colour, normal and bright, on a shell of its own: one another test left may come after the empty
    // panel showed, its button gone under a click (W249).
    await page.locator(".lead-terminal-tab-new").click();
    const tab = page.locator(".lead-terminal-tab.selected");
    await expect(tab).toBeVisible();
    const terminal = page.locator(".lead-terminal .xterm");
    await terminal.click();
    await page.keyboard.type(
      "for i in 0 1 2 3 4 5 6 7; do printf '\\033[3%sm col%s \\033[9%sm bright%s\\033[0m ' $i $i $i $i; done; echo",
    );
    await page.keyboard.press("Enter");
    // Its ground is the page's, its rows are coloured by the theme.
    await expect(
      page.locator(".lead-terminal .xterm-rows").getByText("bright7").last(),
    ).toBeVisible();
    expect(
      await background(page, ".lead-terminal .xterm-scrollable-element"),
    ).toContain(look.page);
    await page.locator(".lead-terminal").screenshot({
      path: path.join(shots, `theme-terminal-${theme}.png`),
    });
    // The shell closed before the page reloads, which would show the panel before its tabs.
    const tabs = page.locator(".lead-terminal-tab");
    const shells = await tabs.count();
    await tab.locator(".lead-terminal-tab-close").click();
    await expect(tabs).toHaveCount(shells - 1);
    await choose(page, "");
  });

  test(`the lead's blocks follow the theme chosen, ${theme}`, async ({
    page,
  }) => {
    pauseActive();
    const tag = randomUUID().slice(0, 8);
    const wishId = randomUUID();
    const title = `Read by the lamp ${tag}`;
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-theme-"));
    const file = path.join(dir, "lamp.json");
    fs.writeFileSync(
      file,
      JSON.stringify({
        version: 1,
        create_time: at,
        wish: { id: wishId, title, create_time: at },
        tasks: [],
      }),
    );
    try {
      djinn("wish", "import", file);
      djinn(
        "block",
        "put",
        wishId,
        "--kind",
        "note",
        "--title",
        "How the wick burns",
        "--content",
        [
          "The wick **burns** from the top; `oil` rises by itself.",
          "",
          "```go",
          'func burn(wick string) error {\n\treturn fmt.Errorf("no oil in %s", wick)\n}',
          "```",
          "",
          "```mermaid",
          "flowchart LR\n  Oil --> Wick --> Light",
          "```",
          "",
          "| Oil | Hours |",
          "| --- | --- |",
          "| Olive | 6 |",
        ].join("\n"),
      );
      await open(page, theme);

      // The lead's blocks, in the agents' tab, opened: Markdown, a code block, a Mermaid diagram on the card's ground.
      await page.locator(".wish-nav").filter({ hasText: title }).click();
      await expect(page.locator(".hero h1")).toHaveText(title);
      await page.getByRole("tab", { name: /^For agents/ }).click();
      const heading = page
        .locator(".agent-block .fold-heading")
        .filter({ hasText: "How the wick burns" });
      if ((await heading.getAttribute("aria-expanded")) === "false")
        await heading.click();
      const block = page.locator(".ac-markdown").filter({ hasText: "wick" });
      await expect(block.locator(".ac-code")).toBeVisible();
      // Mermaid draws in its frame: an SVG with the three nodes of the source, on the theme's ground, in the theme's
      // ink.
      const diagram = page.frameLocator(".mermaid-support iframe");
      const svg = diagram.locator("#render svg");
      await expect(svg).toBeVisible();
      await expect(svg.locator(".node")).toHaveCount(3);
      for (const node of ["Oil", "Wick", "Light"])
        await expect(
          svg.locator(".node").filter({ hasText: node }),
        ).toHaveCount(1);
      await expect(
        page.locator(".mermaid-support").getByRole("status"),
      ).toHaveCount(0);
      await expect
        .poll(() =>
          diagram
            .locator("body")
            .evaluate((e) => getComputedStyle(e).backgroundColor),
        )
        .toBe(look.card);
      // The frame fits the diagram: no tall empty ground under it.
      expect(
        await page
          .locator(".mermaid-support iframe")
          .evaluate((e) => e.clientHeight),
      ).toBeLessThan(200);
      // The labels follow the theme: light ink on the dark ground, dark ink on the light one.
      await expect
        .poll(() =>
          svg
            .locator(".node .nodeLabel, .node text")
            .first()
            .evaluate((e) => {
              const style = getComputedStyle(e);
              const ink = e instanceof SVGElement ? style.fill : style.color;
              const [r, g, b] = ink.match(/\d+/g)!.map(Number);
              return r + g + b > 382 ? "light" : "dark";
            }),
        )
        .toBe(theme === "dark" ? "light" : "dark");
      await page.screenshot({
        path: path.join(shots, `theme-wish-${theme}.png`),
      });
      await block.screenshot({
        path: path.join(shots, `theme-blocks-${theme}.png`),
      });
      await choose(page, "");
    } finally {
      try {
        djinn("wish", "delete", wishId);
      } catch {}
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });

  test(`the settings follow the theme chosen, ${theme}`, async ({ page }) => {
    pauseActive();
    await open(page, theme);
    await page.getByTitle("Connections & preferences").click();
    const settings = page.locator(".modal");
    await expect(settings).toBeVisible();
    await expect(page.getByLabel("Theme")).toHaveValue(theme);
    expect(await background(page, ".modal")).toContain(look.card);
    await page.screenshot({
      path: path.join(shots, `theme-settings-${theme}.png`),
    });
    await page.keyboard.press("Escape");
    await expect(settings).toHaveCount(0);
    await choose(page, "");
  });

  test(`the creation page of a new wish follows the theme chosen, ${theme}`, async ({
    page,
  }) => {
    pauseActive();
    await open(page, theme);
    await page.getByRole("button", { name: "New wish" }).first().click();
    const creation = page.locator(".wish-creation");
    await expect(creation).toBeVisible();
    await page.screenshot({
      path: path.join(shots, `theme-new-wish-${theme}.png`),
    });
    await page.keyboard.press("Escape");
    await expect(creation).toHaveCount(0);
    await choose(page, "");
  });
}

test("the light creation fallback keeps its status readable when WebGL is unavailable", async ({
  page,
}) => {
  await disableWishSmokeWebGL(page);
  await page.addInitScript(() => localStorage.setItem("djinn.theme", "light"));
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "New wish" }).first().click();

  const creation = page.locator(".wish-creation");
  const smoke = creation.locator(".wish-smoke");
  await expect(creation.locator(".wish-creation-intro h1")).toHaveCSS(
    "opacity",
    "1",
  );
  await expect(creation.getByRole("textbox")).toBeVisible();
  await expect(creation.locator(".wish-creation-controls")).toBeVisible();
  await expect(
    creation.locator(".wish-creation-footer .button.accent"),
  ).toBeVisible();
  await expect(smoke.locator(".wish-smoke-canvas")).toHaveAttribute(
    "data-error",
    /./,
  );
  const fallback = smoke.locator(".wish-smoke-fallback");
  await expect(fallback).toBeVisible();
  const contrast = await smoke.evaluate((element) => {
    const channel = (value: string) => {
      const match = value.match(/\d+(?:\.\d+)?/g)?.map(Number) ?? [];
      return match.length >= 3 ? match.slice(0, 3) : [0, 0, 0];
    };
    const luminance = (rgb: number[]) =>
      rgb.reduce((sum, value, index) => {
        const linear =
          value / 255 <= 0.03928
            ? value / 255 / 12.92
            : ((value / 255 + 0.055) / 1.055) ** 2.4;
        return sum + linear * [0.2126, 0.7152, 0.0722][index];
      }, 0);
    const style = getComputedStyle(element);
    const text = getComputedStyle(
      element.querySelector(".wish-smoke-fallback")!,
    );
    const foreground = luminance(channel(text.color));
    const background = luminance(channel(style.backgroundColor));
    return (
      (Math.max(foreground, background) + 0.05) /
      (Math.min(foreground, background) + 0.05)
    );
  });
  expect(contrast).toBeGreaterThan(4.5);
  await page.screenshot({
    path: path.join(shots, "theme-smoke-fallback-light.png"),
    fullPage: true,
  });
  await page.keyboard.press("Escape");
});

test(
  "the light smoke canvas fallback keeps its ASCII palette readable when workers are unavailable",
  { tag: "@solver" },
  async ({ page }) => {
    // Without workers the solver runs on the page's thread: on a software WebGL2 it holds the page for seconds.
    test.setTimeout(240_000);
    await page.addInitScript(() => {
      localStorage.setItem("djinn.theme", "light");
      Object.defineProperty(globalThis, "Worker", {
        configurable: true,
        value: undefined,
      });
    });
    // This path renders the ASCII fallback on the page thread. Keep the palette
    // fixture compact so it checks readability without becoming a performance test.
    await page.setViewportSize({ width: 800, height: 600 });
    await page.goto(process.env.DJINN_URL!);
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await page.getByRole("button", { name: "New wish" }).first().click();

    const creation = page.locator(".wish-creation");
    await expect(creation.locator(".wish-creation-intro h1")).toHaveCSS(
      "opacity",
      "1",
    );
    await expect(creation.getByRole("textbox")).toBeVisible();
    await expect(creation.locator(".wish-creation-controls")).toBeVisible();
    await expect(
      creation.locator(".wish-creation-footer .button.accent"),
    ).toBeVisible();
    const smoke = page.locator(".wish-creation .wish-smoke");
    await expect(smoke.locator(".wish-smoke-worker-canvas")).toHaveCount(0);
    const canvas = smoke.locator(".wish-smoke-canvas");
    const error = await canvas.getAttribute("data-error");
    if (error) {
      await expect(smoke.locator(".wish-smoke-fallback")).toBeVisible();
    } else {
      await expect(canvas).toHaveAttribute("data-visible", "true");
      const palette = await smoke.evaluate((element) => ({
        foreground: getComputedStyle(
          element.querySelector(".wish-smoke-ascii pre")!,
        ).color,
        background: getComputedStyle(element).backgroundColor,
        layers: element.querySelectorAll(".wish-smoke-ascii pre").length,
      }));
      expect(palette.layers).toBe(8);
      const channel = (value: string) =>
        value
          .match(/\d+(?:\.\d+)?/g)
          ?.map(Number)
          .slice(0, 3) ?? [0, 0, 0];
      const luminance = (rgb: number[]) =>
        rgb.reduce((sum, value, index) => {
          const linear =
            value / 255 <= 0.03928
              ? value / 255 / 12.92
              : ((value / 255 + 0.055) / 1.055) ** 2.4;
          return sum + linear * [0.2126, 0.7152, 0.0722][index];
        }, 0);
      const foreground = luminance(channel(palette.foreground));
      const background = luminance(channel(palette.background));
      const contrast =
        (Math.max(foreground, background) + 0.05) /
        (Math.min(foreground, background) + 0.05);
      expect(contrast).toBeGreaterThan(4.5);
    }
    await page.screenshot({
      path: path.join(shots, "theme-smoke-fallback-canvas-light.png"),
    });
    await page.keyboard.press("Escape");
  },
);
