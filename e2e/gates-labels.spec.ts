// The labels of the Gates tab of the Hourglass (W160, Q97): the lanes grouped by project and the Sankey name each gate,
// its requests and the time it was held, and no label's box overlaps another's, whatever the length. The gates are the
// five the developer saw overlap: djinn's e2e and lint, dolmen's fmt, paid and stack. The history is a fixture; the
// djinn is the real djinn up --browser (see global-setup.ts). Screenshots, dark and light, go to
// test-results/e2e/gates-labels-*.png.
// Run alone with: go tool task e2e -- e2e/gates-labels.spec.ts
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { type Page, expect, test } from "./test";
import path from "node:path";

import {
  GateServiceHistoryRequestSchema,
  GateServiceHistoryResponseSchema,
  GateServiceListResponseSchema,
} from "../gen/ts/machine/v1/machine_pb";
import { TaskKind } from "../gen/ts/plan/v1/plan_pb";

const shots = path.join(__dirname, "../test-results/e2e");

// The last hour: per gate, its project, its number of holds and the minutes of each.
const GATES: [string, string, number, number][] = [
  ["djinn", "e2e", 3, 1],
  ["djinn", "lint", 8, 0.66],
  ["dolmen", "fmt", 11, 0.02],
  ["dolmen", "paid", 4, 6.3],
  ["dolmen", "stack", 2, 3],
];

function history(now: number) {
  const min = 60_000;
  const ts = (ago: number) => timestampFromMs(now - ago);
  const intervals = GATES.flatMap(([project, gate, count, minutes], g) =>
    Array.from({ length: count }, (_, i) => {
      const asked = 55 * min - i * 4 * min - g * 30_000;
      return {
        gate,
        command: `go tool task ${gate}`,
        project,
        taskId: `task-W${g}${i}`,
        taskCode: `W${g}${i}`,
        taskKind: TaskKind.WORK,
        askedTime: ts(asked),
        grantTime: ts(asked - 1_000),
        releaseTime: ts(asked - 1_000 - minutes * min),
      };
    }),
  );
  return create(GateServiceHistoryResponseSchema, {
    startTime: ts(60 * min),
    readTime: ts(0),
    intervals,
    runs: [],
  });
}

async function route(page: Page) {
  const proto = (body: Uint8Array) => ({
    status: 200,
    contentType: "application/proto",
    body: Buffer.from(body),
  });
  await page.route("**/machine.v1.GateService/List", (r) =>
    r.fulfill(
      proto(
        toBinary(
          GateServiceListResponseSchema,
          create(GateServiceListResponseSchema, {
            gates: GATES.map(([project, name]) => ({ name, project })),
          }),
        ),
      ),
    ),
  );
  // The window whole at the first read; nothing new at the reads that follow (after_time set).
  await page.route("**/machine.v1.GateService/History", (r) => {
    const ask = fromBinary(
      GateServiceHistoryRequestSchema,
      r.request().postDataBuffer() ?? new Uint8Array(),
    );
    const message = history(Date.now());
    if (ask.afterTime) message.intervals = [];
    return r.fulfill(
      proto(toBinary(GateServiceHistoryResponseSchema, message)),
    );
  });
}

type Box = { x: number; y: number; w: number; h: number; text: string };

// The boxes of the elements matched, as drawn: for an element whose content overflows it, the content's box.
const boxes = (page: Page, selector: string) =>
  page.locator(selector).evaluateAll((els) =>
    els.map((el) => {
      const r = el.getBoundingClientRect();
      // An SVG text has no scroll box: its rectangle is what it draws.
      const svg = el instanceof SVGElement;
      const h = svg ? r.height : Math.max(r.height, el.scrollHeight);
      const w = svg ? r.width : Math.max(r.width, el.scrollWidth);
      return {
        x: r.x,
        y: r.y,
        w,
        h,
        text: (el.textContent ?? "").trim(),
      };
    }),
  );

// The pairs of boxes that overlap by more than half a pixel.
function overlaps(list: Box[]): string[] {
  const out: string[] = [];
  for (let i = 0; i < list.length; i++)
    for (let j = i + 1; j < list.length; j++) {
      const a = list[i];
      const b = list[j];
      const x = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
      const y = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
      if (x > 0.5 && y > 0.5) out.push(`${a.text} / ${b.text}`);
    }
  return out;
}

// Every line of text in a lane's label shows whole in its box: none squeezed by the next (W160 saw lint's and paid's
// names pressed to nothing under a count wrapped on two lines), none run past its label's bottom.
const clipped = (page: Page) =>
  page
    .locator(".gates-flow-label > :not(.gates-strip-scale)")
    .evaluateAll((els) =>
      els
        .filter((el) => {
          const r = el.getBoundingClientRect();
          const p = el.parentElement!.getBoundingClientRect();
          return (
            r.height < 15 ||
            el.scrollHeight > el.clientHeight + 0.5 ||
            r.bottom > p.bottom + 0.5 ||
            r.top < p.top - 0.5 ||
            (getComputedStyle(el).textOverflow !== "ellipsis" &&
              el.scrollWidth > el.clientWidth + 0.5)
          );
        })
        .map((el) => (el.textContent ?? "").trim()),
    );

for (const theme of ["dark", "light"])
  test(`the Gates tab: the labels of five gates in two projects never overlap (${theme})`, async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));
    await route(page);
    await page.setViewportSize({ width: 1280, height: 1400 });
    await page.goto(process.env.DJINN_URL!);
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();
    await page.evaluate((theme) => {
      document.documentElement.dataset.theme = theme;
    }, theme);
    await page.locator(".sidebar .hourglass-nav").click();
    await page.locator("#hourglass-tab-gates").click();

    const flow = page.locator(".gates-flow");
    await expect(flow.locator(".gates-flow-name")).toHaveText([
      "Running",
      "djinn",
      "e2e",
      "lint",
      "dolmen",
      "fmt",
      "paid",
      "stack",
    ]);
    await expect(flow.locator(".gates-flow-label").nth(2)).toContainText(
      "3 requests",
    );
    await expect(flow.locator(".gates-flow-label").nth(5)).toContainText(
      "11 requests",
    );
    // Each lane's label says its gate, count and time whole on hover, even cut.
    await expect(flow.locator(".gates-flow-label").nth(6)).toHaveAttribute(
      "title",
      /^paid\n4 requests · /,
    );
    await flow.scrollIntoViewIfNeeded();
    if (process.env.GATES_SHOTS)
      await flow.screenshot({
        path: path.join(shots, `gates-labels-lanes-${theme}.png`),
      });
    // The labels of the lanes: each line within its label, no label over another, each lane as tall as its label.
    expect(await clipped(page)).toEqual([]);
    expect(overlaps(await boxes(page, ".gates-flow-label"))).toEqual([]);
    expect(
      overlaps(await boxes(page, ".gates-flow-tracks > .gates-row")),
    ).toEqual([]);

    // The Sankey: a text per node, none over another.
    const sankey = page.locator(".gates-sankey");
    await sankey.scrollIntoViewIfNeeded();
    await expect(sankey.locator(".gates-sankey-node text")).toContainText([
      "dolmen 17",
      "djinn 11",
      "fmt 11",
      "paid 4",
      "stack 2",
      "lint 8",
      "e2e 3",
    ]);
    if (process.env.GATES_SHOTS)
      await sankey.screenshot({
        path: path.join(shots, `gates-labels-sankey-${theme}.png`),
      });
    expect(overlaps(await boxes(page, ".gates-sankey-node text"))).toEqual([]);

    expect(errors).toEqual([]);
  });
