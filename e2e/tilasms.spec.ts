// The Tilasms tab, against the real djinn up --browser (see global-setup.ts): the lead puts a tilasm with the command
// line, the developer opens it in the tab, in a frame Djinn serves with a policy of its own (its scripts and files run,
// a fetch is refused, Djinn's page is out of reach), searches it, "talisman" too, and restores its first version once
// the lead put another. Every run makes a fresh wish, so --repeat-each works on the same djinn. A screenshot of the tab
// goes to test-results/e2e/.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const shots = path.join(__dirname, "../test-results/e2e");
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

// The tilasm: a page, a script of its own that says what it could do, and an image.
const index = `<!doctype html><html><head><title>The lamp's anatomy</title></head><body>
<h1>The lamp's anatomy</h1><p>A wick carries the oil to the flame.</p><img id="img" src="img/wick.svg" alt="wick">
<p id="run">scripts do not run</p><p id="net">no answer</p><p id="djinn">no answer</p>
<script src="js/app.js"></script></body></html>`;
const app = `document.getElementById("run").textContent = "scripts run";
document.addEventListener("securitypolicyviolation", (e) => {
  if (e.violatedDirective.startsWith("connect-src"))
    document.getElementById("net").textContent = "network refused by " + e.violatedDirective;
});
fetch(new URL("/plan.v1.WishService/List", location.href)).then(
  () => (document.getElementById("net").textContent = "network reached"),
  () => {},
);
try {
  document.getElementById("djinn").textContent = "Djinn reached: " + parent.document.title;
} catch {
  document.getElementById("djinn").textContent = "Djinn out of reach";
}`;

test("a tilasm put by the command line opens in the Tilasms tab, isolated, and its search finds it", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Explain the lamp ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-tilasm-"));
  try {
    fs.mkdirSync(path.join(dir, "js"));
    fs.mkdirSync(path.join(dir, "img"));
    fs.writeFileSync(path.join(dir, "index.html"), index);
    fs.writeFileSync(path.join(dir, "js/app.js"), app);
    fs.writeFileSync(
      path.join(dir, "img/wick.svg"),
      '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><rect width="20" height="20"/></svg>',
    );
    const put = JSON.parse(
      djinn("tilasm", "put", dir, "--wish", wishId, "--json"),
    ).tilasm;
    expect(put.code).toBe("L01");

    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const tab = page.getByRole("tab", { name: /^Tilasms/ });
    await expect(tab).toHaveText("Tilasms1");
    await tab.click();
    const panel = page.getByRole("tabpanel", { name: "Tilasms" });
    const row = panel.locator(".tilasm-row").filter({ hasText: "L01" });
    await expect(row).toContainText("The lamp's anatomy");
    await expect(row).toContainText("lead");

    // Open: the frame runs the tilasm's own script and shows its image; the network and Djinn are out of reach.
    await row.getByRole("button", { name: "The lamp's anatomy" }).click();
    const frame = page.frameLocator("iframe.tilasm-frame");
    await expect(frame.locator("#run")).toHaveText("scripts run");
    await expect(frame.locator("#net")).toHaveText(
      "network refused by connect-src",
    );
    await expect(frame.locator("#djinn")).toHaveText("Djinn out of reach");
    await expect
      .poll(() =>
        frame.locator("#img").evaluate((img: HTMLImageElement) => img.width),
      )
      .toBe(20);
    await page.screenshot({ path: path.join(shots, "tilasms-dark.png") });

    // The search, on the titles and the text within the wish; the kind's own name finds them all.
    const search = panel.getByRole("searchbox");
    for (const words of ["wick flame", "ANATOMY", "talisman", "talismans"]) {
      await search.fill(words);
      await expect(panel.locator(".tilasm-row")).toHaveCount(1);
    }
    await search.fill("paraffin");
    await expect(panel.locator(".tilasm-row")).toHaveCount(0);
    await expect(panel).toContainText("No tilasm holds “paraffin”");
    await search.fill("");
    await expect(panel.locator(".tilasm-row")).toHaveCount(1);

    // A new version put by the command line replaces what the frame shows; the history restores the first one.
    fs.writeFileSync(
      path.join(dir, "index.html"),
      index.replace("A wick carries the oil to the flame.", "Second version."),
    );
    djinn("tilasm", "put", dir, "--wish", wishId, "--code", "L01");
    await expect(frame.locator("body")).toContainText("Second version.");
    await row.getByRole("button", { name: "Versions" }).click();
    const versions = row.locator(".tilasm-history li");
    await expect(versions).toHaveCount(2);
    await versions
      .nth(1)
      .getByRole("button", { name: "Restore this version" })
      .click();
    await expect(frame.locator("body")).toContainText(
      "A wick carries the oil to the flame.",
    );
    await expect(versions).toHaveCount(3);
    await expect(versions.first()).toContainText("developer");
    await expect(versions.first()).toContainText("restores v1");
    expect(errors).toEqual([]);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
    djinn("wish", "pause", wishId);
  }
});
