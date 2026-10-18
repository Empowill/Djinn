// An approval the lead's agent asks for in its terminal is answered in the window, against the real djinn up --browser
// (see global-setup.ts): the attention bar shows it as a blocking line, its options as buttons, in the flying carpet as
// in every wish; a click goes to the lead's terminal as its keys would, only while it shows that very choice. The lead
// is a fake codex, a POSIX script: it draws Codex's approval of a command, reads one key, then draws its prompt. No
// model runs.
import { expect, test } from "./test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { dropLead, leadFolder, leadRows as rows } from "./lead";

const binary = path.resolve(__dirname, "../bin/djinn-e2e");
// Not the session of another spec: one session runs in one terminal.
const session = "7f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f";
const codex = () => path.join(process.env.DJINN_E2E_HOME!, "bin", "codex");
const title = "Would you like to run the following command?";

function djinn(...args: string[]) {
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

test.skip(process.platform === "win32", "the fake codex is a POSIX script");
test.beforeAll(() =>
  fs.writeFileSync(
    codex(),
    `#!/bin/sh
case "$1" in --version|login) echo 0.0.0; exit 0;; esac
stty raw -echo
printf '\\033[2J\\033[H${title}\\r\\n\\r\\n$ djinn wish brief\\r\\n\\r\\n› 1. Yes, proceed (y)\\r\\n  2. No, cancel\\r\\n\\r\\nPress enter to confirm or esc to cancel\\r\\n'
dd bs=1 count=1 >/dev/null 2>&1
printf '\\033[2J\\033[HAPPROVAL-RECEIVED\\r\\n› \\033[2mAsk Codex to do anything\\033[0m\\r\\n'
stty -raw echo
exec cat
`,
    { mode: 0o755 },
  ),
);
test.afterEach(dropLead);
test.afterAll(() => fs.rmSync(codex(), { force: true }));

test("the lead's approval shows in the attention bar, checks the command and sends the developer's answer", async ({
  page,
}) => {
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);
  const wishTitle = "Approve from the window";
  const wishId = JSON.parse(djinn("wish", "make", wishTitle, "--json")).wish
    .id as string;
  djinn(
    "lead",
    "set",
    session,
    "--provider",
    "codex",
    "--directory",
    leadFolder("permission-"),
  );
  djinn("lead", "resume");
  try {
    const url = new URL(process.env.DJINN_URL!);
    url.pathname = "/";
    await page.goto(url.toString());
    const item = page.locator("#lead-prompt");
    await expect(item).toContainText("Approval for the Djinn");
    await expect(item).toContainText(title);
    await expect(item).toContainText("$ djinn wish brief");
    await expect(
      item.getByRole("button", { name: "Yes, proceed (y)" }),
    ).toBeVisible();
    await expect(
      item.getByRole("button", { name: "No, cancel" }),
    ).toBeVisible();

    // The same options on another command are another decision: refused, and nothing is typed.
    const token = url.searchParams.get("token");
    const stale = await page.request.post(
      new URL("/plan.v1.LeadService/Choose", url).toString(),
      {
        headers: { Authorization: `Bearer ${token}` },
        data: {
          option: 1,
          title,
          options: ["Yes, proceed (y)", "No, cancel"],
          lines: ["$ a different command"],
        },
      },
    );
    expect(stale.status()).toBe(400);
    await expect(item).toBeVisible();

    // The line shows in every wish too: the one lead serves them all. Its button answers in the lead's terminal.
    await page.locator(".wish-nav").filter({ hasText: wishTitle }).click();
    await expect(item).toContainText("$ djinn wish brief");
    await item.getByRole("button", { name: "Yes, proceed (y)" }).click();
    await expect(item).toHaveCount(0);
    await show("lead");
    await expect(rows(page, "APPROVAL-RECEIVED")).toHaveCount(1);
  } finally {
    djinn("wish", "delete", wishId);
  }
});
