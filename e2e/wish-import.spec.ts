// Import a wish file from the interface, against the real djinn up --browser (see global-setup.ts). Every run makes
// fresh identifiers, so --repeat-each works on the same djinn: a wish imported twice is already here.
import { type Page, expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
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

type Wish = { id: string; state: string; ready?: boolean };
const wishes = (): Wish[] => JSON.parse(djinn("wish", "list", "--json")).wishes;

// open shows the interface once its saved workspace is loaded. The load replaces the whole workspace: a wish
// imported before it ends is lost. The status bar says "Sauvegarde locale" only after it.
async function open(page: Page) {
  await page.goto(process.env.DJINN_URL!);
  await expect(
    page.locator(".app-statusbar").getByText("Sauvegarde locale"),
  ).toBeVisible();
}

// importFile imports a wish file through the interface's button and its file picker.
async function importFile(page: Page, file: string) {
  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Importer un souhait" }).click();
  await (await chooser).setFiles(file);
}

// A wish export in its JSON form, as `djinn wish export --file x.json` writes it.
const at = "2026-10-07T21:10:00Z";
const exportOf = (wishId: string) => ({
  version: 1,
  create_time: at,
  wish: {
    id: wishId,
    title: "Ship the lamp",
    project_ids: ["p1"],
    create_time: at,
  },
  projects: [
    {
      id: "p1",
      name: "lamp-e2e",
      remote: "https://example.com/acme/lamp-e2e",
      git: true,
    },
  ],
  tasks: [
    {
      id: randomUUID(),
      wish_id: wishId,
      project_id: "p1",
      code: "T01",
      title: "Polish the brass",
      status: "TASK_STATUS_PENDING",
      create_time: at,
    },
  ],
  questions: [
    {
      id: randomUUID(),
      code: "Q01",
      wish_id: wishId,
      text: "Which oil for the wick?",
      options: ["Olive — the classic", "Paraffin — brighter"],
      recommendation: "A, for the smell.",
      create_time: at,
    },
  ],
  blocks: [
    {
      id: randomUUID(),
      wish_id: wishId,
      kind: "section",
      title: "Lexicon of the lamp",
      position: "1000",
      content: "# Lexicon\n\nA **wick** carries the oil.",
      create_time: at,
    },
  ],
});

test("a wish file imports from the interface", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-import-"));
  const file = path.join(dir, "lamp.json");
  fs.writeFileSync(file, JSON.stringify(exportOf(randomUUID())));

  await open(page);
  await importFile(page, file);

  await expect(page.getByText("Ship the lamp").first()).toBeVisible();
  await expect(page.getByText("Which oil for the wick?").first()).toBeVisible();
  await expect(page.getByText("Lexicon of the lamp").first()).toBeVisible();
  await page.getByRole("button", { name: "Ouvrir le support" }).first().click();
  await expect(page.getByText("A wick carries the oil.").first()).toBeVisible();
  // A wish at work: no result to approve, nothing to grant.
  await expect(page.getByText("Valider ce résultat")).toHaveCount(0);
  await expect(page.getByText("Mon vœu est exaucé")).toHaveCount(0);
  await page.screenshot({
    path: path.join(__dirname, "../test-results/e2e/wish-import.png"),
  });
  fs.rmSync(dir, { recursive: true, force: true });
  expect(errors).toEqual([]);
});

// A wish whose tasks are all done and whose question is answered: Djinn proposes to grant it. Its title is its own:
// the interface finds a mission's wish by title.
const readyOf = (readyId: string, title: string) => ({
  version: 1,
  create_time: at,
  wish: { id: readyId, title, create_time: at },
  tasks: [
    {
      id: randomUUID(),
      wish_id: readyId,
      code: "T01",
      title: "Trim the wick",
      status: "TASK_STATUS_DONE",
      create_time: at,
    },
  ],
  questions: [
    {
      id: randomUUID(),
      code: "Q01",
      wish_id: readyId,
      text: "Light it tonight?",
      create_time: at,
      answer: { choice: "CHOICE_YES", create_time: at },
    },
  ],
});

test("a ready wish is granted by the user, from the interface", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-import-"));
  const file = path.join(dir, "ready.json");
  const readyId = randomUUID();
  const title = `Light the lamp ${readyId.slice(0, 8)}`;
  fs.writeFileSync(file, JSON.stringify(readyOf(readyId, title)));
  // A djinn grants three wishes at a time: the earlier specs' wishes make way, or this one would import paused.
  for (const w of wishes().filter((w) => w.state === "WISH_STATE_ACTIVE"))
    djinn("wish", "pause", w.id);
  // As on a loaded machine, the saved workspace comes late: the import must wait for it, or it is lost.
  await page.route("**/ui.v1.UiService/LoadState", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 400));
    await route.continue();
  });

  await open(page);
  await importFile(page, file);

  await expect(page.getByText(title).first()).toBeVisible();
  const grant = page.getByRole("button", { name: "Mon vœu est exaucé" });
  await expect(grant).toBeVisible();
  // Djinn proposes, and never grants by itself.
  const state = () => wishes().find((w) => w.id === readyId)!;
  expect(state().ready).toBe(true);
  expect(state().state).toBe("WISH_STATE_ACTIVE");

  await grant.click();
  await expect(page.getByText("Souhait exaucé").first()).toBeVisible();
  await expect(grant).toHaveCount(0);
  expect(state().state).toBe("WISH_STATE_GRANTED");
  await page.screenshot({
    path: path.join(__dirname, "../test-results/e2e/wish-granted.png"),
  });
  fs.rmSync(dir, { recursive: true, force: true });
  expect(errors).toEqual([]);
});
