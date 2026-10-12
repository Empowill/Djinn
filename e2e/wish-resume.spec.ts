// djinn lead resume against the real djinn up --browser (see global-setup.ts): the command line asks djinn to resume the
// one lead in the lead's terminal, focused on a wish, and to show that wish; the page follows. The lead is a fake claude
// on djinn's PATH that says how it was called, then repeats what it reads.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import path from "node:path";
import { dropLead, leadFolder, leadRows, setLead } from "./lead";

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

test.skip(process.platform === "win32", "the fake claude is a shell script");

test.afterEach(dropLead);

test("lead resume shows the wish and runs the lead in its terminal, focused on the wish", async ({
  page,
}) => {
  const folder = leadFolder();
  const made = djinn("wish", "make", "Resume in the window", "--json");
  const wishId = JSON.parse(made).wish.id as string;
  setLead(session, folder);
  const resumed = djinn("lead", "resume", "--wish-id", wishId);
  expect(resumed).toContain("terminal: lead");

  const url = new URL(process.env.DJINN_URL!);
  url.pathname = "/";
  await page.goto(url.toString());
  const terminal = page.getByRole("region", { name: "Terminal" });
  await expect(
    terminal.locator(".lead-terminal-command").first(),
  ).toContainText(`claude --resume ${session}`);
  await expect(
    leadRows(page, `fake-claude --resume ${session} in ${folder}`),
  ).toHaveCount(1);
  // Typed in the lead, so echoed, then read after Enter: the fake lead says it again.
  await expect(
    leadRows(page, `Focus on the wish Resume in the window (${wishId})`),
  ).toHaveCount(2);
  // The wish is the mission shown.
  await expect(page.getByText("Resume in the window").first()).toBeVisible();
  djinn("wish", "delete", wishId);
});
