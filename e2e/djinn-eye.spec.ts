// The Djinn's eye (src/djinn-eye.tsx, Decision Q38 A, W259): it rests by the element that has the focus, and only there.
// A click on a wish in the side panel, open or folded, even the wish shown, opens its first tab and gives the focus to
// its first question: the eye opens in the seal and flies to it. It follows the focus to the next question on a curve
// that can be followed, to a tab, to Write to the Djinn's field, where it waits while the developer types; with the focus
// on nothing that has a place, it listens on Write to the Djinn. Both themes; with less motion asked, it fades from one
// place to the next. A recorded lead, never started, keeps it awake: no agent runs.
// Run alone with: go tool task e2e -- e2e/djinn-eye.spec.ts
import { expect, type Page, test } from "./test";
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

interface Followed {
  samples: Sample[];
  // The flight's own path, its keyframes' centres, read once as it starts: the frames a loaded machine paints in flight
  // are few (one every 70 ms at a load of 40), the path the engine follows is not.
  path: { x: number; y: number }[];
}

// follow samples the eye's centre at every frame for ms, measured in the page: a Playwright step would take its own
// snapshots meanwhile.
function follow(page: Page, ms: number): Promise<Followed> {
  return page.evaluate(
    (ms) =>
      new Promise<Followed>((resolve) => {
        const samples: Sample[] = [];
        const path: { x: number; y: number }[] = [];
        const start = performance.now();
        let seen = false;
        const tick = () => {
          const eye = document.querySelector<HTMLElement>(".djinn-eye");
          const box = eye?.getBoundingClientRect();
          const flying = !!eye?.hasAttribute("data-flying");
          if (flying) seen = true;
          samples.push({
            t: performance.now() - start,
            x: box ? box.left + box.width / 2 : NaN,
            y: box ? box.top + box.height / 2 : NaN,
            flying,
          });
          const anim = flying && !path.length && eye?.getAnimations()[0];
          if (anim && eye && anim.effect instanceof KeyframeEffect)
            for (const frame of anim.effect.getKeyframes()) {
              const m = /translate\(([-\d.]+)px, ([-\d.]+)px\)/.exec(
                String(frame.transform),
              );
              if (m)
                path.push({
                  x: +m[1] + eye.offsetWidth / 2,
                  y: +m[2] + eye.offsetHeight / 2,
                });
            }
          if (seen && !flying) resolve({ samples, path });
          else if (performance.now() - start < ms || flying)
            requestAnimationFrame(tick);
          else resolve({ samples, path });
        };
        tick();
      }),
    ms,
  );
}

// flown is the flight: a path of many small steps, never a jump, with at least three frames painted in flight and
// followed for long enough to be seen. The frame count itself is a pace budget, checked in the @budget case below.
function flown({ samples, path }: Followed) {
  const air = samples.filter((s) => s.flying);
  expect(air.length, "frames in flight").toBeGreaterThanOrEqual(3);
  expect(path.length, "steps of the path").toBeGreaterThan(20);
  const length = Math.hypot(
    path.at(-1)!.x - path[0].x,
    path.at(-1)!.y - path[0].y,
  );
  for (let i = 1; i < air.length; i++) {
    const step = Math.hypot(air[i].x - air[i - 1].x, air[i].y - air[i - 1].y);
    expect(step, `painted step ${i} of ${length.toFixed(0)} px`).toBeLessThan(
      length,
    );
  }
  for (let i = 1; i < path.length; i++) {
    const step = Math.hypot(
      path[i].x - path[i - 1].x,
      path[i].y - path[i - 1].y,
    );
    expect(step, `step ${i} of ${length.toFixed(0)} px`).toBeLessThan(
      Math.max(length / 5, 4),
    );
  }
  const first = samples.indexOf(air[0]);
  const last = samples.lastIndexOf(air.at(-1)!);
  const before = samples[first - 1] ?? air[0];
  const after = samples[last + 1] ?? air.at(-1)!;
  expect(after.t - before.t, "time in flight").toBeGreaterThan(400);
  return path;
}

const perchOf = (page: Page, id: string) =>
  page.locator(`#question-${id} > .djinn-eye-perch > .djinn-eye`);
const tabEye = (page: Page, view: string) =>
  page.locator(`#view-tab-${view} > .djinn-eye-perch > .djinn-eye`);
const pillEye = (page: Page) =>
  page.locator(
    ".workspace-action .lead-socket > .djinn-eye-perch > .djinn-eye",
  );

// pauseOthers makes way: three wishes at a time, and the earlier specs' are active.
function pauseOthers() {
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);
}

test.afterEach(dropLead);

test("a click on a wish focuses its first question, and the eye goes with the focus: down the column, to a tab, to the field you type in", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  pauseOthers();
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
    const wick = ask("Which wick?");
    const oil = ask("Which oil for the lamp?");
    // Needed before the merge, the last asked: it once drew the eye away from the question focused; no more.
    ask("Where to buy the oil?", "--before", "before the merge");

    await page.goto(process.env.DJINN_URL!);
    const nav = page.locator(".wish-nav").filter({ hasText: title });
    await nav.click();
    await expect(page.locator("#view-tab-main")).toHaveAttribute(
      "aria-selected",
      "true",
    );
    // The first question of the column has the focus at once, and the eye leaves the seal for its margin, reading it.
    await expect(page.locator(`#question-${wick}`)).toBeFocused();
    const eye = perchOf(page, wick);
    await expect(eye).toBeVisible({ timeout: 10_000 });
    await expect(eye).toHaveAttribute("data-mood", "reading");
    await expect(eye).toHaveAttribute("aria-hidden", "true");
    await expect(page.locator(".djinn-eye")).toHaveCount(1);
    // In the margin, left of the question: it covers none of it.
    const row = await page.locator(`#question-${wick}`).boundingBox();
    const box = await eye.boundingBox();
    expect(box!.x + box!.width).toBeLessThanOrEqual(row!.x);
    await page.screenshot({ path: "test-results/e2e/djinn-eye-landed.png" });

    // ↓ moves the focus to the next question: the eye follows, on a curve.
    const sampled = follow(page, 2_600);
    await page.keyboard.press("ArrowDown");
    const air = flown(await sampled);
    await expect(page.locator(`#question-${oil}`)).toBeFocused();
    // Down the column it bows into the margin on its left, and lands by the question that has the focus.
    const from = air[0];
    const to = air.at(-1)!;
    expect(to.y).toBeGreaterThan(from.y + 10);
    expect(Math.min(...air.map((s) => s.x))).toBeLessThan(
      Math.min(from.x, to.x) - 1,
    );
    await expect(perchOf(page, oil)).toHaveAttribute("data-mood", "reading");
    await page.screenshot({ path: "test-results/e2e/djinn-eye-reading.png" });

    // A tab takes the focus: the eye rests under it, above the tab's page, looking up at it.
    const tasks = page.locator("#view-tab-tasks");
    await tasks.click();
    await expect(tasks).toBeFocused();
    await expect(tabEye(page, "tasks")).toBeVisible({ timeout: 5_000 });
    await expect(tabEye(page, "tasks")).toHaveAttribute(
      "data-mood",
      "watching",
    );
    const tab = await tasks.boundingBox();
    const under = await tabEye(page, "tasks").boundingBox();
    expect(under!.y).toBeGreaterThanOrEqual(tab!.y + tab!.height);
    expect(under!.x).toBeGreaterThanOrEqual(tab!.x);
    expect(under!.x + under!.width).toBeLessThanOrEqual(tab!.x + tab!.width);
    await page.screenshot({ path: "test-results/e2e/djinn-eye-tab.png" });

    // The wish shown, clicked again from that tab, the second question open: its first tab, its first question focused.
    await nav.click();
    await expect(page.locator("#view-tab-main")).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(page.locator(`#question-${wick}`)).toBeFocused();
    await expect(page.locator(`#question-${wick}`)).toHaveClass(/current/);
    await expect(perchOf(page, wick)).toBeVisible({ timeout: 5_000 });

    // Writing to the Djinn: the field has the focus, the eye goes to the socket beside it, and waits there while the
    // developer types, even when every question leaves.
    await page.keyboard.press(
      process.platform === "darwin" ? "Meta+I" : "Control+I",
    );
    const writing = page.getByRole("dialog", { name: "To the Djinn" });
    await writing.locator("textarea").pressSequentially("A word");
    await expect(writing.locator("textarea")).toBeFocused();
    await expect(pillEye(page)).toBeVisible({ timeout: 5_000 });
    await expect(pillEye(page)).toHaveAttribute("data-mood", "listening");
    // The socket stands left of the text: it covers none of it.
    const field = await writing.locator("textarea").boundingBox();
    const socket = await pillEye(page).boundingBox();
    expect(socket!.x + socket!.width).toBeLessThanOrEqual(field!.x + 1);
    // For the shot alone: the porthole opens (0.6 s), then the eye blinks once.
    await page.waitForTimeout(900);
    await page.screenshot({ path: "test-results/e2e/djinn-eye-writing.png" });
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
    const { samples: still } = await follow(page, 1_200);
    const placed = still.filter((s) => !Number.isNaN(s.x));
    expect(placed.length).toBeGreaterThan(0);
    for (const s of placed) {
      expect(s.flying).toBe(false);
      expect(Math.abs(s.x - placed[0].x) + Math.abs(s.y - placed[0].y)).toBe(0);
    }

    // Done writing: the pill folds, the focus on it, and the eye listens in its socket.
    await page.keyboard.press("Escape");
    await expect(writing).toHaveCount(0);
    await expect(pillEye(page)).toBeVisible({ timeout: 5_000 });
    await expect(pillEye(page)).toHaveAttribute("data-mood", "listening");
    await page.screenshot({ path: "test-results/e2e/djinn-eye-listening.png" });
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});

test("a click on a wish with no question opens its first tab, and the eye listens on Write to the Djinn: light, less motion, the menu folded", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  pauseOthers();
  const title = `Eye none ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  setLead(session, leadFolder("eye-"));
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto(process.env.DJINN_URL!);
  await page.evaluate(() => {
    localStorage.setItem("djinn.theme", "light");
    document.documentElement.dataset.theme = "light";
  });
  try {
    const nav = page.locator(`.wish-nav[title*="${title}"]`);
    const main = page.locator("#view-tab-main");
    await nav.click();
    await expect(main).toHaveAttribute("aria-selected", "true");
    await expect(page.locator(".question-none")).toBeVisible();
    await expect(pillEye(page)).toBeVisible({ timeout: 10_000 });
    await expect(pillEye(page)).toHaveAttribute("data-mood", "listening");
    await page.screenshot({
      path: "test-results/e2e/djinn-eye-no-question-light.png",
    });

    // A tab takes the focus: with less motion asked, the eye fades there, it does not fly.
    const sampled = follow(page, 1_500);
    await page.locator("#view-tab-decisions").click();
    expect((await sampled).samples.some((s) => s.flying)).toBe(false);
    await expect(tabEye(page, "decisions")).toBeVisible({ timeout: 5_000 });

    // Folded to its rail, the side panel lands the same: the first tab, the eye back on Write to the Djinn.
    await page.locator(".sidebar-toggle").click();
    await expect(page.locator(".wish-app.sidebar-collapsed")).toHaveCount(1);
    try {
      await nav.click();
      await expect(main).toHaveAttribute("aria-selected", "true");
      await expect(pillEye(page)).toBeVisible({ timeout: 5_000 });
      await expect(pillEye(page)).toHaveAttribute("data-mood", "listening");
      await page.screenshot({
        path: "test-results/e2e/djinn-eye-no-question-folded.png",
      });
    } finally {
      await page.locator(".sidebar-toggle").click();
      await expect(page.locator(".wish-app.sidebar-collapsed")).toHaveCount(0);
    }
    expect(errors).toEqual([]);
  } finally {
    await page.evaluate(() => localStorage.removeItem("djinn.theme"));
    djinn("wish", "pause", wishId);
  }
});

// The frame count in flight is a pace budget: other workers' load drops frames without changing the path, so it runs
// on demand (playwright.config.ts).
test(
  "the eye paints more than eight frames in flight",
  { tag: "@budget" },
  async ({ page }) => {
    test.setTimeout(60_000);
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));
    pauseOthers();
    const title = `Eye budget ${randomUUID().slice(0, 8)}`;
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
      const wick = ask(
        "Which wick?",
        "--context",
        Array.from({ length: 12 }, (_, i) => `- Wick ${i + 1}`).join("\n"),
      );
      const oil = ask("Which oil for the lamp?");

      await page.goto(process.env.DJINN_URL!);
      await page.locator(".wish-nav").filter({ hasText: title }).click();
      await expect(page.locator(`#question-${wick}`)).toBeFocused();
      await expect(perchOf(page, wick)).toBeVisible({ timeout: 10_000 });

      let best = 0;
      for (let i = 0; i < 6 && best <= 8; i++) {
        const next = i % 2 === 0 ? oil : wick;
        const sampled = follow(page, 2_600);
        await page.keyboard.press(i % 2 === 0 ? "ArrowDown" : "ArrowUp");
        const { samples } = await sampled;
        await expect(perchOf(page, next)).toBeVisible({ timeout: 5_000 });
        best = Math.max(best, samples.filter((s) => s.flying).length);
      }
      expect(best, "frames in flight").toBeGreaterThan(8);
      expect(errors).toEqual([]);
    } finally {
      djinn("wish", "pause", wishId);
    }
  },
);

test("when the Djinn speaks, the eye comes to its socket and the bubble queues replies and decisions", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  pauseOthers();
  const title = `Eye speaks ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  setLead(session, leadFolder("eye-"));
  const qId = JSON.parse(
    djinn(
      "question",
      "ask",
      "Which lamp rests on the table?",
      wishId,
      "--options",
      "Brass",
      "--options",
      "Glass",
      "--json",
    ),
  ).question.id as string;
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.evaluate(
      ([id]) => {
        localStorage.setItem(
          "djinn-lead-last-sent",
          JSON.stringify({ [id]: "What happened to the brass lamp?" }),
        );
      },
      [wishId],
    );
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(page.locator(`#question-${qId}`)).toBeFocused();
    await expect(perchOf(page, qId)).toBeVisible({ timeout: 10_000 });

    // WorkspaceAction keeps a fixed 22px radius folded and expanded.
    const pill = page.locator(".workspace-action-lead");
    await expect(pill).toHaveCSS("border-radius", "22px");

    // Two messages from the lead: a reply and a lone decision.
    const firstReply =
      "The brass lamp waits in the third drawer beneath the chart table, polished and ready for the night watch before the next tide turns.";
    const secondDecided =
      "Trimmed the spare wick alone while the gale held the harbour.";
    djinn("lead", "say", wishId, firstReply);
    djinn("lead", "say", wishId, secondDecided, "--decided");

    // The eye comes to its socket on the pill even while a question is open, and Bubble B shows 1 / 2.
    const bubble = page.locator(".lead-bubble");
    await expect(bubble).toBeVisible({ timeout: 10_000 });
    await expect(pillEye(page)).toBeVisible({ timeout: 10_000 });
    await expect(bubble).toHaveClass(/is-side/);
    await expect(bubble.locator(".lead-bubble-kind")).toHaveText("Djinn");
    await expect(bubble.locator(".lead-bubble-count")).toHaveText("1 / 2");
    await expect(bubble.locator(".lead-bubble-text")).toHaveText(firstReply);

    await page.waitForTimeout(900);
    await page.screenshot({
      path: "test-results/e2e/djinn-eye-bubble-dark.png",
    });

    // Hover opens the full message past the two-line preview and shows the last-sent quote.
    await bubble.hover();
    await expect(bubble).toHaveClass(/is-open/, { timeout: 2_000 });
    await expect(bubble.locator(".lead-bubble-quote")).toBeVisible();
    await expect(bubble.locator(".lead-bubble-quote")).toContainText(
      "In reply to: “What happened to the brass lamp?”",
    );

    // Light theme screenshot of the open bubble.
    await page.evaluate(() => {
      localStorage.setItem("djinn.theme", "light");
      document.documentElement.dataset.theme = "light";
    });
    await page.waitForTimeout(250);
    await page.screenshot({
      path: "test-results/e2e/djinn-eye-bubble-light.png",
    });
    await page.evaluate(() => {
      localStorage.removeItem("djinn.theme");
      delete document.documentElement.dataset.theme;
    });

    // A click marks the first message seen and shows the second (--decided).
    await bubble.click();
    await expect(bubble.locator(".lead-bubble-kind")).toHaveText("Decided");
    await expect(bubble.locator(".lead-bubble-count")).toHaveCount(0);
    await expect(bubble.locator(".lead-bubble-quote")).toHaveCount(0);
    await expect(bubble.locator(".lead-bubble-text")).toHaveText(secondDecided);

    // Reloading keeps the first message seen and brings back the unseen second message from the last 24 h.
    await page.reload();
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(bubble).toBeVisible({ timeout: 10_000 });
    await expect(bubble.locator(".lead-bubble-kind")).toHaveText("Decided");
    await expect(bubble.locator(".lead-bubble-text")).toHaveText(secondDecided);
    await expect(pillEye(page)).toBeVisible({ timeout: 10_000 });

    // Clicking the last message closes the bubble and returns the eye to the question perch.
    await bubble.click();
    await expect(bubble).toHaveCount(0);
    await expect(perchOf(page, qId)).toBeVisible({ timeout: 10_000 });
    expect(errors).toEqual([]);
  } finally {
    await page.evaluate(() => localStorage.removeItem("djinn.theme"));
    djinn("wish", "pause", wishId);
  }
});
