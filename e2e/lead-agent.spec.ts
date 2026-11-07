// The wish's head against the real djinn up --browser (see global-setup.ts): the Summoning Circle, whose Djinn's row
// names Antigravity while the recorded lead is Claude's, starts a lead of it in the lead's terminal, from the same first
// message as every lead, and it becomes the one lead (W578: the window's top has no agent selector). The description
// under the title edits in place.
// The agents are fake: claude and agy scripts on djinn's PATH.
import { create, toBinary } from "@bufbuild/protobuf";
import { expect, test } from "./test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";
import {
  dropLead,
  dropTerminalLead,
  leadFolder,
  leadRows,
  setLead,
} from "./lead";
import { portable } from "./portable";
import {
  LeadServiceGetCircleResponseSchema,
  Provider,
  WorkerKind,
} from "../gen/ts/plan/v1/plan_pb";

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
  }[];
  return listed.find((w) => w.id === id)!;
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
  test.beforeEach(dropTerminalLead);
  test.afterEach(dropLead);

  test("the Circle starts an Antigravity lead when the Djinn's row names it, which becomes the lead", async ({
    page,
  }) => {
    const folder = leadFolder();
    const project = JSON.parse(djinn("project", "add", folder, "--json"))
      .project.id as string;
    // A new lead starts in the first of Djinn's projects that has a folder here, never in the home folder.
    const first = (
      JSON.parse(djinn("project", "list", "--json")).projects as {
        directory?: string;
      }[]
    )
      .map((p) => p.directory ?? "")
      .find((d) => d !== "" && fs.existsSync(d));
    const title = `Lead with agy ${randomUUID().slice(0, 8)}`;
    const wishId = await open(page, title, project);
    setLead(session, folder);

    // No selector at the top: Talk to the Djinn alone.
    const actions = page.locator(".topbar-actions");
    await expect(
      actions.getByRole("button", { name: "Talk to the Djinn" }),
    ).toBeVisible();
    await expect(actions.locator("[aria-haspopup=menu]")).toHaveCount(0);

    // The Circle's Djinn row names Antigravity (answered here, so the shared djinn's settings stay as they are).
    await page.route("**/plan.v1.LeadService/GetCircle", (route) =>
      route.fulfill({
        contentType: "application/proto",
        body: Buffer.from(
          toBinary(
            LeadServiceGetCircleResponseSchema,
            create(LeadServiceGetCircleResponseSchema, {
              rows: [
                {
                  kind: WorkerKind.LEAD,
                  provider: Provider.ANTIGRAVITY,
                  model: "gemini-4-argon-high",
                },
              ],
              models: [
                {
                  id: "claude-opus-5-5",
                  label: "Claude Opus 5.5",
                  provider: Provider.CLAUDE,
                },
                {
                  id: "gemini-4-argon-high",
                  label: "Gemini 4 Argon (High)",
                  provider: Provider.ANTIGRAVITY,
                },
              ],
              providers: [
                {
                  provider: Provider.CLAUDE,
                  signedIn: true,
                  loginCommand: "claude auth login",
                  leadSupported: true,
                },
                {
                  provider: Provider.ANTIGRAVITY,
                  signedIn: true,
                  loginCommand: "agy",
                  leadSupported: true,
                },
              ],
            }),
          ),
        ),
      }),
    );
    await page.locator(".summoning-nav").click();
    const start = page.locator(".summoning-lead-start");
    await expect(start).toContainText(
      "Talk to the Djinn resumes its session on Claude.",
    );
    await start
      .getByRole("button", { name: "Start the Djinn on Antigravity" })
      .click();

    const terminal = page.getByRole("region", { name: "Terminal" });
    await expect(
      terminal.locator(".lead-terminal-command").first(),
    ).toContainText("agy -i 'You lead the active wishes of this Djinn.");
    await expect(
      leadRows(page, "fake-agy -i You lead the active wishes of this Djinn."),
    ).toHaveCount(1);
    await expect(page.locator(".summoning-lead-note")).toContainText(
      "The folder's most recent conversation is the lead now",
    );
    // The recorded lead is Antigravity's now: nothing more to start.
    await expect(start).toHaveCount(0);
    expect(JSON.parse(djinn("lead", "get", "--json")).lead).toMatchObject({
      provider: "PROVIDER_ANTIGRAVITY",
      directory: first,
    });
    djinn("wish", "delete", wishId);
  });
});
