// A push from the projects panel shows what it does (W489): its sync line spins while its push check runs (a check
// that waits for a file), the same after a reload, then shows a check mark and leaves once the remote holds the
// branch, its counts following the remote. Nothing is said as a toast. No model is called.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
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

function git(dir: string, ...args: string[]): string {
  return execFileSync(
    "git",
    ["-c", "user.name=e2e", "-c", "user.email=e2e@example.com", ...args],
    { cwd: dir, encoding: "utf8" },
  );
}

test("a push from the projects panel spins while its checks run, then its line follows the remote", async ({
  page,
}) => {
  test.skip(process.platform === "win32", "the push check is a shell script");
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const suffix = randomUUID().slice(0, 8);
  const home = process.env.DJINN_E2E_HOME!;
  const folder = fs.realpathSync(fs.mkdtempSync(path.join(home, "brass-")));
  const bare = path.join(
    fs.realpathSync(fs.mkdtempSync(path.join(home, "origin-"))),
    "origin.git",
  );
  // The push check waits until the test lets it go.
  const release = path.join(folder + "-release");
  git(folder, "init", "--quiet", "-b", "main");
  git(folder, "config", "user.name", "e2e");
  git(folder, "config", "user.email", "e2e@example.com");
  fs.writeFileSync(path.join(folder, "README.md"), "A brass lamp.\n");
  fs.writeFileSync(
    path.join(folder, "wait.sh"),
    `while [ ! -f '${release}' ]; do sleep 0.1; done\n`,
  );
  fs.mkdirSync(path.join(folder, ".agents"));
  fs.writeFileSync(
    path.join(folder, ".agents", "settings.txtpb"),
    'checks { name: "slow" command: "sh wait.sh" when: CHECK_WHEN_PUSH }\n',
  );
  git(folder, "add", ".");
  git(folder, "commit", "--quiet", "-m", "A brass lamp");
  git(folder, "init", "--quiet", "--bare", bare);
  git(folder, "remote", "add", "origin", bare);
  git(folder, "push", "--quiet", "origin", "main");
  fs.writeFileSync(path.join(folder, "README.md"), "A brass lamp, polished.\n");
  git(folder, "commit", "--quiet", "-am", "Polish the lamp");

  const name = `brass-${suffix}`;
  const projectId = JSON.parse(
    djinn("project", "add", folder, "--name", name, "--json"),
  ).project.id as string;
  const wishId = JSON.parse(
    djinn(
      "wish",
      "make",
      `Push the lamp ${suffix}`,
      "--project-ids",
      projectId,
      "--json",
    ),
  ).wish.id as string;
  try {
    await page.goto(process.env.DJINN_URL!);
    const line = page
      .locator(".project-nav")
      .filter({ hasText: name })
      .locator(".sync-line")
      .filter({ hasText: "main" });
    await expect(line.locator(".sync-ahead")).toHaveText("↑1");
    await line.locator(".sync-push").click();

    // Its check runs: a spinner in the button, disabled, the check on hover; a reload shows it the same.
    const button = line.locator(".sync-push");
    await expect(button).toHaveAttribute("title", "Push checks running: slow");
    await expect(button).toBeDisabled();
    await expect(button.locator(".sync-spinner")).toBeVisible();
    await page.reload();
    await expect(button).toHaveAttribute("title", "Push checks running: slow");
    await expect(line.locator(".sync-ahead")).toHaveText("↑1");

    // Let it go: pushed, a check mark, the counts in sync at once; then the line leaves.
    fs.writeFileSync(release, "");
    await expect(line.locator(".sync-status.pushed")).toBeVisible({
      timeout: 30_000,
    });
    await expect(line.locator(".sync-ahead")).toHaveCount(0);
    await expect(line.locator(".sync-push")).toHaveCount(0);
    expect(git(bare, "rev-parse", "main")).toBe(
      git(folder, "rev-parse", "main"),
    );
    await expect(line).toHaveCount(0, { timeout: 15_000 });
    await expect(page.locator(".toast")).toHaveCount(0);
    expect(errors).toEqual([]);
  } finally {
    fs.writeFileSync(release, "");
    djinn("wish", "delete", wishId);
  }
});
