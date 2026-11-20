// What a hold waited for, in the Gates tab of the Hourglass (W175, W176): pointed at, a band of the frieze names its
// request, its command, and under the command each holder it waited for in turn with how long it waited for it
// (GateInterval.wait_reason, joined by Hold.Waited, W174), never a holder's time frozen when the cause changed
// ("held by W2 …, for 0s"). The history replays W173's trial on dolmen's stack: W1 shares it for its recette, W2 waits
// to hold it alone for its build, W3 waits behind both to share it. It is a fixture; the djinn is the real djinn up
// --browser (see global-setup.ts). With GATES_SHOTS set, screenshots, dark and light, go to
// test-results/e2e/gates-hold-wait-*.png.
// Run alone with: go tool task e2e -- e2e/gates-hold-wait.spec.ts
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { type Page, expect, test } from "./test";
import path from "node:path";

import {
  GateMode,
  GateServiceHistoryRequestSchema,
  GateServiceHistoryResponseSchema,
  GateServiceListResponseSchema,
  GateWaitCause,
} from "../gen/ts/machine/v1/machine_pb";
import { TaskKind } from "../gen/ts/plan/v1/plan_pb";

const shots = path.join(__dirname, "../test-results/e2e");

const LABEL = "stack (dolmen, .agents/djinn.yaml)";
// What W2 and W3 waited for, as Hold.Waited joins it: the gate's label, then each holder with its own time.
const W2_WAITED = `${LABEL}: shared by W1: task recette (13m0s)`;
const W3_WAITED = `${LABEL}: shared by W1: task recette (11m0s), then held by W2: task build (10m0s)`;

function history(now: number) {
  const min = 60_000;
  const ts = (ago: number) => timestampFromMs(now - ago);
  const hold = (
    code: string,
    command: string,
    asked: number,
    grant: number,
    release: number,
    extra = {},
  ) => ({
    gate: "stack",
    command,
    project: "dolmen",
    taskId: `task-${code}`,
    taskCode: code,
    taskKind: TaskKind.WORK,
    askedTime: ts(asked * min),
    grantTime: ts(grant * min),
    releaseTime: ts(release * min),
    ...extra,
  });
  return create(GateServiceHistoryResponseSchema, {
    startTime: ts(60 * min),
    readTime: ts(0),
    intervals: [
      hold("W1", "task recette", 50, 50, 35, { mode: GateMode.SHARED }),
      hold("W2", "task build", 48, 35, 25, {
        waitCause: GateWaitCause.HELD,
        waitReason: W2_WAITED,
      }),
      hold("W3", "task recette", 46, 25, 20, {
        mode: GateMode.SHARED,
        waitCause: GateWaitCause.HELD,
        waitReason: W3_WAITED,
      }),
    ],
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
            gates: [{ name: "stack", project: "dolmen" }],
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

for (const theme of ["dark", "light"])
  test(`the Gates tab: a band pointed at says each holder its request waited for, with its time (${theme})`, async ({
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
    await expect(flow.locator(".gates-band")).toHaveText(["W1", "W2", "W3"]);
    const readout = flow.locator(".gates-flow-readout");

    // W3, behind both: its command, and under it each holder in turn, each with its own time. The band in the middle of
    // the screen, so the bubble below it shows whole above the terminal panel.
    const w3 = flow.locator(".gates-band").filter({ hasText: "W3" });
    await w3.evaluate((el) => el.scrollIntoView({ block: "center" }));
    await w3.hover();
    await expect(readout).toContainText("dolmen · stack · W3");
    const command = readout.locator(".gates-readout-command");
    const why = readout.locator(".gates-readout-why");
    await expect(command).toHaveText("task recette");
    await expect(why).toHaveText(W3_WAITED);
    await expect(why).not.toContainText(", for ");
    // Below the command, whole inside the bubble, and the bubble whole on screen.
    const c = (await command.boundingBox())!;
    const w = (await why.boundingBox())!;
    const r = (await readout.boundingBox())!;
    expect(w.y).toBeGreaterThanOrEqual(c.y + c.height - 0.5);
    expect(w.x).toBeGreaterThanOrEqual(r.x);
    expect(w.x + w.width).toBeLessThanOrEqual(r.x + r.width + 0.5);
    expect(w.y + w.height).toBeLessThanOrEqual(r.y + r.height + 0.5);
    await expect(readout).toBeInViewport({ ratio: 1 });
    if (process.env.GATES_SHOTS)
      await page.screenshot({
        path: path.join(shots, `gates-hold-wait-${theme}.png`),
        clip: {
          x: r.x - 160,
          y: r.y - 80,
          width: r.width + 200,
          height: r.height + 120,
        },
      });

    // W2 waited for W1's share alone.
    await flow.locator(".gates-band").filter({ hasText: "W2" }).hover();
    await expect(why).toHaveText(W2_WAITED);
    await expect(command).toHaveText("task build");

    // W1 waited for no one: no reason under its command.
    await flow.locator(".gates-band").filter({ hasText: "W1" }).hover();
    await expect(command).toHaveText("task recette");
    await expect(why).toHaveCount(0);

    expect(errors).toEqual([]);
  });
