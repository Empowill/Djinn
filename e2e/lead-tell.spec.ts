// An answer reaches the lead, against the real djinn up --browser (see global-setup.ts): the wish's lead does not run,
// djinn reopens it on its session, and types the answer in its terminal, then Enter. The lead is a fake claude on
// djinn's PATH that says each line it reads.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
// Not the session of wish-resume.spec.ts: one session runs in one terminal.
const session = "6f1c9d2e-3a4b-4c5d-8e7f-a0b1c2d3e4f5";

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

test.skip(process.platform === "win32", "the fake claude is a shell script");

test.afterAll(() => show("main"));

test("an answer is typed in the terminal of the wish's lead, reopened", async ({
  page,
}) => {
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lead-")),
  );
  const wishId = JSON.parse(djinn("wish", "make", "Tell the lead", "--json"))
    .wish.id as string;
  djinn("wish", "set-lead", wishId, session, "--directory", folder);
  djinn(
    "question",
    "ask",
    "Which lamp first?",
    wishId,
    "--options",
    "Brass",
    "--options",
    "Glass",
  );
  djinn(
    "question",
    "answer",
    "Q01",
    "b",
    "--wish-id",
    wishId,
    "--note",
    "lighter",
  );

  await show(`lead-${wishId}`);
  const url = new URL(process.env.DJINN_URL!);
  url.pathname = "/";
  await page.goto(url.toString());
  const rows = page.locator(".lead-terminal .xterm-rows > div");
  await expect(
    rows.filter({ hasText: `fake-claude --resume ${session} in ${folder}` }),
  ).toHaveCount(1);
  // Typed, so echoed, then read after Enter: the fake lead says it again.
  await expect(
    rows.filter({ hasText: 'Djinn: Q01 answered B — "Glass"' }),
  ).toHaveCount(2, { timeout: 15_000 });

  djinn("wish", "pause", wishId);
});
