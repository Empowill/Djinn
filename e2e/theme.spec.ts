// The light theme everywhere, against the real djinn up --browser (see global-setup.ts): the system's theme by
// default, then dark and light chosen in the settings, on the dialogs, the lead's blocks (Markdown, code, Mermaid) and
// the terminal. Screenshots of each, dark and light, go to test-results/e2e/theme-*.png.
import { type Page, expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

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

test("the dialogs, the lead's blocks and the terminal follow the theme, the system's by default", async ({
  page,
}) => {
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
    const look = looks[theme];

    // The lead's blocks: Markdown, a code block, a Mermaid diagram on the card's ground.
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(page.locator(".hero h1")).toHaveText(title);
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
    await expect(page.locator(".modal")).toBeVisible();
    await page.screenshot({
      path: path.join(shots, `theme-new-wish-${theme}.png`),
    });
    await page.keyboard.press("Escape");
  }
  await choose(page, "");
});
