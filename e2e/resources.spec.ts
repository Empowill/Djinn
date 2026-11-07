// The resources panel of the Hourglass (W542; under the flying carpet before) against the real djinn up --browser (see
// global-setup.ts), its series and commands replaced by fixtures (W502): the charts say the value under the pointer and
// at the arrow keys, grow as new samples come without losing the point shown, and a worker's eye opens its task in its
// wish, on the Tasks tab. The Gates tab of the Hourglass lists every project's commands, the most total time first,
// and the gate a worker holds links one tab to the other. Screenshots, dark and light, go to
// test-results/e2e/resources-*.png and hourglass-*.png.
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { type Page, expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import {
  CommandServiceListResponseSchema,
  GateServiceListResponseSchema,
  MachineServiceSeriesRequestSchema,
  MachineServiceSeriesResponseSchema,
} from "../gen/ts/machine/v1/machine_pb";
import { Provider } from "../gen/ts/plan/v1/plan_pb";
import { portable } from "./portable";

portable();

const shots = path.join(__dirname, "../test-results/e2e");
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

type Wish = { id: string; state: string };
const wishes = (): Wish[] =>
  JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];

const GIB = 1024n ** 3n;
const MIB = 1024n ** 2n;

// The series of the last minutes: the machine and workers, a sample every 5 s; a peak of the machine's CPU at 95%.
function series(taskId: string, wishId: string, title: string, end: number) {
  const times = Array.from({ length: 24 }, (_, i) => end - (23 - i) * 5_000);
  return create(MachineServiceSeriesResponseSchema, {
    machine: {
      cores: 8,
      memoryTotalBytes: 16n * GIB,
      loadLimitShare: 0.8,
      points: times.map((t, i) => ({
        time: timestampFromMs(t),
        cpuPercent: i === 12 ? 95 : 30,
        memoryBytes: 6n * GIB,
        djinnCpuPercent: 8,
        djinnMemoryBytes: 2n * GIB,
        selfCpuPercent: 1,
        selfMemoryBytes: 140n * MIB,
      })),
    },
    disk: {
      availableBytes: 180n * GIB,
      totalBytes: 512n * GIB,
      folders: [
        { path: "/home/dev/.cache/go-build", sizeBytes: 14n * GIB },
        { path: "/home/dev/djinn/node_modules", sizeBytes: 640n * MIB },
        {
          path: "/home/dev/.config/djinn/projects/oil/worktrees",
          sizeBytes: 420n * MIB,
        },
        { path: "/home/dev/.config/djinn", sizeBytes: 180n * MIB },
      ],
    },
    workers: [
      {
        taskId: `${taskId}-watch`,
        wishId,
        code: "W2",
        title: "Watch the pull request checks until CI passes",
        provider: Provider.WATCH,
        peakCpuPercent: 4,
        peakMemoryBytes: 48n * MIB,
        points: times.map((t) => ({
          time: timestampFromMs(t),
          cpuPercent: 2,
          memoryBytes: 42n * MIB,
        })),
      },
      {
        taskId,
        wishId,
        code: "W1",
        title,
        provider: Provider.CLAUDE,
        model: "claude-opus-5-5",
        peakCpuPercent: 80,
        peakMemoryBytes: 400n * MIB,
        points: times.map((t) => ({
          time: timestampFromMs(t),
          cpuPercent: 40,
          memoryBytes: 300n * MIB,
        })),
        gates: [lint(taskId, times[4], times[8])],
      },
    ],
    gates: [lint(taskId, times[4], times[8])],
  });
}

// W1 asked for lint at asked, and holds it since granted.
function lint(taskId: string, asked: number, granted: number) {
  return {
    gate: "lint",
    command: "go tool task lint",
    taskId,
    project: "oil",
    askedTime: timestampFromMs(asked),
    grantTime: timestampFromMs(granted),
  };
}

// What came since: one sample of each.
function since(taskId: string, wishId: string, title: string, at: number) {
  const full = series(taskId, wishId, title, at);
  full.machine!.points = [
    { ...full.machine!.points[0], time: timestampFromMs(at), cpuPercent: 55 },
  ];
  full.workers[0].points = [
    {
      ...full.workers[0].points[0],
      time: timestampFromMs(at),
      cpuPercent: 2,
    },
  ];
  full.workers[1].points = [
    {
      ...full.workers[1].points[0],
      time: timestampFromMs(at),
      cpuPercent: 70,
    },
  ];
  return full;
}

const cost = (
  projectId: string,
  command: string,
  gate: string,
  runs: number,
  seconds: number,
  wait: number,
) => ({
  projectId,
  command,
  gate,
  runs,
  asked: runs + 1,
  seconds,
  recentRuns: [{ gate, waitSeconds: wait, heldSeconds: seconds + 1 }],
});

async function routeResources(
  page: Page,
  taskId: string,
  wishId: string,
  title: string,
  projectId: string,
) {
  const start = Date.now();
  let calls = 0;
  await page.route("**/machine.v1.MachineService/Series", (route) => {
    const ask = fromBinary(
      MachineServiceSeriesRequestSchema,
      route.request().postDataBuffer() ?? new Uint8Array(),
    );
    // The range whole, or what came since its last point: one more sample at each read.
    const message = ask.since
      ? since(taskId, wishId, title, start + 5_000 * ++calls)
      : series(taskId, wishId, title, start);
    return route.fulfill({
      status: 200,
      contentType: "application/proto",
      body: Buffer.from(toBinary(MachineServiceSeriesResponseSchema, message)),
    });
  });
  // The gates now: W1 holds lint, a holder outside any task waits for it.
  await page.route("**/machine.v1.GateService/List", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/proto",
      body: Buffer.from(
        toBinary(
          GateServiceListResponseSchema,
          create(GateServiceListResponseSchema, {
            gates: [
              {
                name: "lint",
                holder: "W1: go tool task lint",
                holderTaskId: taskId,
                since: timestampFromMs(start - 60_000),
                waiting: ["a holder outside any task: go tool task lint"],
              },
            ],
          }),
        ),
      ),
    }),
  );
  // The commands of every project, one at rest among them.
  await page.route("**/machine.v1.CommandService/List", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/proto",
      body: Buffer.from(
        toBinary(
          CommandServiceListResponseSchema,
          create(CommandServiceListResponseSchema, {
            costs: [
              {
                ...cost(randomUUID(), "go tool task e2e", "e2e", 3, 600, 0),
                projectName: "a project at rest",
              },
              cost(projectId, "go tool task e2e", "e2e", 2, 840, 30),
              cost(
                projectId,
                "go tool task e2e -- e2e/wish-render.spec.ts",
                "e2e",
                6,
                95,
                12,
              ),
              cost(
                projectId,
                "go tool task e2e -- e2e/resources.spec.ts",
                "e2e",
                3,
                40,
                0,
              ),
              cost(projectId, "go tool task lint", "lint", 20, 55, 4),
              cost(
                projectId,
                "go test -tags headless ./internal/machine/",
                "test",
                9,
                12,
                1,
              ),
              cost(projectId, "go test ./internal/store/", "test", 4, 3, 0),
            ],
          }),
        ),
      ),
    }),
  );
}

// What each test makes, paused once it ends: the specs after find the place as they left it (e2e/test.ts).
const made: string[] = [];
test.afterEach(() => {
  for (const id of made.splice(0)) djinn("wish", "pause", id);
});

test("the resources panel: values on hover and keys, live samples, and a worker's eye opening its task in its wish", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);

  const tag = randomUUID().slice(0, 8);
  const wishId = randomUUID();
  made.push(wishId);
  const azimaId = randomUUID();
  const w1 = randomUUID();
  const title = `Measure the lamp's oil, then say in full what the wick burns while the flame holds steady ${tag}`;
  const at = "2026-10-12T09:10:00Z";
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-resources-"));
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "oil-")),
  );
  const projectId = JSON.parse(
    djinn("project", "add", folder, "--name", `oil-${tag}`, "--json"),
  ).project.id as string;
  const file = path.join(dir, "lamp.json");
  fs.writeFileSync(
    file,
    JSON.stringify({
      version: 1,
      create_time: at,
      projects: [{ id: projectId, name: `oil-${tag}` }],
      wish: {
        id: wishId,
        title: `Resources ${tag}`,
        project_ids: [projectId],
        create_time: at,
      },
      tasks: [
        {
          id: azimaId,
          wish_id: wishId,
          code: "T1",
          title: "The lamp",
          kind: "TASK_KIND_AZIMA",
          status: "TASK_STATUS_PENDING",
          create_time: at,
        },
        {
          id: w1,
          wish_id: wishId,
          part_of: azimaId,
          code: "W1",
          title,
          project_id: projectId,
          prompt: "Measure the oil.",
          status: "TASK_STATUS_DONE",
          provider: "PROVIDER_CLAUDE",
          create_time: at,
        },
        {
          id: randomUUID(),
          wish_id: wishId,
          part_of: azimaId,
          code: "W2",
          title: "Light the wick",
          depends_on: [w1],
          status: "TASK_STATUS_PENDING",
          create_time: at,
        },
      ],
    }),
  );
  djinn("wish", "import", file);

  await routeResources(page, w1, wishId, title, projectId);
  await page.setViewportSize({ width: 1440, height: 1400 });
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".hero h1")).toHaveText("Flying carpet", {
    timeout: 15_000,
  });
  await page.locator(".sidebar .hourglass-nav").click();
  await expect(page.locator(".hero h1")).toHaveText("Hourglass");

  const panel = page.locator(".consumption-panel");
  await panel.scrollIntoViewIfNeeded();
  // Ten minutes: the two minutes of samples spread over a fifth of the charts.
  await panel.getByRole("button", { name: "10 min" }).click();
  await expect(panel.getByRole("button", { name: "10 min" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  // The Resources tab shows the machine's CPU and memory charts as well as the two worker heatmaps side by side.
  await expect(panel.locator(".resource-machine .resource-chart")).toHaveCount(
    2,
  );
  await expect(panel.locator(".resource-heatmap-group")).toHaveCount(2);
  await expect(panel.locator(".resource-heatmap-switch")).toHaveCount(0);
  const worker = panel.locator(".resource-worker", { hasText: "W1" });
  // The title in full on the worker's title line: it wraps, never cut.
  await expect(worker.locator(".resource-worker-name")).toHaveText(title);
  const name = await worker
    .locator(".resource-worker-name")
    .evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
  expect(name.scroll).toBeLessThanOrEqual(name.client);
  // Larger charts: the CPU and memory of the worker, side by side.
  const box = await worker
    .locator(".resource-chart-plot")
    .first()
    .boundingBox();
  expect(box!.height).toBeGreaterThanOrEqual(180);
  if (process.env.RESOURCE_SHOTS) {
    await panel.screenshot({
      path: path.join(shots, "hourglass-resources-dark.png"),
    });
  }

  // W11: at 1280 px, the machine's CPU and memory charts and each worker's fit their card, the axis whole.
  await page.setViewportSize({ width: 1280, height: 1400 });
  // The widest overflow of a chart past its card, in px, once the charts have measured their new width.
  const overflow = () =>
    panel.evaluate((el) =>
      Math.max(
        ...[...el.querySelectorAll(".resource-chart")].map((chart) => {
          const svg = chart.querySelector("svg.resource-chart-svg")!;
          return Math.max(
            svg.getBoundingClientRect().width -
              chart.getBoundingClientRect().width,
            chart.scrollWidth - chart.clientWidth,
          );
        }),
      ),
    );
  await expect.poll(overflow).toBeLessThanOrEqual(1);
  await page.setViewportSize({ width: 1440, height: 1400 });

  // Machine has two tabs: the gates have their own tab in the Hourglass.
  await expect(panel.getByRole("tab")).toHaveText(["Resources", "Disk"]);
  // The gate W1 holds leads there, on the gate's card.
  await worker.locator(".resource-worker-gate").click();
  await expect(page.locator("#hourglass-tab-gates")).toHaveAttribute(
    "aria-selected",
    "true",
  );
  const gates = page.locator(".gates-tab");
  await expect(gates.locator("#gate-lint")).toBeInViewport();
  await expect(gates.locator("#gate-lint")).toContainText(
    "W1: go tool task lint, for",
  );
  await expect(gates.locator("#gate-lint")).toContainText(
    "a holder outside any task",
  );
  // The last hour: W1's band on lint.
  await expect(gates.locator(".gates-band")).toHaveCount(1);
  // The commands of every project, the most total time first; the top one marked.
  await expect(gates.locator(".gates-command-name code")).toHaveText([
    "go tool task e2e",
    "go tool task e2e",
    "go tool task lint",
    "go tool task e2e -- e2e/wish-render.spec.ts",
    "go tool task e2e -- e2e/resources.spec.ts",
    "go test -tags headless ./internal/machine/",
    "go test ./internal/store/",
  ]);
  await expect(gates.locator(".gates-command.is-top")).toContainText(
    "a project at rest",
  );
  // A line opens on its runs.
  await gates.locator(".gates-command-open").nth(2).click();
  await expect(gates.locator(".gates-runs tbody tr")).toHaveCount(1);
  // The waits by gate, the most held first.
  await expect(gates.locator(".gates-wait-row th")).toHaveText([
    "e2e",
    "test",
    "lint",
  ]);
  if (process.env.RESOURCE_SHOTS)
    for (const width of [1280, 760]) {
      await page.setViewportSize({ width, height: 1400 });
      await page.mouse.move(0, 0);
      await page.evaluate(() => {
        document.querySelector(".mission-scroll")!.scrollTop = 0;
      });
      await page.screenshot({
        path: path.join(shots, `hourglass-gates-tab-${width}-dark.png`),
        fullPage: true,
      });
    }
  await page.setViewportSize({ width: 1440, height: 1400 });
  // The holder's code leads back to its row on Machine.
  await gates
    .locator("#gate-lint")
    .getByRole("button", { name: "W1", exact: true })
    .click();
  await expect(page.locator("#hourglass-tab-machine")).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(worker).toBeInViewport();

  // Djinn apart from what it started, both named.
  await expect(panel.locator(".resource-legend li")).toHaveText([
    "Solid: the whole machine",
    "Dashed: Djinn with everything it started (window, the Djinn's agent, workers, gates)",
    "Dotted: Djinn's own process alone (djinn up)",
  ]);

  // Hover: the machine's CPU at its peak, a minute ago.
  const cpu = panel.locator(".resource-machine .resource-chart.cpu");
  const plot = cpu.locator(".resource-chart-plot");
  await panel.locator(".resource-machine").scrollIntoViewIfNeeded();
  const area = (await plot.boundingBox())!;
  // The peak is the 13th of 24 samples, a minute back: the pointer walks from the right until the readout says it.
  let seen = "";
  for (let x = area.width - 20; x > 60 && !seen.includes("95%"); x -= 2) {
    await page.mouse.move(area.x + x, area.y + area.height / 2);
    const readout = cpu.locator(".resource-chart-readout");
    seen = (await readout.count()) ? ((await readout.textContent()) ?? "") : "";
  }
  expect(seen).toContain("CPU 95%");
  // The readout names both of Djinn's series, its CPU a share of the whole machine.
  expect(seen).toContain("Djinn and what it started 8%");
  expect(seen).toContain("Djinn itself 1%");
  await expect(cpu.locator(".resource-chart-cursor")).toHaveCount(1);
  if (process.env.RESOURCE_SHOTS)
    await page.screenshot({
      path: path.join(shots, "resources-hover-dark.png"),
      clip: (await panel.locator(".resource-machine").boundingBox())!,
    });

  // The keys, the pointer away: the last sample.
  await page.mouse.move(0, 0);
  await expect(cpu.locator(".resource-chart-readout")).toHaveCount(0);
  await plot.focus();
  await page.keyboard.press("End");
  await expect(cpu.locator(".resource-chart-readout strong")).toHaveText(
    /CPU (30|55)%/,
  );
  // Live: the next read brings a sample at 55%, the chart's summary says it, the focus stays.
  await expect(cpu.locator(".resource-chart-now")).toContainText("now 55%", {
    timeout: 12_000,
  });
  await expect(plot).toBeFocused();
  await page.keyboard.press("End");
  await expect(cpu.locator(".resource-chart-readout strong")).toHaveText(
    "CPU 55%",
  );
  await page.keyboard.press("ArrowLeft");
  await expect(cpu.locator(".resource-chart-readout strong")).toHaveText(
    /CPU \d+%/,
  );
  await page.keyboard.press("Escape");
  await expect(cpu.locator(".resource-chart-readout")).toHaveCount(0);

  if (process.env.RESOURCE_SHOTS) {
    await plot.evaluate((el: SVGElement) => el.blur());
    await panel.locator(".consumption-panel-heading").scrollIntoViewIfNeeded();
    await panel.screenshot({
      path: path.join(shots, "hourglass-machine-dark.png"),
    });
    await panel.getByRole("tab", { name: "Disk" }).click();
    await panel.screenshot({
      path: path.join(shots, "hourglass-disk-dark.png"),
    });
    // The light theme across both tabs, and the Gates tab of the Hourglass.
    await page.evaluate(async () => {
      document.documentElement.dataset.theme = "light";
      await Promise.all(
        document
          .getAnimations()
          .filter((a) => a instanceof CSSTransition)
          .map((a) => a.finished.catch(() => {})),
      );
    });
    await page.mouse.move(0, 0);
    await panel.screenshot({
      path: path.join(shots, "hourglass-disk-light.png"),
    });
    await page.locator("#hourglass-tab-gates").click();
    await page.screenshot({
      path: path.join(shots, "hourglass-gates-tab-light.png"),
      fullPage: true,
    });
    await page.locator("#hourglass-tab-machine").click();
    await panel.getByRole("tab", { name: "Resources" }).click();
    await panel.screenshot({
      path: path.join(shots, "hourglass-resources-light.png"),
    });
    await panel.screenshot({ path: path.join(shots, "resources-light.png") });
    await page.evaluate(() => {
      document.documentElement.dataset.theme = "dark";
    });
  } else {
    await panel.getByRole("tab", { name: "Resources" }).click();
  }

  await expect(
    worker.locator(".resource-chart.cpu .resource-chart-now"),
  ).toContainText("now 70%");

  // The eye, by the keyboard: the wish opens on its Tasks tab, the task's card open and in sight.
  const eye = worker.getByRole("button", { name: "Open W1 in its wish" });
  await eye.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(".hero h1")).toHaveText(`Resources ${tag}`);
  const card = page.locator(`#task-${w1}`);
  await expect(card).toBeVisible();
  await expect(card).toHaveClass(/open/);
  await expect(card).toBeFocused();
  await expect(card).toContainText("Measure the oil.");
  expect(errors).toEqual([]);
});
