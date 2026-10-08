// djinn wish resume against the real djinn up --browser (see global-setup.ts): the command line asks djinn to show the
// wish and to resume its lead in the lead's terminal, and the page follows. The lead is a fake claude on djinn's PATH.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const session = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69";

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

// show asks djinn for a terminal, as the command line asks for the lead's.
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

test.skip(process.platform === "win32", "the fake claude is a shell script");

// The next specs find the window's own terminal again.
test.afterAll(() => show("main"));

test("wish resume shows the wish and runs its lead in the lead's terminal", async ({
  page,
}) => {
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lead-")),
  );
  const made = djinn("wish", "make", "Resume in the window", "--json");
  const wishId = JSON.parse(made).wish.id as string;
  djinn("wish", "set-lead", wishId, session, "--directory", folder);
  const resumed = djinn("wish", "resume", wishId);
  expect(resumed).toContain(`terminal: lead-${wishId}`);

  const url = new URL(process.env.DJINN_URL!);
  url.pathname = "/";
  await page.goto(url.toString());
  const terminal = page.getByRole("region", { name: "Terminal" });
  await expect(
    terminal.locator(".lead-terminal-command").first(),
  ).toContainText(`claude --resume ${session}`);
  await expect(
    page
      .locator(".lead-terminal .xterm-rows > div")
      .filter({ hasText: `fake-claude --resume ${session} in ${folder}` }),
  ).toHaveCount(1);
  // The wish is the mission shown.
  await expect(page.getByText("Resume in the window").first()).toBeVisible();
});
