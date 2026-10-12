// The buttons at the top of a wish, against the real djinn up --browser (see global-setup.ts): Share writes the wish's
// export (binary protobuf), a file the command line imports again; Lead resumes the one lead in its terminal, focused
// on the wish. The lead is a fake claude on djinn's PATH that repeats what it reads. The chip beside the push strategy
// says where the wish's work lands, in a real Git repository, and changes its branch. The trash has its own spec
// (wish-lifecycle.spec.ts).
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";
import { dropLead, leadFolder, leadRows, setLead } from "./lead";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const session = "6c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e4f";

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

// open makes a wish set aside, and shows it in the window.
async function open(page: import("@playwright/test").Page, title: string) {
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await expect(page.locator(".hero h1")).toHaveText(title);
  return wishId;
}

test("Share writes the wish's export, which imports again", async ({
  page,
}) => {
  const title = `Share the lamp ${randomUUID().slice(0, 8)}`;
  const wishId = await open(page, title);
  djinn(
    "block",
    "put",
    wishId,
    "--kind",
    "note",
    "--title",
    "Oil",
    "--content",
    "Whale oil, no more.",
  );

  await page
    .locator(".topbar-actions")
    .getByRole("button", { name: "Share" })
    .click();
  const toast = page.locator(".toast");
  await expect(toast).toContainText("Wish exported to ");
  const file = (await toast.innerText())
    .replace(/^.*Wish exported to /s, "")
    .trim();
  expect(fs.statSync(file).size).toBeGreaterThan(0);

  // The file is the wish: gone, then imported again from it, with its title and its block.
  djinn("wish", "delete", wishId);
  djinn("wish", "import", file);
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes as {
    id: string;
    title: string;
  }[];
  expect(listed.find((w) => w.id === wishId)?.title).toBe(title);
  const blocks = JSON.parse(djinn("block", "list", wishId, "--json"))
    .blocks as { title: string }[];
  expect(blocks.map((b) => b.title)).toContain("Oil");
  djinn("wish", "delete", wishId);
});

test.describe("Lead", () => {
  test.skip(process.platform === "win32", "the fake claude is a shell script");
  test.afterEach(dropLead);

  test("Lead resumes the one lead in its terminal, focused on the wish", async ({
    page,
  }) => {
    const folder = leadFolder();
    const title = `Lead from the button ${randomUUID().slice(0, 8)}`;
    const wishId = await open(page, title);
    setLead(session, folder);

    const lead = page
      .locator(".topbar-actions")
      .getByRole("button", { name: "Lead" });
    await expect(lead).toHaveAttribute(
      "title",
      "Focus the lead on this wish, in the terminal",
    );
    await lead.click();
    const terminal = page.getByRole("region", { name: "Terminal" });
    await expect(
      terminal.locator(".lead-terminal-command").first(),
    ).toContainText(`claude --resume ${session}`);
    await expect(
      leadRows(page, `fake-claude --resume ${session} in ${folder}`),
    ).toHaveCount(1);
    // Typed in the lead, so echoed, then read after Enter: the fake lead says it again.
    await expect(
      leadRows(page, `Focus on the wish ${title} (${wishId})`),
    ).toHaveCount(2);
    djinn("wish", "delete", wishId);
  });
});

function git(dir: string, ...args: string[]): string {
  return execFileSync(
    "git",
    ["-c", "user.name=e2e", "-c", "user.email=e2e@example.com", ...args],
    { cwd: dir, encoding: "utf8" },
  ).trim();
}

// contrast is the ratio between an element's text colour and the first ground behind it that is not transparent.
async function contrast(
  page: import("@playwright/test").Page,
  selector: string,
) {
  return page
    .locator(selector)
    .first()
    .evaluate((el) => {
      const rgb = (c: string) => (c.match(/[\d.]+/g) ?? []).map(Number);
      const lum = ([r, g, b]: number[]) =>
        [r, g, b]
          .map((v) => v / 255)
          .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
          .reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i], 0);
      let ground: Element | null = el;
      let back = [0, 0, 0, 0];
      while (ground && (back.length < 3 || back[3] === 0)) {
        back = rgb(getComputedStyle(ground).backgroundColor);
        ground = ground.parentElement;
      }
      const [a, b] = [lum(rgb(getComputedStyle(el).color)), lum(back)].sort(
        (x, y) => y - x,
      );
      return (a + 0.05) / (b + 0.05);
    });
}

test("the head says where the wish's work lands, and a click changes the branch", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const suffix = randomUUID().slice(0, 8);
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lands-")),
  );
  git(folder, "init", "--quiet", "-b", "main");
  fs.writeFileSync(path.join(folder, "README.md"), "A lamp.\n");
  git(folder, "add", ".");
  git(folder, "commit", "--quiet", "-m", "A lamp");
  git(folder, "branch", "release");
  const projectId = JSON.parse(
    djinn("project", "add", folder, "--name", `lands-${suffix}`, "--json"),
  ).project.id as string;
  const title = `Where it lands ${suffix}`;
  const wishId = JSON.parse(
    djinn(
      "wish",
      "make",
      title,
      "--project-ids",
      projectId,
      "--paused",
      "--json",
    ),
  ).wish.id as string;
  const landsIn = () =>
    (
      JSON.parse(djinn("wish", "list", "--json")).wishes as {
        id: string;
        integration_branches?: { branch: string }[];
      }[]
    ).find((w) => w.id === wishId)?.integration_branches?.[0]?.branch;
  try {
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const chip = page.locator(".hero-meta button.wish-lands");
    await expect(chip).toHaveText("Lands in main");

    // Read in both themes, switched as the settings do: a reload within a minute of the Lead test above would show
    // its wish again, the one djinn was last asked to show.
    for (const theme of ["dark", "light"] as const) {
      await page.evaluate((v) => {
        localStorage.setItem("djinn.theme", v);
        document.documentElement.dataset.theme = v;
      }, theme);
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      await expect(chip).toBeVisible();
      // Polled: the colours fade from one theme to the other.
      await expect
        .poll(() => contrast(page, ".hero-meta .wish-lands code"))
        .toBeGreaterThanOrEqual(4.5);
      await expect
        .poll(() => contrast(page, ".hero-meta .wish-lands > span"))
        .toBeGreaterThanOrEqual(4.5);
    }

    // The picker lists the project's branches; a new name is made from the current one.
    await chip.click();
    const picker = page.getByRole("dialog", {
      name: "Where the wish's work lands",
    });
    await expect(picker.locator(".branch-option")).toHaveCount(2);
    await expect(picker.getByRole("button", { name: "main" })).toHaveAttribute(
      "aria-current",
      "true",
    );
    await expect(picker.getByRole("button", { name: "release" })).toBeVisible();
    await picker.getByRole("textbox", { name: "Branch" }).fill("feat/lamp");
    await picker
      .getByRole("button", { name: "New branch feat/lamp from main" })
      .click();
    await expect(page.locator(".toast")).toContainText(
      "The wish's work now lands in feat/lamp.",
    );
    await expect(chip).toHaveText("Lands in feat/lamp");
    await expect(picker).toHaveCount(0);
    expect(landsIn()).toBe("feat/lamp");
    expect(git(folder, "rev-parse", "feat/lamp")).toBe(
      git(folder, "rev-parse", "main"),
    );

    // An existing branch, picked from the list.
    await chip.click();
    await picker.getByRole("button", { name: "release" }).click();
    await expect(chip).toHaveText("Lands in release");
    expect(landsIn()).toBe("release");
    expect(errors).toEqual([]);
  } finally {
    await page.evaluate(() => localStorage.removeItem("djinn.theme"));
    djinn("wish", "delete", wishId);
  }
});
