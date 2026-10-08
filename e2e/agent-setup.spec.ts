// The setup of the agents, against the real djinn up --browser (see global-setup.ts), with the agents' states
// simulated: the page reads them from UiService.GetEnvironment, answered here. The install command is a harmless
// printf, run for real through the shell of a terminal of djinn; nothing is installed.
import { create, toBinary } from "@bufbuild/protobuf";
import { expect, test, type Page } from "@playwright/test";

import {
  ProviderSchema,
  ProviderState,
  UiServiceGetEnvironmentResponseSchema,
} from "../gen/ts/ui/v1/ui_pb";

test.skip(process.platform === "win32", "the commands are POSIX shell lines");

type States = {
  claude: ProviderState;
  codex: ProviderState;
  agy: ProviderState;
};

// simulate answers GetEnvironment with the agents in states, read at each request: a test changes them as it goes.
async function simulate(page: Page, states: States) {
  await page.route("**/ui.v1.UiService/GetEnvironment", async (route) => {
    const agent = (
      id: keyof States,
      name: string,
      install: string,
      login: string,
    ) =>
      create(ProviderSchema, {
        id,
        name,
        state: states[id],
        available: states[id] !== ProviderState.MISSING,
        command:
          states[id] !== ProviderState.MISSING ? `/usr/local/bin/${id}` : "",
        version: states[id] !== ProviderState.MISSING ? "1.2.3" : "",
        installCommand: install,
        loginCommand: login,
      });
    const res = create(UiServiceGetEnvironmentResponseSchema, {
      version: "dev",
      platform: process.platform,
      providers: [
        agent(
          "claude",
          "Claude Code",
          "printf 'install''ed claude\\n'",
          "printf 'sign''ed in\\n'",
        ),
        agent(
          "codex",
          "Codex",
          "printf 'install''ed codex\\n'",
          "printf 'sign''ed in codex\\n'",
        ),
        agent("agy", "Antigravity", "printf 'install''ed agy\\n'", "agy"),
      ],
    });
    await route.fulfill({
      status: 200,
      contentType: "application/proto",
      body: Buffer.from(toBinary(UiServiceGetEnvironmentResponseSchema, res)),
    });
  });
}

const row = (page: Page, id: string) =>
  page.locator(`.provider-setting[data-agent="${id}"]`);

test("with no agent ready, the setup opens by itself, installs an agent and checks it again", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const states: States = {
    claude: ProviderState.MISSING,
    codex: ProviderState.SIGNED_OUT,
    agy: ProviderState.MISSING,
  };
  await simulate(page, states);
  await page.goto(process.env.DJINN_URL!);

  const panel = page.getByRole("dialog");
  await expect(panel.getByText("Set up the agents")).toBeVisible();
  await expect(panel.getByText(/No agent is ready yet/)).toBeVisible();
  const claude = row(page, "claude");
  await expect(claude.getByText(/Next step: install it/)).toBeVisible();
  await expect(
    row(page, "codex").getByText(/Next step: sign in/),
  ).toBeVisible();
  await expect(
    row(page, "codex").getByRole("button", { name: "Sign in" }),
  ).toBeVisible();
  await expect(claude.getByRole("button", { name: "Sign in" })).toHaveCount(0);

  // The command ends: the page checks again by itself, and finds the agent installed and signed in.
  states.claude = ProviderState.READY;
  await claude.getByRole("button", { name: "Install" }).click();
  const step = page.getByRole("group", { name: "Installing" });
  await expect(step.locator(".xterm-rows")).toContainText("installed claude");
  await expect(step.getByRole("status")).toHaveText(/Done/);
  await expect(claude.getByText("Ready", { exact: true })).toBeVisible();
  await expect(claude.getByRole("button", { name: "Install" })).toHaveCount(0);
  await expect(panel.getByText(/No agent is ready yet/)).toHaveCount(0);

  await panel.getByRole("button", { name: "Close" }).last().click();
  await expect(page.getByText("Set up the agents")).toHaveCount(0);
  // Once offered, it does not open by itself again.
  await page.reload();
  await expect(page.locator(".sidebar")).toBeVisible();
  await page.waitForTimeout(500);
  await expect(page.getByText("Set up the agents")).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("a new wish whose agent is not ready offers the setup, and keeps what was typed", async ({
  page,
}) => {
  await simulate(page, {
    claude: ProviderState.READY,
    codex: ProviderState.SIGNED_OUT,
    agy: ProviderState.MISSING,
  });
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".sidebar")).toBeVisible();
  await page
    .locator(".sidebar")
    .getByRole("button", { name: /New wish/ })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("What do you wish?").fill("Polish the lamp");
  await expect(dialog.locator(".agent-warning")).toHaveCount(0);

  await dialog.getByLabel("Agent").selectOption({ label: "Antigravity" });
  await expect(
    dialog.getByText("Antigravity is not installed on this computer yet."),
  ).toBeVisible();
  await dialog.getByLabel("Agent").selectOption({ label: "Codex" });
  await expect(
    dialog.getByText("Codex is installed, but not signed in."),
  ).toBeVisible();

  await dialog.getByRole("button", { name: "Set it up" }).click();
  await expect(dialog.getByText("Set up the agents")).toBeVisible();
  await expect(
    row(page, "codex").getByRole("button", { name: "Sign in" }),
  ).toBeVisible();
  await dialog.getByRole("button", { name: "Back to the wish" }).click();
  await expect(dialog.getByLabel("What do you wish?")).toHaveValue(
    "Polish the lamp",
  );
});

test("the settings show the setup of the agents", async ({ page }) => {
  await simulate(page, {
    claude: ProviderState.READY,
    codex: ProviderState.UNKNOWN,
    agy: ProviderState.MISSING,
  });
  await page.goto(process.env.DJINN_URL!);
  await page.getByTitle("Connections & preferences").click();
  const dialog = page.getByRole("dialog");
  await expect(
    row(page, "claude").getByText("Ready", { exact: true }),
  ).toBeVisible();
  // Sign-in unknown: signing in stays offered, never pushed.
  await expect(
    row(page, "codex").getByText(/cannot tell whether it is signed in/),
  ).toBeVisible();
  await expect(
    row(page, "codex").getByRole("button", { name: "Sign in" }),
  ).toBeVisible();
  await expect(
    row(page, "agy").getByRole("button", { name: "Install" }),
  ).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Check" })).toHaveCount(3);
});
