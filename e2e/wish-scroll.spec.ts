// The page keeps your place (src/scroll-anchor.ts): a question answered out of sight above the one you read goes
// away, and the one you read does not move on screen, not even for a frame. Twice: with Chromium's own scroll
// anchoring, and with the script that stands in for it where WebKit lacks it (the window on Linux).
import { expect, test } from "@playwright/test";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

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

    const scroller = page.locator(".mission-scroll");
    // The list's order is the interface's: the seventh card is read, the first one is answered.
    const cards = scroller.locator(".question-card");
    await expect(cards).toHaveCount(20);
    const readText = await cards.nth(6).locator("h3").innerText();
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

    // Read the seventh question halfway down; the first one is out of sight above.
    await read.evaluate((el) => el.scrollIntoView({ block: "center" }));
    const viewTop = (await scroller.boundingBox())!.y;
    const box = (await above.boundingBox())!;
    expect(box.y + box.height).toBeLessThan(viewTop);
    const before = await scroller.evaluate((el) => el.scrollTop);

    // Every frame from now on, where the question read is on screen.
    await read.evaluate((el) => {
      const w = window as unknown as { tops: number[]; sampling: boolean };
      w.tops = [];
      w.sampling = true;
      const step = () => {
        w.tops.push(el.getBoundingClientRect().top);
        if (w.sampling) requestAnimationFrame(step);
      };
      step();
    });

    // The first question opens, then is answered, from out of sight: no click scrolls to it.
    await above.locator(".question-heading").dispatchEvent("click");
    const confirm = above.getByRole("button", { name: "Confirm this choice" });
    await expect(confirm).toBeAttached();
    await confirm.dispatchEvent("click");
    await expect(above).toHaveCount(0);
    await expect(scroller.locator(".decisions-section .count")).toHaveText(
      "19",
    );

    const tops = await page.evaluate(async () => {
      const w = window as unknown as { tops: number[]; sampling: boolean };
      await new Promise((r) => requestAnimationFrame(() => r(undefined)));
      w.sampling = false;
      return w.tops;
    });
    const drift = Math.max(...tops.map((top) => Math.abs(top - tops[0])));
    expect(tops.length).toBeGreaterThan(10);
    expect(drift).toBeLessThanOrEqual(1);
    // The scroller did move: the page above the question read changed, and the view followed it.
    expect(await scroller.evaluate((el) => el.scrollTop)).not.toBe(before);

    await page.screenshot({
      path: path.join(
        __dirname,
        `../test-results/e2e/wish-scroll-${engine}.png`,
      ),
    });
    fs.rmSync(dir, { recursive: true, force: true });
    expect(errors).toEqual([]);
  });
