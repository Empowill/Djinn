// The one lead of the e2e's djinn (see global-setup.ts): every spec of a run shares it, and its terminal, "lead". A
// spec that records or starts it calls dropLead before it ends, so that the next specs find no lead running, and none
// to reopen: an answer, a routed request, reopens the recorded lead to tell it.
import { expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

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

async function terminals(method: string, body: object) {
  const url = new URL(process.env.DJINN_URL!);
  const res = await fetch(
    new URL(`/terminal.v1.TerminalService/${method}`, url),
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${url.searchParams.get("token")}`,
      },
      body: JSON.stringify(body),
    },
  );
  expect(res.ok).toBe(true);
  return res.json();
}

// runningTerminals are the terminals of djinn whose program runs: their name and command line.
export async function runningTerminals(): Promise<
  { id: string; name: string; command: string[] }[]
> {
  return (await terminals("List", {})).terminals ?? [];
}

// leadFolder makes a folder for a lead to run in, under the e2e's data folder.
export function leadFolder(prefix = "lead-"): string {
  return fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, prefix)),
  );
}

// setLead records the lead's claude session, in folder, as djinn lead set does.
export function setLead(session: string, folder: string) {
  djinn("lead", "set", session, "--directory", folder);
}

// leadRows are the rows of the lead's terminal, shown in the window, that hold text.
export function leadRows(
  page: import("@playwright/test").Page,
  text: string | RegExp,
) {
  return page
    .locator(".lead-terminal .xterm-rows > div")
    .filter({ hasText: text });
}

// dropLead hangs up the lead's terminal and waits for it to end, then leaves a lead recorded in a folder gone: no
// later spec reopens it. Djinn keeps no command to forget its lead.
export async function dropLead() {
  const deadline = Date.now() + 10_000;
  for (;;) {
    const lead = (await runningTerminals()).filter((t) => t.name === "lead");
    if (lead.length === 0) break;
    if (Date.now() > deadline) throw new Error("the lead's terminal runs on");
    for (const t of lead) await terminals("Close", { id: t.id });
    await new Promise((r) => setTimeout(r, 100));
  }
  const gone = leadFolder("gone-");
  setLead(goneSession, gone);
  fs.rmSync(gone, { recursive: true, force: true });
  drainLead(goneSession);
}

const goneSession = "00000000-0000-4000-8000-000000000000";

// waitingLine is the line djinn types in the lead's terminal when events wait for it and no watch does.
export const waitingLine = /Djinn: \d+ events? waiting, run djinn lead watch/;

// leadWatch runs djinn lead watch as the lead's session, and returns its digest and the cursor it printed ("" for
// none).
export function leadWatch(
  session: string,
  ...args: string[]
): { digest: string; cursor: string } {
  const digest = djinn("lead", "watch", "--session", session, ...args);
  return { digest, cursor: /Cursor (\S+)\./.exec(digest)?.[1] ?? "" };
}

// drainLead acknowledges every event waiting for the lead, as its watch would: a later spec's lead hears of none.
export function drainLead(session: string) {
  let after: string[] = [];
  for (let i = 0; i < 50; i++) {
    const { cursor } = leadWatch(session, "--timeout", "0s", ...after);
    if (!cursor) return;
    after = ["--after", cursor];
  }
}
