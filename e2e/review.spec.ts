// Review and decide at a glance, against the real djinn up --browser (see global-setup.ts): the bar of what waits,
// "Enlighten me" read by the lead in the brief, a revision from the command line, "Rub the lamp" read back from the
// command line, a block for agents only in its discreet tab, folded, with no mark. Screenshots of the flying carpet and of the wish, dark and
// light, go to test-results/e2e/.
import { type Page, expect, test } from "./test";
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

const at = "2026-10-08T09:10:00Z";
// A wish whose tasks went every way: done, failed, planned, cut short.
const lampOf = (wishId: string, title: string) => ({
  version: 1,
  create_time: at,
  wish: { id: wishId, title, create_time: at },
  tasks: [
    ["W1", "Trim the wick", "TASK_STATUS_DONE"],
    ["W2", "Polish the brass", "TASK_STATUS_FAILED"],
    ["W3", "Fill the oil", "TASK_STATUS_INTERRUPTED"],
    ["W4", "Light it", "TASK_STATUS_PENDING"],
  ].map(([code, title, status]) => ({
    id: randomUUID(),
    wish_id: wishId,
    code,
    title,
    status,
    provider: "PROVIDER_CLAUDE",
    create_time: at,
    ...(status === "TASK_STATUS_FAILED"
      ? { error: "the brass was lacquered" }
      : {}),
  })),
});

const SHOTS = Boolean(process.env.REVIEW_SHOTS);

async function shoot(page: Page, name: string) {
  if (!SHOTS) return;
  // The cards fade in: the shot waits for them.
  for (const card of await page.locator(".question-card").all())
    await expect(card).toHaveCSS("opacity", "1");
  await page.screenshot({ path: path.join(shots, `${name}.png`) });
}

async function theme(page: Page, value: "" | "light") {
  await page.evaluate((v) => {
    if (v) localStorage.setItem("djinn.theme", v);
    else localStorage.removeItem("djinn.theme");
  }, value);
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme",
    value || "dark",
  );
}

function setupReviewWish() {
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);
  const tag = randomUUID().slice(0, 8);
  const wishId = randomUUID();
  const title = `Light the lamp ${tag}`;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-review-"));
  const file = path.join(dir, "lamp.json");
  fs.writeFileSync(file, JSON.stringify(lampOf(wishId, title)));
  djinn("wish", "import", file);
  djinn(
    "question",
    "ask",
    "Which oil for the wick?",
    wishId,
    "--options",
    "Olive — the classic",
    "--options",
    "Paraffin — brighter",
    "--recommendation",
    "A: it smells good, and the wick lasts.",
    "--context",
    "## What it changes\n\nThe oil sets how long the lamp burns.\n\n- Olive: softer light.\n- Paraffin: brighter, smells.",
    "--before",
    "before the first light",
  );
  djinn(
    "question",
    "ask",
    "May the worker polish the brass again?",
    wishId,
    "--recommendation",
    "Yes: the lacquer is off now.",
  );
  djinn(
    "block",
    "put",
    wishId,
    "--kind",
    "section",
    "--title",
    "Ship on Friday",
    "--content",
    "We ship **on Friday**, unless it rains.\n\n| Day | Weather |\n| --- | --- |\n| Fri | Sun |",
  );
  return {
    wishId,
    title,
    cleanup() {
      djinn("wish", "delete", wishId);
      fs.rmSync(dir, { recursive: true, force: true });
    },
  };
}

test("the attention bar names what holds up the wish on the flying carpet and on the wish's page, dark and light", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);
  // W501: a wish shown by a link, then deleted, as operating-load.spec.ts leaves it: the show of the last minute that
  // each reload below gets must not take the window from this spec's wish to the flying carpet.
  const shown = JSON.parse(
    djinn("wish", "make", "Shown then gone", "--paused", "--json"),
  ).wish.id as string;
  djinn("open", `djinn://wish/${shown}`);
  djinn("wish", "delete", shown);

  const { title, cleanup } = setupReviewWish();
  try {
    // Tall enough for a screenshot to hold most of the page: it scrolls inside the window.
    await page.setViewportSize({ width: 1440, height: 2200 });
    await page.goto(process.env.DJINN_URL!);
    await expect(page.locator(".hero h1")).toHaveText("Flying carpet");
    // The bar of what waits: one line per question that holds up the wish, a click goes there; the one needed before
    // the first light under those words. The other question can wait: the questions column alone shows it. W2, failed,
    // is the lead's, never the developer's (W96, Q273); W3, cut short and not resumed by Djinn, asks nothing: it is
    // history.
    const bar = page.locator(".attention-bar");
    await expect(bar).toContainText("1 thing waits for you");
    await expect(bar.locator(".attention-level")).toHaveText([
      "before the first light",
    ]);
    await expect(bar).not.toContainText("W2");
    await expect(bar).not.toContainText("W3");
    await expect(
      page
        .locator(".question-group-head")
        .filter({ hasText: title })
        .locator(".question-group-count"),
    ).toHaveText("2");
    if (SHOTS) {
      await shoot(page, "review-flight-plan-dark");
      await theme(page, "light");
      await shoot(page, "review-flight-plan-light");
      await theme(page, "");
    }

    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(page.locator(".hero h1")).toHaveText(title);
    const card = page
      .locator(".question-card")
      .filter({ hasText: "Which oil for the wick?" });
    await bar
      .locator(".attention-item")
      .filter({ hasText: "Which oil for the wick?" })
      .click();
    await expect(card).toBeFocused();
    await expect(card.locator(".question-context h2")).toHaveText(
      "What it changes",
    );
    await shoot(page, "review-wish-dark");
    if (SHOTS) {
      await theme(page, "light");
    }
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    await page
      .locator(".wish-task")
      .filter({ hasText: "Polish the brass" })
      .locator(".wish-task-heading")
      .click();
    await shoot(page, "review-wish-light");
    // From another tab, a line of the bar brings its question back into sight, in the wish's first tab.
    await bar
      .locator(".attention-item")
      .filter({ hasText: "Which oil for the wick?" })
      .click();
    await expect(page.getByRole("tab", { name: /^Wish/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(card).toBeFocused();
    if (SHOTS) {
      await theme(page, "");
    }
  } finally {
    cleanup();
  }
  expect(errors).toEqual([]);
});

test("a question is enlightened, revised by its investigator and on the flying carpet, then rubbed in one click; a block waits in the agents' tab", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const { wishId, title, cleanup } = setupReviewWish();
  try {
    await page.setViewportSize({ width: 1440, height: 2200 });
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(page.locator(".hero h1")).toHaveText(title);
    const bar = page.locator(".attention-bar");
    const card = page
      .locator(".question-card")
      .filter({ hasText: "Which oil for the wick?" });

    // Enlighten me: one click sends what the answer's note says to the lead, no second field; the question waits for
    // the lead, not for you, folded to one line out of the way, and opens on a click.
    await card
      .getByLabel("Note with the answer")
      .fill("How long does each burn?");
    await card.getByRole("button", { name: "Enlighten me" }).click();
    const digging = page
      .locator(".investigating-section .question-card")
      .filter({ hasText: "Which oil for the wick?" });
    await expect(digging).toHaveClass(/folded/);
    await expect(digging.locator(".question-fold")).toContainText(
      "Being investigated",
    );
    await expect(digging.locator(".question-fold-note")).toHaveText(
      "You asked: How long does each burn?",
    );
    await expect(digging.locator("textarea, .option")).toHaveCount(0);
    // Out of the bar too: the question that can wait never was in it, so nothing waits there.
    await expect(bar.locator(".attention-item")).toHaveCount(0);
    if (SHOTS) {
      await digging.screenshot({
        path: path.join(shots, "review-question-folded.png"),
      });
    }
    await digging.locator(".question-fold").click();
    await expect(digging).not.toHaveClass(/folded/);
    await expect(digging.locator(".investigating-note")).toHaveText(
      "You asked to find out more: How long does each burn?",
    );
    await expect(digging.locator("textarea")).toHaveCount(1);
    await expect(digging.getByLabel("Note with the answer")).toHaveValue("");
    const brief = djinn("wish", "brief", wishId);
    expect(brief).toContain("## To investigate");
    expect(brief).toContain("How long does each burn?");

    const tasks = JSON.parse(
      djinn("task", "list", "--wish-id", wishId, "--json"),
    ).tasks as Array<{ id: string; code: string; role: string }>;
    const investigator = tasks.find((t) => t.role === "TASK_ROLE_INVESTIGATOR");
    expect(investigator).toBeDefined();

    // The investigator revises from the command line: the question waits for you again, revised, its earlier version apart.
    djinn(
      "question",
      "revise",
      "Q01",
      "--wish-id",
      wishId,
      "--task-id",
      investigator!.id,
      "--context",
      "## What we found\n\nOlive burns 6 hours, paraffin 9.",
      "--options",
      "Olive — the classic",
      "--options",
      "Paraffin — brighter",
      "--options",
      "Beeswax — cleanest",
      "--recommendation",
      "B: it lasts half as long again.",
    );
    await expect(page.locator(".investigating-section")).toHaveCount(0);
    const revised = page
      .locator(".decisions-section .question-card")
      .filter({ hasText: "Which oil for the wick?" });
    // Revised, it waits for an answer again, back in its place: the question opened meanwhile stays open, nothing
    // moves under the reader (AGENTS.md, Interface); a click opens it.
    await expect(revised.locator(".revised-badge")).toHaveText("Revised");
    await expect(revised).not.toHaveClass(/folded/);
    await expect(revised).toHaveClass(/closed/);
    await expect(
      page
        .locator(".decisions-section .question-card")
        .filter({ hasText: "May the worker polish the brass again?" }),
    ).toHaveClass(/current/);
    await revised.locator(".question-row-title").click();
    await expect(revised).toHaveClass(/current/);
    // The new context only; the version before it takes no room: Versions (2), in the card's corner, opens it below
    // the question, to read, and closes it again (W114).
    await expect(revised.locator(".question-context > .prose h2")).toHaveText(
      "What we found",
    );
    await expect(revised).not.toContainText("Historique");
    const versions = revised.locator(".question-corner .question-versions");
    await expect(versions).toHaveText("Versions (2)");
    await expect(versions).toHaveAttribute("aria-expanded", "false");
    const earlier = revised.locator(".question-versions-list");
    await expect(earlier).toHaveCount(0);
    await versions.click();
    await expect(versions).toHaveAttribute("aria-expanded", "true");
    await expect(earlier.locator("li").first()).toContainText("Version 1");
    await expect(earlier.locator("h2")).toHaveText("What it changes");
    await expect(earlier).toContainText(
      "Recommended: A: it smells good, and the wick lasts.",
    );
    await earlier.scrollIntoViewIfNeeded();
    await shoot(page, "review-wish-versions-dark");
    await versions.click();
    await expect(earlier).toHaveCount(0);
    await expect(revised.locator(".option-recommended")).toHaveCount(1);
    await expect(
      revised.locator(".option", { has: page.locator(".option-recommended") }),
    ).toContainText("Paraffin");
    await expect(
      revised.getByRole("button", { name: /Beeswax/ }),
    ).toBeVisible();
    // The new recommendation is the option chosen until you pick another.
    await expect(
      revised.getByRole("button", { name: /Paraffin/ }),
    ).toHaveAttribute("aria-pressed", "true");
    await revised.scrollIntoViewIfNeeded();
    await shoot(page, "review-wish-revised-dark");

    // On the Flying carpet too, a question sent back under investigation and revised returns to the open column without a reload.
    await page.keyboard.press("Control+1");
    await expect(page.locator(".hero h1")).toHaveText("Flying carpet");
    djinn(
      "question",
      "enlighten",
      "Q01",
      "--wish-id",
      wishId,
      "--note",
      "And the price?",
    );
    await expect(
      page
        .locator(".investigating-section .question-card")
        .filter({ hasText: "Which oil for the wick?" }),
    ).toBeVisible();
    djinn(
      "question",
      "revise",
      "Q01",
      "--wish-id",
      wishId,
      "--context",
      "## The price\n\nParaffin costs three dinars.",
      "--options",
      "Olive — the classic",
      "--options",
      "Paraffin — brighter",
      "--options",
      "Beeswax — cleanest",
      "--recommendation",
      "B: it lasts half as long again.",
    );
    await expect(page.locator(".investigating-section")).toHaveCount(0);
    const onCarpet = page
      .locator(".decisions-section .question-card")
      .filter({ hasText: "Which oil for the wick?" });
    await expect(onCarpet).toContainText("Revised ×2");
    await onCarpet.locator(".question-row-title").click();
    await expect(onCarpet).toContainText("Versions (3)");
    await expect(onCarpet).toContainText("The price");

    // Rub the lamp: one click answers with the option selected, here the recommended one, and the lead reads it from
    // the command line.
    await onCarpet.getByRole("button", { name: "Rub the lamp" }).click();
    await expect(onCarpet).toHaveCount(0);
    const [q01] = JSON.parse(
      djinn("question", "list", "--wish-id", wishId, "--json"),
    ).questions;
    expect(q01.answer.choice).toBe("CHOICE_B");
    expect(q01.revision).toBe(2);

    // A block is for agents: not on the wish's tab, but in the last one, folded, with no mark; opened, its Markdown.
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(page.locator(".wish-block")).toHaveCount(0);
    const agents = page.getByRole("tab", { name: /^For agents/ });
    await expect(agents).toHaveText("For agents1");
    await agents.click();
    const block = page
      .locator(".wish-block")
      .filter({ hasText: "Ship on Friday" });
    await expect(block.locator(".fold-heading")).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    await expect(block.locator("table")).toHaveCount(0);
    await block.locator(".fold-heading").click();
    await expect(block.locator("strong")).toHaveText("on Friday");
    await expect(block.locator("table td").first()).toHaveText("Fri");
    await expect(block.getByRole("button", { name: /Approve/ })).toHaveCount(0);
  } finally {
    cleanup();
  }
  expect(errors).toEqual([]);
});
