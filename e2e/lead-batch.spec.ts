// The Djinn's eye and the side Lead journal during a batch of the driven lead (L44 v3, Decision Q112 B, W456):
// the eye turns blue with a wandering gaze while thinking, blinks twice with a golden flame flare when done, and turns
// red under a lowered lid when a batch failed; What runs shows the Lead journal with one line per command as the batch
// goes, then the duration and cost when done or the cause when it failed.
// Run alone with: go tool task e2e -- e2e/lead-batch.spec.ts
import { create, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import {
  LeadBatchState,
  LeadServiceGetResponseSchema,
  Provider,
} from "../gen/ts/plan/v1/plan_pb";
import { agentsBin, fakeAgents } from "./agents";
import { expect, type Page, test } from "./test";

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

function pauseOthers() {
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);
}

const pillEye = (page: Page) =>
  page.locator(
    ".workspace-action .lead-socket > .djinn-eye-perch > .djinn-eye",
  );

test("a driven batch turns the eye blue with a wandering gaze, streams each command to the side journal under 2 s, and flares gold when done", async ({
  page,
}) => {
  test.setTimeout(60_000);
  pauseOthers();
  const home = process.env.DJINN_E2E_HOME!;
  const cmd1File = path.join(home, "lead-cmd1-ms.txt");
  const step2File = path.join(home, "lead-step2.gate");
  const step3File = path.join(home, "lead-step3.gate");
  for (const f of [cmd1File, step2File, step3File])
    fs.rmSync(f, { force: true });

  const suffix = randomUUID().slice(0, 8);
  const title = `Batch ${suffix}`;
  const created = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish as { id: string; code: string };
  const wishId = created.id;
  const wishCode = created.code || "W1";

  try {
    // Install a gated fake claude before unpausing/sending so we control the batch steps and measure command latency.
    fs.writeFileSync(
      path.join(agentsBin(), "claude"),
      `#!/bin/sh
args=""
headless=0
while [ $# -gt 0 ]; do
  case "$1" in
    --allowedTools|--project) shift 2 ;;
    -p) headless=1; shift ;;
    *) args="\${args:+$args }$1"; shift ;;
  esac
done
if [ "$headless" = "1" ] && [ "$DJINN_LEAD" = "1" ]; then
  read -r _ || true
  date +%s%3N > "${cmd1File}"
  echo '{"type":"assistant","session_id":"019d0000-0000-7000-8000-000000000099","message":{"model":"claude-opus-4-7","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"djinn wish get ${wishCode}"}}]}}'
  while [ ! -f "${step2File}" ]; do sleep 0.02; done
  echo '{"type":"assistant","session_id":"019d0000-0000-7000-8000-000000000099","message":{"model":"claude-opus-4-7","content":[{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"djinn task list --wish W99"}}]}}'
  while [ ! -f "${step3File}" ]; do sleep 0.02; done
  echo '{"type":"assistant","session_id":"019d0000-0000-7000-8000-000000000099","message":{"model":"claude-opus-4-7","content":[{"type":"text","text":"Batch completed."}]}}'
  echo '{"type":"result","subtype":"success","session_id":"019d0000-0000-7000-8000-000000000099","total_cost_usd":0.18}'
  exit 0
fi
echo "fake-claude $args in $(pwd)"
exec cat
`,
      { mode: 0o755 },
    );

    djinn("wish", "activate", wishId);
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(pillEye(page)).toBeVisible({ timeout: 10_000 });

    // Send a message from Write to the Djinn to trigger a driven batch on this wish.
    await page.keyboard.press(
      process.platform === "darwin" ? "Meta+I" : "Control+I",
    );
    const writing = page.getByRole("dialog", { name: "To the Djinn" });
    await writing.locator("textarea").fill("Check the lamp");
    await writing.locator("textarea").press("Enter");

    const log = page.locator(".glance .lead-side-log");
    const lines = log.locator(".lead-side-log-line");

    // Wait for the first command line to appear in the window and measure the latency from stdout emission.
    await expect(lines.first()).toHaveText(`› wish get ${wishCode}`, {
      timeout: 15_000,
    });
    const seenMs = Date.now();
    const emittedMs = Number(fs.readFileSync(cmd1File, "utf8").trim());
    const latencyMs = seenMs - emittedMs;
    console.log(`LEAD_CMD_TO_WINDOW_MS=${latencyMs}`);
    expect(latencyMs).toBeGreaterThanOrEqual(0);
    expect(latencyMs).toBeLessThan(2_000);

    // While the batch runs, the first command is marked .now and the eye is in the thinking mood.
    await expect(lines.first()).toHaveClass(/now/);
    await expect(pillEye(page)).toHaveAttribute("data-mood", "thinking");

    // Release the second command (on another wish W99) and check it appends below the first.
    fs.writeFileSync(step2File, "ok");
    await expect(lines).toHaveCount(2);
    await expect(lines.nth(0)).toHaveText(`› wish get ${wishCode}`);
    await expect(lines.nth(0)).not.toHaveClass(/now/);
    await expect(lines.nth(1)).toHaveText("› task list --wish W99");
    await expect(lines.nth(1)).toHaveClass(/now/);

    // Release the end of the batch: the eye flares gold (data-end="done") and the journal shows the done line.
    fs.writeFileSync(step3File, "ok");
    await expect(lines).toHaveCount(3);
    await expect(lines.nth(2)).toHaveClass(/done/);
    await expect(lines.nth(2)).toContainText("✓ done ·");
    await expect(lines.nth(2)).toContainText("$0.18");
    await expect(pillEye(page)).toHaveAttribute("data-end", "done");

    // After the 2.65 s completion flare, the eye returns to listening with no data-end.
    await expect(pillEye(page)).not.toHaveAttribute("data-end", "done", {
      timeout: 5_000,
    });
    await expect(pillEye(page)).toHaveAttribute("data-mood", "listening");
  } finally {
    fakeAgents(agentsBin());
    for (const f of [cmd1File, step2File, step3File])
      fs.rmSync(f, { force: true });
    djinn("wish", "pause", wishId);
  }
});

test("a failed batch in light theme turns the eye red and writes the failure cause at the end of the side journal", async ({
  page,
}) => {
  pauseOthers();
  const suffix = randomUUID().slice(0, 8);
  const title = `Failed batch ${suffix}`;
  const wish = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish as { id: string; code: string };
  djinn("wish", "activate", wish.id);
  const start = timestampFromDate(new Date(Date.now() - 8_000));
  const end = timestampFromDate(new Date());

  await page.route("**/plan.v1.LeadService/Get", (route) =>
    route.fulfill({
      contentType: "application/proto",
      body: Buffer.from(
        toBinary(
          LeadServiceGetResponseSchema,
          create(LeadServiceGetResponseSchema, {
            driven: true,
            seat: { provider: Provider.CLAUDE, model: "claude-opus-4-7" },
            batch: {
              id: "b-fail",
              state: LeadBatchState.FAILED,
              startTime: start,
              endTime: end,
              provider: Provider.CLAUDE,
              model: "claude-opus-4-7",
              wishIds: [wish.id],
              commands: [`djinn wish get ${wish.code || "W1"}`],
              error: "session exited with code 1",
            },
          }),
        ),
      ),
    }),
  );

  try {
    await page.goto(process.env.DJINN_URL!);
    await page.evaluate(() => {
      localStorage.setItem("djinn.theme", "light");
      document.documentElement.dataset.theme = "light";
    });
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(page.locator('html[data-theme="light"]')).toHaveCount(1);

    const eye = pillEye(page);
    await expect(eye).toBeVisible({ timeout: 10_000 });
    await expect(eye).toHaveAttribute("data-mood", "failed");

    const lines = page.locator(".glance .lead-side-log .lead-side-log-line");
    await expect(lines).toHaveCount(2);
    await expect(lines.nth(0)).toHaveText(`› wish get ${wish.code || "W1"}`);
    await expect(lines.nth(1)).toHaveClass(/failed/);
    await expect(lines.nth(1)).toHaveText("✕ session exited with code 1");
  } finally {
    djinn("wish", "pause", wish.id);
  }
});
