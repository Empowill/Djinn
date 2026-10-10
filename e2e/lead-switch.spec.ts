// Each wish shows its own lead, against the real djinn up --browser (see global-setup.ts): the wish shown in the
// window brings its lead's terminal while it runs; otherwise the tab shown stays, and no terminal starts by itself. The leads are a fake
// claude on djinn's PATH that says how it was called, then repeats what it reads.
import { expect, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
// Not the sessions of the other specs: one session runs in one terminal.
const sessions = {
  brass: "3d5e7f90-1a2b-4c3d-8e4f-5a6b7c8d9e01",
  glass: "9e8d7c6b-5a4f-4e3d-9c2b-1a0f9e8d7c02",
};

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

// wishWithLead makes a wish whose lead runs, in a folder of its own.
function wishWithLead(title: string, session: string) {
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lead-")),
  );
  const id = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  djinn("wish", "set-lead", id, session, "--directory", folder);
  expect(djinn("wish", "resume", id)).toContain(`terminal: lead-${id}`);
  return { id, folder };
}

function rows(page: Page, text: string) {
  return page.locator(".lead-terminal .xterm-rows > div").filter({
    hasText: text,
  });
}

test.skip(process.platform === "win32", "the fake claude is a shell script");

const made: string[] = [];
// The next specs find the window's own terminal again, and room for their wishes.
test.afterAll(async () => {
  for (const id of made) djinn("wish", "pause", id);
  await show("main");
});

test("switching the wish switches the terminal to its own lead", async ({
  page,
}) => {
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const glass = wishWithLead("Glass lamp", sessions.glass);
  // Resumed last, so the window opens on it: djinn asked to show it.
  const brass = wishWithLead("Brass lamp", sessions.brass);
  const none = JSON.parse(
    djinn("wish", "make", "Lamp without a lead", "--json"),
  ).wish.id as string;
  made.push(glass.id, brass.id, none);

  const url = new URL(process.env.DJINN_URL!);
  url.pathname = "/";
  await page.goto(url.toString());
  const terminal = page.getByRole("region", { name: "Terminal" });
  const command = terminal.locator(".lead-terminal-command").first();
  const brassLine = `fake-claude --resume ${sessions.brass} in ${brass.folder}`;
  const glassLine = `fake-claude --resume ${sessions.glass} in ${glass.folder}`;
  await expect(command).toContainText(`claude --resume ${sessions.brass}`);
  await expect(rows(page, brassLine)).toHaveCount(1);

  // Something typed to Brass's lead: it says it back.
  await page.locator(".lead-terminal .xterm").click();
  await page.keyboard.type("polish the brass");
  await page.keyboard.press("Enter");
  await expect(rows(page, "polish the brass")).toHaveCount(2);

  // The other wish, its own lead.
  await page.locator(".wish-nav", { hasText: "Glass lamp" }).click();
  await expect(command).toContainText(`claude --resume ${sessions.glass}`);
  await expect(rows(page, glassLine)).toHaveCount(1);
  await expect(rows(page, brassLine)).toHaveCount(0);

  // A wish whose lead does not run: the tab shown stays, and no other terminal starts.
  await page.locator(".wish-nav", { hasText: "Lamp without a lead" }).click();
  await expect(command).toContainText(`claude --resume ${sessions.glass}`);
  await expect(terminal.getByRole("tab", { name: "Terminal" })).toHaveCount(0);

  // Back to Brass: the same lead, what it said still there.
  await page.locator(".wish-nav", { hasText: "Brass lamp" }).click();
  await expect(command).toContainText(`claude --resume ${sessions.brass}`);
  await expect(rows(page, brassLine)).toHaveCount(1);
  await expect(rows(page, "polish the brass")).toHaveCount(2);

  // The flight plan shows no wish: the tab shown stays.
  await page.locator(".plan-nav").click();
  await expect(command).toContainText(`claude --resume ${sessions.brass}`);
});
