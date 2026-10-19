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

function writeLeadDriven(driven?: boolean) {
  const file = path.join(process.env.DJINN_E2E_HOME!, "settings.json");
  let settings: Record<string, unknown>;
  try {
    settings = JSON.parse(fs.readFileSync(file, "utf8")) as Record<
      string,
      unknown
    >;
  } catch {
    settings = {};
  }
  if (driven === undefined) delete settings.leadDriven;
  else settings.leadDriven = driven;
  fs.writeFileSync(file, `${JSON.stringify(settings, null, 2)}\n`);
}

// useTerminalLead sets leadDriven: false in settings.json so specs that test the terminal lead fallback run it.
export function useTerminalLead() {
  writeLeadDriven(false);
}

// leadJournal returns the batches recorded in the driven lead's journal (for wishId when given).
export function leadJournal(
  wishId?: string,
): { id: string; state: string; events?: string[]; note?: string }[] {
  const args = ["lead", "journal", "--json"];
  if (wishId) args.push("--wish-id", wishId);
  return JSON.parse(djinn(...args)).batches ?? [];
}

// dropLead hangs up the lead's terminal and waits for it to end, leaves a lead recorded in a folder gone, drains
// waiting events, and restores the default driven lead setting.
export async function dropLead() {
  writeLeadDriven(false);
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
  writeLeadDriven(undefined);
}

// dropTerminalLead cleans up the lead and switches settings.json to leadDriven: false for terminal-lead specs.
export async function dropTerminalLead() {
  await dropLead();
  useTerminalLead();
}

const goneSession = "00000000-0000-4000-8000-000000000000";

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
