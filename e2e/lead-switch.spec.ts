// One lead for every wish, against the real djinn up --browser (see global-setup.ts): djinn lead resume --wish-id from
// the command line shows the wish and resumes the one lead in its terminal, focused on that wish; Talk to the Djinn on
// another wish brings the same lead's terminal and focuses it on that wish; switching the wish shown leaves that
// terminal as it is, and no terminal starts by itself. The lead is a fake claude on djinn's PATH that says how it was
// called, then repeats what it reads.
import { expect, test, type Page } from "./test";
import { execFileSync } from "node:child_process";
import path from "node:path";
import {
  dropLead,
  dropTerminalLead,
  leadFolder,
  leadRows as rows,
  setLead,
} from "./lead";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
// Not the session of the other specs: one session runs in one terminal.
const session = "3d5e7f90-1a2b-4c3d-8e4f-5a6b7c8d9e01";

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

function wish(title: string) {
  return JSON.parse(djinn("wish", "make", title, "--json")).wish.id as string;
}

// lead clicks Talk to the Djinn at the top of the wish shown.
async function lead(page: Page) {
  const button = page
    .locator(".topbar-actions")
    .getByRole("button", { name: "Talk to the Djinn", exact: true });
  await expect(button).toHaveAttribute(
    "title",
    "Opens the Djinn's terminal, focused on this wish",
  );
  await button.click();
}

test.skip(process.platform === "win32", "the fake claude is a shell script");

const made: string[] = [];
test.beforeEach(dropTerminalLead);
// The next specs find room for their wishes, and no lead running: after each test, before the fixture of e2e/test.ts
// reads the place.
test.afterEach(async () => {
  await dropLead();
  for (const id of made.splice(0)) djinn("wish", "delete", id);
});

test("Lead on each wish focuses the one lead on it, whatever wish is shown", async ({
  page,
}) => {
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const folder = leadFolder();
  setLead(session, folder);
  const glass = wish("Glass lamp");
  const brass = wish("Brass lamp");
  const other = wish("Lamp left alone");
  made.push(glass, brass, other);

  const url = new URL(process.env.DJINN_URL!);
  url.pathname = "/";
  await page.goto(url.toString());
  const terminal = page.getByRole("region", { name: "Terminal" });
  const command = terminal.locator(".lead-terminal-command").first();
  const started = `fake-claude --resume ${session} in ${folder}`;
  // No terminal starts by itself.
  await expect(command).toHaveCount(0);

  // djinn lead resume on Brass: the page shows Brass and the lead resumes on its session, told to focus on Brass.
  const resumed = djinn("lead", "resume", "--wish-id", brass);
  expect(resumed).toContain("terminal: lead");
  await expect(page.locator(".hero h1")).toHaveText("Brass lamp");
  await expect(command).toContainText(`claude --resume ${session}`);
  await expect(rows(page, started)).toHaveCount(1);
  // Typed in the lead, so echoed, then read after Enter: the fake lead says it again.
  await expect(
    rows(page, `Focus on the wish Brass lamp (${brass})`),
  ).toHaveCount(2);

  // Something typed to the lead: it says it back.
  await page.locator(".lead-terminal .xterm").click();
  await page.keyboard.type("polish the brass");
  await page.keyboard.press("Enter");
  await expect(rows(page, "polish the brass")).toHaveCount(2);

  // Another wish shown: the same lead stays, what it said still there, and no other terminal starts.
  await page.locator(".wish-nav", { hasText: "Lamp left alone" }).click();
  await expect(page.locator(".hero h1")).toHaveText("Lamp left alone");
  await expect(command).toContainText(`claude --resume ${session}`);
  await expect(rows(page, "polish the brass")).toHaveCount(2);
  await expect(terminal.locator(".lead-terminal-tab")).toHaveCount(1);

  // Lead on Glass: the same lead, not started again, now told to focus on Glass.
  await page.locator(".wish-nav", { hasText: "Glass lamp" }).click();
  await lead(page);
  await expect(
    rows(page, `Focus on the wish Glass lamp (${glass})`),
  ).toHaveCount(2);
  await expect(rows(page, started)).toHaveCount(1);
  await expect(rows(page, "polish the brass")).toHaveCount(2);
  await expect(terminal.locator(".lead-terminal-tab")).toHaveCount(1);
  expect(
    (
      JSON.parse(djinn("lead", "get", "--json")).lead as {
        session_id: string;
      }
    ).session_id,
  ).toBe(session);

  // The flying carpet shows no wish: the lead stays shown.
  await page.locator(".plan-nav").click();
  await expect(command).toContainText(`claude --resume ${session}`);
});
