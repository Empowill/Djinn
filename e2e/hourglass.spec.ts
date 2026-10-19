// The Hourglass (W542, Decision Q253) against the real djinn up --browser (see global-setup.ts): the flying carpet
// draws no live graph and reads no series, one line of the machine's load leads to the Hourglass, right below it in the
// side panel; the Hourglass reads the machine's series live while it shows, and once left, no measure is read or stays
// open, and the window keeps the streams it had before. The worker is the fake agent: no model is called.
// Run alone with: go tool task e2e -- e2e/hourglass.spec.ts
import { create, toBinary } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { type Request, expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import path from "node:path";

import {
  CommandServiceListResponseSchema,
  MachineServiceSeriesResponseSchema,
} from "../gen/ts/machine/v1/machine_pb";
import { Provider } from "../gen/ts/plan/v1/plan_pb";

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

// The machine and the worker of the wish, a sample every 5 s up to now.
function series(taskId: string, wishId: string) {
  const end = Date.now();
  const times = Array.from({ length: 12 }, (_, i) => end - (11 - i) * 5_000);
  return create(MachineServiceSeriesResponseSchema, {
    machine: {
      cores: 8,
      memoryTotalBytes: 16n * 1024n ** 3n,
      points: times.map((t) => ({
        time: timestampFromMs(t),
        cpuPercent: 30,
        memoryBytes: 6n * 1024n ** 3n,
      })),
    },
    workers: [
      {
        taskId,
        wishId,
        code: "W1",
        title: "Count the grains",
        provider: Provider.FAKE,
        peakCpuPercent: 40,
        peakMemoryBytes: 300n * 1024n ** 2n,
        points: times.map((t) => ({
          time: timestampFromMs(t),
          cpuPercent: 20,
          memoryBytes: 200n * 1024n ** 2n,
        })),
      },
    ],
  });
}

// The reads of the measures, polled by the Hourglass.
const measure = /\/machine\.v1\.(MachineService\/Series|CommandService\/List)$/;

test("the Hourglass reads its measures while it shows, and leaving it stops them", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state?: string }) =>
      w.state !== "WISH_STATE_PAUSED" && w.state !== "WISH_STATE_GRANTED",
  ))
    djinn("wish", "pause", w.id);

  const title = `Hourglass ${randomUUID().slice(0, 8)}`;
  const made = JSON.parse(djinn("wish", "make", title, "--json")).wish;
  const wishId = made.id as string;
  if (made.state !== "WISH_STATE_ACTIVE") djinn("wish", "activate", wishId);
  const taskId = JSON.parse(
    djinn(
      "task",
      "spawn",
      wishId,
      "--title",
      "Count the grains",
      "--provider",
      "fake",
      "--prompt",
      "text counting\nsleep 60s",
      "--json",
    ),
  ).task.id as string;

  const reads = { series: 0, commands: 0 };
  await page.route("**/machine.v1.MachineService/Series", (route) => {
    reads.series++;
    return route.fulfill({
      status: 200,
      contentType: "application/proto",
      body: Buffer.from(
        toBinary(MachineServiceSeriesResponseSchema, series(taskId, wishId)),
      ),
    });
  });
  await page.route("**/machine.v1.CommandService/List", (route) => {
    reads.commands++;
    return route.fulfill({
      status: 200,
      contentType: "application/proto",
      body: Buffer.from(
        toBinary(
          CommandServiceListResponseSchema,
          create(CommandServiceListResponseSchema, {}),
        ),
      ),
    });
  });
  const open = new Set<Request>();
  page.on("request", (req) => open.add(req));
  page.on("requestfinished", (req) => open.delete(req));
  page.on("requestfailed", (req) => open.delete(req));
  // The window's long-lived streams: the watches open.
  const streams = async () => {
    await page.waitForTimeout(500);
    return [...open]
      .map((r) => new URL(r.url()).pathname)
      .filter((p) => /\/Watch\w*$/.test(p))
      .sort();
  };

  try {
    await page.goto(process.env.DJINN_URL!);
    await expect(page.locator(".hero h1")).toHaveText("Flying carpet");
    await expect(
      page
        .locator(".flight-plan .glance-wide .glance-row .glance-dot.run")
        .first(),
    ).toBeVisible({
      timeout: 15_000,
    });
    // The menu: the Hourglass at the bottom of the side panel, the Grimoire under it (W89), right above the profile.
    const entries = page.locator(
      ".sidebar .plan-nav, .sidebar .wish-list, .sidebar .project-list, .sidebar .import-nav, .sidebar .hourglass-nav, .sidebar .grimoire-nav, .sidebar .sidebar-bottom",
    );
    await expect(entries.nth(0)).toHaveClass(/plan-nav/);
    await expect(entries.nth(1)).toHaveClass(/wish-list/);
    await expect(entries.nth(2)).toHaveClass(/project-list/);
    await expect(entries.nth(3)).toHaveClass(/import-nav/);
    await expect(entries.nth(4)).toHaveClass(/hourglass-nav/);
    await expect(entries.nth(4)).toHaveText("Hourglass");
    await expect(entries.nth(5)).toHaveClass(/grimoire-nav/);
    await expect(entries.nth(5)).toHaveText("Grimoire");
    await expect(entries.nth(6)).toHaveClass(/sidebar-bottom/);

    // The flying carpet: no live graph, no series read; one line of the machine's load.
    await expect(page.locator(".consumption-panel")).toHaveCount(0);
    await expect(page.locator(".resource-chart")).toHaveCount(0);
    const line = page.locator(".flight-plan .hourglass-line");
    await expect(line).toContainText(/\d+ of \d+ workers? running, memory /);
    const before = await streams();
    expect(reads).toEqual({ series: 0, commands: 0 });

    // The line leads to the Hourglass: the machine first, the worker's row, read live.
    await line
      .getByRole("button", { name: "See it live in the Hourglass" })
      .click();
    await expect(page.locator(".hourglass .hero h1")).toHaveText("Hourglass");
    await expect(page.locator(".sidebar .hourglass-nav")).toHaveClass(
      /selected/,
    );
    await expect(page.locator(".flight-plan")).toHaveCount(0);
    const sections = page.locator(".hourglass-section");
    await expect(sections).toHaveCount(1);
    await expect(sections.first()).toHaveAttribute("id", "hourglass-machine");
    await expect(page.locator(`#worker-${taskId}`)).toContainText(
      "Count the grains",
    );
    await sections.first().getByRole("tab", { name: "Gates" }).click();
    await expect(
      sections.first().locator(".resource-machine .resource-chart"),
    ).toHaveCount(2);
    await expect
      .poll(() => reads.series, { timeout: 12_000 })
      .toBeGreaterThan(1);
    expect(reads.commands).toBeGreaterThan(0);

    // Left: the flying carpet again; no measure is read any more, none stays open, the streams are those of before.
    await page.locator(".sidebar .plan-nav").click();
    await expect(page.locator(".hero h1")).toHaveText("Flying carpet");
    await expect(page.locator(".consumption-panel")).toHaveCount(0);
    const left = { ...reads };
    // Longer than the series' 5 s between two reads.
    await page.waitForTimeout(6_500);
    expect(reads).toEqual(left);
    expect(
      [...open].filter((r) => measure.test(new URL(r.url()).pathname)),
    ).toEqual([]);
    expect(await streams()).toEqual(before);

    // The worker's eye still opens its task in its wish.
    await page.locator(".sidebar .hourglass-nav").click();
    await page
      .locator(`#worker-${taskId}`)
      .getByRole("button", { name: "Open W1 in its wish" })
      .click();
    await expect(page.locator(".hero h1")).toHaveText(title);
    await expect(page.locator(`#task-${taskId}`)).toBeVisible();
    expect(errors).toEqual([]);
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
