// An answer reaches the one lead, against the real djinn up --browser (see global-setup.ts), through its queue
// (decision Q191): answered while the lead does not run, it waits, and djinn types one short line in the lead's
// terminal, then Enter, "Djinn: N events waiting, run djinn lead watch", once the lead runs; the lead's watch prints
// it. Answered in another wish while the lead's watch waits, it goes to that watch at once, and nothing is typed. The
// lead is a fake claude on djinn's PATH that says each line it reads.
import { expect, test } from "@playwright/test";
import { execFile, execFileSync } from "node:child_process";
import path from "node:path";
import { promisify } from "node:util";
import {
  dropLead,
  leadFolder,
  leadRows as rows,
  leadWatch,
  setLead,
  waitingLine,
} from "./lead";

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

// askAndAnswer asks a question with two options in a wish, and answers it from a plain shell, as the developer.
function askAndAnswer(wishId: string, first: string, second: string) {
  const asked = JSON.parse(
    djinn(
      "question",
      "ask",
      `Which lamp first, ${first} or ${second}?`,
      wishId,
      "--options",
      first,
      "--options",
      second,
      "--json",
    ),
  );
  const code = asked.question.code as string;
  djinn(
    "question",
    "answer",
    code,
    "b",
    "--wish-id",
    wishId,
    "--note",
    "lighter",
  );
  return code;
}

test.skip(process.platform === "win32", "the fake claude is a shell script");

test.afterEach(dropLead);

test("an answer waits for the lead, which hears of it from one line and its watch; another wish's goes to the same watch", async ({
  page,
}) => {
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const folder = leadFolder();
  const wishId = JSON.parse(djinn("wish", "make", "Tell the lead", "--json"))
    .wish.id as string;
  const other = JSON.parse(
    djinn("wish", "make", "Tell the same lead", "--json"),
  ).wish.id as string;
  setLead(session, folder);
  const first = askAndAnswer(wishId, "Brass", "Glass");

  djinn("lead", "resume");

  await show("lead");
  const url = new URL(process.env.DJINN_URL!);
  url.pathname = "/";
  await page.goto(url.toString());
  await expect(
    rows(page, `fake-claude --resume ${session} in ${folder}`),
  ).toHaveCount(1);
  // One short line, typed, so echoed, then read after Enter: the fake lead says it again. The answer is not typed.
  await expect(rows(page, waitingLine)).toHaveCount(2, { timeout: 15_000 });
  await expect(rows(page, `${first} answered`)).toHaveCount(0);

  // The lead's watch prints the answer, with a cursor.
  const watched = leadWatch(session, "--timeout", "0s");
  expect(watched.digest).toContain(`- ${first} answer: answered B — "Glass"`);
  expect(watched.cursor).not.toBe("");

  // The lead watches again, acknowledging the first: an answer in another wish comes to that watch at once.
  const running = promisify(execFile)(
    binary,
    [
      "lead",
      "watch",
      "--session",
      session,
      "--after",
      watched.cursor,
      "--timeout",
      "30s",
    ],
    {
      env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
      encoding: "utf8",
    },
  );
  await new Promise((r) => setTimeout(r, 500));
  const second = askAndAnswer(other, "Copper", "Tin");
  const { stdout } = await running;
  expect(stdout).toContain(`- ${second} answer: answered B — "Tin"`);
  expect(stdout).not.toContain("Glass");
  // Nothing more was typed in the lead's terminal: no second line, no answer.
  await expect(rows(page, waitingLine)).toHaveCount(2);
  await expect(rows(page, `${second} answered`)).toHaveCount(0);
  await expect(
    rows(page, `fake-claude --resume ${session} in ${folder}`),
  ).toHaveCount(1);
  leadWatch(
    session,
    "--after",
    /Cursor (\S+)\./.exec(stdout)![1],
    "--timeout",
    "0s",
  );

  djinn("wish", "delete", wishId);
  djinn("wish", "delete", other);
});
