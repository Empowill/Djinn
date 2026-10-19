// End-to-end test for the operating load slider: 5 notches, keyboard and click
// navigation, round stops on the track, server synchronization via LoadService RPCs, and the live two-line forecast
// shown while the gauge is hovered.
import { expect, test, type Page } from "./test";
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

  test("operating load slider displays in topbar, reflects server setting, and translates its labels and tooltips", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    const fr: Record<string, string> = JSON.parse(
      fs.readFileSync(path.join(__dirname, "../locales/fr.json"), "utf8"),
    );
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
      const description = await btn.getAttribute("aria-description");
      expect(description).toBeTruthy();
      expect(description?.length).toBeGreaterThan(15);
      expect(description?.toLowerCase()).toContain("workers");
    }

    const ocBtn = page.locator('.operating-load-notch[data-notch="overclock"]');
    const ocDescription = await ocBtn.getAttribute("aria-description");
    expect(ocDescription).toContain("saturate CPU and memory");

    const activeBtn = page.locator(".operating-load-notch.active");
    await expect(activeBtn).toHaveAttribute("data-notch", "medium");

    const autoBtn = page.locator('.operating-load-notch[data-notch="auto"]');
    await expect(autoBtn).toBeVisible();
    const autoDescription = await autoBtn.getAttribute("aria-description");
    expect(autoDescription).toBeTruthy();
    expect(autoDescription?.length).toBeGreaterThan(10);
    await expect(autoBtn).not.toHaveClass(/active/);

    try {
      await page.evaluate(() => localStorage.setItem("djinn.language", "fr"));
      await page.reload();
      await expect(
        page.locator(".app-statusbar").getByText(fr["status.live"]),
      ).toBeVisible();

      await expect(range).toHaveAttribute("aria-label", fr["load.label"]);

      for (const notch of notches) {
        const btn = page.locator(
          `.operating-load-notch[data-notch="${notch}"]`,
        );
        await expect(btn).toBeVisible();
        await expect(btn).toHaveText(fr[`load.notch.${notch}`]);
        const description = await btn.getAttribute("aria-description");
        expect(description).toBeTruthy();
        expect(description?.length).toBeGreaterThan(15);
        expect(description).toContain(fr[`load.notch.${notch}`]);
      }

      const frOcDescription = await ocBtn.getAttribute("aria-description");
      expect(frOcDescription).toContain(fr["load.notch.overclock"]);
      expect(frOcDescription?.length).toBeGreaterThan(50);

      await expect(autoBtn).toBeVisible();
      await expect(autoBtn).toHaveText(fr["load.notch.auto"]);
      expect(await autoBtn.getAttribute("aria-description")).toBeTruthy();

      const forecastEl = page.locator(".operating-load-forecast");
      await page.locator(".operating-load-slider").hover();
      await expect(forecastEl).toBeVisible();
      const cpuPrefix = fr["load.forecast_cpu_running"].split("{")[0];
      const memPrefix = fr["load.forecast_memory"].split("{")[0];
      await expect(forecastEl).toContainText(cpuPrefix);
      await expect(forecastEl).toContainText(memPrefix);

      // Overclock's risk and Auto's explanation in the preview (W37).
      await ocBtn.hover();
      await expect(
        forecastEl.locator(".operating-load-forecast-risk"),
      ).toHaveText(fr["load.overclock_risk"]);
      await autoBtn.hover();
      await expect(forecastEl).toContainText(
        fr["load.tooltip.auto"].split("{")[0],
      );
    } finally {
      await page.evaluate(() => localStorage.removeItem("djinn.language"));
    }

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

  // W28: notch buttons, round stops on the track (with keyboard focus), and clicking anywhere on the track all set the
  // load notch.
  test("clicking a notch button, a round stop, or anywhere on the track sets the load notch", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const range = page.locator(".operating-load-input");
    const stops = page.locator(".operating-load-stop");
    await expect(stops).toHaveCount(5);
    const notches = ["minimal", "medium", "high", "max", "overclock"] as const;
    for (const notch of notches) {
      const stop = page.locator(`.operating-load-stop[data-notch="${notch}"]`);
      await expect(stop).toHaveAttribute(
        "aria-label",
        notch[0].toUpperCase() + notch.slice(1),
      );
      expect(await stop.evaluate((el) => getComputedStyle(el).cursor)).toBe(
        "pointer",
      );
    }
    const medium = page.locator('.operating-load-stop[data-notch="medium"]');
    await expect(medium).toHaveClass(/\bcurrent\b/);
    await expect(medium).toHaveAttribute("aria-pressed", "true");

    // Each stop sits where the thumb's centre lands at its notch, on the track's line.
    const box = await range.boundingBox();
    expect(box).toBeTruthy();
    if (!box) return;
    for (const [i, notch] of notches.entries()) {
      const stop = await page
        .locator(`.operating-load-stop[data-notch="${notch}"]`)
        .boundingBox();
      expect(stop).toBeTruthy();
      expect(stop!.x + stop!.width / 2).toBeCloseTo(
        box.x + 7 + ((box.width - 14) * i) / 4,
        0,
      );
      expect(stop!.y + stop!.height / 2).toBeCloseTo(box.y + box.height / 2, 0);
    }

    // Clicking notch buttons sets the load notch.
    const minBtn = page.locator('.operating-load-notch[data-notch="minimal"]');
    await minBtn.click();
    await expect(range).toHaveValue("1");
    await expect(minBtn).toHaveClass(/active/);
    await expect.poll(getNotch).toBe("minimal");

    const ocBtn = page.locator('.operating-load-notch[data-notch="overclock"]');
    await ocBtn.click();
    await expect(range).toHaveValue("5");
    await expect(ocBtn).toHaveClass(/active/);
    await expect.poll(getNotch).toBe("overclock");

    // Clicking round stops on the track sets the load notch.
    const high = page.locator('.operating-load-stop[data-notch="high"]');
    await high.click();
    await expect(range).toHaveValue("3");
    await expect.poll(getNotch).toBe("high");
    await expect(high).toHaveAttribute("aria-pressed", "true");
    await expect(
      page.locator('.operating-load-notch[data-notch="high"]'),
    ).toHaveClass(/active/);

    const minimal = page.locator('.operating-load-stop[data-notch="minimal"]');
    await minimal.click();
    await expect(range).toHaveValue("1");
    await expect.poll(getNotch).toBe("minimal");

    // Keyboard: each stop takes the focus; Enter or Space selects it. The pointer leaves first: resting on a stop, it
    // previews that stop's notch.
    await page.mouse.move(0, 0);
    const max = page.locator('.operating-load-stop[data-notch="max"]');
    await max.focus();
    await expect(max).toBeFocused();
    await page.keyboard.press("Enter");
    await expect.poll(getNotch).toBe("max");
    await expect(range).toHaveValue("4");

    const overclock = page.locator(
      '.operating-load-stop[data-notch="overclock"]',
    );
    await page.keyboard.press("Tab");
    await expect(overclock).toBeFocused();
    await page.keyboard.press("Space");
    await expect.poll(getNotch).toBe("overclock");
    await expect(range).toHaveValue("5");

    // A stop exits auto mode as its label does.
    await page.locator('.operating-load-notch[data-notch="auto"]').click();
    await expect.poll(() => djinn("load", "get")).toContain("auto: true");
    await medium.click();
    await expect.poll(() => djinn("load", "get")).not.toContain("auto: true");
    await expect.poll(getNotch).toBe("medium");

    // Clicking anywhere on the track picks the nearest notch.
    const slider = page.locator(".operating-load-slider");
    const sliderBox = await slider.boundingBox();
    expect(sliderBox).toBeTruthy();
    if (!sliderBox) return;

    await page.mouse.click(sliderBox.x + 8, sliderBox.y + 10);
    await expect.poll(getNotch).toBe("minimal");

    await page.mouse.click(sliderBox.x + sliderBox.width - 8, sliderBox.y + 10);
    await expect.poll(getNotch).toBe("overclock");

    await page.mouse.click(
      sliderBox.x + sliderBox.width * 0.5,
      sliderBox.y + 10,
    );
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

  test("external change via cli sets auto mode and updates the slider in the window", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();

    const range = page.locator(".operating-load-input");
    const autoBtn = page.locator('.operating-load-notch[data-notch="auto"]');
    const effectiveEl = page.locator(".operating-load-effective");
    await expect(range).toHaveValue("2");

    // Set auto via CLI
    djinn("load", "set", "auto");
    await expect(autoBtn).toHaveClass(/active/);
    await expect(effectiveEl).toBeVisible();
    await expect(effectiveEl).toHaveAttribute(
      "data-effective",
      /^(minimal|medium|high|max|overclock)$/,
    );

    // Set manual via CLI exits auto and updates slider
    djinn("load", "set", "high");
    await expect(autoBtn).not.toHaveClass(/active/);
    await expect(effectiveEl).not.toBeVisible();
    await expect.poll(async () => await range.inputValue()).toBe("3");
    await expect(
      page.locator('.operating-load-notch[data-notch="high"]'),
    ).toHaveClass(/active/);

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

    // W28: the limits stay hidden until the gauge is hovered, then show over the page, the bar keeping its size.
    const forecastEl = page.locator(".operating-load-forecast");
    const loadEl = page.locator(".operating-load");
    await page.mouse.move(0, 0);
    await expect(forecastEl).toBeAttached();
    await expect(forecastEl).toBeHidden();
    const before = await loadEl.boundingBox();
    await page.locator(".operating-load-slider").hover();
    await expect(forecastEl).toBeVisible();
    expect(await loadEl.boundingBox()).toEqual(before);
    const shown = await forecastEl.boundingBox();
    expect(shown!.y).toBeGreaterThanOrEqual(before!.y + before!.height);
    await page.mouse.move(0, 0);
    await expect(forecastEl).toBeHidden();
    // The keyboard shows it too.
    await page.locator(".operating-load-input").focus();
    await page.keyboard.press("ArrowRight");
    await expect(forecastEl).toBeVisible();
    await page.keyboard.press("ArrowLeft");
    await page.locator(".operating-load-slider").hover();

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
        "aria-description",
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
      "aria-description",
      /Lowering the load (pauses the newest workers|lets the workers above its limit finish)/,
    );
    expect(errors).toEqual([]);
  });

  // W32, W37: hovering a notch or stop updates the forecast immediately before saving, and the pointer previews the
  // stop nearest it all along the track: across the gap between Max and Overclock the preview never falls back to the
  // level in effect. A gauge's fill rises to the level, striped on to the preview.
  test("hovering previews notches and moving across the gap between Max and Overclock never previews High", async ({
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

    // Hovering a stop previews its notch the same way.
    await page.locator('.operating-load-stop[data-notch="max"]').hover();
    await expect(forecastEl).toBeVisible();
    await expect(forecastEl).toHaveAttribute("data-notch", "max");
    expect(getNotch()).toBe("medium");

    // The coloured preview is the only hover: no title brings the browser's tooltip up over it (W32).
    await expect(page.locator(".operating-load [title]")).toHaveCount(0);
    await expect(page.locator(".operating-load[title]")).toHaveCount(0);

    // Move away
    await page.mouse.move(0, 0);
    await expect(forecastEl).toHaveAttribute("data-notch", "medium");

    djinn("load", "set", "high");
    const slider = page.locator(".operating-load-slider");
    await expect(slider).toHaveAttribute("data-notch", "high");
    const centre = async (notch: string) => {
      const box = (await page
        .locator(`.operating-load-stop[data-notch="${notch}"]`)
        .boundingBox())!;
      return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
    };
    const high = await centre("high");
    const max = await centre("max");
    const overclock = await centre("overclock");

    // The fill stands at High, its right end on High's stop.
    // How far a fill's right end stands from x, in pixels.
    const fillOff = (selector: string, x: number) =>
      page
        .locator(selector)
        .evaluate((el, x) => Math.abs(el.getBoundingClientRect().right - x), x);
    await expect
      .poll(() => fillOff(".operating-load-fill", high.x))
      .toBeLessThan(1.5);
    await expect
      .poll(() => fillOff(".operating-load-fill-reach", high.x))
      .toBeLessThan(1.5);

    // Every preview the slider shows, in order.
    await slider.evaluate((el) => {
      const seen: string[] = [];
      (window as unknown as { seen: string[] }).seen = seen;
      new MutationObserver(() => seen.push(el.dataset.notch!)).observe(el, {
        attributeFilter: ["data-notch"],
      });
    });
    const seen = () =>
      page.evaluate(() => (window as unknown as { seen: string[] }).seen);

    await page.mouse.move(max.x, max.y);
    await expect(slider).toHaveAttribute("data-notch", "max");
    await page.mouse.move(overclock.x, overclock.y, { steps: 24 });
    await expect(slider).toHaveAttribute("data-notch", "overclock");
    // Striped on to Overclock, the solid fill still at High.
    await expect
      .poll(() => fillOff(".operating-load-fill-reach", overclock.x))
      .toBeLessThan(1.5);
    expect(await fillOff(".operating-load-fill", high.x)).toBeLessThan(1.5);
    await page.mouse.move(max.x, max.y, { steps: 24 });
    await expect(slider).toHaveAttribute("data-notch", "max");
    expect(await seen()).toEqual(["max", "overclock", "max"]);
    // Back between High and Max, nearer High: High.
    await page.mouse.move(high.x + (max.x - high.x) * 0.3, high.y, {
      steps: 8,
    });
    await expect(slider).toHaveAttribute("data-notch", "high");
    expect(await seen()).toEqual(["max", "overclock", "max", "high"]);
    // Nothing applied while hovering.
    expect(getNotch()).toBe("high");

    // A click in the gap sets the nearest stop.
    await page.mouse.click(max.x + (overclock.x - max.x) * 0.7, max.y);
    await expect.poll(getNotch).toBe("overclock");
    // Overclock set, the gauge's width may move by a pixel or two: its stop is measured again.
    await expect
      .poll(async () =>
        fillOff(".operating-load-fill", (await centre("overclock")).x),
      )
      .toBeLessThan(1.5);
    expect(errors).toEqual([]);
  });

  // W37: W32 left Overclock's risk and Auto's explanation in aria-description only; the coloured preview shows them.
  test("the preview warns of Overclock's risk and explains Auto", async ({
    page,
  }) => {
    const errors = collectErrors(page);
    await page.goto(djinnURL());
    await expect(
      page.locator(".app-statusbar").getByText("Live"),
    ).toBeVisible();
    const forecast = page.locator(".operating-load-forecast");
    const risk = forecast.locator(".operating-load-forecast-risk");
    const notes = forecast.locator(".operating-load-forecast-note");
    const autoText = /Auto: follows the load of the programs outside Djinn/;

    // Overclock previewed: the warning, inside the preview's width.
    await page.locator('.operating-load-notch[data-notch="overclock"]').hover();
    await expect(risk).toBeVisible();
    await expect(risk).toHaveText(
      "Risk: Djinn will saturate CPU and memory; other applications may slow down or be terminated.",
    );
    const panel = (await forecast.boundingBox())!;
    const warning = (await risk.boundingBox())!;
    expect(warning.x).toBeGreaterThanOrEqual(panel.x);
    expect(warning.x + warning.width).toBeLessThanOrEqual(
      panel.x + panel.width,
    );
    // Another level previewed: no warning.
    await page.locator('.operating-load-notch[data-notch="medium"]').hover();
    await expect(forecast).toHaveAttribute("data-notch", "medium");
    await expect(notes).toHaveCount(0);

    // The Auto button hovered: Auto's explanation.
    const autoBtn = page.locator('.operating-load-notch[data-notch="auto"]');
    await autoBtn.hover();
    await expect(forecast).toBeVisible();
    await expect(notes).toHaveCount(1);
    await expect(notes).toContainText(autoText);
    await page.mouse.move(0, 0);
    await expect(forecast).toBeHidden();
    await page.locator(".operating-load-slider").hover();
    await expect(notes).toHaveCount(0);

    // Overclock set, the Auto button hovered: both, the warning in the theme's danger colour.
    djinn("load", "set", "overclock");
    await expect(
      page.locator('.operating-load-notch[data-notch="overclock"]'),
    ).toHaveClass(/active/);
    await autoBtn.hover();
    await expect(risk).toBeVisible();
    await expect(notes).toHaveCount(2);
    await expect(notes.nth(1)).toContainText(autoText);
    const checkDanger = async () => {
      const colours = await risk.evaluate((el) => {
        const probe = document.createElement("span");
        probe.style.color = "var(--danger)";
        el.appendChild(probe);
        const danger = getComputedStyle(probe).color;
        probe.remove();
        return { colour: getComputedStyle(el).color, danger };
      });
      expect(colours.colour).toBe(colours.danger);
    };
    await checkDanger();
    if (process.env.LOAD_SHOTS) {
      const outDir = path.resolve(__dirname, "../test-results/screenshots");
      fs.mkdirSync(outDir, { recursive: true });
      for (const theme of ["dark", "light"] as const) {
        await page.evaluate(
          (t) => document.documentElement.setAttribute("data-theme", t),
          theme,
        );
        await checkDanger();
        await forecast.evaluate((el) =>
          Promise.all(
            el
              .getAnimations({ subtree: true })
              .map((a) => a.finished.catch(() => {})),
          ),
        );
        await forecast.screenshot({
          path: path.join(outDir, `notch-risk-${theme}.png`),
        });
      }
      await page.evaluate(() =>
        document.documentElement.setAttribute("data-theme", "dark"),
      );
    }

    // Auto on: its explanation shows while the gauge is hovered.
    djinn("load", "set", "auto");
    await expect(autoBtn).toHaveClass(/active/);
    await page.locator(".operating-load-slider").hover();
    await expect(notes.filter({ hasText: autoText })).toHaveCount(1);
    // And why auto runs at its notch, in one line (W253): measured every 20 s, not yet at first. Overclock was the
    // notch chosen, so auto starts at it and climbs no higher: it holds it while the machine's load outside Djinn is
    // unmeasured, moderate or low, and steps down once that load stayed high, a step a beat at most (W291).
    await expect(forecast.locator(".operating-load-auto-reason")).toHaveText(
      /^Outside (load|memory) .*: (holds Overclock|up to Overclock|down to (Max|High) in .+)$/,
    );

    // No title anywhere in the gauge (W32); both texts stay in aria-description.
    await expect(page.locator(".operating-load [title]")).toHaveCount(0);
    await expect(
      page.locator('.operating-load-notch[data-notch="overclock"]'),
    ).toHaveAttribute(
      "aria-description",
      /Risk: Djinn will saturate CPU and memory/,
    );
    await expect(autoBtn).toHaveAttribute("aria-description", autoText);
    expect(errors).toEqual([]);
  });
});
