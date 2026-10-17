// A project's gear in the menu, against the real djinn up --browser (see global-setup.ts): a project whose skill
// declares an inbox source this machine has not decided on has something to fix. Its gear turns red with a small
// flame, which flickers by transform and opacity only and stands still in a window at rest; folded, the flame passes
// onto its folder. The gear opens the project's panel; the flying carpet's inbox gives the project one line, which
// opens it too. Said no to, the source settles it: the gear goes grey and the line goes.
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

const skill = `---
name: kiln-mentions
description: The mentions of the kiln, for the end-to-end tests.
metadata:
  djinn:
    source:
      watch: "sh kiln-source.sh"
      summary: "The kiln's mentions."
---

# The kiln's mentions
`;

test("a project with something to fix: its gear red with a flame, folded on its folder, one line in the inbox; both open its panel", async ({
  page,
}) => {
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "kiln-")),
  );
  fs.mkdirSync(path.join(folder, ".agents", "skills", "kiln-mentions"), {
    recursive: true,
  });
  fs.writeFileSync(
    path.join(folder, ".agents", "skills", "kiln-mentions", "SKILL.md"),
    skill,
  );
  fs.writeFileSync(path.join(folder, "kiln-source.sh"), "exit 0\n");
  djinn("project", "add", folder, "--name", "kiln");
  try {
    // Djinn counts what waits for you in the project: the source no one decided on.
    const kiln = JSON.parse(djinn("project", "list", "--json")).projects.find(
      (p: { name: string }) => p.name === "kiln",
    );
    expect(kiln.to_fix).toBe(1);
    // The folder as Djinn stored it, which the window shows: on Windows its long name, where the temporary folder
    // may give a short one (RUNNER~1) that realpathSync keeps.
    const directory: string = kiln.directory;
    expect(path.basename(directory)).toBe(path.basename(folder));
    const notice = JSON.parse(
      djinn("project", "show", "kiln", "--json"),
    ).notices.find(
      (n: { kind: string }) => n.kind === "PROJECT_NOTICE_KIND_TO_FIX",
    );
    expect(notice.subject).toBe("kiln/kiln-mentions");

    await page.goto(process.env.DJINN_URL!);
    const row = page
      .locator(".project-list .project-nav")
      .filter({ hasText: "kiln" });
    const gear = row.locator("button.project-gear");
    await expect(gear).toHaveAttribute(
      "aria-label",
      "Configuration of kiln: 1 thing to fix",
    );
    await expect(gear).toHaveClass(/\bhot\b/);
    await expect(gear.locator(".project-flame")).toBeVisible();
    // A project with nothing to fix keeps a grey gear: no other flame than kiln's in the menu.
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

    // The gear opens the project's panel.
    await gear.click();
    const panel = page.getByRole("dialog", { name: "kiln" });
    await expect(panel).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(panel).toBeHidden();

    // Folded, no gear: the flame passes onto the folder, whose tooltip counts.
    await page.getByRole("button", { name: "Collapse the navigation" }).click();
    await expect(page.locator(".project-list .project-gear")).toHaveCount(0);
    // By its title, not a CSS attribute selector: a Windows path's backslashes are escapes in CSS.
    await expect(
      page
        .locator(".project-list")
        .getByTitle(`${directory} · 1 thing to fix`, { exact: true })
        .locator(".project-folder .project-flame"),
    ).toBeVisible();
    await page.getByRole("button", { name: "Expand the navigation" }).click();

    // The flying carpet's inbox gives the project one line: its name, its count, the first thing to fix as Djinn says
    // it; its button opens the same panel.
    await page.locator(".sidebar .plan-nav").click();
    const line = page
      .locator(".inbox .inbox-project")
      .filter({ hasText: "Project kiln" });
    await expect(line).toHaveCount(1);
    await expect(line).toContainText("1 thing to fix");
    await expect(line).toContainText(notice.text);
    await line.getByRole("button", { name: "Open its configuration" }).click();
    await expect(panel).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(panel).toBeHidden();

    // Said no to on this machine, the source settles it: the gear goes grey, the line goes.
    djinn("inbox", "unplug", "kiln/kiln-mentions");
    await expect(gear).toHaveAttribute("aria-label", "Configuration of kiln");
    await expect(gear).not.toHaveClass(/\bhot\b/);
    await expect(gear.locator(".project-flame")).toHaveCount(0);
    await expect(line).toHaveCount(0);
  } finally {
    try {
      djinn("inbox", "unplug", "kiln/kiln-mentions");
    } catch {}
  }
});
