// A project's gear in the menu and its configuration panel, against the real djinn up --browser (see global-setup.ts):
// a Git project whose copy has another branch checked out than its reference, with the team's settings, a skill that
// offers a wish template, one that is know-how alone, and one that offers an inbox source this machine has not decided
// on. Its gear turns red with a small flame, which flickers by transform and opacity only and stands still in a window
// at rest; folded, the flame passes onto its folder; the flying carpet's inbox gives the project one line. Both open
// the panel, which says what to fix first, the reference branch (chosen in your own file or taken back from the CLI),
// the checks and settings with where each comes from, and each skill's role. Saying no to the source settles it: the
// gear goes grey and the line goes. PROJECT_SHOTS=<folder> also writes the panel's reference box there.
import { expect, test } from "./test";
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
  return execFileSync(
    "git",
    [
      "-C",
      dir,
      "-c",
      "commit.gpgsign=false",
      "-c",
      "maintenance.auto=false",
      ...args,
    ],
    { encoding: "utf8" },
  ).trim();
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

test("a project with something to fix: its gear and inbox line open its panel; the panel shows what to fix, its reference branch, where values come from and each skill's role", async ({
  page,
}) => {
  // The page is measured in both themes, three tabs each (W111): more than the default budget.
  test.setTimeout(90_000);
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
  const teamFile = path.join(folder, ".agents", "djinn.yaml");
  const teamSettings =
    'settings:\n  checks:\n    - {name: lint, command: "true", when: commit}\n  merge_main: off\n' +
    "  instructions:\n    general: Run the tasks, not the tools.\n    mockup: Both themes.\n";
  fs.writeFileSync(teamFile, teamSettings);
  // The reference: origin/main as fetched, the remote's default branch; the copy works on feature.
  git(folder, "init", "-q", "-b", "main");
  git(folder, "config", "user.name", "Djinn e2e");
  git(folder, "config", "user.email", "e2e@example.com");
  git(folder, "remote", "add", "origin", "https://example.invalid/lantern.git");
  git(folder, "add", "-A");
  git(folder, "commit", "-q", "-m", "the lamp");
  git(folder, "update-ref", "refs/remotes/origin/main", "HEAD");
  git(folder, "update-ref", "refs/remotes/origin/dev", "HEAD");
  git(
    folder,
    "symbolic-ref",
    "refs/remotes/origin/HEAD",
    "refs/remotes/origin/main",
  );
  git(folder, "checkout", "-q", "-b", "feature");
  const commit = git(folder, "rev-parse", "--short=7", "HEAD");
  djinn("project", "add", folder, "--name", "lantern");
  try {
    // Djinn counts what waits for you in the project: the source no one decided on.
    const lantern = JSON.parse(
      djinn("project", "list", "--json"),
    ).projects.find((p: { name: string }) => p.name === "lantern");
    expect(lantern.to_fix).toBe(1);
    // The folder as Djinn stored it, which the window shows: on Windows its long name, where the temporary folder may
    // give a short one (RUNNER~1) that realpathSync keeps.
    const directory: string = lantern.directory;
    expect(path.basename(directory)).toBe(path.basename(folder));
    const notice = JSON.parse(
      djinn("project", "show", "lantern", "--json"),
    ).notices.find(
      (n: { kind: string }) => n.kind === "PROJECT_NOTICE_KIND_TO_FIX",
    );
    expect(notice.subject).toBe("lantern/lamp-news");

    await page.goto(process.env.DJINN_URL!);
    const row = page
      .locator(".project-list .project-nav")
      .filter({ hasText: "lantern" });
    const gear = row.locator("button.project-gear");
    await expect(gear).toHaveAttribute(
      "aria-label",
      "Configuration of lantern: 1 thing to fix",
    );
    await expect(gear).toHaveClass(/\bhot\b/);
    await expect(gear.locator(".project-flame")).toBeVisible();
    await expect(page.locator(".project-list .project-flame")).toHaveCount(
      await page.locator(".project-list .project-gear.hot").count(),
    );

    // The flame flickers by transform and opacity alone, and stands still once the window rests.
    const flicker = await gear.locator(".project-flame-glyph").evaluate((el) =>
      el.getAnimations().map((a) => ({
        name: (a as CSSAnimation).animationName,
        keys: [
          ...new Set(
            (a.effect as KeyframeEffect)
              .getKeyframes()
              .flatMap((k) => Object.keys(k))
              .filter(
                (key) =>
                  !["offset", "easing", "composite", "computedOffset"].includes(
                    key,
                  ),
              ),
          ),
        ].sort(),
      })),
    );
    expect(flicker).toEqual([
      { name: "project-flame", keys: ["opacity", "transform"] },
    ]);
    await page.evaluate(() => {
      Document.prototype.hasFocus = () => false;
      window.dispatchEvent(new Event("blur"));
    });
    await expect(page.locator("html[data-still]")).toHaveCount(1);
    expect(
      await gear
        .locator(".project-flame-glyph")
        .evaluate((el) => el.getAnimations().map((a) => a.playState)),
    ).toEqual(["paused"]);
    await page.evaluate(() => {
      Document.prototype.hasFocus = () => true;
      window.dispatchEvent(new Event("focus"));
    });
    await expect(page.locator("html[data-still]")).toHaveCount(0);

    // Folded, no gear: the flame passes onto the folder, whose tooltip counts.
    await page.getByRole("button", { name: "Collapse the navigation" }).click();
    await expect(page.locator(".project-list .project-gear")).toHaveCount(0);
    await expect(
      page
        .locator(".project-list")
        .getByTitle(`${directory} · 1 thing to fix`, { exact: true })
        .locator(".project-folder .project-flame"),
    ).toBeVisible();
    await page.getByRole("button", { name: "Expand the navigation" }).click();

    // The flying carpet's inbox gives the project one line: its name, its count, the first thing to fix as Djinn says
    // it; its button opens the panel, and so does the gear.
    await page.locator(".sidebar .plan-nav").click();
    const line = page
      .locator(".inbox .inbox-project")
      .filter({ hasText: "Project lantern" });
    await expect(line).toHaveCount(1);
    await expect(line).toContainText("1 thing to fix");
    await expect(line).toContainText(notice.text);
    // The settings are a page of the window, in tabs (W111).
    const panel = page.locator(".project-page");
    const tab = (name: string) =>
      panel.getByRole("tab", { name, exact: false }).click();
    await line.getByRole("button", { name: "Open its configuration" }).click();
    await expect(panel).toBeVisible();
    await page.locator(".sidebar .plan-nav").click();
    await expect(panel).toBeHidden();

    await gear.click();
    await expect(panel.locator(".eyebrow")).toHaveText("PROJECT CONFIGURATION");
    await expect(panel.getByRole("tab")).toHaveCount(8);

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
    const reference = boxes.filter({
      has: page.locator(".config-line strong", { hasText: "Reference branch" }),
    });
    await expect(reference).toContainText(`main origin/main at ${commit}`);
    await expect(reference.locator(".config-from")).toHaveText(
      "Djinn's default",
    );
    await expect(reference).toContainText(
      "Your copy has feature checked out, not the reference branch origin/main",
    );
    await expect(reference).toContainText("no worker starts from your copy");
    const shots = process.env.PROJECT_SHOTS;
    if (shots) {
      await reference.scrollIntoViewIfNeeded();
      await reference.screenshot({
        path: path.join(shots, "project-reference.png"),
      });
    }

    // Where each value comes from: the team's file names the check and merge_main, the file itself on hover; the
    // checks are sourced as djinn.yaml (C10).
    await tab("Gates and checks");
    const checks = boxes.filter({
      has: page.locator("h3", { hasText: "Checks" }),
    });
    await expect(checks.locator(".setting-row strong")).toHaveText("lint");
    const team = checks.locator(".setting-row .config-from");
    await expect(team).toHaveText("the team's file");
    await expect(team).toHaveAttribute(
      "title",
      path.join(directory, ".agents", "djinn.yaml"),
    );
    await expect(checks.locator("h3 .config-from")).toHaveText("djinn.yaml");
    await tab("Integration and push");
    const mergeMain = panel.locator('tr[data-setting="merge_main"]');
    await expect(mergeMain.locator('[data-side="repository"]')).toHaveText(
      "off",
    );
    await expect(mergeMain.locator('[data-side="local"]')).toHaveText("absent");
    await expect(mergeMain.locator('[data-side="retained"]')).toContainText(
      "the team's file",
    );
    // Where workers start, for each push strategy, the project's own marked (W227): never from the copy.
    await expect(panel).toContainText(
      "Per wish: from the wish's integration branch; a new wish's is main",
    );
    await expect(panel).toContainText("Never from your copy");

    // Each skill's role for Djinn.
    await tab("Skills");
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

    await tab("General");
    // The reference branch chosen in the panel (W149): Change lists the remote's branches; the choice is written in
    // your own file, never in the team's, and wins over its name; djinn project set-branch --unset takes it back.
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

    expect(fs.readFileSync(teamFile, "utf8")).toBe(teamSettings);
    const devShown = JSON.parse(djinn("project", "show", "lantern", "--json"));
    expect(devShown.reference).toMatchObject({
      branch: "dev",
      source: "SETTING_SOURCE_DEVELOPER",
    });
    expect(fs.readFileSync(devShown.developer_file, "utf8")).toMatch(
      /main_branch:\s+"dev"/,
    );

    // A branch the remote does not hold is refused; --unset gives the default back (or the team's when set).
    expect(() => djinn("project", "set-branch", "lantern", "nope")).toThrow(
      /holds no origin\/nope/,
    );
    fs.writeFileSync(teamFile, teamSettings + "  main_branch: main\n");
    git(folder, "commit", "-q", "-am", "set main_branch");
    git(folder, "update-ref", "refs/remotes/origin/main", "HEAD");
    git(folder, "reset", "-q", "--hard", "HEAD~1");
    const unsetRepo = JSON.parse(
      djinn("project", "set-branch", "lantern", "--unset", "--json"),
    );
    expect(unsetRepo.reference).toMatchObject({
      branch: "main",
      source: "SETTING_SOURCE_REPOSITORY",
    });
    git(folder, "update-ref", "refs/remotes/origin/main", "HEAD");

    // Saying no settles it: nothing left to fix, the source unplugged on this machine, the gear grey, the inbox line gone.
    await toFix.getByRole("button", { name: "Say no" }).click();
    await expect(panel.locator(".config-box.to-fix")).toHaveCount(0);
    await tab("Skills");
    await expect(
      panel.locator(".inbox-source-row").filter({ hasText: "lamp-news" }),
    ).toContainText("Unplugged: runs nothing");
    // The workers' instructions by kind (W114): the Summoning Circle's and your own set from the command line over
    // the repository's; the tab shows the three layers, a kind on the general text, and writes yours from the window.
    djinn("lead", "instructions", "--kind", "mockup", "--text", "Light first.");
    djinn(
      "project",
      "instructions",
      "lantern",
      "--kind",
      "mockup",
      "--text",
      "Dark first.",
    );
    // The page reads the project as it opens: leave it and open it again by its gear.
    await page
      .locator(".sidebar")
      .getByText("Hourglass", { exact: true })
      .click();
    await expect(panel).toBeHidden();
    await gear.click();
    await tab("Workers' brief");
    const kind = (name: string) =>
      panel.locator(`tbody[data-instruction="${name}"]`);
    await expect(kind("mockup").locator('td[data-side="machine"]')).toHaveText(
      "Light first.",
    );
    await expect(
      kind("mockup").locator('td[data-side="repository"] .setting-struck'),
    ).toHaveText("Both themes.");
    await expect(kind("mockup").locator('td[data-side="retained"]')).toHaveText(
      "Dark first.your file",
    );
    await expect(kind("work").locator('td[data-side="retained"]')).toHaveText(
      "Run the tasks, not the tools.the general text",
    );
    await kind("ifrit").getByRole("button", { name: "set mine" }).click();
    await kind("ifrit")
      .getByRole("textbox", { name: /Your instructions to the Ifrit/ })
      .fill("Say the URL.");
    await kind("ifrit").getByRole("button", { name: "Save" }).click();
    await expect(kind("ifrit").locator('td[data-side="retained"]')).toHaveText(
      "Say the URL.your file",
    );
    const mine = JSON.parse(
      djinn("project", "instructions", "lantern", "--json"),
    ).instructions.find(
      (r: { kind: string }) => r.kind === "INSTRUCTION_KIND_IFRIT",
    );
    expect(mine).toMatchObject({
      developer: "Say the URL.",
      source: "SETTING_SOURCE_DEVELOPER",
    });
    await shotsAndContrast(page, panel);
    // Without an active wish the flying carpet has no entry: leave by the Hourglass.
    await page
      .locator(".sidebar")
      .getByText("Hourglass", { exact: true })
      .click();
    await expect(panel).toBeHidden();
    await expect(gear).toHaveAttribute(
      "aria-label",
      "Configuration of lantern",
    );
    await expect(gear).not.toHaveClass(/\bhot\b/);
    await expect(gear.locator(".project-flame")).toHaveCount(0);
    await expect(line).toHaveCount(0);

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
  } finally {
    try {
      djinn("inbox", "unplug", "lantern/lamp-news");
    } catch {}
    // The Summoning Circle's text is the shared djinn's, for every project: the next specs never see it.
    try {
      djinn("lead", "instructions", "--kind", "mockup", "--unset");
    } catch {}
  }
});

// shotsAndContrast measures the page's secondary text against the ground under it, in both themes: 4.5:1 at least
// (W111); PROJECT_SHOTS=<folder> also writes a shot of each theme.
async function shotsAndContrast(
  page: import("@playwright/test").Page,
  panel: import("@playwright/test").Locator,
) {
  for (const theme of ["dark", "light"]) {
    await page.evaluate((t) => {
      document.documentElement.dataset.theme = t;
    }, theme);
    for (const name of [
      "General",
      "Integration and push",
      "Workers' brief",
      "Permissions",
    ]) {
      await panel.getByRole("tab", { name, exact: false }).click();
      const worst = await panel.evaluate((root) => {
        const lin = (c: number) =>
          c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
        const lum = (rgb: string) => {
          const [r, g, b] = (rgb.match(/[\d.]+/g) ?? []).map(Number);
          return (
            0.2126 * lin(r / 255) +
            0.7152 * lin(g / 255) +
            0.0722 * lin(b / 255)
          );
        };
        const ground = (el: Element | null): string => {
          for (; el; el = el.parentElement) {
            const bg = getComputedStyle(el).backgroundColor;
            if (bg && !bg.startsWith("rgba(0, 0, 0, 0)") && !/, 0\)$/.test(bg))
              return bg;
          }
          return getComputedStyle(document.body).backgroundColor;
        };
        let min = { ratio: 99, text: "" };
        const sel =
          ".settings-table th, .setting-key, .setting-absent, .setting-struck, .settings-foot, .settings-lead, .project-tab-detail, .config-from, .setting-note td";
        for (const el of root.querySelectorAll(sel)) {
          if (!(el as HTMLElement).offsetParent || !el.textContent?.trim())
            continue;
          const a = lum(getComputedStyle(el).color);
          const b = lum(ground(el));
          const ratio = (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
          if (ratio < min.ratio)
            min = {
              ratio,
              text: `${el.className}: ${el.textContent.slice(0, 40)}`,
            };
        }
        return min;
      });
      expect(
        worst.ratio,
        `${theme} ${name}: ${worst.text}`,
      ).toBeGreaterThanOrEqual(4.5);
    }
    const shots = process.env.PROJECT_SHOTS;
    if (shots) {
      await panel.getByRole("tab", { name: "General" }).click();
      await page.screenshot({
        path: path.join(shots, `project-page-${theme}.png`),
        fullPage: true,
      });
      // The instructions by kind, whole: the terminal's panel covers the lower half of the window.
      await panel.getByRole("tab", { name: "Workers' brief" }).click();
      const size = page.viewportSize();
      await page.setViewportSize({ width: 1440, height: 1800 });
      await panel
        .locator(".settings-box")
        .first()
        .screenshot({ path: path.join(shots, `project-brief-${theme}.png`) });
      if (size) await page.setViewportSize(size);
    }
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}
