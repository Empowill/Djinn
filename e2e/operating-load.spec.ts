// End-to-end test for the operating load slider: 5 notches, keyboard and click
// navigation, server synchronization via LoadService RPCs, and live memory display.
import { expect, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { portable } from "./portable";

portable();

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

function djinnURL(pathname = "/"): string {
  const url = new URL(process.env.DJINN_URL!);
  url.pathname = pathname;
  return url.toString();
}

function collectErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  page.on("console", (msg) => {
    if (msg.type() === "error") errors.push(msg.text());
  });
  return errors;
}

function getNotch(): string {
  const out = djinn("load", "get").trim();
  const match = out.match(/notch:\s*(\w+)/);
  return match ? match[1] : out;
}

test.describe("operating load slider", () => {
  test.beforeEach(async () => {
    // Reset notch to medium before each test.
    djinn("load", "set", "medium");
  });

  test.afterEach(async () => {
    // Reset notch to medium after each test.
    djinn("load", "set", "medium");
  });

  test.afterAll(async () => {
    // Always leave notch at medium after tests finish.
    djinn("load", "set", "medium");
  });

  test("operating load slider displays in topbar and reflects server setting", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const loadEl = page.locator(".operating-load");
    await expect(loadEl).toBeVisible();

    const range = page.locator(".operating-load-input");
    await expect(range).toBeVisible();
    await expect(range).toHaveValue("3");

    const notches = ["minimal", "light", "medium", "high", "max"];
    for (const notch of notches) {
      const btn = page.locator(`.operating-load-notch[data-notch="${notch}"]`);
      await expect(btn).toBeVisible();
      const title = await btn.getAttribute("title");
      expect(title).toBeTruthy();
      expect(title?.length).toBeGreaterThan(10);
    }

    const activeBtn = page.locator(".operating-load-notch.active");
    await expect(activeBtn).toHaveAttribute("data-notch", "medium");
    expect(errors).toEqual([]);
  });

  test("keyboard navigation changes the load notch and notifies server", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const range = page.locator(".operating-load-input");
    await range.focus();

    // Step up to high (value 4)
    await page.keyboard.press("ArrowRight");
    await expect(range).toHaveValue("4");
    await expect.poll(getNotch).toBe("high");

    // Step up to max (value 5)
    await page.keyboard.press("ArrowRight");
    await expect(range).toHaveValue("5");
    await expect.poll(getNotch).toBe("max");

    // Step down to high (value 4)
    await page.keyboard.press("ArrowLeft");
    await expect(range).toHaveValue("4");
    await expect.poll(getNotch).toBe("high");

    // Jump to minimal (value 1)
    await page.keyboard.press("Home");
    await expect(range).toHaveValue("1");
    await expect.poll(getNotch).toBe("minimal");

    // Jump to max (value 5)
    await page.keyboard.press("End");
    await expect(range).toHaveValue("5");
    await expect.poll(getNotch).toBe("max");

    expect(errors).toEqual([]);
  });

  test("clicking a notch button sets the load notch", async ({ page }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const lightBtn = page.locator('.operating-load-notch[data-notch="light"]');
    await lightBtn.click();

    const range = page.locator(".operating-load-input");
    await expect(range).toHaveValue("2");
    await expect(lightBtn).toHaveClass(/active/);
    await expect.poll(getNotch).toBe("light");

    const highBtn = page.locator('.operating-load-notch[data-notch="high"]');
    await highBtn.click();
    await expect(range).toHaveValue("4");
    await expect(highBtn).toHaveClass(/active/);
    await expect.poll(getNotch).toBe("high");

    expect(errors).toEqual([]);
  });

  test("external change via cli updates the slider in the window", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const range = page.locator(".operating-load-input");
    await expect(range).toHaveValue("3");

    djinn("load", "set", "minimal");
    await expect.poll(async () => await range.inputValue()).toBe("1");
    await expect(
      page.locator('.operating-load-notch[data-notch="minimal"]'),
    ).toHaveClass(/active/);

    djinn("load", "set", "max");
    await expect.poll(async () => await range.inputValue()).toBe("5");
    await expect(
      page.locator('.operating-load-notch[data-notch="max"]'),
    ).toHaveClass(/active/);

    expect(errors).toEqual([]);
  });

  test("live memory displays real and forecast memory against machine RAM", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const memoryEl = page.locator(".operating-load-memory");
    await expect(memoryEl).toBeVisible();

    // Memory format is: "<worker> · <forecast> / <total>"
    await expect(memoryEl).toHaveText(/·.+\//);
    const title = await memoryEl.getAttribute("title");
    expect(title).toBeTruthy();
    expect(title).toContain("RAM");

    expect(errors).toEqual([]);
  });
});

test.describe("in French", () => {
  test.use({ locale: "fr-FR" });
  const fr: Record<string, string> = JSON.parse(
    fs.readFileSync(path.join(__dirname, "../locales/fr.json"), "utf8"),
  );

  test.beforeEach(async () => {
    djinn("load", "set", "medium");
  });

  test.afterEach(async () => {
    djinn("load", "set", "medium");
  });

  test.afterAll(async () => {
    djinn("load", "set", "medium");
  });

  test("the slider labels and tooltips follow the catalog", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText(fr["status.live"]),
    ).toBeVisible();

    const range = page.locator(".operating-load-input");
    await expect(range).toHaveAttribute("aria-label", fr["load.label"]);

    const notches = ["minimal", "light", "medium", "high", "max"] as const;
    for (const notch of notches) {
      const btn = page.locator(`.operating-load-notch[data-notch="${notch}"]`);
      await expect(btn).toBeVisible();
      await expect(btn).toHaveText(fr[`load.notch.${notch}`]);
      await expect(btn).toHaveAttribute("title", fr[`load.tooltip.${notch}`]);
    }

    const memoryEl = page.locator(".operating-load-memory");
    await expect(memoryEl).toBeVisible();
    await expect(memoryEl).toHaveText(/·.+\//);

    expect(errors).toEqual([]);
  });
});
