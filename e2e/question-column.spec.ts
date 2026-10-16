// The questions column (Decision Q08, mockup L04 v2) on the flying carpet: the open questions of two wishes on the
// page's ground, grouped by wish under their titles, the first open in full and the others two-line rows. The arrows
// go from one question to the next across the wishes and stop at both ends; a letter or a digit picks; Enter puts the
// caret in the note; Escape steps back from the note to the question, then clears the pick, then leaves the column,
// where Down comes back; Space and Page Down scroll a long question. Answered, the one below opens, else the one
// above. A wish's page shows its own questions only. The question that has the focus shows a gold ring, it alone; with
// none focused, the first arrow gives the focus to the open one and the next moves (W59). Moving to another question
// scrolls its title to the top of the view, wherever the page was, with Chromium's scroll anchoring and with the script
// that stands in for it in WebKit. Shots, wide and narrow, dark and light, go to test-results/e2e/question-column-*.png.
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

type Wish = { id: string; state: string; rank?: number };
const wishes = (): Wish[] =>
  JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];

// A long context, as a revised question's: its versions in full, a table, a code block.
const LONG = [
  "~~~",
  "# Prints the oils in stock, one a line.",
  "djinn oil list",
  "~~~",
  "",
  "**Why**: the lamp burns one oil at a time.",
  "",
  "| Oil | Hours |",
  "| --- | --- |",
  "| Olive | 9 |",
  "| Paraffin | 12 |",
  "",
  "## Previous versions",
  "",
  ...Array.from(
    { length: 30 },
    (_, i) =>
      `### Round ${i}\n\nThe lamp was asked about its oil again, and answered at length the ${i}th time.\n`,
  ),
].join("\n");

// shoot takes the page in both themes, tall: the terminal panel covers the window's lower half.
async function shoot(page: Page, name: string) {
  const size = page.viewportSize()!;
  await page.setViewportSize({ width: size.width, height: 2400 });
  for (const theme of ["dark", "light"]) {
    await page.evaluate(async (t) => {
      document.documentElement.dataset.theme = t;
      // The theme's transitions end; the dots of running work pulse for ever.
      await Promise.all(
        document
          .getAnimations()
          .filter((a) => a instanceof CSSTransition)
          .map((a) => a.finished),
      );
    }, theme);
    await page.screenshot({
      path: path.join(
        __dirname,
        `../test-results/e2e/question-column-${name}-${theme}.png`,
      ),
    });
  }
  await page.evaluate(() => {
    document.documentElement.dataset.theme = "dark";
  });
  await page.setViewportSize(size);
}

test("the questions column: grouped by wish, arrows across the groups, letters, Enter and Escape", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  // Three wishes at a time: the earlier specs' wishes make way.
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);

  const tag = randomUUID().slice(0, 8);
  const lampTitle = `Column lamp ${tag}`;
  const oilTitle = `Column oil ${tag}`;
  const lampId = JSON.parse(djinn("wish", "make", lampTitle, "--json")).wish
    .id as string;
  const oilId = JSON.parse(djinn("wish", "make", oilTitle, "--json")).wish
    .id as string;
  try {
    djinn(
      "question",
      "ask",
      "Which oil for the lamp?",
      lampId,
      "--options",
      "Olive",
      "--options",
      "Paraffin",
      "--context",
      LONG,
      "--recommendation",
      "**B**: it burns longer.",
    );
    djinn(
      "question",
      "ask",
      "Which wick?",
      lampId,
      "--options",
      "Cotton",
      "--options",
      "Hemp",
    );
    djinn(
      "question",
      "ask",
      "Where to buy the oil?",
      oilId,
      "--options",
      "Market",
      "--options",
      "Shop",
      "--options",
      "Online",
    );
    // The groups follow the wishes' rank.
    const rank = new Map(wishes().map((w) => [w.id, w.rank ?? 0]));
    const lampFirst = rank.get(lampId)! < rank.get(oilId)!;
    const titles = lampFirst ? [lampTitle, oilTitle] : [oilTitle, lampTitle];
    const asked = {
      [lampTitle]: ["Which oil for the lamp?", "Which wick?"],
      [oilTitle]: ["Where to buy the oil?"],
    };
    const order = titles.flatMap((title) => asked[title]);

    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(process.env.DJINN_URL!);
    await expect(page.locator(".hero h1")).toHaveText("Flying carpet");

    const column = page.locator(".question-column");
    const rows = column.locator(".question-row");
    await expect(rows).toHaveCount(3);
    await expect(column.locator(".question-group-head .name")).toHaveText(
      titles,
    );
    await expect(rows.locator(".question-row-title")).toHaveText(order);
    // No card around the column: it sits on the page's ground.
    await expect(column).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
    await expect(column).toHaveCSS("border-top-width", "0px");
    // The first is open in full, the others closed: no note, no gestures.
    const row = (text: string) => rows.filter({ hasText: text });
    // A click on its title opens a question, or puts the focus on the open one.
    const open = (text: string) =>
      row(text).locator(".question-row-title").click();
    const first = row(order[0]);
    await expect(first).toHaveClass(/current/);
    await expect(first.locator("textarea")).toHaveCount(1);
    await expect(first.locator(".question-actions button")).toHaveText([
      /Withdraw question…/,
      /Enlighten me/,
      /Rub the lamp/,
    ]);
    for (const text of order.slice(1)) {
      await expect(row(text)).toHaveClass(/closed/);
      await expect(row(text).locator("textarea")).toHaveCount(0);
    }
    // The long one is never cut: its last version shows, its table, its code block with Copy.
    const oil = row("Which oil for the lamp?");
    await open("Which oil for the lamp?");
    await expect(oil).toHaveClass(/current/);
    await expect(oil.getByText("answered at length the 29th time")).toHaveCount(
      1,
    );
    await expect(oil.locator("table")).toHaveCount(1);
    await expect(oil.locator(".ac-copy")).toHaveText("Copy");
    await expect(oil.locator(".read-more")).toHaveCount(0);
    // The column is about 5/9 of the two.
    const columns = await page
      .locator(".main-columns")
      .evaluate((el) =>
        [...el.children].map((c) => c.getBoundingClientRect().width),
      );
    const moves = await page
      .locator(".main-moves")
      .evaluate((el) => el.getBoundingClientRect().width);
    expect(moves / (columns[0] + columns[1])).toBeCloseTo(5 / 9, 1);
    // The shot holds the three: the short one open.
    await open("Which wick?");
    await page.locator(".mission-scroll").evaluate((el) => el.scrollTo(0, 0));
    await shoot(page, "wide");

    // Space and Page Down scroll the long question: the arrows stay with the questions.
    await oil.focus();
    const scroller = page.locator(".mission-scroll");
    const top = () => scroller.evaluate((el) => el.scrollTop);
    const before = await top();
    await page.keyboard.press("Space");
    await expect.poll(top).toBeGreaterThan(before);
    const after = await top();
    await page.keyboard.press("PageDown");
    await expect.poll(top).toBeGreaterThan(after);
    await expect(oil).toHaveClass(/current/);

    // Down and up, across the wishes, stopping at both ends; the question reached opens and takes the focus.
    await page.locator(".mission-scroll").evaluate((el) => el.scrollTo(0, 0));
    await open(order[0]);
    await expect(row(order[0])).toBeFocused();
    // The question that has the focus shows a gold ring, it alone; the hover over a closed one draws none.
    const ring = (text: string) =>
      row(text).evaluate((el) => getComputedStyle(el).outlineStyle);
    const sand = await page.evaluate(() => {
      const probe = document.createElement("span");
      probe.style.color = "var(--sand)";
      document.querySelector(".review")!.append(probe);
      const colour = getComputedStyle(probe).color;
      probe.remove();
      return colour;
    });
    await expect(row(order[0])).toHaveCSS("outline-color", sand);
    await expect(row(order[0])).toHaveCSS("outline-style", "solid");
    await row(order[1]).hover();
    expect(await ring(order[1])).toBe("none");
    expect(await ring(order[2])).toBe("none");
    for (const text of order.slice(1)) {
      await page.keyboard.press("ArrowDown");
      await expect(row(text)).toHaveClass(/current/);
      await expect(row(text)).toBeFocused();
      for (const other of order)
        expect(await ring(other)).toBe(other === text ? "solid" : "none");
    }
    await page.keyboard.press("ArrowDown");
    await expect(row(order[2])).toBeFocused();
    await page.keyboard.press("Home");
    await expect(row(order[0])).toBeFocused();
    await page.keyboard.press("ArrowUp");
    await expect(row(order[0])).toBeFocused();
    await page.keyboard.press("End");
    await expect(row(order[2])).toBeFocused();
    // The page shows the question by its title.
    await expect(row(order[2]).locator(".question-row-title")).toBeInViewport();

    // Where to buy the oil: no recommendation, the lamp waits for a pick; a letter or a digit picks.
    const buy = row("Where to buy the oil?");
    await open("Where to buy the oil?");
    await expect(buy).toBeFocused();
    const rub = buy.getByRole("button", { name: "Rub the lamp" });
    const option = (name: string) => buy.getByRole("button", { name });
    await expect(rub).toBeDisabled();
    await page.keyboard.press("c");
    await expect(option("Online")).toHaveAttribute("aria-pressed", "true");
    await page.keyboard.press("2");
    await expect(option("Shop")).toHaveAttribute("aria-pressed", "true");
    await expect(option("Online")).toHaveAttribute("aria-pressed", "false");
    await expect(rub).toBeEnabled();
    // Enter puts the caret in the note; Escape gives the focus back to the question, then clears the pick, then
    // leaves the column.
    const note = buy.getByLabel("Note with the answer");
    await page.keyboard.press("Enter");
    await expect(note).toBeFocused();
    await page.keyboard.type("Fresh.");
    await page.keyboard.press("Escape");
    await expect(buy).toBeFocused();
    await expect(note).toHaveValue("Fresh.");
    await page.keyboard.press("Escape");
    await expect(option("Shop")).toHaveAttribute("aria-pressed", "false");
    await expect(rub).toBeDisabled();
    await page.keyboard.press("Escape");
    await expect(rows.and(page.locator(".current"))).toHaveCount(0);
    expect(
      await page.evaluate(() => document.activeElement === document.body),
    ).toBe(true);
    // Down comes back to the first.
    await page.keyboard.press("ArrowDown");
    await expect(row(order[0])).toBeFocused();
    await expect(row(order[0])).toHaveClass(/current/);

    // Answered from the column, by the keys: the note sends it, the one above opens and takes the focus.
    await open("Where to buy the oil?");
    await page.keyboard.press("a");
    await page.keyboard.press("Enter");
    await expect(note).toBeFocused();
    await expect(note).toHaveValue("Fresh.");
    await page.keyboard.press("Enter");
    await expect(buy).toHaveCount(0);
    const [answered] = JSON.parse(
      djinn("question", "list", "--wish-id", oilId, "--json"),
    ).questions;
    expect(answered.answer.choice).toBe("CHOICE_A");
    expect(answered.answer.note).toBe("Fresh.");
    // The one below it opens, else the one above.
    const at = order.indexOf("Where to buy the oil?");
    const left = order.filter((text) => text !== "Where to buy the oil?");
    const heir = left[at] ?? left[at - 1];
    await expect(row(heir)).toHaveClass(/current/);
    await expect(row(heir)).toBeFocused();

    // The glance on the right (Decision Q08): each wish under its title, nothing running here. It stays in sight
    // while the long question scrolls by.
    const glance = page.locator(".glance");
    await expect(glance.locator(".glance-head button")).toHaveText(titles);
    await expect(glance.locator(".glance-quiet")).toHaveText([
      "Nothing runs, nothing planned.",
      "Nothing runs, nothing planned.",
    ]);
    await expect(glance.locator(".glance-narrow")).toBeHidden();
    await open("Which oil for the lamp?");
    await scroller.evaluate((el) => el.scrollTo(0, 0));
    const startTop = (await glance.boundingBox())!.y;
    await scroller.evaluate((el) => el.scrollBy(0, 900));
    await expect
      .poll(async () => (await glance.boundingBox())!.y)
      .toBeLessThan(startTop);
    const stuck = await page.evaluate(() => {
      const top = document
        .querySelector(".mission-scroll")!
        .getBoundingClientRect().top;
      return (
        document.querySelector(".glance")!.getBoundingClientRect().top - top
      );
    });
    expect(stuck).toBeGreaterThanOrEqual(0);
    expect(stuck).toBeLessThan(200);

    // Narrow: one column, the glance first, one line per wish, then the questions.
    await page.setViewportSize({ width: 720, height: 900 });
    await expect
      .poll(() =>
        page
          .locator(".main-columns")
          .evaluate((el) => getComputedStyle(el).gridTemplateColumns),
      )
      .not.toContain(" ");
    await expect(glance.locator(".glance-wide")).toBeHidden();
    await expect(glance.locator(".glance-line .name")).toHaveText(titles);
    await expect(glance.locator(".glance-line-marks")).toHaveText([
      "nothing",
      "nothing",
    ]);
    expect((await glance.boundingBox())!.y).toBeLessThan(
      (await column.boundingBox())!.y,
    );
    await open("Which wick?");
    await page.locator(".mission-scroll").evaluate((el) => el.scrollTo(0, 0));
    await shoot(page, "narrow");

    // A wish's page shows its own questions only, with no heading.
    await page.setViewportSize({ width: 1440, height: 900 });
    await page
      .locator(".glance-head button")
      .filter({ hasText: lampTitle })
      .click();
    await expect(page.locator(".hero h1")).toHaveText(lampTitle);
    await expect(rows.locator(".question-row-title")).toHaveText(
      asked[lampTitle],
    );
    await expect(column.locator(".question-group-head")).toHaveCount(0);
    await expect(rows.first()).toHaveClass(/current/);
    // No question has the focus: the first Down gives it to the open one, the second moves to the next.
    expect(
      await page.evaluate(
        () => !document.activeElement?.closest(".question-row"),
      ),
    ).toBe(true);
    await page.keyboard.press("ArrowDown");
    await expect(rows.first()).toBeFocused();
    await expect(rows.first()).toHaveCSS("outline-style", "solid");
    await expect(row("Which wick?")).toHaveClass(/closed/);
    await page.keyboard.press("ArrowDown");
    await expect(row("Which wick?")).toBeFocused();
    await expect(row("Which wick?")).toHaveClass(/current/);
    // From a button of the page, not a field, Up gives the focus back to the open question first.
    await page.locator(".attention-bar button").first().focus();
    await page.keyboard.press("ArrowUp");
    await expect(row("Which wick?")).toBeFocused();
    await page.keyboard.press("ArrowUp");
    await expect(rows.first()).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(row("Which wick?")).toBeFocused();
    await page.locator(".mission-scroll").evaluate((el) => el.scrollTo(0, 0));
    await shoot(page, "wish");

    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "delete", lampId);
    djinn("wish", "delete", oilId);
  }
});

// Three questions, the two last long, asked in this order: moving from one to another, by an arrow or a click, scrolls
// the new one's title to the top of the view, below the bar of what waits for you, wherever the page was scrolled: in
// the middle of the long one open, or at its end. Twice: with Chromium's own scroll anchoring, and with the script
// that stands in for it where WebKit lacks it (the window on Linux), which must not bring the page back.
for (const engine of ["native", "script"] as const)
  test(`moving to another question shows its title at the top (${engine})`, async ({
    page,
  }) => {
    test.setTimeout(60_000);
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
    const wishId = randomUUID();
    const title = `Title on top ${engine} ${wishId.slice(0, 8)}`;
    const texts = ["Which lamp?", "Which oil?", "Which wick?"];
    const questions = texts.map((text, i) => ({
      id: randomUUID(),
      code: `Q0${i + 1}`,
      wish_id: wishId,
      text,
      options: ["Yes", "No"],
      context: i ? LONG : "",
      create_time: `2026-10-16T10:0${i}:00Z`,
    }));
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-title-"));
    const file = path.join(dir, "title.json");
    fs.writeFileSync(
      file,
      JSON.stringify({
        version: 1,
        create_time: "2026-10-16T10:00:00Z",
        wish: { id: wishId, title, create_time: "2026-10-16T10:00:00Z" },
        questions,
      }),
    );
    try {
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.goto(process.env.DJINN_URL!);
      const importer = page
        .locator(".sidebar")
        .getByRole("button", { name: "Import a wish" });
      await expect(importer).toBeEnabled();
      const chooser = page.waitForEvent("filechooser");
      await importer.click();
      await (await chooser).setFiles(file);
      await expect(page.locator(".hero h1")).toHaveText(title);
      const scroller = page.locator(".mission-scroll");
      await expect(scroller).toHaveCSS(
        "overflow-anchor",
        engine === "script" ? "none" : "auto",
      );
      const rows = page.locator(".question-column .question-row");
      await expect(rows).toHaveCount(3);
      const row = (text: string) => rows.filter({ hasText: text });
      // Where the title stands below the top of the view, under the bar of what waits for you: read two frames
      // after the move, then again later, so a scroll that brings the page back is caught.
      const offset = (text: string) =>
        row(text).evaluate(async (el) => {
          const frame = () => new Promise((r) => requestAnimationFrame(r));
          await frame();
          await frame();
          const scroll = el.closest(".mission-scroll")!;
          const bar = scroll.querySelector(":scope > .attention-bar");
          const top = bar
            ? bar.getBoundingClientRect().bottom
            : scroll.getBoundingClientRect().top;
          return (
            el.querySelector(".question-row-title")!.getBoundingClientRect()
              .top - top
          );
        });
      const onTop = async (text: string) => {
        await expect(row(text)).toHaveClass(/current/);
        await expect(row(text)).toBeFocused();
        for (let i = 0; i < 2; i++) {
          const at = await offset(text);
          expect(at, `${text}'s title below the top`).toBeGreaterThanOrEqual(0);
          expect(at, `${text}'s title at the top`).toBeLessThan(40);
          await page.waitForTimeout(300);
        }
      };
      const scrollBy = (y: number) =>
        scroller.evaluate((el, y) => el.scrollBy(0, y), y);

      // A click on a closed one: the long Which oil? opens, from its title.
      await row("Which oil?").locator(".question-row-title").click();
      await onTop("Which oil?");
      // Read into its middle, then Down: Which wick? reads from its title, not from where the page was.
      await scrollBy(700);
      await page.keyboard.press("ArrowDown");
      await onTop("Which wick?");
      // At the end of Which wick?, Up: Which oil? again from its title.
      await scrollBy(1500);
      await page.keyboard.press("ArrowUp");
      await onTop("Which oil?");
      // In its middle, a click on the closed Which wick? below.
      await scrollBy(900);
      await row("Which wick?").locator(".question-row-title").click();
      await onTop("Which wick?");
      expect(errors).toEqual([]);
    } finally {
      djinn("wish", "delete", wishId);
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
