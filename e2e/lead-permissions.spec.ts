// Permission requests from a simulated Codex lead are answered in the window. No model runs.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const binary = path.resolve(__dirname, "../bin/djinn-e2e");
const session = "7f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f";
const codex = () => path.join(process.env.DJINN_E2E_HOME!, "bin", "codex");
function djinn(...args: string[]) {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

test.skip(process.platform === "win32", "the simulated lead is a POSIX script");
test.beforeAll(() =>
  fs.writeFileSync(
    codex(),
    `#!/bin/sh
case "$1" in --version|login) echo 0.0.0; exit 0;; esac
stty raw -echo
printf '\\033[2J\\033[HWould you like to run the following command?\\r\\n\\r\\n$ djinn wish brief\\r\\n\\r\\n› 1. Yes, proceed (y)\\r\\n  2. No, cancel\\r\\n\\r\\nPress enter to confirm or esc to cancel\\r\\n'
dd bs=1 count=1 >/dev/null 2>&1
printf '\\033[2J\\033[HAPPROVAL-RECEIVED\\r\\n› \\033[2mAsk Codex to do anything\\033[0m\\r\\n'
stty -raw echo
exec cat
`,
    { mode: 0o755 },
  ),
);
test.afterAll(async () => {
  const url = new URL(process.env.DJINN_URL!);
  const res = await fetch(new URL("/ui.v1.UiService/Show", url), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${url.searchParams.get("token")}`,
    },
    body: JSON.stringify({ terminal: "main" }),
  });
  expect(res.ok).toBe(true);
  fs.rmSync(codex(), { force: true });
});

test("the approval card checks the command and sends the user's answer", async ({
  page,
}) => {
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "permission-")),
  );
  const title = "Approve from the window";
  const wishId = JSON.parse(
    djinn("wish", "make", title, "--provider", "codex", "--json"),
  ).wish.id as string;
  djinn(
    "wish",
    "set-lead",
    wishId,
    session,
    "--provider",
    "codex",
    "--directory",
    folder,
  );
  djinn("wish", "resume", wishId);
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const card = page.locator(`#lead-prompt-${wishId}`);
    await expect(card).toContainText("$ djinn wish brief");
    await expect(
      card.getByRole("button", { name: "1 Yes, proceed (y)" }),
    ).toBeVisible();

    const url = new URL(process.env.DJINN_URL!);
    const stale = await page.request.post(
      new URL("/plan.v1.WishService/Choose", url).toString(),
      {
        headers: { Authorization: `Bearer ${url.searchParams.get("token")}` },
        data: {
          wishId,
          option: 1,
          title: "Would you like to run the following command?",
          options: ["Yes, proceed (y)", "No, cancel"],
          lines: ["$ a different command"],
        },
      },
    );
    expect(stale.status()).toBe(400);
    await expect(card).toBeVisible();

    // The request remains visible from the overview even when the terminal is folded away.
    await page.locator(".plan-nav").click();
    await expect(card).toContainText("$ djinn wish brief");
    await card.getByRole("button", { name: "1 Yes, proceed (y)" }).click();
    await expect(card).toHaveCount(0);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const rows = page.locator(".lead-terminal .xterm-rows > div");
    await expect(rows.filter({ hasText: "APPROVAL-RECEIVED" })).toHaveCount(1);

    await page
      .getByRole("button", { name: "Add an indication", exact: true })
      .click();
    const panel = page.getByRole("dialog", { name: "Indication for the lead" });
    const text = "Keep going after approval";
    await panel.getByLabel("An indication for this wish").fill(text);
    await panel.getByRole("button", { name: "Save the indication" }).click();
    await expect(panel.getByText("Saved · pending")).toBeVisible();
    await expect(panel.getByLabel("An indication for this wish")).toHaveValue(
      "",
    );

    // Approval leaves the lead ready for a wake referencing the durable record, rather than its raw text.
    const instructions = () =>
      JSON.parse(djinn("instruction", "list", wishId, "--json")).instructions;
    const saved = instructions();
    expect(saved).toHaveLength(1);
    const instruction = saved[0];
    expect(instruction).toMatchObject({
      id: expect.any(String),
      wish_id: wishId,
      code: "I01",
      text,
      status: "INSTRUCTION_STATUS_PENDING",
      create_time: expect.any(String),
      update_time: expect.any(String),
    });
    const wake = `Developer instruction ${instruction.code} (${instruction.id}) persisted.`;
    const read = `djinn instruction list ${wishId}`;
    // xterm may wrap the reference across screen rows.
    await expect
      .poll(async () => (await rows.allTextContents()).join(""), {
        timeout: 10_000,
      })
      .toContain(wake);
    await expect
      .poll(async () => (await rows.allTextContents()).join(""))
      .toContain(read);
    await expect(rows.filter({ hasText: text })).toHaveCount(0);

    await page.reload();
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const entry = page.locator(`#instruction-${instruction.id}`);
    await expect(entry).toContainText(text);
    await expect(entry.getByText("Pending", { exact: true })).toBeVisible();
    expect(instructions()).toEqual(saved);
  } finally {
    djinn("wish", "pause", wishId);
  }
});
