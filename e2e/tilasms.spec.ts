// The Tilasms tab, against the real djinn up --browser (see global-setup.ts): the lead puts a tilasm with the command
// line, the developer opens it in the tab, in a frame Djinn serves with a policy of its own (its scripts and files run,
// a fetch is refused, Djinn's page is out of reach), searches it, "talisman" too, and restores its first version once
// the lead put another; it goes full size beside the side panel and back. Every run makes a fresh wish, so
// --repeat-each works on the same djinn. A tilasm a question links to opens full size over the wish's first page and
// over the flying carpet, and closes back on the link. Screenshots of the tab, and of a tilasm full size in both
// themes, go to test-results/e2e/.
import { expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const shots = path.join(__dirname, "../test-results/e2e");
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

// The tilasm: a page, a script of its own that says what it could do, and an image.
const index = `<!doctype html><html><head><title>The lamp's anatomy</title></head><body>
<h1>The lamp's anatomy</h1><p>A wick carries the oil to the flame.</p><img id="img" src="img/wick.svg" alt="wick">
<p id="run">scripts do not run</p><p id="net">no answer</p><p id="djinn">no answer</p>
<script src="js/app.js"></script></body></html>`;
const app = `document.getElementById("run").textContent = "scripts run";
document.addEventListener("securitypolicyviolation", (e) => {
  if (e.violatedDirective.startsWith("connect-src"))
    document.getElementById("net").textContent = "network refused by " + e.violatedDirective;
});
fetch(new URL("/plan.v1.WishService/List", location.href)).then(
  () => (document.getElementById("net").textContent = "network reached"),
  () => {},
);
try {
  document.getElementById("djinn").textContent = "Djinn reached: " + parent.document.title;
} catch {
  document.getElementById("djinn").textContent = "Djinn out of reach";
}`;

test("a tilasm put by the command line opens in the Tilasms tab, isolated, and its search finds it", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Explain the lamp ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-tilasm-"));
  try {
    fs.mkdirSync(path.join(dir, "js"));
    fs.mkdirSync(path.join(dir, "img"));
    fs.writeFileSync(path.join(dir, "index.html"), index);
    fs.writeFileSync(path.join(dir, "js/app.js"), app);
    fs.writeFileSync(
      path.join(dir, "img/wick.svg"),
      '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><rect width="20" height="20"/></svg>',
    );
    const put = JSON.parse(
      djinn("tilasm", "put", dir, "--wish", wishId, "--json"),
    ).tilasm;
    expect(put.code).toBe("L01");

    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const tab = page.getByRole("tab", { name: /^Tilasms/ });
    await expect(tab).toHaveText("Tilasms1");
    await tab.click();
    const panel = page.getByRole("tabpanel", { name: "Tilasms" });
    const row = panel.locator(".tilasm-row").filter({ hasText: "L01" });
    await expect(row).toContainText("The lamp's anatomy");
    await expect(row).toContainText("lead");

    // Open: the frame runs the tilasm's own script and shows its image; the network and Djinn are out of reach.
    await row.getByRole("button", { name: "The lamp's anatomy" }).click();
    const frame = page.frameLocator("iframe.tilasm-frame");
    await expect(frame.locator("#run")).toHaveText("scripts run");
    await expect(frame.locator("#net")).toHaveText(
      "network refused by connect-src",
    );
    await expect(frame.locator("#djinn")).toHaveText("Djinn out of reach");
    await expect
      .poll(() =>
        frame.locator("#img").evaluate((img: HTMLImageElement) => img.width),
      )
      .toBe(20);
    await page.screenshot({ path: path.join(shots, "tilasms-dark.png") });

    // Full size: the frame covers everything right of the side panel, which stays usable; Escape (outside the frame)
    // and the small button leave it, the same tilasm open, the page and the frame scrolled where they were.
    const scroller = page.locator(".wish-view .mission-scroll");
    await frame
      .locator("body")
      .evaluate((body) => (body.style.minHeight = "4000px"));
    await frame.locator("body").evaluate(() => window.scrollTo(0, 900));
    await scroller.evaluate((el) => (el.scrollTop = 120));
    const places = async () => ({
      page: await scroller.evaluate((el) => el.scrollTop),
      frame: await frame.locator("body").evaluate(() => window.scrollY),
    });
    const before = await places();
    expect(before.page).toBe(120);
    expect(before.frame).toBe(900);
    const full = page.locator(".tilasm-view.full > iframe.tilasm-frame");
    const enter = page.getByRole("button", { name: "Full size", exact: true });
    const leave = page.getByRole("button", { name: "Leave full size" });
    const list = panel.locator(".tilasm-list");
    for (const how of ["Escape", "button"]) {
      await enter.click();
      await expect(full).toBeVisible();
      await expect(leave).toBeFocused();
      const shell = (await page.locator(".main-shell").boundingBox())!;
      const box = (await full.boundingBox())!;
      for (const side of ["x", "y", "width", "height"] as const)
        expect(Math.abs(box[side] - shell[side])).toBeLessThan(1);
      const sidebar = (await page.locator("#djinn-sidebar").boundingBox())!;
      expect(sidebar.x + sidebar.width).toBeLessThanOrEqual(box.x + 1);
      expect(
        await page.evaluate(
          ([x, y]) =>
            !!document
              .elementFromPoint(x, y)
              ?.closest("#djinn-sidebar, .app-toolbar"),
          [sidebar.x + sidebar.width / 2, sidebar.y + sidebar.height / 2],
        ),
      ).toBe(true);
      // What the frame covers is out of the keyboard's reach, and keeps its place.
      await expect(list).toHaveJSProperty("inert", true);
      expect(await places()).toEqual(before);
      if (how === "Escape") {
        for (const theme of ["dark", "light"]) {
          await page.evaluate(async (theme) => {
            document.documentElement.dataset.theme = theme;
            await Promise.all(
              document
                .getAnimations()
                .filter((a) => a instanceof CSSTransition)
                .map((a) => a.finished.catch(() => {})),
            );
          }, theme);
          await page.screenshot({
            path: path.join(shots, `tilasms-full-${theme}.png`),
          });
        }
        await page.evaluate(() => {
          document.documentElement.dataset.theme = "dark";
        });
        await page.keyboard.press("Escape");
      } else await leave.click();
      await expect(full).toHaveCount(0);
      await expect(enter).toBeFocused();
      await expect(row).toHaveClass(/\bopen\b/);
      await expect(list).toHaveJSProperty("inert", false);
      expect({ how, ...(await places()) }).toEqual({ how, ...before });
    }
    await frame.locator("body").evaluate((body) => {
      body.style.minHeight = "";
      window.scrollTo(0, 0);
    });

    // The search, on the titles and the text within the wish; the kind's own name finds them all.
    const search = panel.getByRole("searchbox");
    for (const words of ["wick flame", "ANATOMY", "talisman", "talismans"]) {
      await search.fill(words);
      await expect(panel.locator(".tilasm-row")).toHaveCount(1);
    }
    await search.fill("paraffin");
    await expect(panel.locator(".tilasm-row")).toHaveCount(0);
    await expect(panel).toContainText("No tilasm holds “paraffin”");
    await search.fill("");
    await expect(panel.locator(".tilasm-row")).toHaveCount(1);

    // A new version put by the command line replaces what the frame shows; the history restores the first one.
    fs.writeFileSync(
      path.join(dir, "index.html"),
      index.replace("A wick carries the oil to the flame.", "Second version."),
    );
    djinn("tilasm", "put", dir, "--wish", wishId, "--code", "L01");
    await expect(frame.locator("body")).toContainText("Second version.");
    await row.getByRole("button", { name: "Versions" }).click();
    const versions = row.locator(".tilasm-history li");
    await expect(versions).toHaveCount(2);
    await versions
      .nth(1)
      .getByRole("button", { name: "Restore this version" })
      .click();
    await expect(frame.locator("body")).toContainText(
      "A wick carries the oil to the flame.",
    );
    await expect(versions).toHaveCount(3);
    await expect(versions.first()).toContainText("developer");
    await expect(versions.first()).toContainText("restores v1");

    // Choosing a single .html file in the drop zone adds a new tilasm, opens it, and lists it first (newest to oldest).
    const single = path.join(dir, "sketch.html");
    fs.writeFileSync(
      single,
      "<!doctype html><html><head><title>Flame sketch</title></head><body><p>Single HTML file.</p></body></html>",
    );
    await panel
      .locator('.tilasm-drop input[type="file"]')
      .setInputFiles(single);
    await expect(panel.locator(".tilasm-row")).toHaveCount(2);
    await expect(panel.locator(".tilasm-row").first()).toContainText("L02");
    await expect(panel.locator(".tilasm-row").first()).toContainText(
      "Flame sketch",
    );
    await expect(frame.locator("body")).toContainText("Single HTML file.");

    // Another view opened from the side panel leaves full size.
    await enter.click();
    await expect(full).toBeVisible();
    await page.locator(".hourglass-nav").click();
    await expect(page.locator("iframe.tilasm-frame")).toHaveCount(0);
    await expect(page.locator(".main-shell [inert]")).toHaveCount(0);
    expect(errors).toEqual([]);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
    djinn("wish", "pause", wishId);
  }
});

test("a tilasm a question links to opens full size over the wish's page and the flying carpet, and closes back on the link", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);
  const title = `Read the lamp ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-tilasm-"));
  try {
    fs.writeFileSync(path.join(dir, "index.html"), index);
    fs.mkdirSync(path.join(dir, "js"));
    fs.mkdirSync(path.join(dir, "img"));
    fs.writeFileSync(path.join(dir, "js/app.js"), app);
    fs.writeFileSync(
      path.join(dir, "img/wick.svg"),
      '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><rect width="20" height="20"/></svg>',
    );
    const id = JSON.parse(
      djinn("tilasm", "put", dir, "--wish", wishId, "--json"),
    ).tilasm.id as string;
    const href = `djinn://tilasm/${id}`;
    // A context long enough that the link is read with the page scrolled.
    const long = Array.from(
      { length: 24 },
      (_, i) => `Paragraph ${i + 1} on the wick, the oil and the flame.`,
    ).join("\n\n");
    const question = "Which wick for the lamp?";
    djinn(
      "question",
      "ask",
      question,
      wishId,
      "--options",
      "Cotton",
      "--options",
      "Hemp",
      "--context",
      `${long}\n\nThe lamp's anatomy: ${href}`,
    );

    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(process.env.DJINN_URL!);
    const shell = page.locator(".main-shell");
    const view = shell.locator(":scope > .wish-view");
    const scroller = view.locator(".mission-scroll");
    const over = shell.locator(":scope > .tilasm-view.over");
    const frame = over.locator("iframe.tilasm-frame");
    const close = over.getByRole("button", { name: "Close the tilasm" });
    for (const where of ["wish", "carpet"]) {
      if (where === "wish")
        await page.locator(".wish-nav").filter({ hasText: title }).click();
      else {
        await page.locator(".plan-nav").click();
        await expect(page.locator(".hero h1")).toHaveText("Flying carpet");
      }
      const card = view.locator(".question-card").filter({ hasText: question });
      const link = card
        .locator(".question-context")
        .getByRole("link", { name: href });
      const hemp = card.getByRole("button", { name: /Hemp/ });
      await hemp.click();
      await expect(hemp).toHaveAttribute("aria-pressed", "true");
      // In the middle of the view: at its top, the bar of what waits for you would take the click, and Playwright
      // would scroll again to click, after the scroll is read.
      await link.evaluate((el) => el.scrollIntoView({ block: "center" }));
      // The card is marked: the same element after, never drawn again.
      await card.evaluate(
        (el, where) => el.setAttribute("data-kept", where),
        where,
      );
      const top = await scroller.evaluate((el) => el.scrollTop);
      expect(top).toBeGreaterThan(0);
      for (const how of ["Escape", "button"]) {
        await link.click();
        // Over the view, full size: everything right of the side panel; the tilasm's own page runs in it.
        await expect(frame).toBeVisible();
        await expect(
          page.frameLocator(".tilasm-view.over iframe").locator("#run"),
        ).toHaveText("scripts run");
        const box = (await frame.boundingBox())!;
        const room = (await shell.boundingBox())!;
        for (const side of ["x", "y", "width", "height"] as const)
          expect(Math.abs(box[side] - room[side])).toBeLessThan(1);
        await expect(close).toBeFocused();
        // Nothing navigated: the view stays mounted under it, inert, scrolled where it was.
        await expect(view).toHaveJSProperty("inert", true);
        await expect(card).toHaveAttribute("data-kept", where);
        expect(await scroller.evaluate((el) => el.scrollTop)).toBe(top);
        await expect(
          page.getByRole("tabpanel", { name: "Tilasms" }),
        ).toHaveCount(0);
        if (where === "carpet")
          await expect(page.locator(".hero h1")).toHaveText("Flying carpet");
        if (how === "Escape") {
          if (where === "wish")
            await page.screenshot({
              path: path.join(shots, "tilasm-over.png"),
            });
          await page.keyboard.press("Escape");
        } else await close.click();
        // Closed: back on the link, the question as it was, ready to answer; nothing moved.
        await expect(over).toHaveCount(0);
        await expect(link).toBeFocused();
        await expect(view).toHaveJSProperty("inert", false);
        await expect(card).toHaveAttribute("data-kept", where);
        await expect(hemp).toHaveAttribute("aria-pressed", "true");
        expect({
          how,
          top: await scroller.evaluate((el) => el.scrollTop),
        }).toEqual({ how, top });
      }
    }
    // Nothing answered the question.
    const [asked] = JSON.parse(
      djinn("question", "list", "--wish-id", wishId, "--json"),
    ).questions;
    expect(asked.answer).toBeUndefined();
    expect(errors).toEqual([]);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
    djinn("wish", "pause", wishId);
  }
});
