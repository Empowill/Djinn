// The update banner against the real djinn up --browser (see global-setup.ts), its update stream replaced by builds of
// two branches and a newer Djinn ready (W468): each on one line, named by commit, branch and wish, what it adds and
// lacks unfolded on demand, the other branch's build folded, and a × that dismisses it for good, across a reload.
// Screenshots, dark and light, go to test-results/e2e/update-banner-*.png.
import { type MessageInitShape, create, toBinary } from "@bufbuild/protobuf";
import { type Page, expect, test } from "@playwright/test";
import path from "node:path";

import {
  BuildSchema,
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
