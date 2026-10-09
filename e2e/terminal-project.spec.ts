// The window's terminal opens in a project, never in the home folder: against a djinn of its own (no --terminal-dir,
// as when djinn starts from a menu), on a data folder of its own. Without a project, the terminal asks for one; once
// one is created from there, its shell starts in that project's folder. The spec stops only the djinn it started.
import { expect, test } from "@playwright/test";
import { type ChildProcess, spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

test.skip(
  process.platform === "win32",
  "the terminal runs PowerShell on Windows; this test reads a POSIX shell",
);

let proc: ChildProcess | undefined;
let exited: Promise<unknown> | undefined;
let home = "";

test.afterEach(async () => {
  if (proc && proc.exitCode === null) {
    proc.kill("SIGINT");
    await exited;
  }
  if (home) fs.rmSync(home, { recursive: true, force: true });
});

test("without a project, the terminal asks for one, then opens in it", async ({
  page,
}) => {
  home = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-")));
  const folder = path.join(home, "lamp");
  fs.mkdirSync(folder);
  proc = spawn(binary, ["up", "--browser"], {
    stdio: ["ignore", "pipe", "inherit"],
    env: {
      ...process.env,
      DJINN_HOME: path.join(home, "data"),
      SHELL: "/bin/sh",
    },
  });
  exited = new Promise((resolve) => proc!.once("exit", resolve));
  const url = await new Promise<string>((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error("djinn printed no URL")),
      10_000,
    );
    let out = "";
    proc!.stdout!.on("data", (chunk: Buffer) => {
      out += chunk.toString();
      const match = out.match(/http:\/\/127\.0\.0\.1:\d+\/\?token=[0-9a-f]+/);
      if (match) {
        clearTimeout(timer);
        resolve(match[0]);
      }
    });
    proc!.once("exit", (code) => reject(new Error(`djinn exited: ${code}`)));
  });

  await page.goto(url);
  const terminal = page.getByRole("region", { name: "Terminal" });
  await expect(terminal.getByText("Create a project first")).toBeVisible();
  // No shell started: not in the home folder, not anywhere.
  await expect(terminal.locator(".lead-terminal-command")).toHaveCount(0);

  await terminal.getByRole("button", { name: "Create a project" }).click();
  await page.getByRole("textbox", { name: "Folder" }).fill(folder);
  await page.getByRole("button", { name: "Add the project" }).click();

  // The shell starts in the project's folder, and the request for a project is gone.
  await expect(terminal.locator(".lead-terminal-command").first()).toHaveText(
    "/bin/sh",
  );
  await expect(terminal.locator(".lead-terminal-command").nth(1)).toHaveText(
    folder,
  );
  await expect(terminal.getByText("Create a project first")).toHaveCount(0);
});
