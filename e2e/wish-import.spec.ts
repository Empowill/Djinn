import { expect, test } from "@playwright/test";
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
  await page.screenshot({
    path: path.join(__dirname, "../test-results/e2e/wish-import.png"),
  });
  fs.rmSync(dir, { recursive: true, force: true });
  expect(errors).toEqual([]);
});
