// The light theme everywhere, against the real djinn up --browser (see global-setup.ts): the system's theme by
// default, then dark and light chosen in the settings, on the dialogs, the lead's blocks (Markdown, code, Mermaid) and
// the terminal. Screenshots of each, dark and light, go to test-results/e2e/theme-*.png and the merge review directory
// (DJINN_MERGE_VISUAL_DIR, or /tmp/djinn-merge-visual-20261010).
import { expect, test, type Locator, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { disableWishSmokeWebGL } from "./wish-smoke-fixture";
import { mergeVisual } from "./merge-visuals";

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

const MIN_EVOLVED_SMOKE_GLYPHS = 100;
const EVOLVED_SMOKE_TIMEOUT_MS = 20_000;

function visibleSmokeGlyphs(layers: string[]): number {
  return layers.reduce(
    (count, layer) => count + (layer.match(/\S/g)?.length ?? 0),
    0,
  );
}

/**
 * Wait until the smoke surface has either applied a visible worker frame or
 * truthfully switched to its readable fallback.  A screenshot taken after
 * the creation controls appear can still catch the intentionally hidden
 * backing canvas before the first frame; the data attributes establish the
 * first visible frame, then this helper waits for evolved ASCII density.
 */
async function waitForSettledSmoke(
  smoke: Locator,
): Promise<"rendered" | "fallback"> {
  const canvas = smoke.locator(".wish-smoke-canvas");
  const readState = async () => {
    const [ready, visible, error, workerError, fallbackVisible] =
      await Promise.all([
        canvas.getAttribute("data-ready"),
        canvas.getAttribute("data-visible"),
        canvas.getAttribute("data-error"),
        canvas.getAttribute("data-worker-error"),
        smoke.locator(".wish-smoke-fallback").isVisible(),
      ]);
    if (ready === "true" && visible === "true") return "rendered" as const;
    if (error || workerError || fallbackVisible) return "fallback" as const;
    return "pending" as const;
  };
  await expect
    .poll(readState, {
      timeout: 60_000,
      intervals: [100, 250, 500, 1_000],
    })
    .toMatch(/^(rendered|fallback)$/);
  const state = await readState();

  if (state === "fallback") {
    await expect(smoke.locator(".wish-smoke-fallback")).toBeVisible();
    return state;
  }

  // The worker applies ASCII layers and the backing frame together.  The
  // worker's first ready frame can contain only the edge of the warmup field;
  // require a substantive evolved frame before saving a screenshot.  A real
  // fallback remains an honest success path if the worker later fails.
  const evolvedAt = Date.now();
  let lastGlyphCount = 0;
  let fallbackObserved = false;
  await expect
    .poll(
      async () => {
        const [ready, visible, error, workerError, fallbackVisible, layers] =
          await Promise.all([
            canvas.getAttribute("data-ready"),
            canvas.getAttribute("data-visible"),
            canvas.getAttribute("data-error"),
            canvas.getAttribute("data-worker-error"),
            smoke.locator(".wish-smoke-fallback").isVisible(),
            smoke.locator(".wish-smoke-ascii pre").allTextContents(),
          ]);
        if (error || workerError || fallbackVisible) {
          fallbackObserved = true;
          return "fallback";
        }
        if (ready !== "true" || visible !== "true") return "pending";
        lastGlyphCount = visibleSmokeGlyphs(layers);
        return lastGlyphCount >= MIN_EVOLVED_SMOKE_GLYPHS
          ? `rendered:${lastGlyphCount}`
          : `pending:${lastGlyphCount}`;
      },
      {
        timeout: EVOLVED_SMOKE_TIMEOUT_MS,
        intervals: [100, 250, 500, 1_000],
        message: `wait for at least ${MIN_EVOLVED_SMOKE_GLYPHS} evolved smoke glyphs`,
      },
    )
    .toMatch(/^(fallback|rendered:\d+)$/);
  if (
    fallbackObserved ||
    (await smoke.locator(".wish-smoke-fallback").isVisible())
  ) {
    await expect(smoke.locator(".wish-smoke-fallback")).toBeVisible();
    return "fallback";
  }
  console.info(
    `settled smoke: ${lastGlyphCount} visible glyphs after ${Date.now() - evolvedAt}ms`,
  );
  await expect(canvas).toHaveAttribute("data-ready", "true");
  await expect(canvas).toHaveAttribute("data-visible", "true");
  return state;
}

test("the dialogs, the lead's blocks and the terminal follow the theme, the system's by default", async ({
  page,
}) => {
  test.setTimeout(90_000);
  test.skip(process.platform === "win32", "the terminal runs a POSIX shell");
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);
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

  await page.setViewportSize({ width: 1440, height: 1300 });
  // The system's theme by default: light under a light system, dark under a dark one.
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto(process.env.DJINN_URL!);
  await choose(page, "");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.emulateMedia({ colorScheme: "dark" });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  // Every ANSI colour, normal and bright, on the terminal.
  const terminal = page.locator(".lead-terminal .xterm");
  const openTerminal = async () => {
    if (!(await terminal.count())) {
      await page
        .getByRole("button", { name: /New terminal/ })
        .last()
        .click();
    }
    const expandTerminal = page
      .locator(".lead-terminal-bar button[title='Expand the terminal']")
      .last();
    if (await expandTerminal.isVisible()) await expandTerminal.click();
    await expect(terminal).toBeVisible();
  };
  await openTerminal();
  await terminal.click();
  await page.keyboard.type(
    "for i in 0 1 2 3 4 5 6 7; do printf '\\033[3%sm col%s \\033[9%sm bright%s\\033[0m ' $i $i $i $i; done; echo",
  );
  await page.keyboard.press("Enter");
  await expect(
    page.locator(".lead-terminal .xterm-rows").getByText("bright7").last(),
  ).toBeVisible();

  const looks = {
    dark: { page: "rgb(17, 17, 17)", card: "rgb(25, 25, 25)" },
    light: { page: "rgb(246, 246, 244)", card: "rgb(255, 255, 255)" },
  };
  for (const theme of ["dark", "light"] as const) {
    await choose(page, theme);
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    await openTerminal();
    const look = looks[theme];

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
      await expect(svg.locator(".node").filter({ hasText: node })).toHaveCount(
        1,
      );
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

    // The terminal: its ground is the page's, its rows are coloured by the theme.
    await expect(
      page.locator(".lead-terminal .xterm-rows").getByText("bright7").last(),
    ).toBeVisible();
    expect(
      await background(page, ".lead-terminal .xterm-scrollable-element"),
    ).toContain(look.page);
    await page.screenshot({
      path: path.join(shots, `theme-wish-${theme}.png`),
    });
    await page.screenshot({
      path: mergeVisual(`${theme}/wish-head.png`),
      fullPage: true,
    });
    await block.screenshot({
      path: path.join(shots, `theme-blocks-${theme}.png`),
    });
    await page.locator(".lead-terminal").screenshot({
      path: path.join(shots, `theme-terminal-${theme}.png`),
    });

    // The dialogs: the settings, and a new wish.
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
    await page.getByRole("button", { name: "New wish" }).first().click();
    const creation = page.locator(".wish-creation");
    await expect(creation).toBeVisible();
    await expect(creation.locator(".wish-creation-intro h1")).toHaveCSS(
      "opacity",
      "1",
    );
    await expect(creation.locator(".wish-creation-controls")).toBeVisible();
    await expect(creation.locator(".wish-creation-controls")).toHaveCSS(
      "opacity",
      "1",
    );
    await expect(creation.locator(".wish-composer")).toHaveCSS("opacity", "1");
    await expect(creation.getByRole("textbox")).toBeEnabled();
    await expect(creation.locator(".wish-composer-input")).toHaveCSS(
      "background-color",
      "rgba(0, 0, 0, 0)",
    );
    await expect(creation.locator(".wish-composer-placeholder")).toBeVisible();
    await expect(
      creation.locator(".wish-creation-footer .button.accent"),
    ).toBeVisible();
    await expect(creation.locator(".wish-smoke")).toBeVisible();
    await waitForSettledSmoke(creation.locator(".wish-smoke"));
    await page.screenshot({
      path: path.join(shots, `theme-new-wish-${theme}.png`),
    });
    await page.screenshot({
      path: mergeVisual(`${theme}/rich-composer.png`),
      fullPage: true,
    });
    // Keep a populated-editor capture as evidence that the settled renderer
    // remains visible while the user has started a request; never submit it.
    const editor = creation.getByRole("textbox");
    await editor.fill("A small lantern that learns to glow.");
    await expect(editor).toHaveValue("A small lantern that learns to glow.");
    await page.screenshot({
      path: mergeVisual(`${theme}/smoke-settled.png`),
      fullPage: true,
    });
    await creation.locator(".wish-smoke").screenshot({
      path: mergeVisual(`${theme}/smoke-surface-settled.png`),
    });
    await page.keyboard.press("Escape");
  }
  await choose(page, "");
});

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
    path: mergeVisual("light/smoke-fallback.png"),
    fullPage: true,
  });
  await page.keyboard.press("Escape");
});

test("the light smoke canvas fallback keeps its ASCII palette readable when workers are unavailable", async ({
  page,
}) => {
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
    path: mergeVisual("light/smoke-fallback-canvas.png"),
    fullPage: true,
  });
  await page.keyboard.press("Escape");
});
