"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const os = require("node:os");
const { _electron } = require("playwright");
(async () => {
  const root = path.resolve(__dirname, "..");
  const profile = await fs.realpath(
    await fs.mkdtemp(path.join(os.tmpdir(), "djinn-mission-ui-")),
  );
  const bin = path.join(profile, "bin");
  await fs.mkdir(bin);
  const quote = (value) => "'" + value.replaceAll("'", "'\\''") + "'";
  await fs.writeFile(
    path.join(bin, "codex"),
    "#!/bin/sh\nexec " +
      quote(process.execPath) +
      " " +
      quote(path.join(root, "tests/fixtures/fake-codex.cjs")) +
      ' "$@"\n',
    { mode: 0o755 },
  );
  const app = await _electron.launch({
    executablePath: require("electron"),
    args: [root],
    env: {
      ...process.env,
      PATH: bin + path.delimiter + process.env.PATH,
      DJINN_USER_DATA: profile,
      DJINN_DEV_URL: "",
      DJINN_FIXTURE_REQUESTS: path.join(profile, "requests.jsonl"),
    },
  });
  const failures = [];
  try {
    const page = await app.firstWindow();
    page.setDefaultTimeout(8_000);
    await app.evaluate(({ BrowserWindow }) =>
      BrowserWindow.getAllWindows()[0].hide(),
    );
    await page.waitForSelector(".app-statusbar");
    const environment = await page.evaluate(() =>
      window.djinn.getEnvironment(),
    );
    assert.match(
      environment.providers.find((provider) => provider.id === "codex")
        ?.version || "",
      /codex-fixture/,
      "creation must use the fake provider",
    );
    await page
      .getByRole("button", { name: "Créer un projet", exact: true })
      .first()
      .click();
    const intention = page.getByRole("textbox", { name: "Nom du projet" });
    await intention.fill("Vérifier le focus et le défilement de la création");
    const observe = async (selector) =>
      page.evaluate(async (selector) => {
        const input = document.querySelector(selector),
          modal = document.querySelector('[role="dialog"]');
        input.focus({ preventScroll: true });
        const records = [];
        for (let i = 0; i < 12; i++) {
          await new Promise((r) => setTimeout(r, 250));
          records.push({
            focused: document.activeElement === input,
            scroll: modal.scrollTop,
          });
        }
        return records;
      }, selector);
    const first = await observe('[role="dialog"] input');
    if (first.some((x) => !x.focused))
      failures.push(
        "Le focus quitte le nom du projet pendant les ticks du parent",
      );
    await page.getByText("Réglages avancés", { exact: true }).click();
    const picker = page.getByRole("button", {
      name: "Choisir le modèle",
      exact: true,
    });
    await picker.click();
    const search = page.getByRole("combobox", {
      name: "Rechercher un modèle",
      exact: true,
    });
    await page.getByRole("option", { name: /Fixture Model/ }).waitFor();
    await search.fill("fixture fast");
    await search.evaluate((input) => input.focus());
    await search.evaluate((input) => input.scrollIntoView({ block: "center" }));
    const initial = await page
      .locator('[role="dialog"]')
      .evaluate((modal) => modal.scrollTop);
    const second = await observe('[role="dialog"] [role="combobox"]');
    if (second.some((x) => !x.focused))
      failures.push("Le focus quitte la recherche de modèle");
    if (initial < 100 || second.some((x) => x.scroll < initial - 5))
      failures.push("Le défilement de la deuxième partie revient en haut");
    assert.equal(await search.inputValue(), "fixture fast");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Enter");
    await search.waitFor({ state: "detached" });
    assert.match(await picker.innerText(), /Fixture Fast/);
    await picker.click();
    await search.waitFor();
    await page.keyboard.press("Escape");
    await search.waitFor({ state: "detached" });
    assert.equal(
      await page.locator('[role="dialog"]').count(),
      1,
      "first Escape only closes model picker",
    );
    const close = page.getByRole("button", {
      name: "Fermer la fenêtre",
      exact: true,
    });
    await close.focus();
    await page.keyboard.press("Shift+Tab");
    assert.equal(
      await page
        .locator('[role="dialog"]')
        .evaluate((modal) => modal.contains(document.activeElement)),
      true,
      "Shift+Tab remains trapped in the dialog",
    );
    await page.keyboard.press("Tab");
    assert.equal(
      await close.evaluate((button) => document.activeElement === button),
      true,
      "Tab wraps back to the first control",
    );
    await page.keyboard.press("Escape");
    await page.waitForSelector('[role="dialog"]', { state: "detached" });
    assert.equal(
      await page
        .getByRole("button", { name: "Créer un projet", exact: true })
        .first()
        .evaluate((button) => document.activeElement === button),
      true,
      "closing returns focus to its trigger",
    );
    if (process.env.DJINN_UI_MODAL_ONLY) {
      console.log(
        JSON.stringify({
          modalFocus:
            first.every((x) => x.focused) && second.every((x) => x.focused),
          scrollBefore: initial,
          scrollAfter: second.map((x) => x.scroll),
          failures,
        }),
      );
      assert.deepEqual(failures, []);
      return;
    }
    const order = await page.evaluate(() => {
      const selectors = [
        ".topbar",
        ".hero",
        ".step-timeline",
        ".mission-bar",
        ".team-section",
      ];
      return selectors.map((selector) => ({
        selector,
        y: document.querySelector(selector)?.getBoundingClientRect().top,
      }));
    });
    if (
      order.some((x) => x.y === undefined) ||
      order.some((x, i) => i > 0 && x.y < order[i - 1].y)
    )
      failures.push("Ordre des sections incorrect : " + JSON.stringify(order));
    assert.equal(
      await page.locator(".step-context,.execution-status").count(),
      0,
      "mission banners are removed",
    );
    assert.equal(
      await page.locator(".step-timeline .phase-circle").count(),
      4,
      "original circle timeline renders configured steps",
    );
    const heroes = await page.locator(".hero").count();
    if (heroes !== 1)
      failures.push("Le header de mission doit apparaître exactement une fois");
    const screenshots = process.env.DJINN_UI_SCREENSHOTS;
    if (screenshots) await fs.mkdir(screenshots, { recursive: true });
    for (const [width, height] of [
      [1440, 900],
      [1024, 900],
      [1024, 650],
    ]) {
      await app.evaluate(
        ({ BrowserWindow }, { width, height }) =>
          BrowserWindow.getAllWindows()[0].setContentSize(width, height),
        { width, height },
      );
      await page
        .locator(".tabbar")
        .getByRole("button", { name: /^Mission(?:\s*\d+)?$/ })
        .click();
      const dimensions = await page.evaluate(() => ({
        width: window.innerWidth,
        overflow: document.documentElement.scrollWidth > window.innerWidth,
        missionScroll: Array.from(
          document.querySelectorAll(".main-scroll,.mission-scroll"),
        ).filter(
          (el) =>
            getComputedStyle(el).overflowY === "auto" &&
            el.scrollHeight > el.clientHeight,
        ).length,
      }));
      if (dimensions.overflow)
        failures.push("Débordement horizontal à " + width + "px");
      if (dimensions.missionScroll > 1)
        failures.push("Deux zones de défilement de mission à " + width + "px");
      const reachable = await page.locator(".team-section").evaluate((team) => {
        let ancestor = team.parentElement;
        while (ancestor && ancestor !== document.body) {
          if (
            getComputedStyle(ancestor).overflowY === "auto" &&
            ancestor.clientHeight < 150
          )
            return false;
          ancestor = ancestor.parentElement;
        }
        return true;
      });
      if (!reachable)
        failures.push("Zone de travail écrasée à " + width + "x" + height);
      if (screenshots)
        await page.screenshot({
          path: path.join(
            screenshots,
            "mission-" + width + (height === 650 ? "-compact" : "") + ".png",
          ),
        });
    }
    await app.evaluate(({ BrowserWindow }) =>
      BrowserWindow.getAllWindows()[0].setContentSize(1024, 900),
    );
    await page
      .getByRole("button", { name: "Supports", exact: false })
      .first()
      .click();
    if ((await page.locator(".hero").count()) !== 1)
      failures.push("Le header de mission disparaît dans les Supports");
    if (process.env.DJINN_UI_SCREENSHOT)
      await page.screenshot({ path: process.env.DJINN_UI_SCREENSHOT });
    await page
      .getByRole("button", { name: "Créer un projet", exact: true })
      .first()
      .click();
    const project = path.join(profile, "project");
    await fs.mkdir(project);
    await page
      .getByRole("textbox", { name: "Nom du projet", exact: true })
      .fill("Projet de test");
    await page
      .getByRole("textbox", { name: /^Dossier du projet/ })
      .fill(project);
    await page.getByText("Réglages avancés", { exact: true }).click();
    await page
      .getByRole("spinbutton", { name: /Workers simultanés/ })
      .fill("8");
    await page
      .getByRole("button", { name: "Choisir le modèle", exact: true })
      .click();
    await page
      .getByRole("combobox", { name: "Rechercher un modèle" })
      .fill("fixture fast");
    await page.getByRole("option", { name: /Fixture Fast/ }).click();
    if (screenshots)
      await page.screenshot({
        path: path.join(screenshots, "creation-1024.png"),
      });
    await page
      .getByRole("button", { name: "Créer le projet", exact: true })
      .click();
    await page.waitForSelector('[role="dialog"]', { state: "detached" });
    await page
      .getByRole("button", { name: "Nouvelle mission", exact: true })
      .click();
    await page
      .getByRole("heading", { name: "Qu’allez-vous créer ?" })
      .waitFor();
    await page
      .getByRole("textbox", { name: "Votre intention" })
      .fill("Vérifier la mission fullscreen et ses supports.");
    await page
      .getByRole("button", { name: "Préparer la mission", exact: true })
      .click();
    await page
      .getByRole("heading", { name: "Djinn prépare votre timeline" })
      .waitFor();
    await page
      .getByRole("button", { name: "Mettre en pause", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Reprendre la préparation", exact: true })
      .waitFor();
    await page
      .getByText("Vérifier la mission fullscreen et ses supports.")
      .waitFor();
    if (screenshots)
      await page.screenshot({
        path: path.join(screenshots, "mission-created-1024.png"),
      });
    let requestText = "";
    for (let attempt = 0; attempt < 20 && !requestText.trim(); attempt += 1) {
      try {
        requestText = await fs.readFile(
          path.join(profile, "requests.jsonl"),
          "utf8",
        );
      } catch {
        requestText = "";
      }
      if (!requestText.trim())
        await new Promise((resolve) => setTimeout(resolve, 100));
    }
    assert.ok(requestText.trim(), "native provider requests were recorded");
    const requests = requestText.trim().split("\n").map(JSON.parse);
    assert.ok(
      requests.some(
        (r) => r.method === "thread/start" && r.model === "fixture-fast",
      ),
      "selected exact model reaches native provider",
    );
    assert.ok(
      requests.some(
        (r) => r.method === "turn/start" && r.model === "fixture-fast",
      ),
    );
    console.log(
      JSON.stringify({
        profile,
        modalFocus:
          first.every((x) => x.focused) && second.every((x) => x.focused),
        scrollBefore: initial,
        scrollAfter: second.map((x) => x.scroll),
        order,
        failures,
      }),
    );
    assert.deepEqual(failures, []);
  } finally {
    await app.close();
  }
})().catch((e) => {
  console.error(e.message);
  process.exitCode = 1;
});
