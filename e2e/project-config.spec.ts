// A project's configuration panel, against the real djinn up --browser (see global-setup.ts): a Git project whose
// copy has another branch checked out than its reference, with the team's settings, a skill that offers a wish
// template, one that is know-how alone, and one that offers an inbox source this machine has not decided on. The
// panel says what to fix first, with its choices; then the reference branch, its commit and where its name comes from;
// the checks and settings with where each comes from; and each skill's role. Saying no to the source settles it.
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

function git(dir: string, ...args: string[]): string {
  return execFileSync("git", ["-C", dir, ...args], { encoding: "utf8" }).trim();
}

const skills: Record<string, string> = {
  "fix-the-wick": `---
name: fix-the-wick
description: Fixes a wick.
metadata:
  djinn:
    wish:
      title: "Fix wick {n}"
      match: '(?i)fix wick (?P<n>\\d+)'
---

# Fix a wick
`,
  "trim-the-wick": `---
name: trim-the-wick
description: How the workers trim a wick.
---

# Trim a wick
`,
  "lamp-news": `---
name: lamp-news
description: Reads the lamp news.
metadata:
  djinn:
    source:
      watch: "sh lamp-news.sh"
      summary: "The lamp news addressed to you."
---

# Lamp news
`,
};

test("a project's configuration says what to fix first, its reference branch, where each value comes from and each skill's role", async ({
  page,
}) => {
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lantern-")),
  );
  for (const [name, text] of Object.entries(skills)) {
    fs.mkdirSync(path.join(folder, ".agents", "skills", name), {
      recursive: true,
    });
    fs.writeFileSync(
      path.join(folder, ".agents", "skills", name, "SKILL.md"),
      text,
    );
  }
  fs.writeFileSync(path.join(folder, "lamp-news.sh"), "exit 0\n");
  fs.writeFileSync(
    path.join(folder, ".agents", "settings.txtpb"),
    'checks { name: "lint" command: "true" when: CHECK_WHEN_COMMIT }\nmerge_main: MERGE_MAIN_OFF\n',
  );
  // The reference: origin/main as fetched, the remote's default branch; the copy works on feature.
  git(folder, "init", "-q", "-b", "main");
  git(folder, "config", "user.name", "Djinn e2e");
  git(folder, "config", "user.email", "e2e@example.com");
  git(folder, "remote", "add", "origin", "https://example.invalid/lantern.git");
  git(folder, "add", "-A");
  git(folder, "commit", "-q", "-m", "the lamp");
  git(folder, "update-ref", "refs/remotes/origin/main", "HEAD");
  git(
    folder,
    "symbolic-ref",
    "refs/remotes/origin/HEAD",
    "refs/remotes/origin/main",
  );
  git(folder, "checkout", "-q", "-b", "feature");
  const commit = git(folder, "rev-parse", "--short=7", "HEAD");
  djinn("project", "add", folder, "--name", "lantern");

  await page.goto(process.env.DJINN_URL!);
  await page
    .locator(".project-list")
    .getByRole("button", { name: "lantern", exact: true })
    .click();
  const panel = page.getByRole("dialog", { name: "lantern" });
  await expect(panel.locator(".eyebrow")).toHaveText("PROJECT CONFIGURATION");

  // To fix first: the source this machine has not decided on, its two choices and what each changes.
  const boxes = panel.locator(".config-box");
  const toFix = boxes.first();
  await expect(toFix).toHaveClass(/to-fix/);
  await expect(toFix.locator("h3")).toHaveText("To fix (1)");
  await expect(toFix).toContainText(
    "The skill lamp-news offers an inbox source this machine has not decided on",
  );
  await expect(toFix.getByRole("button", { name: "Plug in" })).toBeVisible();
  await expect(toFix).toContainText("the project stops asking");
  await expect(toFix).toContainText("djinn inbox plug lantern/lamp-news");

  // The reference branch, its commit and where its name comes from; the copy on another branch, an information.
  const reference = boxes.filter({ hasText: "Reference branch" });
  await expect(reference).toContainText(`main origin/main at ${commit}`);
  await expect(reference.locator(".config-from")).toHaveText("Djinn's default");
  await expect(reference).toContainText(
    "Your copy has feature checked out, not the reference branch origin/main",
  );

  // Where each value comes from: the team's file names the check and merge_main, the file itself on hover.
  const checks = boxes.filter({
    has: page.locator("h3", { hasText: "Checks" }),
  });
  await expect(checks.locator(".setting-row strong")).toHaveText("lint");
  const team = checks.locator(".config-from");
  await expect(team).toHaveText("the team's file");
  await expect(team).toHaveAttribute(
    "title",
    path.join(folder, ".agents", "settings.txtpb"),
  );
  const settings = boxes.filter({
    has: page.locator("h3", { hasText: "Settings" }),
  });
  await expect(
    settings
      .locator(".config-setting")
      .filter({ has: page.locator("code", { hasText: /^merge_main$/ }) }),
  ).toContainText("the team's file");
  await expect(settings.locator(".config-more summary")).toContainText(
    "at Djinn's default",
  );

  // Each skill's role for Djinn.
  const skillBox = boxes.filter({
    has: page.locator("h3", { hasText: "Skills (3)" }),
  });
  await expect(
    skillBox.locator(".skill-row").filter({ hasText: "fix-the-wick" }),
  ).toContainText("Wish template “Fix wick {n}”");
  await expect(
    skillBox.locator(".skill-row").filter({ hasText: "trim-the-wick" }),
  ).toContainText("Know-how for the workers");
  await expect(
    skillBox.locator(".skill-row").filter({ hasText: "lamp-news" }),
  ).toContainText("Inbox source");

  // Saying no settles it: nothing left to fix, the source unplugged on this machine.
  await toFix.getByRole("button", { name: "Say no" }).click();
  await expect(panel.locator(".config-box.to-fix")).toHaveCount(0);
  await expect(
    panel.locator(".inbox-source-row").filter({ hasText: "lamp-news" }),
  ).toContainText("Unplugged: runs nothing");
  const shown = JSON.parse(djinn("project", "show", "lantern", "--json"));
  expect(
    (shown.notices ?? []).filter(
      (n: { kind: string }) => n.kind === "PROJECT_NOTICE_KIND_TO_FIX",
    ),
  ).toHaveLength(0);
  expect(shown.reference).toMatchObject({
    branch: "main",
    ref: "origin/main",
    source: "SETTING_SOURCE_DEFAULT",
    checked_out: "feature",
  });
});

// The reference branch chosen in the panel (W149): Change lists the remote's branches; the choice is written in your
// own file, never in the team's, and wins over its name; djinn project set-branch --unset takes it back.
test("a project's reference branch is chosen in its panel, in your own file, and taken back from the command line", async ({
  page,
}) => {
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "wick-")),
  );
  const teamFile = path.join(folder, ".agents", "settings.txtpb");
  fs.mkdirSync(path.dirname(teamFile), { recursive: true });
  fs.writeFileSync(teamFile, 'main_branch: "main"\n');
  git(folder, "init", "-q", "-b", "main");
  git(folder, "config", "user.name", "Djinn e2e");
  git(folder, "config", "user.email", "e2e@example.com");
  git(folder, "remote", "add", "origin", "https://example.invalid/wick.git");
  git(folder, "add", "-A");
  git(folder, "commit", "-q", "-m", "the wick");
  git(folder, "update-ref", "refs/remotes/origin/main", "HEAD");
  git(folder, "update-ref", "refs/remotes/origin/dev", "HEAD");
  const commit = git(folder, "rev-parse", "--short=7", "HEAD");
  djinn("project", "add", folder, "--name", "wick");

  await page.goto(process.env.DJINN_URL!);
  await page
    .locator(".project-list")
    .getByRole("button", { name: "wick", exact: true })
    .click();
  const panel = page.getByRole("dialog", { name: "wick" });
  const reference = panel
    .locator(".config-box")
    .filter({ hasText: "Reference branch" });
  await expect(reference.locator(".config-from")).toHaveText("the team's file");
  await reference.getByRole("button", { name: "Change" }).click();
  const list = reference.getByRole("combobox", { name: "Reference branch" });
  await expect(list.locator("option")).toHaveText(["dev", "main"], {
    useInnerText: true,
  });
  await expect(reference).toContainText("never in the repository");
  await list.selectOption("dev");
  await reference.getByRole("button", { name: "Choose" }).click();
  await expect(reference).toContainText(`dev origin/dev at ${commit}`);
  await expect(reference.locator(".config-from")).toHaveText("your file");
  await expect(reference.getByRole("combobox")).toHaveCount(0);

  expect(fs.readFileSync(teamFile, "utf8")).toBe('main_branch: "main"\n');
  const shown = JSON.parse(djinn("project", "show", "wick", "--json"));
  expect(shown.reference).toMatchObject({
    branch: "dev",
    source: "SETTING_SOURCE_DEVELOPER",
  });
  // prototext puts one or two spaces after a colon.
  expect(fs.readFileSync(shown.developer_file, "utf8")).toMatch(
    /main_branch:\s+"dev"/,
  );

  // A branch the remote does not hold is refused; --unset gives the team's main back.
  expect(() => djinn("project", "set-branch", "wick", "nope")).toThrow(
    /holds no origin\/nope/,
  );
  const unset = JSON.parse(
    djinn("project", "set-branch", "wick", "--unset", "--json"),
  );
  expect(unset.reference).toMatchObject({
    branch: "main",
    source: "SETTING_SOURCE_REPOSITORY",
  });
});
