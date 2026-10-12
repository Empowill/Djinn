// A lead that could not start, or ended at once, stays visible until Lead starts it again, against the real djinn up
// --browser (see global-setup.ts): a dot on Lead, its tooltip saying why, and the lead's tab keeps its last output.
// The lead is a fake codex, a POSIX script that fails while a marker file exists. No model runs.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { dropLead, leadFolder, leadRows as rows } from "./lead";

const binary = path.resolve(__dirname, "../bin/djinn-e2e");
// Not the session of another spec: one session runs in one terminal.
const session = "5d2e8f1a-3b4c-4d5e-8f6a-7b8c9d0e1f2a";
const codex = () => path.join(process.env.DJINN_E2E_HOME!, "bin", "codex");
const failing = () => path.join(process.env.DJINN_E2E_HOME!, "codex-fails");

function djinn(...args: string[]) {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

test.skip(process.platform === "win32", "the fake codex is a POSIX script");
test.beforeAll(() =>
  fs.writeFileSync(
    codex(),
    `#!/bin/sh
case "$1" in --version|login) echo 0.0.0; exit 0;; esac
if [ -f "$DJINN_HOME/codex-fails" ]; then
  echo FAKE-CODEX-LEAD-FAILURE
  exit 2
fi
echo FAKE-CODEX-READY
exec cat
`,
    { mode: 0o755 },
  ),
);
test.afterEach(async () => {
  fs.rmSync(failing(), { force: true });
  await dropLead();
});
test.afterAll(() => fs.rmSync(codex(), { force: true }));

// openWish makes a wish and shows it, its Lead button at the top.
async function openWish(page: import("@playwright/test").Page, title: string) {
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);
  const id = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  const url = new URL(process.env.DJINN_URL!);
  url.pathname = "/";
  await page.goto(url.toString());
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  return id;
}

test("an ended lead retains output and retries explicitly", async ({
  page,
}) => {
  fs.writeFileSync(failing(), "1");
  djinn(
    "lead",
    "set",
    session,
    "--provider",
    "codex",
    "--directory",
    leadFolder("ended-"),
  );
  const id = await openWish(page, "Show a lead that ended");
  try {
    const lead = page
      .locator(".split-button")
      .getByRole("button", { name: "Talk to the Djinn", exact: true });
    const dot = lead.locator(".lead-dot");
    await expect(dot).toHaveCount(0);

    // The lead ends at once: the dot says how, and its tab keeps what it wrote.
    await lead.click();
    await expect(dot).toHaveClass(/failed/);
    await expect(lead).toHaveAttribute(
      "title",
      "The Djinn ended (code 2): its tab keeps its last output. Talk to the Djinn starts it again",
    );
    await expect(rows(page, "FAKE-CODEX-LEAD-FAILURE")).toHaveCount(1);
    await expect(
      page.getByRole("tab", { name: "The Djinn", exact: true }),
    ).toBeVisible();
    await expect(page.locator(".lead-terminal-state")).toHaveText("exited (2)");

    // After a reload too: nothing started it again.
    await page.reload();
    await page
      .locator(".wish-nav")
      .filter({ hasText: "Show a lead that ended" })
      .click();
    await expect(dot).toHaveClass(/failed/);
    await page.getByRole("tab", { name: "The Djinn", exact: true }).click();
    await expect(rows(page, "FAKE-CODEX-LEAD-FAILURE")).toHaveCount(1);

    // Lead, explicitly: a new program, which runs; the dot goes.
    fs.rmSync(failing(), { force: true });
    await lead.click();
    await expect(dot).toHaveCount(0);
    await expect(rows(page, "FAKE-CODEX-READY")).toHaveCount(1);
    await expect(rows(page, "FAKE-CODEX-LEAD-FAILURE")).toHaveCount(0);
  } finally {
    djinn("wish", "delete", id);
  }
});

test("a lead that could not start shows why on Lead until it starts", async ({
  page,
}) => {
  const folder = leadFolder("gone-lead-");
  djinn("lead", "set", session, "--provider", "codex", "--directory", folder);
  fs.rmSync(folder, { recursive: true, force: true });
  const id = await openWish(page, "Show a lead that could not start");
  try {
    const lead = page
      .locator(".split-button")
      .getByRole("button", { name: "Talk to the Djinn", exact: true });
    await lead.click();
    await expect(lead.locator(".lead-dot")).toHaveClass(/failed/);
    await expect(lead).toHaveAttribute(
      "title",
      /^The Djinn could not start: the lead's folder .* is not on this machine/,
    );

    // The folder is back: Lead starts it, and the dot goes.
    fs.mkdirSync(folder);
    await lead.click();
    await expect(lead.locator(".lead-dot")).toHaveCount(0);
    await expect(rows(page, "FAKE-CODEX-READY")).toHaveCount(1);
  } finally {
    djinn("wish", "delete", id);
  }
});
