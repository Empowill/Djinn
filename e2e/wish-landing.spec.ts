// A click on a wish in the side panel lands on its first tab, the questions (W174): even on the wish shown, from a
// tilasm a question opened full size over its page, from the Tilasms tab's tilasm full size, from each other tab, the
// side panel open or folded to its rail. The page lands at its top, or at the question that had the focus, its title
// at the top of the view and the focus on it again. A click on another wish opens it on its questions too. Twice:
// with Chromium's own scroll anchoring, and with the script that stands in for it where WebKit lacks it (the window on
// Linux), which must not bring the page back.
import { type Page, expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { portable } from "./portable";

portable();

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

// A context long enough that the page scrolls.
const LONG = Array.from(
  { length: 30 },
  (_, i) => `Paragraph ${i + 1} on the wick, the oil and the flame.`,
).join("\n\n");

// Where a question's title stands below the top of the view, under the bar of what waits for you: read two frames
// after the click, then again later, so a scroll that brings the page back is caught.
async function titleOnTop(page: Page, text: string) {
  const row = page
    .locator(".question-column .question-row")
    .filter({ hasText: text });
  await expect(row).toHaveClass(/current/);
  await expect(row).toBeFocused();
  for (let i = 0; i < 2; i++) {
    const at = await row.evaluate(async (el) => {
      const frame = () => new Promise((r) => requestAnimationFrame(r));
      await frame();
      await frame();
      const scroll = el.closest(".mission-scroll")!;
      const bar = scroll.querySelector(":scope > .attention-bar");
      const top = bar
        ? bar.getBoundingClientRect().bottom
        : scroll.getBoundingClientRect().top;
      return (
        el.querySelector(".question-row-title")!.getBoundingClientRect().top -
        top
      );
    });
    expect(at, `${text}'s title below the top`).toBeGreaterThanOrEqual(0);
    expect(at, `${text}'s title at the top`).toBeLessThan(40);
    await page.waitForTimeout(300);
  }
}

for (const engine of ["native", "script"] as const)
  test(`a click on a wish in the side panel brings back its questions (${engine})`, async ({
    page,
  }) => {
    test.setTimeout(90_000);
    if (engine === "script")
      await page.addInitScript(() => {
        const supports = CSS.supports.bind(CSS);
        CSS.supports = ((...args: [string, string?]) =>
          args[0] === "overflow-anchor"
            ? false
            : supports(...(args as [string, string]))) as typeof CSS.supports;
      });
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));
    // Three wishes at a time: the earlier specs' wishes make way.
    const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
    for (const w of listed.filter(
      (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
    ))
      djinn("wish", "pause", w.id);
    const tag = `${engine} ${randomUUID().slice(0, 8)}`;
    const title = `Land on the lamp ${tag}`;
    const other = `Land on the oil ${tag}`;
    const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
      .id as string;
    const otherId = JSON.parse(djinn("wish", "make", other, "--json")).wish
      .id as string;
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-landing-"));
    try {
      fs.writeFileSync(
        path.join(dir, "index.html"),
        "<!doctype html><html><head><title>The lamp's anatomy</title></head><body><h1>The lamp's anatomy</h1></body></html>",
      );
      const id = JSON.parse(
        djinn("tilasm", "put", dir, "--wish", wishId, "--json"),
      ).tilasm.id as string;
      const href = `djinn://tilasm/${id}`;
      const wick = "Which wick for the lamp?";
      const oil = "Which oil for the lamp?";
      for (const [text, wish, context] of [
        [wick, wishId, `${LONG}\n\nThe lamp's anatomy: ${href}`],
        [oil, wishId, LONG],
        ["Which jar for the oil?", otherId, LONG],
      ])
        djinn(
          "question",
          "ask",
          text,
          wish,
          "--options",
          "Cotton",
          "--options",
          "Hemp",
          "--context",
          context,
        );

      await page.setViewportSize({ width: 1280, height: 900 });
      await page.goto(process.env.DJINN_URL!);
      const shell = page.locator(".main-shell");
      const view = shell.locator(":scope > .wish-view");
      const scroller = view.locator(".mission-scroll");
      const over = shell.locator(":scope > .tilasm-view.over");
      const nav = page.locator(`.wish-nav[title*="${title}"]`);
      const main = page.locator("#view-tab-main");
      const column = view.locator(".question-column");
      const rows = column.locator(".question-row");
      const scrollTop = () => scroller.evaluate((el) => el.scrollTop);
      // Landed: the first tab, its questions, nothing over it, the page at its top and kept there.
      const landed = async () => {
        await expect(main).toHaveAttribute("aria-selected", "true");
        await expect(rows).toHaveCount(2);
        await expect(rows.first()).toContainText(wick);
        await expect(over).toHaveCount(0);
        await expect(page.locator(".tilasm-view.full")).toHaveCount(0);
        await expect(view).toHaveJSProperty("inert", false);
        await expect.poll(scrollTop).toBe(0);
        await page.waitForTimeout(300);
        expect(await scrollTop()).toBe(0);
        await expect(column).toBeInViewport();
      };
      const scrollDown = () =>
        scroller.evaluate((el) => el.scrollBy(0, el.scrollHeight));

      await nav.click();
      await expect(page.locator(".hero h1")).toHaveText(title);
      await landed();

      // On its questions already, read down the page, nothing focused: back to the top.
      await scrollDown();
      expect(await scrollTop()).toBeGreaterThan(0);
      await nav.click();
      await landed();

      // A tilasm the open question links to, full size over the page: the click closes it, and the page comes back
      // to the question that had the focus (the link's), its title at the top, the focus on it again.
      const link = rows
        .filter({ hasText: wick })
        .locator(".question-context")
        .getByRole("link", { name: href });
      // In the middle of the view: at its top, the bar of what waits for you would take the click.
      await link.evaluate((el) => el.scrollIntoView({ block: "center" }));
      await link.click();
      await expect(over.locator("iframe.tilasm-frame")).toBeVisible();
      await expect(view).toHaveJSProperty("inert", true);
      await scroller.evaluate((el) => el.scrollBy(0, 900));
      await nav.click();
      await expect(over).toHaveCount(0);
      await expect(view).toHaveJSProperty("inert", false);
      await expect(main).toHaveAttribute("aria-selected", "true");
      await titleOnTop(page, wick);

      // The Tilasms tab, its tilasm full size: the click closes it and lands on the questions; the tab shows its
      // list again afterwards, the tilasm not reopened.
      await page.locator("#view-tab-tilasms").click();
      const panel = page.getByRole("tabpanel", { name: "Tilasms" });
      await panel
        .locator(".tilasm-row")
        .filter({ hasText: "L01" })
        .getByRole("button", { name: "The lamp's anatomy" })
        .click();
      await page
        .getByRole("button", { name: "Full size", exact: true })
        .click();
      await expect(
        page.locator(".tilasm-view.full > iframe.tilasm-frame"),
      ).toBeVisible();
      await nav.click();
      await landed();
      await expect(panel).toHaveCount(0);
      await page.locator("#view-tab-tilasms").click();
      await expect(panel.locator(".tilasm-row")).toHaveCount(1);
      await expect(page.locator("iframe.tilasm-frame")).toHaveCount(0);

      // Each other tab, read down: the click lands on the questions, at the top.
      for (const tab of ["tasks", "review", "decisions", "agents"]) {
        await page.locator(`#view-tab-${tab}`).click();
        await expect(page.locator(`#view-tab-${tab}`)).toHaveAttribute(
          "aria-selected",
          "true",
        );
        await scrollDown();
        await nav.click();
        await landed();
      }

      // Folded to the rail of icons: the same.
      await page.locator(".sidebar-toggle").click();
      await expect(page.locator(".wish-app.sidebar-collapsed")).toHaveCount(1);
      try {
        await page.locator("#view-tab-decisions").click();
        await nav.click();
        await landed();
        await page.locator("#view-tab-tasks").click();
        await nav.click();
        await landed();
      } finally {
        await page.locator(".sidebar-toggle").click();
        await expect(page.locator(".wish-app.sidebar-collapsed")).toHaveCount(
          0,
        );
      }

      // Another wish, from a tab of this one: it opens on its questions, and back here too.
      await page.locator("#view-tab-tasks").click();
      await page.locator(`.wish-nav[title*="${other}"]`).click();
      await expect(page.locator(".hero h1")).toHaveText(other);
      await expect(main).toHaveAttribute("aria-selected", "true");
      await expect(rows).toHaveCount(1);
      await page.locator("#view-tab-decisions").click();
      await nav.click();
      await expect(page.locator(".hero h1")).toHaveText(title);
      await landed();
      expect(errors).toEqual([]);
    } finally {
      djinn("wish", "delete", wishId);
      djinn("wish", "delete", otherId);
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
