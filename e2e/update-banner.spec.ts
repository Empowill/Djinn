// The update banner against the real djinn up --browser (see global-setup.ts), its update stream replaced by builds of
// two branches and a newer Djinn ready (W468): each on one line, named by commit, branch and wish, what it adds and
// lacks unfolded on demand, the other branch's build folded, and a × that dismisses it for good, across a reload; on
// a row of its own, over none of the toolbar's controls.
// Screenshots, dark and light, go to test-results/e2e/update-banner-*.png.
import { type MessageInitShape, create, toBinary } from "@bufbuild/protobuf";
import { type Page, expect, test } from "@playwright/test";
import path from "node:path";

import {
  BuildSchema,
  InstallStep,
  UiServiceWatchResponseSchema,
} from "../gen/ts/ui/v1/ui_pb";

const shots = path.join(__dirname, "../test-results/e2e");

const build = (init: MessageInitShape<typeof BuildSchema>) =>
  create(BuildSchema, {
    project: "djinn",
    base: "abcdef0123456789",
    ...init,
  });

// The update as Djinn would send it: the running Djinn's branch built ahead, Clément's branch built behind it, and
// Clément's build already installed at the path.
const side = build({
  wishTitle: "Clément's branches",
  branch: "feat/clement-port",
  sha: "e60f8fc000000000",
  tasks: ["W2"],
  summaries: [{ code: "W2", title: "Port Clément's work" }],
  changes: ["port: Clément's work"],
  changesCount: 1,
  lacks: ["ui: the tasks tab", "ui: the diff", "harness: azimas"],
  lacksCount: 3,
});
const update = create(UiServiceWatchResponseSchema, {
  update: {
    current: "local-gabcdef0",
    ready: "local-ge60f8fc",
    readyBuild: side,
    builds: [
      build({
        wishTitle: "Run Djinn on itself",
        branch: "feat/wails-go",
        sha: "aaaa1111bbbb2222",
        running: true,
        tasks: ["W466"],
        summaries: [{ code: "W466", title: "The tasks tab as a tree" }],
        changes: ["ui: the tasks tab as a tree"],
        changesCount: 1,
      }),
      side,
    ],
  },
});

// connectStream is a Connect server stream of one message, then its end, in the binary format the page reads.
function connectStream(message: Uint8Array): Buffer {
  const frame = (flags: number, body: Uint8Array) => {
    const head = Buffer.alloc(5);
    head.writeUInt8(flags, 0);
    head.writeUInt32BE(body.length, 1);
    return Buffer.concat([head, Buffer.from(body)]);
  };
  return Buffer.concat([
    frame(0, message),
    frame(2, new TextEncoder().encode("{}")),
  ]);
}

async function open(page: Page) {
  await page.route("**/ui.v1.UiService/Watch", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/connect+proto",
      body: connectStream(toBinary(UiServiceWatchResponseSchema, update)),
    }),
  );
  await page.goto(process.env.DJINN_URL!);
}

test("the update banner names each build, unfolds what it holds, and its × dismisses it for good", async ({
  page,
}) => {
  await open(page);
  await page.evaluate(() => localStorage.removeItem("djinn.update.dismissed"));
  await page.reload();
  const banner = page.locator(".update-banner");
  const parts = banner.locator(":scope > .update-banner-part");
  // The newer Djinn ready, then the running branch's build, then the other branch's folded.
  await expect(parts).toHaveCount(3);
  await expect(parts.nth(0).locator(".update-banner-text")).toHaveText(
    "New Djinn ready: e60f8fc0 of feat/clement-port · Clément's branches — adds 1 task · older than the running Djinn on 3 commits",
  );
  await expect(parts.nth(1).locator(".update-banner-text")).toHaveText(
    "Build aaaa1111 of feat/wails-go · Run Djinn on itself — adds 1 task",
  );
  await expect(parts.nth(2).locator(".update-banner-text")).toHaveText(
    "A build of another branch: feat/clement-port",
  );
  // Each part holds one line.
  for (const i of [0, 1, 2]) {
    const head = await parts
      .nth(i)
      .locator(".update-banner-head")
      .boundingBox();
    expect(head!.height).toBeLessThan(40);
  }
  // The banner takes its own row above the app: no control of the toolbar lies under it, the load slider's notches
  // first (laid over the app, it took their clicks).
  const box = await banner.boundingBox();
  const load = await page.locator(".operating-load").boundingBox();
  expect(load!.y).toBeGreaterThanOrEqual(box!.y + box!.height);
  for (const notch of ["minimal", "overclock", "auto"])
    await page
      .locator(`.operating-load-notch[data-notch="${notch}"]`)
      .click({ trial: true, timeout: 2_000 });
  for (const theme of ["dark", "light"]) {
    await page.evaluate((v) => localStorage.setItem("djinn.theme", v), theme);
    await page.reload();
    await expect(parts).toHaveCount(3);
    await banner.screenshot({
      path: path.join(shots, `update-banner-${theme}.png`),
    });
  }

  // The details of the ready Djinn: the tasks it adds, then the commits it lacks of the running one, folded.
  await parts.nth(0).getByRole("button", { name: "Details" }).click();
  const body = parts.nth(0).locator(".update-banner-body");
  await expect(body.locator(".update-banner-tasks")).toHaveText(
    "W2 Port Clément's work",
  );
  await body.getByText("Older than the running Djinn on 3 commits").click();
  await expect(body.locator(".update-banner-lacks li")).toHaveText([
    "ui: the tasks tab",
    "ui: the diff",
    "harness: azimas",
  ]);
  // The other branch's build unfolds on its own line, with its install and its ×.
  await parts.nth(2).getByRole("button", { name: "Details" }).click();
  const other = banner.locator(".update-banner-others .update-banner-part");
  await expect(other.locator(".update-banner-text")).toHaveText(
    "Build e60f8fc0 of feat/clement-port · Clément's branches — adds 1 task · older than the running Djinn on 3 commits",
  );
  await expect(
    other.getByRole("button", { name: "Install and restart" }),
  ).toBeVisible();

  // Dismissed, the ready Djinn and the side branch's build go, and stay gone after a reload.
  await parts.nth(0).getByRole("button", { name: "Dismiss" }).click();
  await expect(parts).toHaveCount(2);
  await other.getByRole("button", { name: "Dismiss" }).click();
  await expect(parts).toHaveCount(1);
  await page.reload();
  await expect(parts).toHaveCount(1);
  await expect(parts.first().locator(".update-banner-text")).toContainText(
    "feat/wails-go",
  );
  await page.evaluate(() => {
    localStorage.removeItem("djinn.update.dismissed");
    localStorage.removeItem("djinn.theme");
  });
});

// W554: the banner holds no spinner of its own. The window's call to install never answers (a Djinn frozen, a lost
// link): Djinn's stream says the install builds, then that it passed its deadline, with its last lines; the banner
// says so, and offers to try again.
test("an install that never answers ends at its deadline, as Djinn says it", async ({
  page,
}) => {
  const line = build({
    wishTitle: "Robustness",
    branch: "feat/robustness",
    sha: "bbbb2222cccc3333",
    running: true,
    changes: ["ui: the banner"],
    changesCount: 1,
  });
  const states = [
    { builds: [line] },
    {
      builds: [line],
      installing: {
        sha: line.sha,
        step: InstallStep.BUILDING,
        doing: "install: go build ./cmd/djinn",
      },
    },
    {
      builds: [line],
      installing: {
        sha: line.sha,
        step: InstallStep.FAILED,
        doing: "install: go build ./cmd/djinn",
        error:
          "it did not end within 20m0s, at install: go build ./cmd/djinn: Djinn stopped it",
        log: [
          "task: [ui] npm run build",
          "task: [install] go build ./cmd/djinn",
        ],
      },
    },
  ];
  let now = 0;
  await page.route("**/ui.v1.UiService/Watch", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/connect+proto",
      body: connectStream(
        toBinary(
          UiServiceWatchResponseSchema,
          create(UiServiceWatchResponseSchema, {
            update: { current: "local-abcdef0", ...states[now] },
          }),
        ),
      ),
    }),
  );
  // The call to install is never answered.
  let asked = 0;
  await page.route("**/ui.v1.UiService/Update", () => {
    asked++;
  });
  await page.goto(process.env.DJINN_URL!);
  await page.evaluate(() => localStorage.removeItem("djinn.update.dismissed"));
  const part = page.locator(".update-banner > .update-banner-part").first();
  await part.getByRole("button", { name: "Install and restart" }).click();
  await expect(part.locator(".update-banner-actions")).toContainText(
    "Installing…",
  );
  expect(asked).toBe(1);
  // Djinn says it builds, and which step runs.
  now = 1;
  await expect(part.locator(".update-banner-actions")).toContainText(
    "Building… (install: go build ./cmd/djinn)",
  );
  // Past the deadline: the failure, its last lines, Try again; no spinner, though the call still waits.
  now = 2;
  await expect(part.locator(".update-banner-text")).toHaveText(
    "Build bbbb2222 did not install: it did not end within 20m0s, at install: go build ./cmd/djinn: Djinn stopped it",
  );
  await expect(part.getByRole("button", { name: "Try again" })).toBeVisible();
  await expect(part).not.toContainText("Building…");
  await part.getByRole("button", { name: "Details" }).click();
  await expect(part.locator(".update-banner-log")).toHaveText(
    "task: [ui] npm run build\ntask: [install] go build ./cmd/djinn",
  );
  await part.screenshot({
    path: path.join(shots, "update-banner-failed.png"),
  });
});

// W554: a green push of the running line installed by itself: the banner only offers the restart, with what the
// newer Djinn adds; another branch's build stays offered, to install by a click.
test("a build of the running line installed by itself is offered as a restart, another branch's as an install", async ({
  page,
}) => {
  const line = build({
    wishTitle: "Robustness",
    branch: "feat/robustness",
    sha: "dddd4444eeee5555",
    running: true,
    tasks: ["W554"],
    summaries: [
      { code: "W554", title: "Builds of our own branch install by themselves" },
    ],
    changes: ["ui: the banner offers the restart"],
    changesCount: 1,
  });
  const other = build({
    wishTitle: "Side",
    branch: "feat/side",
    sha: "ffff6666aaaa7777",
    changes: ["side: work"],
    changesCount: 1,
  });
  await page.route("**/ui.v1.UiService/Watch", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/connect+proto",
      body: connectStream(
        toBinary(
          UiServiceWatchResponseSchema,
          create(UiServiceWatchResponseSchema, {
            update: {
              current: "local-abcdef0",
              ready: "local-dddd444",
              installed: true,
              readyBuild: line,
              builds: [other],
            },
          }),
        ),
      ),
    }),
  );
  await page.goto(process.env.DJINN_URL!);
  await page.evaluate(() => localStorage.removeItem("djinn.update.dismissed"));
  await page.reload();
  const parts = page.locator(".update-banner > .update-banner-part");
  await expect(parts).toHaveCount(2);
  await expect(parts.nth(0).locator(".update-banner-text")).toHaveText(
    "Djinn local-dddd444 is ready: dddd4444 of feat/robustness · Robustness — adds 1 task",
  );
  await expect(
    parts.nth(0).getByRole("button", { name: "Restart" }),
  ).toBeVisible();
  await expect(parts.nth(1).locator(".update-banner-text")).toHaveText(
    "A build of another branch: feat/side",
  );
  await parts.nth(1).getByRole("button", { name: "Details" }).click();
  await expect(
    page
      .locator(".update-banner-others")
      .getByRole("button", { name: "Install and restart" }),
  ).toBeVisible();
  await page.locator(".update-banner").screenshot({
    path: path.join(shots, "update-banner-restart.png"),
  });
});
