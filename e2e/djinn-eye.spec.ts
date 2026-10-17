// The Djinn's eye (src/djinn-eye.tsx, Decision Q38 A): it opens in the seal, flies to the question that matters, follows
// the developer to the question they go to on a curve that can be followed, waits while they type, and rests on Write to
// the Djinn once no question is open. A recorded lead, never started, keeps it awake: no agent runs.
// Run alone with: go tool task e2e -- e2e/djinn-eye.spec.ts
import { expect, type Page, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import path from "node:path";

import { dropLead, leadFolder, setLead } from "./lead";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
// Not the session of another spec: one session runs in one terminal.
const session = "3b7e1f20-5c4d-4e8a-9f60-7a2b1c3d4e5f";

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

interface Sample {
  t: number;
  x: number;
  y: number;
  flying: boolean;
}

// follow samples the eye's centre at every frame for ms, measured in the page: a Playwright step would take its own
// snapshots meanwhile.
function follow(page: Page, ms: number): Promise<Sample[]> {
  return page.evaluate(
    (ms) =>
      new Promise<Sample[]>((resolve) => {
        const out: Sample[] = [];
        const start = performance.now();
        const tick = () => {
          const eye = document.querySelector(".djinn-eye");
          const box = eye?.getBoundingClientRect();
          out.push({
            t: performance.now() - start,
            x: box ? box.left + box.width / 2 : NaN,
            y: box ? box.top + box.height / 2 : NaN,
            flying: !!eye?.hasAttribute("data-flying"),
          });
          if (performance.now() - start < ms) requestAnimationFrame(tick);
          else resolve(out);
        };
        tick();
      }),
    ms,
  );
}

// flown is the part of the samples in flight: it moved by many small steps, never a jump.
function flown(samples: Sample[]) {
  const air = samples.filter((s) => s.flying);
  expect(air.length, "frames in flight").toBeGreaterThan(8);
  const first = air[0];
  const last = air.at(-1)!;
  const length = Math.hypot(last.x - first.x, last.y - first.y);
  for (let i = 1; i < air.length; i++) {
    const step = Math.hypot(air[i].x - air[i - 1].x, air[i].y - air[i - 1].y);
    expect(step, `step ${i} of ${length.toFixed(0)} px`).toBeLessThan(
      Math.max(length / 3, 4),
    );
  }
  expect(last.t - first.t, "time in flight").toBeGreaterThan(400);
  return air;
}

const perchOf = (page: Page, id: string) =>
  page.locator(`#question-${id} > .djinn-eye-perch > .djinn-eye`);

test.afterEach(dropLead);

test("the eye flies from the seal to the question that matters, follows the one you go to, waits while you type", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);
  const title = `Eye ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  setLead(session, leadFolder("eye-"));
  const ask = (text: string, ...more: string[]) =>
    JSON.parse(
      djinn(
        "question",
        "ask",
        text,
        wishId,
        "--options",
        "Yes",
        "--options",
        "No",
        "--json",
        ...more,
      ),
    ).question.id as string;
  try {
    // The oldest is needed before the merge: it matters most, and is open first.
    const wick = ask("Which wick?", "--before", "before the merge");
    const oil = ask("Which oil for the lamp?");
    ask("Where to buy the oil?");

    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(page.locator(`#question-${oil}`)).toBeVisible();

    // It leaves the seal for the margin of the question that matters, watching it.
    const eye = perchOf(page, wick);
    await expect(eye).toBeVisible({ timeout: 10_000 });
    await expect(eye).toHaveAttribute("data-mood", "watching");
    await expect(eye).toHaveAttribute("aria-hidden", "true");
    // In the margin, left of the question: it covers none of it.
    const row = await page.locator(`#question-${wick}`).boundingBox();
    const box = await eye.boundingBox();
    expect(box!.x + box!.width).toBeLessThanOrEqual(row!.x);
    await page.screenshot({ path: "test-results/e2e/djinn-eye-watching.png" });

    // The first ↓ gives the open question the focus; the second goes to the next one: the eye follows, on a curve.
    await page.evaluate(() => (document.activeElement as HTMLElement)?.blur());
    await page.keyboard.press("ArrowDown");
    await expect(page.locator(`#question-${wick}`)).toBeFocused();
    const sampled = follow(page, 2_600);
    await page.keyboard.press("ArrowDown");
    const air = flown(await sampled);
    // Down the column it bows into the margin on its left, and lands by the question you went to, reading it.
    const from = air[0];
    const to = air.at(-1)!;
    expect(to.y).toBeGreaterThan(from.y + 10);
    expect(Math.min(...air.map((s) => s.x))).toBeLessThan(
      Math.min(from.x, to.x) - 1,
    );
    await expect(perchOf(page, oil)).toHaveAttribute("data-mood", "reading");
    await page.screenshot({ path: "test-results/e2e/djinn-eye-reading.png" });

    // Writing to the Djinn, the eye waits where it is, even when every question leaves under it.
    await page.keyboard.press(
      process.platform === "darwin" ? "Meta+I" : "Control+I",
    );
    const box2 = page.getByRole("dialog", { name: "To the Djinn" });
    await box2.locator("textarea").pressSequentially("A word");
    for (const code of ["Q01", "Q02", "Q03"])
      djinn(
        "question",
        "withdraw",
        code,
        "--wish-id",
        wishId,
        "--by",
        "developer",
      );
    await expect(page.locator(".question-none")).toBeVisible();
    const still = await follow(page, 1_200);
    const placed = still.filter((s) => !Number.isNaN(s.x));
    expect(placed.length).toBeGreaterThan(0);
    for (const s of placed) {
      expect(s.flying).toBe(false);
      expect(Math.abs(s.x - placed[0].x) + Math.abs(s.y - placed[0].y)).toBe(0);
    }

    // Done writing: it flies to Write to the Djinn, which it lights, and listens.
    await page.keyboard.press("Escape");
    const pill = page.locator(
      ".workspace-action > .djinn-eye-perch > .djinn-eye",
    );
    await expect(pill).toBeVisible({ timeout: 5_000 });
    await expect(pill).toHaveAttribute("data-mood", "listening");
    await page.screenshot({ path: "test-results/e2e/djinn-eye-listening.png" });
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});
