// Changing a wish's agent from the window, against the real djinn up --browser (see global-setup.ts): the claude lead
// stops, and a codex lead starts from the wish's brief. Both are fakes on djinn's PATH that say how they were called.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

import { Provider } from "../gen/ts/plan/v1/plan_pb";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const session = "6d0f3c2a-8b1e-4f5a-9c7d-2e4b6a8c0f13";
const codex = () => path.join(process.env.DJINN_E2E_HOME!, "bin", "codex");

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

test.skip(process.platform === "win32", "the fake agents are shell scripts");

// A fake codex for this spec only: its status checks answer at once, and a lead says how it was called.
test.beforeAll(() =>
  fs.writeFileSync(
    codex(),
    '#!/bin/sh\ncase "$1" in --version|login) echo 0.0.0; exit 0;; esac\n' +
      'echo "fake-codex $(printf %s "$1" | head -c 24)"\nexec cat\n',
    { mode: 0o755 },
  ),
);
test.afterAll(() => fs.rmSync(codex(), { force: true }));

test("changing the agent stops the lead and starts the new one from the brief", async ({
  page,
}) => {
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lead-")),
  );
  const made = djinn("wish", "make", "Switch the agent", "--json");
  const wishId = JSON.parse(made).wish.id as string;
  djinn("wish", "set-lead", wishId, session, "--directory", folder);
  djinn("wish", "resume", wishId);

  const url = new URL(process.env.DJINN_URL!);
  url.pathname = "/";
  await page.goto(url.toString());
  const rows = page.locator(".lead-terminal .xterm-rows > div");
  await expect(
    rows.filter({ hasText: `fake-claude --resume ${session}` }),
  ).toHaveCount(1);

  await page.locator(".wish-provider").click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("combobox").selectOption(String(Provider.CODEX));
  // Said before the change: the conversation does not pass.
  await expect(dialog).toContainText("cannot be passed to the new agent");
  await dialog.getByRole("button", { name: "Switch to Codex" }).click();

  await expect(page.locator(".wish-provider")).toHaveText("Codex");
  await expect(
    rows.filter({ hasText: "fake-codex # Leading a wish in" }),
  ).toHaveCount(1);
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes as {
    id: string;
    provider: string;
  }[];
  expect(listed.find((w) => w.id === wishId)?.provider).toBe("PROVIDER_CODEX");
});
