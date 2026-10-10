// The wish's head against the real djinn up --browser (see global-setup.ts): the arrow beside Lead lists the agents
// found on djinn's PATH, and picking Antigravity starts a lead of it in the wish's lead terminal, from the same first
// message as every lead, while the claude lead stays the wish's. The description under the title edits in place.
// The agents are fake: claude and agy scripts on djinn's PATH.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";
import { portable } from "./portable";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const session = "2f6a1b3c-4d5e-4f60-8a7b-9c0d1e2f3a4b";

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

function wishOf(id: string) {
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes as {
    id: string;
    description?: string;
    lead?: { provider?: string; session_id?: string };
  }[];
  return listed.find((w) => w.id === id)!;
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

// open makes a wish set aside, in projects when given, and shows it in the window.
async function open(
  page: import("@playwright/test").Page,
  title: string,
  ...projects: string[]
) {
  const made = ["wish", "make", title, "--paused", "--json"];
  for (const id of projects) made.push("--project-ids", id);
  const wishId = JSON.parse(djinn(...made)).wish.id as string;
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await expect(page.locator(".hero h1")).toHaveText(title);
  return wishId;
}

test("the description under the title edits in place, as djinn wish describe", async ({
  page,
}) => {
  portable();
  const title = `Describe the lamp ${randomUUID().slice(0, 8)}`;
  const wishId = await open(page, title);
  // The title stands for it until someone writes one.
  const description = page.locator(".hero .wish-description");
  await expect(description).toHaveText(title);

  await description.click();
  const field = page.getByRole("textbox", { name: "Description" });
  await field.fill("Light the house.\nNot the street.");
  await page.locator(".hero h1").click(); // Leaving the field saves it.
  await expect(description).toHaveText("Light the house.\nNot the street.");
  await expect
    .poll(() => wishOf(wishId).description)
    .toBe("Light the house.\nNot the street.");

  // The command line writes it too, and the head follows.
  djinn("wish", "describe", wishId, "--text", "Only the hall.");
  await expect(description).toHaveText("Only the hall.");
  djinn("wish", "delete", wishId);
});

test.describe("Lead of another agent", () => {
  test.skip(process.platform === "win32", "the fake agents are shell scripts");
  // The next specs find the window's own terminal again.
  test.afterAll(() => show("main"));

  test("the arrow beside Lead starts an Antigravity lead, which becomes the wish's lead", async ({
    page,
  }) => {
    const folder = fs.realpathSync(
      fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lead-")),
    );
    // A new lead starts in the wish's project, never in the home folder.
    const project = JSON.parse(djinn("project", "add", folder, "--json"))
      .project.id as string;
    const title = `Lead with agy ${randomUUID().slice(0, 8)}`;
    const wishId = await open(page, title, project);
    djinn("wish", "set-lead", wishId, session, "--directory", folder);

    const actions = page.locator(".topbar-actions");
    await actions.getByRole("button", { name: "Choose the agent" }).click();
    const menu = page.getByRole("menu", { name: "Choose the agent" });
    const claude = menu.getByRole("menuitem", { name: /Claude/ });
    await expect(claude).toHaveAttribute("aria-current", "true");
    await expect(claude).toContainText("The wish's lead: resumes its session");
    const agy = menu.getByRole("menuitem", { name: /Antigravity/ });
    await expect(agy).toBeEnabled();
    await agy.click();
    await expect(menu).toHaveCount(0);

    const terminal = page.getByRole("region", { name: "Terminal" });
    await expect(
      terminal.locator(".lead-terminal-command").first(),
    ).toContainText(`agy -i 'You lead the Djinn wish ${wishId}.`);
    await expect(
      page
        .locator(".lead-terminal .xterm-rows > div")
        .filter({ hasText: "fake-agy -i You lead the Djinn wish" }),
    ).toHaveCount(1);
    await expect(page.locator(".toast")).toContainText(
      "The folder's most recent conversation is the wish's lead now",
    );
    expect(wishOf(wishId).lead).toMatchObject({
      provider: "PROVIDER_ANTIGRAVITY",
      directory: folder,
    });
    djinn("wish", "delete", wishId);
  });
});
