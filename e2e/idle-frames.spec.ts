// The window draws nothing while nothing changes (W513): W503 measured its WebKit repainting a whole core with a
// worker running and nobody looking. With a worker running on the flight plan, once the drawing has rested, no script
// asks for frames, and what loops in CSS only moves by transform and opacity, which the engine composes without
// painting. A window at rest (not focused) pauses even those. The worker is the fake agent: no model is called.
// Run alone with: go tool task e2e -- e2e/idle-frames.spec.ts
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import path from "node:path";

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

test("an idle window asks for no frame and paints no looping animation", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state?: string }) =>
      w.state !== "WISH_STATE_PAUSED" && w.state !== "WISH_STATE_GRANTED",
  ))
    djinn("wish", "pause", w.id);

  const title = `Idle frames ${randomUUID().slice(0, 8)}`;
  const made = JSON.parse(djinn("wish", "make", title, "--json")).wish;
  const wishId = made.id as string;
  if (made.state !== "WISH_STATE_ACTIVE") djinn("wish", "activate", wishId);
  const taskId = JSON.parse(
    djinn(
      "task",
      "spawn",
      wishId,
      "--title",
      "Work a long while",
      "--provider",
      "fake",
      "--prompt",
      "text working\nsleep 60s\ntext after",
      "--json",
    ),
  ).task.id as string;
  // Every frame asked for is counted, from the first script of the page on.
  await page.addInitScript(() => {
    const w = window as unknown as { framesAsked: number };
    w.framesAsked = 0;
    const ask = window.requestAnimationFrame.bind(window);
    window.requestAnimationFrame = (callback) => {
      w.framesAsked++;
      return ask(callback);
    };
  });
  try {
    await page.goto(process.env.DJINN_URL!);
    await expect(page.locator(".flight-plan")).toBeVisible();
    await expect(
      page
        .locator(".flight-plan .glance-wide .glance-row .glance-dot.run")
        .first(),
    ).toBeVisible({
      timeout: 15_000,
    });
    // The drawing moves a while as the worker starts, then rests.
    await expect(page.locator(".machine.active")).toBeVisible();
    await expect(page.locator(".machine.active:not(.moving)")).toBeVisible({
      timeout: 20_000,
    });

    // Measured in the page: a Playwright step would take its own snapshots meanwhile.
    const idle = await page.evaluate(async () => {
      const w = window as unknown as { framesAsked: number };
      w.framesAsked = 0;
      await new Promise((r) => setTimeout(r, 2_000));
      const composed = new Set([
        "offset",
        "easing",
        "composite",
        "computedOffset",
        "transform",
        "opacity",
      ]);
      const painted = document
        .getAnimations()
        .filter(
          (a) =>
            a.playState === "running" &&
            a.effect?.getComputedTiming().iterations === Infinity,
        )
        .flatMap((a) =>
          (a.effect as KeyframeEffect)
            .getKeyframes()
            .flatMap((k) => Object.keys(k))
            .filter((key) => !composed.has(key))
            .map((key) => `${(a as CSSAnimation).animationName}: ${key}`),
        );
      return { frames: w.framesAsked, painted: [...new Set(painted)] };
    });
    expect(idle).toEqual({ frames: 0, painted: [] });

    // Another window takes the focus: what still loops pauses.
    await page.evaluate(() => {
      Document.prototype.hasFocus = () => false;
      window.dispatchEvent(new Event("blur"));
    });
    await expect(page.locator("html[data-still]")).toHaveCount(1);
    const looping = await page.evaluate(
      () =>
        document
          .getAnimations()
          .filter(
            (a) =>
              a.playState === "running" &&
              a.effect?.getComputedTiming().iterations === Infinity,
          ).length,
    );
    expect(looping).toBe(0);
    expect(errors).toEqual([]);
  } finally {
    djinn("task", "stop", taskId);
    djinn("wish", "pause", wishId);
  }
});
