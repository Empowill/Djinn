import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

// A wish export in its JSON form, as `djinn wish export --file x.json` writes it.
const wishId = "01a11833-a440-7479-a067-52615c91da71";
const at = "2026-10-07T21:10:00Z";
const exported = {
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
      id: "01a11876-6480-7ec2-9833-abd058ae4a59",
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
      id: "01a11876-6480-7ec2-9833-000000000001",
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
      id: "01a11876-6480-7ec2-9833-000000000010",
      wish_id: wishId,
      kind: "section",
      title: "Lexicon of the lamp",
      position: "1000",
      content: "# Lexicon\n\nA **wick** carries the oil.",
      create_time: at,
    },
  ],
};

test("a wish file imports from the interface", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-import-"));
  const file = path.join(dir, "lamp.json");
  fs.writeFileSync(file, JSON.stringify(exported));

  await page.goto(process.env.DJINN_URL!);
  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Importer un souhait" }).click();
  await (await chooser).setFiles(file);

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

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

// A wish whose tasks are all done and whose question is answered: Djinn proposes to grant it.
const readyId = "01a11833-a440-7479-a067-52615c91da72";
const ready = {
  version: 1,
  create_time: at,
  wish: { id: readyId, title: "Light the lamp", create_time: at },
  tasks: [
    {
      id: "01a11876-6480-7ec2-9833-abd058ae4a60",
      wish_id: readyId,
      code: "T01",
      title: "Trim the wick",
      status: "TASK_STATUS_DONE",
      create_time: at,
    },
  ],
  questions: [
    {
      id: "01a11876-6480-7ec2-9833-000000000021",
      code: "Q01",
      wish_id: readyId,
      text: "Light it tonight?",
      create_time: at,
      answer: { choice: "CHOICE_YES", create_time: at },
    },
  ],
};

test("a ready wish is granted by the user, from the interface", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-import-"));
  const file = path.join(dir, "ready.json");
  fs.writeFileSync(file, JSON.stringify(ready));

  await page.goto(process.env.DJINN_URL!);
  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Importer un souhait" }).click();
  await (await chooser).setFiles(file);

  await expect(page.getByText("Light the lamp").first()).toBeVisible();
  const grant = page.getByRole("button", { name: "Mon vœu est exaucé" });
  await expect(grant).toBeVisible();
  // Djinn proposes, and never grants by itself.
  const state = () =>
    JSON.parse(
      execFileSync(binary, ["wish", "list", "--json"], {
        env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
        encoding: "utf8",
      }),
    ).wishes.find((w: { id: string }) => w.id === readyId);
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
