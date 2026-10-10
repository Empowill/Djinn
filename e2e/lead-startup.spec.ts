// Lead startup through the window uses the provider command in a retained terminal. This spec keeps the Codex
// executable fake and checks the lifecycle the user sees: a new active wish starts once, a paused wish waits, and an
// ended lead keeps its output until an explicit retry.
import { expect, test, type Locator, type Page } from "@playwright/test";
import { fromBinary } from "@bufbuild/protobuf";
import {
  WishServiceMakeResponseSchema,
  WishState,
} from "../gen/ts/plan/v1/plan_pb";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { disableWishSmokeWebGL } from "./wish-smoke-fixture";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const codex = () => path.join(process.env.DJINN_E2E_HOME!, "bin", "codex");
const failureMarker = () =>
  path.join(process.env.DJINN_E2E_HOME!, "codex-lead-fails");

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

async function show(terminal: string) {
  const url = new URL(process.env.DJINN_URL!);
  const res = await fetch(new URL("/ui.v1.UiService/Show", url), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${url.searchParams.get("token")}`,
    },
    body: JSON.stringify({ terminal }),
  });
  expect(res.ok).toBe(true);
}

test.skip(process.platform === "win32", "the fake Codex is a shell script");
test.beforeEach(async ({ page }) => disableWishSmokeWebGL(page));

// The browser e2e server is shared by specs. Install the fake only for these tests and return its terminal to main
// afterward so the next spec never follows a stale lead.
test.beforeAll(() =>
  fs.writeFileSync(
    codex(),
    `#!/bin/sh
case "$1" in
  --version|login|status) echo 0.0.0; exit 0 ;;
esac
approve=
sandbox=
approval=
for arg in "$@"; do
  case "$arg" in
    --approve-for-me) approve=1 ;;
    --sandbox) sandbox=1 ;;
    --ask-for-approval) approval=1 ;;
  esac
done
if [ "$approve" = 1 ] && { [ "$sandbox" = 1 ] || [ "$approval" = 1 ]; }; then
  echo FAKE-CODEX-MIXED-FLAGS
  exit 2
fi
if [ -f "$DJINN_HOME/codex-lead-fails" ]; then
  echo FAKE-CODEX-LEAD-FAILURE
  exit 2
fi
if [ "$approve" = 1 ]; then
  echo "FAKE-CODEX-AUTO --approve-for-me"
else
  echo FAKE-CODEX-ARGS
fi
exit 0
`,
    { mode: 0o755 },
  ),
);

test.afterAll(async () => {
  await show("main");
  fs.rmSync(codex(), { force: true });
  fs.rmSync(failureMarker(), { force: true });
});

const created: string[] = [];

// Active wishes from earlier specs can occupy the shared server's three slots. Free a slot only when needed for the
// active startup cases; each wish made here is paused again after its test.
function ensureActiveSlot() {
  const listed = (JSON.parse(djinn("wish", "list", "--json")).wishes ?? []) as {
    id: string;
    state: string;
  }[];
  const active = listed.filter((wish) => wish.state === "WISH_STATE_ACTIVE");
  if (active.length < 3) return;
  djinn("wish", "pause", active[active.length - 1].id);
}

test.afterEach(() => {
  for (const id of created.splice(0)) {
    try {
      djinn("wish", "pause", id);
    } catch {
      // A failed test may already have left the wish paused or gone.
    }
  }
  fs.rmSync(failureMarker(), { force: true });
});

// Capture the made wish from the service response: the prompt is deliberately separate from its provisional title.
async function submitWish(page: Page, dialog: Locator, button: string) {
  const response = page.waitForResponse((res) =>
    res.url().endsWith("/plan.v1.WishService/Make"),
  );
  await dialog.getByRole("button", { name: button, exact: true }).click();
  const made = fromBinary(
    WishServiceMakeResponseSchema,
    new Uint8Array(await (await response).body()),
  );
  if (!made.wish) throw new Error("Make returned no wish");
  created.push(made.wish.id);
  return made.wish;
}

async function chooseDropdown(dialog: Locator, label: string, option: string) {
  await dialog.getByRole("button", { name: label, exact: true }).click();
  await dialog.getByRole("option", { name: option, exact: true }).click();
}

test("an active Codex wish starts once with Auto flags", async ({ page }) => {
  ensureActiveSlot();
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lead-start-")),
  );
  const projectName = `Lead startup ${path.basename(folder)}`;
  djinn("project", "add", folder, "--name", projectName);
  const title =
    "Auto launch the Codex lead\n" + "Keep the full request. ".repeat(35);

  await page.goto(process.env.DJINN_URL!);
  await page.locator(".sidebar .new-mission").click();
  const dialog = page.locator(".wish-creation");
  const editor = dialog.getByRole("textbox", {
    name: "What would you like to make happen?",
  });
  await expect(editor).toBeFocused();
  await expect(
    dialog.getByRole("button", { name: "Make the wish", exact: true }),
  ).toBeDisabled();
  await editor.fill(title);
  await dialog.locator(".wish-tag-dropdown").getByRole("button").click();
  await dialog.getByRole("searchbox").fill(projectName);
  await expect(dialog.locator(".wish-project-option")).toHaveCount(1);
  await expect(
    dialog.getByRole("checkbox", { name: projectName }),
  ).toHaveAccessibleDescription(folder);
  await dialog.getByRole("checkbox", { name: projectName }).check();
  await page.screenshot({ path: "test-results/wish-creation.png" });
  await chooseDropdown(dialog, "Agent", "Codex");
  await chooseDropdown(
    dialog,
    "Permissions in the selected projects",
    "Edit, in auto mode",
  );
  const wish = await submitWish(page, dialog, "Make the wish");
  await expect(dialog).toHaveCount(0);

  const status = page.locator(".lead-status");
  await expect(page.locator(".hero h1")).toHaveText(wish.title);
  expect(wish.title).not.toBe(title);
  const brief = djinn("wish", "brief", wish.id);
  expect(brief).toContain("Auto launch the Codex lead");
  expect(brief).not.toContain("**Auto launch the Codex lead");
  expect(brief).toContain("Keep the full request. ".repeat(35).trim());
  await expect(status).toContainText("Lead ended (0)");

  const rows = page.locator(".lead-terminal .xterm-rows > div");
  await expect(rows.filter({ hasText: "FAKE-CODEX-AUTO" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "--approve-for-me" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "--sandbox" })).toHaveCount(0);
  await expect(rows.filter({ hasText: "--ask-for-approval" })).toHaveCount(0);
  await expect(rows.filter({ hasText: "FAKE-CODEX-MIXED-FLAGS" })).toHaveCount(
    0,
  );
  // The lead authors a title through the same public command; the live wish keeps its original request.
  djinn("wish", "rename", wish.id, "Launch the Codex lead");
  await expect(page.locator(".hero h1")).toHaveText("Launch the Codex lead");
  expect(djinn("wish", "brief", wish.id)).toContain("Keep the full request.");
});

test("a paused wish stays unstarted until the user starts it", async ({
  page,
}) => {
  const active = (
    JSON.parse(djinn("wish", "list", "--json")).wishes ?? []
  ).filter((wish: { state: string }) => wish.state === "WISH_STATE_ACTIVE");
  for (let i = active.length; i < 3; i++) {
    const reserve = JSON.parse(
      djinn("wish", "make", `Reserved startup slot ${i}`, "--json"),
    ).wish;
    created.push(reserve.id);
  }
  const title = "Paused Codex lead";
  const starts: string[] = [];
  page.on("request", (request) => {
    if (request.url().endsWith("/plan.v1.WishService/Resume"))
      starts.push(request.url());
  });
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".sidebar .new-mission").click();
  const dialog = page.locator(".wish-creation");
  await dialog
    .getByRole("textbox", { name: "What would you like to make happen?" })
    .fill(title);
  await chooseDropdown(dialog, "Agent", "Codex");
  const wish = await submitWish(page, dialog, "Make it paused");
  await expect(dialog).toHaveCount(0);
  await expect(page.locator(".hero h1")).toHaveText(wish.title);
  expect(wish.title).not.toBe(title);
  expect(djinn("wish", "brief", wish.id)).toContain(title);
  await expect(page.locator(".lead-status")).toContainText("Lead not started");
  await expect(
    page.locator(".lead-status").getByRole("button", { name: "Start lead" }),
  ).toBeVisible();
  expect(wish.lead).toBeUndefined();
  expect(wish.state).toBe(WishState.PAUSED);
  expect(starts).toEqual([]);
});

test("an ended lead retains output and retries explicitly", async ({
  page,
}) => {
  ensureActiveSlot();
  fs.writeFileSync(failureMarker(), "1");
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lead-fail-")),
  );
  const projectName = `Lead failure ${path.basename(folder)}`;
  djinn("project", "add", folder, "--name", projectName);
  const title = "Show a failed lead";

  await page.goto(process.env.DJINN_URL!);
  await page.locator(".sidebar .new-mission").click();
  const dialog = page.locator(".wish-creation");
  await dialog
    .getByRole("textbox", { name: "What would you like to make happen?" })
    .fill(title);
  await dialog.locator(".wish-tag-dropdown").getByRole("button").click();
  await dialog.getByRole("checkbox", { name: projectName }).check();
  await chooseDropdown(dialog, "Agent", "Codex");
  await chooseDropdown(
    dialog,
    "Permissions in the selected projects",
    "Edit, in auto mode",
  );
  const wish = await submitWish(page, dialog, "Make the wish");
  await expect(dialog).toHaveCount(0);

  const status = page.locator(".lead-status");
  await expect(status).toContainText("Lead ended (2)");
  await expect(
    status.getByRole("button", { name: "Retry lead" }),
  ).toBeVisible();
  const rows = page.locator(".lead-terminal .xterm-rows > div");
  await expect(rows.filter({ hasText: "FAKE-CODEX-LEAD-FAILURE" })).toHaveCount(
    1,
  );

  await status.getByRole("button", { name: "View terminal" }).click();
  await expect(rows.filter({ hasText: "FAKE-CODEX-LEAD-FAILURE" })).toHaveCount(
    1,
  );

  fs.rmSync(failureMarker(), { force: true });
  await status.getByRole("button", { name: "Retry lead" }).click();
  await expect(status).toContainText("Lead ended (0)");
  await expect(rows.filter({ hasText: "FAKE-CODEX-AUTO" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "FAKE-CODEX-LEAD-FAILURE" })).toHaveCount(
    0,
  );
});
