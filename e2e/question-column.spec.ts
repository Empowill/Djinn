// The questions column (Decision Q08, mockup L04 v2) on the flying carpet: the open questions of two wishes on the
// page's ground, grouped by wish under their titles, the first open in full and the others two-line rows. The arrows
// go from one question to the next across the wishes and stop at both ends; a letter or a digit picks; Enter puts the
// caret in the note; Escape steps back from the note to the question, then clears the pick, then leaves the column,
// where Down comes back; Space and Page Down scroll a long question. Answered, the one below opens, else the one
// above. A wish's page shows its own questions only. Shots, wide and narrow, dark and light, go to
// test-results/e2e/question-column-*.png.
import { type Page, expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
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
    for (const text of order.slice(1)) {
      await page.keyboard.press("ArrowDown");
      await expect(row(text)).toHaveClass(/current/);
      await expect(row(text)).toBeFocused();
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

    // Narrow: one column, the wishes first as today, then the questions.
    await page.setViewportSize({ width: 720, height: 900 });
    await expect
      .poll(() =>
        page
          .locator(".main-columns")
          .evaluate((el) => getComputedStyle(el).gridTemplateColumns),
      )
      .not.toContain(" ");
    await open("Which wick?");
    await page.locator(".mission-scroll").evaluate((el) => el.scrollTo(0, 0));
    await shoot(page, "narrow");

    // A wish's page shows its own questions only, with no heading.
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.locator(".plan-wish").filter({ hasText: lampTitle }).click();
    await expect(page.locator(".hero h1")).toHaveText(lampTitle);
    await expect(rows.locator(".question-row-title")).toHaveText(
      asked[lampTitle],
    );
    await expect(column.locator(".question-group-head")).toHaveCount(0);
    await expect(rows.first()).toHaveClass(/current/);
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
