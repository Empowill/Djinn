// A wish view with a task card open must keep at most 3 or 4 long-lived streams open concurrently
// (e.g. UiService/Watch, WishService/Watch, LoadService/Watch, TaskService/Watch), leaving free HTTP connections
// for unary calls in Chromium and WebKitGTK (which limit origins to 6 concurrent HTTP/1.1 connections).
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

test("a wish view with a task card open keeps at most 4 long-lived streams", async ({
  page,
}) => {
  const activeStreams = new Set<import("@playwright/test").Request>();

  page.on("request", (req) => {
    const url = req.url();
    // Only track Connect RPCs on the local server.
    if (
      url.includes("/ui.v1.UiService/") ||
      url.includes("/plan.v1.WishService/") ||
      url.includes("/djinn.v1.LoadService/") ||
      url.includes("/terminal.v1.TerminalService/") ||
      url.includes("/plan.v1.TaskService/")
    ) {
      activeStreams.add(req);
    }
  });

  page.on("requestfinished", (req) => {
    activeStreams.delete(req);
  });

  page.on("requestfailed", (req) => {
    activeStreams.delete(req);
  });

  const title = `Stream limit test ${randomUUID().slice(0, 8)}`;
  const made = JSON.parse(djinn("wish", "make", title, "--json")).wish;
  const wishId = made.id as string;
  if (made.state !== "WISH_STATE_ACTIVE") {
    djinn("wish", "activate", wishId);
  }

  const taskId = JSON.parse(
    djinn(
      "task",
      "spawn",
      wishId,
      "--title",
      "Streaming probe",
      "--provider",
      "fake",
      "--prompt",
      "text working\nsleep 30s",
      "--json",
    ),
  ).task.id as string;

  try {
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await page.getByRole("tab", { name: /^Tasks/ }).click();

    const card = page.locator(`#task-${taskId}`);
    await expect(card.locator(".wish-task-heading .status-badge")).toHaveText(
      "Running",
    );

    // Open task card and follow events to open TaskService.Watch
    await card.getByRole("button", { name: /Streaming probe/ }).click();
    await card.locator(".wish-task-events-fold summary").click();
    await expect(
      card.locator(".wish-event").getByText("working", { exact: true }),
    ).toBeVisible();

    // Give a moment for any transient unary requests to finish.
    await page.waitForTimeout(500);

    // Filter to long-lived streams that are still open.
    const openUrls = Array.from(activeStreams).map((r) => r.url());
    // At most 4 long-lived streams:
    // 1. UiService/Watch (unified Show, Update, and Load)
    // 2. WishService/Watch
    // 3. TaskService/Watch
    // 4. TerminalService/Read (if a terminal is open)
    // And specifically NO unmerged watches or runaway background streams.
    expect(
      openUrls.length,
      `Too many open streams (${openUrls.length}):\n${openUrls.join("\n")}`,
    ).toBeLessThanOrEqual(4);
    expect(openUrls.length).toBeGreaterThanOrEqual(2);
    expect(openUrls.some((u) => u.includes("/UiService/WatchShow"))).toBe(
      false,
    );
    expect(openUrls.some((u) => u.includes("/UiService/WatchUpdate"))).toBe(
      false,
    );
    expect(openUrls.some((u) => u.includes("/LoadService/Watch"))).toBe(false);
  } finally {
    try {
      djinn("task", "stop", taskId);
    } catch {
      // Ignored.
    }
    try {
      djinn("wish", "delete", wishId);
    } catch {
      // Ignored.
    }
  }
});
