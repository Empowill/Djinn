// Writing to a wish's lead from the window: the button at the bottom right unfolds a box whose text Djinn types in
// the lead's terminal, as the developer would. Without a lead running, the box says so and resumes it. The lead is
// the fake claude of global-setup.ts, which repeats what it reads: no model is called.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
// Not the session of wish-resume.spec.ts: two terminals never resume one session.
const session = "6f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f";

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

test("a word written in the window reaches the lead's terminal", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "tell-")),
  );
  const title = "Talk to the lead";
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  djinn("wish", "set-lead", wishId, session, "--directory", folder);
  try {
    await page.goto(process.env.DJINN_URL!);
    // Folded, the terminal waits to be opened before it resumes the lead of the wish shown.
    await page
      .locator(".lead-terminal-bar button[title='Collapse the terminal']")
      .last()
      .click();
    await page.locator(".wish-nav").filter({ hasText: title }).click();

    // No lead runs yet: the box says so, and offers to resume it.
    await page.getByRole("button", { name: "Write to the lead" }).click();
    const panel = page.getByRole("dialog", { name: "To the lead" });
    await expect(
      panel.getByText("The lead is not running: nothing can reach it."),
    ).toBeVisible();
    const box = panel.getByLabel(
      "A word for the lead, as if typed in its terminal",
    );
    await expect(box).toBeDisabled();
    await panel.getByRole("button", { name: "Resume the lead" }).click();
    await expect(box).toBeEnabled({ timeout: 10_000 });
    const rows = page.locator(".lead-terminal .xterm-rows > div");
    await expect(
      rows.filter({ hasText: `fake-claude --resume ${session}` }),
    ).toHaveCount(1);

    // Shift+Enter breaks the line, Enter sends: the fake lead reads one line, its breaks made spaces.
    await box.fill("Hello");
    await box.press("Shift+Enter");
    await box.pressSequentially("lead");
    await expect(box).toHaveValue("Hello\nlead");
    await box.press("Enter");
    await expect(box).toHaveValue("");
    await expect(panel.getByText("Sent to the lead")).toBeVisible();
    await expect(rows.filter({ hasText: "Hello lead" }).first()).toBeVisible({
      timeout: 10_000,
    });

    // Escape folds the box.
    await box.press("Escape");
    await expect(panel).toHaveCount(0);

    // The command line says the same, the same way.
    djinn("wish", "tell", wishId, "From the command line");
    await expect(
      rows.filter({ hasText: "From the command line" }).first(),
    ).toBeVisible({ timeout: 10_000 });
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});
