// The page keeps your place (src/scroll-anchor.ts): a question answered out of sight above the one you read (the one
// open in the questions column) goes away, and the one you read does not move on screen, not even for a frame. Twice: with Chromium's own scroll
// anchoring, and with the script that stands in for it where WebKit lacks it (the window on Linux).
// Nothing moves under the reader (AGENTS.md, Interface): a question asked while you read comes below the older ones,
// even one needed before something, a revision keeps the question's place, and the one you read stays where it is on
// screen, scrolled or at the very top of the page, while the bar of what waits for you grows above it.
import { expect, test } from "@playwright/test";
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

const at = "2026-10-07T21:10:00Z";

for (const engine of ["native", "script"] as const)
  test(`a question answered above the one you read does not move it (${engine})`, async ({
    page,
  }) => {
    // As in WebKit without scroll anchoring: the property is unknown.
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
    const questions = Array.from({ length: 20 }, (_, i) => ({
      id: randomUUID(),
      code: `Q${String(i + 1).padStart(2, "0")}`,
      wish_id: wishId,
      text: `Question number ${i + 1}?`,
      options: ["Yes — go", "No — wait"],
      create_time: at,
    }));
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-anchor-"));
    const file = path.join(dir, "anchor.json");
    fs.writeFileSync(
      file,
      JSON.stringify({
        version: 1,
        create_time: at,
        wish: {
          id: wishId,
          title: `Keep my place ${wishId.slice(0, 8)}`,
          create_time: at,
        },
        questions,
      }),
    );

    try {
      await page.goto(process.env.DJINN_URL!);
      await expect(
        page.locator(".sidebar").getByRole("button", { name: "Import a wish" }),
      ).toBeEnabled();
      const chooser = page.waitForEvent("filechooser");
      await page
        .locator(".sidebar")
        .getByRole("button", { name: "Import a wish" })
        .click();
      await (await chooser).setFiles(file);

      // The window shows the wish imported, not the flying carpet it opened on: both list questions.
      await expect(page.locator(".hero h1")).toHaveText(
        `Keep my place ${wishId.slice(0, 8)}`,
      );
      const scroller = page.locator(".mission-scroll");
      // The list's order is the interface's: the twelfth question is open and read, the first one is answered.
      const cards = scroller.locator(".question-card");
      await expect(cards).toHaveCount(20);
      await cards.nth(11).click();
      await expect(cards.nth(11)).toHaveClass(/current/);
      const readText = await cards.nth(11).locator("h3").innerText();
      const aboveText = await cards.nth(0).locator("h3").innerText();
      const read = cards.filter({ hasText: readText });
      const above = cards.filter({ hasText: aboveText });
      await expect(read).toBeVisible();
      await expect(scroller).toHaveCSS(
        "overflow-anchor",
        engine === "script" ? "none" : "auto",
      );
      // The cards are done coming in.
      await expect(read).toHaveCSS("transform", "none");

      // Read the twelfth question halfway down; the first one is out of sight above.
      await read.evaluate((el) => el.scrollIntoView({ block: "center" }));
      const viewTop = (await scroller.boundingBox())!.y;
      const box = (await above.boundingBox())!;
      expect(box.y + box.height).toBeLessThan(viewTop);
      const before = await scroller.evaluate((el) => el.scrollTop);

      // Every frame from now on, where the question read is on screen, and whether the one above is still there.
      await read.evaluate((el, aboveText) => {
        const w = window as unknown as {
          tops: number[];
          shown: boolean[];
          sampling: boolean;
        };
        const scroller = el.closest(".mission-scroll")!;
        w.tops = [];
        w.shown = [];
        w.sampling = true;
        const step = () => {
          w.tops.push(el.getBoundingClientRect().top);
          w.shown.push(
            [...scroller.querySelectorAll(".question-card h3")].some(
              (h) => (h as HTMLElement).innerText === aboveText,
            ),
          );
          if (w.sampling) requestAnimationFrame(step);
        };
        step();
      }, aboveText);

      // The first question, closed, is answered out of sight, as from another window: nothing scrolls to it.
      const code = aboveText
        .match(/^Question number (\d+)\?$/)![1]
        .padStart(2, "0");
      djinn("question", "answer", `Q${code}`, "a", "--wish-id", wishId);
      await expect(above).toHaveCount(0);
      await expect(cards).toHaveCount(19);
      await expect(read).toHaveClass(/current/);

      // The frames are counted, not timed: answered from the command line, the card can go within a few frames.
      const { tops, shown } = await page.evaluate(async () => {
        const w = window as unknown as {
          tops: number[];
          shown: boolean[];
          sampling: boolean;
        };
        while (w.shown.filter((s) => !s).length < 10)
          await new Promise((r) => requestAnimationFrame(() => r(undefined)));
        w.sampling = false;
        return { tops: w.tops, shown: w.shown };
      });
      const drift = Math.max(...tops.map((top) => Math.abs(top - tops[0])));
      // Sampled with the card above there, then for ten frames without it.
      expect(shown[0]).toBe(true);
      expect(shown.filter((s) => !s).length).toBeGreaterThanOrEqual(10);
      expect(drift).toBeLessThanOrEqual(1);
      // The scroller did move: the page above the question read changed, and the view followed it.
      expect(await scroller.evaluate((el) => el.scrollTop)).not.toBe(before);

      await page.screenshot({
        path: path.join(
          __dirname,
          `../test-results/e2e/wish-scroll-${engine}.png`,
        ),
      });
      expect(errors).toEqual([]);
    } finally {
      try {
        djinn("wish", "delete", wishId);
      } catch {}
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });

// tracks samples, every frame, where the element is on screen.
async function track(el: import("@playwright/test").Locator) {
  await el.evaluate((el) => {
    const w = window as unknown as { tops: number[]; sampling: boolean };
    w.tops = [];
    w.sampling = true;
    const step = () => {
      w.tops.push(el.getBoundingClientRect().top);
      if (w.sampling) requestAnimationFrame(step);
    };
    step();
  });
}

// drift stops the sampling ten frames from now, and says how far the element went from where it was at first.
async function drift(page: import("@playwright/test").Page) {
  const tops = await page.evaluate(async () => {
    const w = window as unknown as { tops: number[]; sampling: boolean };
    for (let i = 0; i < 10; i++)
      await new Promise((r) => requestAnimationFrame(() => r(undefined)));
    w.sampling = false;
    return w.tops;
  });
  return Math.max(...tops.map((top) => Math.abs(top - tops[0])));
}

for (const engine of ["native", "script"] as const)
  test(`a question asked while you read comes below, a revision keeps its place, nothing you read moves (${engine})`, async ({
    page,
  }) => {
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
    const minute = (m: number) =>
      new Date(Date.parse(at) + m * 60_000).toISOString();
    // The second question, the one read, is long: open, the page scrolls.
    const long = Array.from(
      { length: 40 },
      (_, i) => `- Line ${i + 1} of what is at stake.`,
    ).join("\n");
    const questions = ["Which brass?", "Which oil?", "Which glass?"].map(
      (text, i) => ({
        id: randomUUID(),
        code: `Q0${i + 1}`,
        wish_id: wishId,
        text,
        options: ["Yes — go", "No — wait"],
        // Needed before something: a line of the bar of what waits each.
        before: "before the merge",
        ...(i === 1 && { context: long }),
        create_time: minute(i),
      }),
    );
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-below-"));
    const file = path.join(dir, "below.json");
    const title = `Below ${wishId.slice(0, 8)}`;
    fs.writeFileSync(
      file,
      JSON.stringify({
        version: 1,
        create_time: at,
        wish: { id: wishId, title, create_time: at },
        questions,
      }),
    );

    try {
      await page.goto(process.env.DJINN_URL!);
      await expect(
        page.locator(".sidebar").getByRole("button", { name: "Import a wish" }),
      ).toBeEnabled();
      const chooser = page.waitForEvent("filechooser");
      await page
        .locator(".sidebar")
        .getByRole("button", { name: "Import a wish" })
        .click();
      await (await chooser).setFiles(file);
      await expect(page.locator(".hero h1")).toHaveText(title);

      const scroller = page.locator(".mission-scroll");
      const cards = scroller.locator(".question-card");
      const codes = cards.locator(".question-id");
      await expect(codes).toHaveText(["Q01", "Q02", "Q03"]);
      const read = cards.filter({ hasText: "Which oil?" });
      await read.locator(".question-row-title").click();
      await expect(read).toHaveClass(/current/);
      await expect(read).toHaveCSS("transform", "none");
      // Each question says in grey when it was asked.
      await expect(read.locator(".question-asked")).toHaveText(
        /^asked \d\d\/\d\d at \d\d:\d\d [AP]M$/,
      );
      const heading = read.locator(".question-row-title");

      // Scrolled, the question read in sight: one asked meanwhile, needed before the merge, comes last.
      await heading.evaluate((el) => el.scrollIntoView({ block: "center" }));
      expect(await scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
      await track(heading);
      djinn(
        "question",
        "ask",
        "Which wick?",
        wishId,
        "--before",
        "before the merge",
      );
      await expect(codes).toHaveText(["Q01", "Q02", "Q03", "Q04"]);
      await expect(page.locator(".attention-bar .attention-item")).toHaveCount(
        4,
      );
      expect(await drift(page)).toBeLessThanOrEqual(1);
      await expect(read).toHaveClass(/current/);

      // Revised, the first question keeps its place, and says when.
      await track(heading);
      djinn(
        "question",
        "revise",
        "Q01",
        "--wish-id",
        wishId,
        "--recommendation",
        "**A**: it shines.",
      );
      const first = cards.filter({ hasText: "Which brass?" });
      await expect(first.locator(".revised-badge")).toHaveText("Revised");
      await expect(first.locator(".question-asked")).toHaveText(
        /^asked \d\d\/\d\d at \d\d:\d\d [AP]M · revised (\d\d\/\d\d )?at \d\d:\d\d [AP]M$/,
      );
      await expect(codes).toHaveText(["Q01", "Q02", "Q03", "Q04"]);
      expect(await drift(page)).toBeLessThanOrEqual(1);

      // At the very top of the page, where scroll anchoring holds nothing: the bar grows by a line, and the page
      // scrolls by as much, so the question read stays where it is.
      await scroller.evaluate((el) => el.scrollTo(0, 0));
      await expect.poll(() => scroller.evaluate((el) => el.scrollTop)).toBe(0);
      await track(heading);
      djinn(
        "question",
        "ask",
        "Which flame?",
        wishId,
        "--before",
        "before the merge",
      );
      await expect(codes).toHaveText(["Q01", "Q02", "Q03", "Q04", "Q05"]);
      await expect(page.locator(".attention-bar .attention-item")).toHaveCount(
        5,
      );
      expect(await drift(page)).toBeLessThanOrEqual(1);
      expect(await scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
      await expect(read).toHaveClass(/current/);

      await page.screenshot({
        path: path.join(
          __dirname,
          `../test-results/e2e/wish-below-${engine}.png`,
        ),
      });
      expect(errors).toEqual([]);
    } finally {
      try {
        djinn("wish", "delete", wishId);
      } catch {}
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
