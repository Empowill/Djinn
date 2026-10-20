// The workers glance, beside the questions (glance.tsx): besides the workers, the work on its way into the wish's branch
// and the repair workers (W137). The wish is imported as Djinn leaves it mid-way: W1's merge runs its lint, W2 waits in
// line behind W4, which corrects W3's red merge, W6 reviews the work W5 left not committed, and the push checks hold
// the push of feat/lamp, TestLogin red. No worker runs, no model is called. A click on a line opens its task; on the
// held push, its failing output. The lines stay with the sidebar folded and on a narrow page, and nothing loops.
// Run alone with: go tool task e2e -- e2e/glance.spec.ts
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

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

const at = "2026-10-16T21:00:00Z";

const exportOf = (wishId: string, title: string, ids: string[]) => {
  const [w1, w2, w3, w4, w5, w6] = ids;
  // The merge's check began a minute and a half ago.
  const checking = new Date(Date.now() - 90_000).toISOString();
  const task = (id: string, code: string, name: string, extra: object) => ({
    id,
    wish_id: wishId,
    code,
    title: name,
    create_time: at,
    ...extra,
  });
  const done = (extra: object) => ({
    status: "TASK_STATUS_DONE",
    end_time: at,
    ...extra,
  });
  return {
    version: 1,
    create_time: at,
    wish: {
      id: wishId,
      title,
      create_time: at,
      pushes: [
        {
          project_id: randomUUID(),
          holds: {
            "feat/lamp": {
              held: "test: go tool task test exited 1",
              failure: {
                state: "INTEGRATION_STATE_RED",
                command: "go tool task test",
                output:
                  "ok  \tlamp/oil\n--- FAIL: TestLogin (0.02s)\nFAIL\tlamp/wick",
              },
            },
          },
          run: {
            branch: "feat/lamp",
            stage: "PUSH_STAGE_HELD",
            note: "test: go tool task test exited 1",
            start_time: at,
            end_time: at,
          },
        },
      ],
    },
    tasks: [
      task(
        w1,
        "W1",
        "Pour the oil",
        done({
          integration: {
            state: "INTEGRATION_STATE_INTEGRATING",
            branch: "feat/lamp",
            check: "lint",
            update_time: checking,
          },
        }),
      ),
      task(
        w2,
        "W2",
        "Trim the wick",
        done({
          integration: {
            state: "INTEGRATION_STATE_PENDING",
            branch: "feat/lamp",
            reason: "waits for W4, which repairs feat/lamp",
          },
        }),
      ),
      task(
        w3,
        "W3",
        "Polish the brass",
        done({
          integration: {
            state: "INTEGRATION_STATE_RED",
            branch: "feat/lamp",
            reason: "lint exited 1",
            corrected_by: w4,
            attempts: 1,
          },
        }),
      ),
      task(w4, "W4", "Correct the checks of W3", {
        status: "TASK_STATUS_PENDING",
        correction: {
          attempt: 1,
          failure: { state: "INTEGRATION_STATE_RED", task_ids: [w3] },
        },
      }),
      task(
        w5,
        "W5",
        "Light the wick",
        done({
          integration: {
            state: "INTEGRATION_STATE_UNCOMMITTED",
            branch: "feat/lamp",
            reviewed_by: w6,
          },
        }),
      ),
      task(w6, "W6", "Review the work W5 left", {
        status: "TASK_STATUS_PENDING",
        review: { attempt: 1, task_ids: [w5] },
      }),
    ],
  };
};

const colour = (page: Page, selector: string) =>
  page
    .locator(selector)
    .first()
    .evaluate((el) => getComputedStyle(el).color);

// token is a status token of review.css, as the page resolves it.
const token = (page: Page, name: string) =>
  page.evaluate((n) => {
    const probe = document.createElement("span");
    probe.style.color = `var(${n})`;
    document.body.append(probe);
    const value = getComputedStyle(probe).color;
    probe.remove();
    return value;
  }, name);

test("the glance shows the merge running, the queue, the held push and the repair workers; each opens its task or output", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const tag = randomUUID().slice(0, 8);
  const wishId = randomUUID();
  const ids = Array.from({ length: 6 }, () => randomUUID());
  const title = `Light the lamp ${tag}`;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-glance-"));
  const file = path.join(dir, "lamp.json");
  fs.writeFileSync(file, JSON.stringify(exportOf(wishId, title, ids)));
  djinn("wish", "import", file);
  fs.rmSync(dir, { recursive: true, force: true });
  // First among the active wishes: the flying carpet shows it.
  djinn("wish", "move", wishId, "--to", "1");
  try {
    await page.goto(process.env.DJINN_URL!);
    await page
      .locator(".plan-nav")
      .filter({ hasText: "Flying carpet" })
      .click();
    const wish = page
      .locator(".glance-wide .glance-wish")
      .filter({ has: page.locator(".glance-head", { hasText: title }) });
    await expect(wish.locator(".glance-count")).toHaveText(
      "1 merging · 1 to merge · push held · 2 repairs",
    );
    const flow = wish.getByRole("list", { name: "Merges and repairs" });
    const lines = flow.locator(".glance-row");
    await expect(lines).toHaveCount(5);
    // The batch merging, its check and its time; the held push; the queue; the repair workers, a wrench each.
    await expect(lines.nth(0)).toContainText("W1 → feat/lamp");
    await expect(lines.nth(0).locator(".glance-after")).toHaveText(
      /^checks lint1m(29|3\d)s$/,
    );
    await expect(lines.nth(1)).toContainText(
      "Push of feat/lamp heldtest red: TestLogin",
    );
    await expect(lines.nth(2)).toContainText(
      "W2 → feat/lampwaits for W4, which repairs feat/lamp",
    );
    await expect(lines.nth(3)).toHaveClass(/glance-repair plan/);
    await expect(lines.nth(3)).toContainText("W4makes the checks of W3 pass");
    await expect(lines.nth(4)).toContainText(
      "W6commits the work W5 left not committed",
    );
    await expect(lines.nth(3).locator("svg.lucide-wrench")).toHaveCount(1);
    // The same language as the workers: blue at work, grey to come, salmon for what failed; never gold.
    expect(await colour(page, ".glance-flow .merging .glance-mark")).toBe(
      await token(page, "--st-running"),
    );
    expect(await colour(page, ".glance-flow .glance-repair .glance-mark")).toBe(
      await token(page, "--st-planned"),
    );
    expect(await colour(page, ".glance-flow .held .glance-mark")).toBe(
      await token(page, "--st-failed"),
    );
    // Nothing of it loops: a window at rest costs nothing (idle-frames.spec.ts).
    expect(
      await flow.evaluate(
        (el) =>
          document
            .getAnimations()
            .filter(
              (a) =>
                a.effect instanceof KeyframeEffect &&
                a.effect.target instanceof Element &&
                el.contains(a.effect.target),
            ).length,
      ),
    ).toBe(0);
    await page.screenshot({
      path: path.join(__dirname, "../test-results/e2e/glance-flow.png"),
    });

    // The held push opens its failing output, in a frame bordered on every side.
    await lines.nth(1).click();
    const dialog = page.getByRole("dialog", {
      name: "Push of feat/lamp held",
    });
    await expect(dialog).toContainText("Failed: TestLogin");
    await expect(dialog.locator("code")).toHaveText("$ go tool task test");
    await expect(dialog.locator(".glance-output")).toContainText(
      "--- FAIL: TestLogin (0.02s)",
    );
    const borders = await dialog.locator(".glance-output").evaluate((el) => {
      const s = getComputedStyle(el);
      return [
        s.borderTopWidth,
        s.borderRightWidth,
        s.borderBottomWidth,
        s.borderLeftWidth,
      ];
    });
    expect(new Set(borders).size).toBe(1);
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);

    // A repair worker opens its task, in the Tasks tab.
    await lines.nth(3).click();
    await expect(page.getByRole("tab", { name: /^Tasks/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(page.locator(`#task-${ids[3]}`)).toBeInViewport();
    await page.getByRole("tab", { name: /^Flying carpet/ }).click();

    // The sidebar folded, the lines stay whole beside the questions.
    await page.locator(".sidebar-toggle").click();
    await expect(page.locator(".wish-app")).toHaveClass(/sidebar-collapsed/);
    await expect(lines).toHaveCount(5);
    await expect(lines.nth(0)).toBeVisible();
    await page.locator(".sidebar-toggle").click();
    await expect(page.locator(".wish-app")).not.toHaveClass(
      /sidebar-collapsed/,
    );

    // Narrow, one line per wish: a wrench per repair worker, a mark for the merges, one for the held push.
    const size = page.viewportSize()!;
    await page.setViewportSize({ width: 700, height: size.height });
    const line = page
      .locator(".glance-narrow .glance-line")
      .filter({ hasText: title });
    await expect(line).toBeVisible();
    await expect(line.locator("svg.lucide-wrench")).toHaveCount(2);
    await expect(line.locator("svg.lucide-git-merge")).toHaveCount(1);
    await expect(line.locator("svg.lucide-cloud-off")).toHaveCount(1);
    await page.setViewportSize(size);

    // The wish's own page shows its glance alike.
    await wish.locator(".glance-head button").click();
    await expect(page.locator("h1.wish-title")).toHaveText(title);
    await expect(
      page
        .getByRole("list", { name: "Merges and repairs" })
        .locator(".glance-row"),
    ).toHaveCount(5);
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "delete", wishId);
  }
});
