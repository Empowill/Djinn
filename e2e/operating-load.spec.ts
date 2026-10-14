// End-to-end test for the operating load slider: 5 notches, keyboard and click
// navigation, server synchronization via LoadService RPCs, and live two-line forecast display.
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
    await expect(range).toHaveValue("2");

    const notches = ["minimal", "medium", "high", "max", "overclock"] as const;
    for (const notch of notches) {
      const btn = page.locator(`.operating-load-notch[data-notch="${notch}"]`);
      await expect(btn).toBeVisible();
      const title = await btn.getAttribute("title");
      expect(title).toBeTruthy();
      expect(title?.length).toBeGreaterThan(15);
      expect(title?.toLowerCase()).toContain("workers");
    }

    const ocBtn = page.locator('.operating-load-notch[data-notch="overclock"]');
    const ocTitle = await ocBtn.getAttribute("title");
    expect(ocTitle).toContain("saturate CPU and memory");

    const activeBtn = page.locator(".operating-load-notch.active");
    await expect(activeBtn).toHaveAttribute("data-notch", "medium");

    const autoBtn = page.locator('.operating-load-notch[data-notch="auto"]');
    await expect(autoBtn).toBeVisible();
    const autoTitle = await autoBtn.getAttribute("title");
    expect(autoTitle).toBeTruthy();
    expect(autoTitle?.length).toBeGreaterThan(10);
    await expect(autoBtn).not.toHaveClass(/active/);

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

    // The gauge shows once the page is chosen, the wishes read (wish-app.tsx): a key reaches the one focused here.
    const range = page.locator(".operating-load-input");
    await range.focus();
    await expect(range).toBeFocused();

    // Medium is value 2. Step up to high (value 3)
    await page.keyboard.press("ArrowRight");
    await expect(range).toHaveValue("3");
    await expect.poll(getNotch).toBe("high");

    // Step up to max (value 4)
    await page.keyboard.press("ArrowRight");
    await expect(range).toHaveValue("4");
    await expect.poll(getNotch).toBe("max");

    // Step up to overclock (value 5)
    await page.keyboard.press("ArrowRight");
    await expect(range).toHaveValue("5");
    await expect.poll(getNotch).toBe("overclock");

    // Step down to max (value 4)
    await page.keyboard.press("ArrowLeft");
    await expect(range).toHaveValue("4");
    await expect.poll(getNotch).toBe("max");

    // Jump to minimal (value 1)
    await page.keyboard.press("Home");
    await expect(range).toHaveValue("1");
    await expect.poll(getNotch).toBe("minimal");

    // Jump to overclock (value 5)
    await page.keyboard.press("End");
    await expect(range).toHaveValue("5");
    await expect.poll(getNotch).toBe("overclock");

    expect(errors).toEqual([]);
  });

  // W496: the window showed the home page, its gauge included, until the wishes were read, then the page it opens on
  // with a gauge of its own: a key pressed on the first one was lost (CI, after the specs that leave wishes active).
  test("the gauge shows once, in the page the window opens on", async ({
    page,
  }) => {
    const wishId = JSON.parse(
      djinn("wish", "make", "Gauge shows once", "--json"),
    ).wish.id as string;
    try {
      // The window opens on this wish: the show of the last minute (UiService.Watch) is this one, not an earlier spec's.
      djinn("open", `djinn://wish/${wishId}`);
      await page.addInitScript(() => {
        const seen: Element[] = [];
        Object.defineProperty(window, "__djinnGauges", { value: seen });
        new MutationObserver(() => {
          for (const gauge of document.querySelectorAll(".operating-load"))
            if (!seen.includes(gauge)) seen.push(gauge);
        }).observe(document, { childList: true, subtree: true });
      });
      await page.goto(djinnURL());
      await expect(
        page.locator(".page-header .breadcrumbs", {
          hasText: "Gauge shows once",
        }),
      ).toBeVisible();
      expect(
        await page.evaluate(() => {
          const seen = (window as unknown as { __djinnGauges: Element[] })
            .__djinnGauges;
          return seen.map((gauge) => gauge.isConnected);
        }),
      ).toEqual([true]);
    } finally {
      djinn("wish", "delete", wishId);
    }
  });

  test("clicking a notch button sets the load notch", async ({ page }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const range = page.locator(".operating-load-input");

    const minBtn = page.locator('.operating-load-notch[data-notch="minimal"]');
    await minBtn.click();
    await expect(range).toHaveValue("1");
    await expect(minBtn).toHaveClass(/active/);
    await expect.poll(getNotch).toBe("minimal");

    const highBtn = page.locator('.operating-load-notch[data-notch="high"]');
    await highBtn.click();
    await expect(range).toHaveValue("3");
    await expect(highBtn).toHaveClass(/active/);
    await expect.poll(getNotch).toBe("high");

    const ocBtn = page.locator('.operating-load-notch[data-notch="overclock"]');
    await ocBtn.click();
    await expect(range).toHaveValue("5");
    await expect(ocBtn).toHaveClass(/active/);
    await expect.poll(getNotch).toBe("overclock");

    expect(errors).toEqual([]);
  });

  test("clicking anywhere on the track picks the nearest notch", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    // boundingBox does not wait: the slider is measured once it shows, not while the top bar mounts again.
    const slider = page.locator(".operating-load-slider");
    await expect(slider).toBeVisible();
    const box = await slider.boundingBox();
    expect(box).toBeTruthy();
    if (!box) return;

    // Click near left edge (minimal)
    await page.mouse.click(box.x + 8, box.y + 10);
    await expect.poll(getNotch).toBe("minimal");

    // Click near right edge (overclock)
    await page.mouse.click(box.x + box.width - 8, box.y + 10);
    await expect.poll(getNotch).toBe("overclock");

    // Click in middle (high or medium depending on exact position)
    await page.mouse.click(box.x + box.width * 0.5, box.y + 10);
    await expect.poll(getNotch).toMatch(/^(medium|high)$/);

    expect(errors).toEqual([]);
  });

  test("auto mode selects a notch and manual selection exits auto mode", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const autoBtn = page.locator('.operating-load-notch[data-notch="auto"]');
    await expect(autoBtn).toBeVisible();
    await expect(autoBtn).not.toHaveClass(/active/);

    // Clicking Auto button enables auto mode
    await autoBtn.click();
    await expect(autoBtn).toHaveClass(/active/);

    // In auto mode, manual notch buttons must not have active class
    const manualNotches = [
      "minimal",
      "medium",
      "high",
      "max",
      "overclock",
    ] as const;
    for (const notch of manualNotches) {
      await expect(
        page.locator(`.operating-load-notch[data-notch="${notch}"]`),
      ).not.toHaveClass(/active/);
    }

    // Effective notch is visible next to the slider
    const effectiveEl = page.locator(".operating-load-effective");
    await expect(effectiveEl).toBeVisible();
    await expect(effectiveEl).toHaveText(/\(\w+\)/);

    // Server reflects auto mode and effective notch
    await expect.poll(() => djinn("load", "get")).toContain("auto: true");

    // Clicking a manual notch exits auto mode
    const highBtn = page.locator('.operating-load-notch[data-notch="high"]');
    await highBtn.click();

    await expect(highBtn).toHaveClass(/active/);
    await expect(autoBtn).not.toHaveClass(/active/);
    await expect(effectiveEl).not.toBeVisible();
    await expect.poll(() => djinn("load", "get")).not.toContain("auto: true");
    await expect.poll(getNotch).toBe("high");

    expect(errors).toEqual([]);
  });

  test("external change via cli sets auto mode and updates window", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const autoBtn = page.locator('.operating-load-notch[data-notch="auto"]');
    const effectiveEl = page.locator(".operating-load-effective");

    // Set auto via CLI
    djinn("load", "set", "auto");
    await expect(autoBtn).toHaveClass(/active/);
    await expect(effectiveEl).toBeVisible();
    await expect(effectiveEl).toHaveAttribute(
      "data-effective",
      /^(minimal|medium|high|max|overclock)$/,
    );

    // Set manual via CLI exits auto
    djinn("load", "set", "high");
    await expect(autoBtn).not.toHaveClass(/active/);
    await expect(effectiveEl).not.toBeVisible();
    await expect(
      page.locator('.operating-load-notch[data-notch="high"]'),
    ).toHaveClass(/active/);

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
    await expect(range).toHaveValue("2");

    djinn("load", "set", "minimal");
    await expect.poll(async () => await range.inputValue()).toBe("1");
    await expect(
      page.locator('.operating-load-notch[data-notch="minimal"]'),
    ).toHaveClass(/active/);

    djinn("load", "set", "overclock");
    await expect.poll(async () => await range.inputValue()).toBe("5");
    await expect(
      page.locator('.operating-load-notch[data-notch="overclock"]'),
    ).toHaveClass(/active/);

    expect(errors).toEqual([]);
  });

  test("live forecast displays CPU and memory on two lines with animated bars", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const forecastEl = page.locator(".operating-load-forecast");
    await expect(forecastEl).toBeVisible();

    // Binding resource line: "limited by (memory|CPU): X workers of ~Y"
    await expect(
      forecastEl.locator(".operating-load-forecast-binding"),
    ).toBeVisible();
    await expect(
      forecastEl.locator(".operating-load-forecast-binding"),
    ).toContainText(/limited by (memory|CPU): \d+ workers? of ~[\d.]+ \w+/i);

    // CPU line: "CPU: X of Y workers run, (low|standard) priority"
    await expect(forecastEl).toContainText(/CPU:\s*\d+ of \d+ workers run/i);
    await expect(forecastEl).toContainText(/(low|standard) priority/i);

    // Memory line: "Memory: X of Y may be engaged; Z used now"
    await expect(forecastEl).toContainText(/Memory:\s*.+may be engaged/i);
    await expect(forecastEl).toContainText(/used now/i);

    // Bars exist for CPU and Memory
    await expect(forecastEl.locator(".operating-load-bar")).toHaveCount(2);
    await expect(forecastEl.locator(".operating-load-bar-limit")).toHaveCount(
      2,
    );
    await expect(forecastEl.locator(".operating-load-bar-used")).toHaveCount(2);

    expect(errors).toEqual([]);
  });

  // W577: what the bar says a notch runs is what the scheduler lets run there, as the server counts it; moving the bar
  // changes the count at once, and the window says what lowering does to the workers above the limit.
  test("each notch shows the workers the scheduler lets run there", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();
    const load = JSON.parse(djinn("load", "get", "--json")) as {
      notch_slots: { notch: string; slots?: number }[];
    };
    const slots = Object.fromEntries(
      load.notch_slots.map((n) => [
        n.notch.replace("LOAD_NOTCH_", "").toLowerCase(),
        n.slots ?? 0,
      ]),
    );
    const notches = ["minimal", "medium", "high", "max", "overclock"] as const;
    for (const notch of notches) {
      expect(slots[notch]).toBeGreaterThan(0);
      await expect(
        page.locator(`.operating-load-notch[data-notch="${notch}"]`),
      ).toHaveAttribute(
        "title",
        new RegExp(`up to ${slots[notch]} workers?\\b`),
      );
    }
    const forecast = page.locator(".operating-load-forecast");
    const label = forecast.locator(".operating-load-forecast-label").first();
    for (const notch of ["high", "minimal", "max"] as const) {
      djinn("load", "set", notch);
      await expect(forecast).toHaveAttribute("data-notch", notch);
      await expect(label).toContainText(
        new RegExp(`CPU: \\d+ of ${slots[notch]} workers run`),
      );
    }
    // What lowering does, said on the CPU line.
    await expect(label).toHaveAttribute(
      "title",
      /Lowering the load (pauses the newest workers|lets the workers above its limit finish)/,
    );
    expect(errors).toEqual([]);
  });

  test("hovering a notch updates the forecast immediately before saving", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const forecastEl = page.locator(".operating-load-forecast");
    const ocBtn = page.locator('.operating-load-notch[data-notch="overclock"]');

    // Hover over overclock button
    await ocBtn.hover();
    await expect(forecastEl).toHaveAttribute("data-notch", "overclock");
    await expect(forecastEl).toContainText(/standard priority/i);

    // But server notch is still medium
    expect(getNotch()).toBe("medium");

    // Move away
    await page.mouse.move(0, 0);
    await expect(forecastEl).toHaveAttribute("data-notch", "medium");

    expect(errors).toEqual([]);
  });

  test("cursor appearance and screenshots at each notch in dark and light modes", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const notches = ["minimal", "medium", "high", "max", "overclock"] as const;
    const loadEl = page.locator(".operating-load");
    const sliderEl = page.locator(".operating-load-slider");

    const outDir = path.resolve(__dirname, "../test-results/screenshots");
    fs.mkdirSync(outDir, { recursive: true });

    for (const theme of ["dark", "light"] as const) {
      await page.evaluate(
        (t) => document.documentElement.setAttribute("data-theme", t),
        theme,
      );
      for (const notch of notches) {
        djinn("load", "set", notch);
        await expect(sliderEl).toHaveAttribute("data-notch", notch);
        await expect(
          page.locator(`.operating-load-notch[data-notch="${notch}"]`),
        ).toHaveClass(/active/);

        // Capture screenshot of operating-load component for visual verification
        await loadEl.screenshot({
          path: path.join(outDir, `notch-${notch}-${theme}.png`),
        });
      }
    }

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

    const notches = ["minimal", "medium", "high", "max", "overclock"] as const;
    for (const notch of notches) {
      const btn = page.locator(`.operating-load-notch[data-notch="${notch}"]`);
      await expect(btn).toBeVisible();
      await expect(btn).toHaveText(fr[`load.notch.${notch}`]);
      const title = await btn.getAttribute("title");
      expect(title).toBeTruthy();
      expect(title?.length).toBeGreaterThan(15);
      expect(title).toContain(fr[`load.notch.${notch}`]);
    }

    const ocBtn = page.locator('.operating-load-notch[data-notch="overclock"]');
    const ocTitle = await ocBtn.getAttribute("title");
    expect(ocTitle).toContain(fr["load.notch.overclock"]);
    expect(ocTitle?.length).toBeGreaterThan(50);

    const autoBtn = page.locator('.operating-load-notch[data-notch="auto"]');
    await expect(autoBtn).toBeVisible();
    await expect(autoBtn).toHaveText(fr["load.notch.auto"]);
    const autoTitle = await autoBtn.getAttribute("title");
    expect(autoTitle).toBeTruthy();

    const forecastEl = page.locator(".operating-load-forecast");
    await expect(forecastEl).toBeVisible();
    const cpuPrefix = fr["load.forecast_cpu_running"].split("{")[0];
    const memPrefix = fr["load.forecast_memory"].split("{")[0];
    await expect(forecastEl).toContainText(cpuPrefix);
    await expect(forecastEl).toContainText(memPrefix);

    expect(errors).toEqual([]);
  });
});
