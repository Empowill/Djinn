// The buttons at the top of a wish, against the real djinn up --browser (see global-setup.ts): Share writes the wish's
// export (binary protobuf), a file the command line imports again; Lead resumes the wish's lead in its own terminal. The lead is a fake
// claude on djinn's PATH. The trash has its own spec (wish-lifecycle.spec.ts).
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const session = "6c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e4f";

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

// open makes a wish set aside, and shows it in the window.
async function open(page: import("@playwright/test").Page, title: string) {
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await expect(page.locator(".hero h1")).toHaveText(title);
  return wishId;
}

test("Share writes the wish's export, which imports again", async ({
  page,
}) => {
  const title = `Share the lamp ${randomUUID().slice(0, 8)}`;
  const wishId = await open(page, title);
  djinn(
    "block",
    "put",
    wishId,
    "--kind",
    "note",
    "--title",
    "Oil",
    "--content",
    "Whale oil, no more.",
  );

  await page
    .locator(".topbar-actions")
    .getByRole("button", { name: "Share" })
    .click();
  const toast = page.locator(".toast");
  await expect(toast).toContainText("Wish exported to ");
  const file = (await toast.innerText())
    .replace(/^.*Wish exported to /s, "")
    .trim();
  expect(fs.statSync(file).size).toBeGreaterThan(0);

  // The file is the wish: gone, then imported again from it, with its title and its block.
  djinn("wish", "delete", wishId);
  djinn("wish", "import", file);
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes as {
    id: string;
    title: string;
  }[];
  expect(listed.find((w) => w.id === wishId)?.title).toBe(title);
  const blocks = JSON.parse(djinn("block", "list", wishId, "--json"))
    .blocks as { title: string }[];
  expect(blocks.map((b) => b.title)).toContain("Oil");
  djinn("wish", "delete", wishId);
});

test.describe("Lead", () => {
  test.skip(process.platform === "win32", "the fake claude is a shell script");
  // The next specs find the window's own terminal again.
  test.afterAll(() => show("main"));

  test("Lead resumes the wish's lead in its own terminal", async ({ page }) => {
    const folder = fs.realpathSync(
      fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lead-")),
    );
    const title = `Lead from the button ${randomUUID().slice(0, 8)}`;
    const wishId = await open(page, title);
    djinn("wish", "set-lead", wishId, session, "--directory", folder);

    await page
      .locator(".topbar-actions")
      .getByRole("button", { name: "Lead" })
      .click();
    const terminal = page.getByRole("region", { name: "Terminal" });
    await expect(
      terminal.locator(".lead-terminal-command").first(),
    ).toContainText(`claude --resume ${session}`);
    await expect(
      page
        .locator(".lead-terminal .xterm-rows > div")
        .filter({ hasText: `fake-claude --resume ${session} in ${folder}` }),
    ).toHaveCount(1);
    djinn("wish", "delete", wishId);
  });
});
