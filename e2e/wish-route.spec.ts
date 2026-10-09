// Every request finds its wish, against the real djinn up --browser (see global-setup.ts): a lead hands a request over
// with djinn wish route --ask, the developer rubs the lamp on the card, and the window shows the new wish with its
// lead started on the request in its own terminal. The lead is a fake claude on djinn's PATH.
import { expect, test } from "@playwright/test";
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

test("a request routed to a new wish starts its lead there, at a rub of the lamp", async ({
  page,
}) => {
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "brass-")),
  );
  djinn("project", "add", folder, "--name", "brass");
  const here = JSON.parse(djinn("wish", "make", "Route from here", "--json"))
    .wish.id as string;
  const request = "wax the brass canoe paddles";
  const asked = JSON.parse(
    djinn("wish", "route", request, "--wish-id", here, "--ask", "--json"),
  );
  const title = "Wax the brass canoe paddles";
  expect(asked.question.options).toEqual([`New wish “${title}”, in brass`]);

  await page.goto(process.env.DJINN_URL!);
  await page
    .locator(".wish-nav")
    .filter({ hasText: "Route from here" })
    .click();
  const card = page
    .locator(".question-card")
    .filter({ hasText: "Where does this request go?" });
  await expect(card).toContainText(request);
  await card.getByRole("button", { name: "Rub the lamp" }).click();

  // The window goes to the new wish, its lead running in its own terminal, on the brief and the request.
  await expect(page.locator(".hero h1")).toHaveText(title);
  const made = JSON.parse(djinn("wish", "list", "--json")).wishes.find(
    (w: { title: string }) => w.title === title,
  );
  expect(made.state).toBe("WISH_STATE_ACTIVE");
  expect(made.lead.session_id).toBeTruthy();
  const terminal = page.getByRole("region", { name: "Terminal" });
  await expect(
    terminal.locator(".lead-terminal-command").first(),
  ).toContainText(`claude --session-id ${made.lead.session_id}`);
  await expect(page.locator(".lead-terminal .xterm-rows")).toContainText(
    "this wish was made for it: " + request,
  );
  const blocks = JSON.parse(
    djinn("block", "list", made.id, "--kind", "request", "--json"),
  ).blocks;
  expect(blocks.map((b: { content: string }) => b.content)).toEqual([request]);

  djinn("wish", "pause", made.id);
  djinn("wish", "pause", here);
});
