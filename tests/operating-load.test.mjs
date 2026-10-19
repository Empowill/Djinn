// The operating load gauge (src/operating-load.tsx): its notches, preview, forecast and Auto. Run with `go tool task
// test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens } from "./screens.mjs";

const { s, h, live } = await screens();

test("operating load gauge: click selects nearest notch, keyboard steps, round stops, colours per notch class", async () => {
  s.loadManager.setSnapshot({
    notch: s.LoadNotch.MEDIUM,
    auto: false,
    chosenNotch: s.LoadNotch.MEDIUM,
    engagedMemoryBytes: 12n * 1024n * 1024n * 1024n,
    workerMemoryBytes: 4n * 1024n * 1024n * 1024n,
    memoryTotalBytes: 32n * 1024n * 1024n * 1024n,
    memoryAvailableBytes: 24n * 1024n * 1024n * 1024n,
    workerForecastBytes: 3n * 1024n * 1024n * 1024n,
    bindingResource: "memory",
    cpuSlots: 8,
    memorySlots: 4,
    baseSlots: 8,
    runningWorkers: 2,
  });

  const screen = live(s.OperatingLoadSlider, {});

  // Initial render at Medium
  let slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "medium");

  let input = screen.one("operating-load-input");
  assert.equal(input.role, "slider");
  assert.equal(input["aria-valuemin"], 1);
  assert.equal(input["aria-valuemax"], 5);
  assert.equal(input["aria-valuenow"], 2);
  assert.match(input["aria-valuetext"], /Medium, 50\s*%/);

  // Check 5 notch buttons rendered
  const buttons = screen.all("operating-load-notch");
  assert.equal(buttons.length, 5);
  const notchSlugs = buttons.map((b) => b.props["data-notch"]);
  assert.deepEqual(notchSlugs, [
    "minimal",
    "medium",
    "high",
    "max",
    "overclock",
  ]);

  // Medium button is active
  const mediumBtn = buttons.find((b) => b.props["data-notch"] === "medium");
  assert.match(mediumBtn.props.className, /active/);

  // 1. Click on the track selects nearest notch
  // Simulate click on track at ratio ~0.0 (near left edge) -> Minimal
  slider.onClick({
    clientX: 5,
    currentTarget: { getBoundingClientRect: () => ({ left: 0, width: 200 }) },
    stopPropagation() {},
  });
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "minimal");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 1);
  assert.match(input["aria-valuetext"], /Minimal, 20\s*%/);

  // Simulate click on track at ratio ~0.95 (near right edge) -> Overclock
  slider.onClick({
    clientX: 195,
    currentTarget: { getBoundingClientRect: () => ({ left: 0, width: 200 }) },
    stopPropagation() {},
  });
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "overclock");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 5);
  assert.match(input["aria-valuetext"], /Overclock, 150\s*%/);

  // Click on a notch button directly: click High button
  const highBtn = screen
    .all("operating-load-notch")
    .find((b) => b.props["data-notch"] === "high");
  highBtn.props.onClick({ stopPropagation() {} });
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "high");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 3);
  assert.match(input["aria-valuetext"], /High, 80\s*%/);

  // 2. Keyboard steps: ArrowRight, ArrowLeft, ArrowUp, ArrowDown, Home, End
  // From High (index 2 / value 3), ArrowRight moves to Max (index 3 / value 4)
  let prevented = false;
  input.onKeyDown({
    key: "ArrowRight",
    preventDefault() {
      prevented = true;
    },
  });
  assert.equal(prevented, true);
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "max");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 4);
  assert.match(input["aria-valuetext"], /Max, 100\s*%/);

  // From Max, ArrowDown moves to High
  prevented = false;
  input.onKeyDown({
    key: "ArrowDown",
    preventDefault() {
      prevented = true;
    },
  });
  assert.equal(prevented, true);
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "high");

  // Home key jumps to Minimal (notch 0 / value 1)
  prevented = false;
  input.onKeyDown({
    key: "Home",
    preventDefault() {
      prevented = true;
    },
  });
  assert.equal(prevented, true);
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "minimal");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 1);

  // End key jumps to Overclock (notch 4 / value 5)
  prevented = false;
  input.onKeyDown({
    key: "End",
    preventDefault() {
      prevented = true;
    },
  });
  assert.equal(prevented, true);
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "overclock");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 5);

  // 3. A round stop per notch on the track (W28): a button named for its notch, the one in effect pressed and filled;
  // a click on one sets its notch as its label does.
  let stops = screen.all("operating-load-stop");
  assert.deepEqual(
    stops.map((b) => [b.type, b.props["aria-label"], b.props.style.left]),
    [
      ["button", "Minimal", "0%"],
      ["button", "Medium", "25%"],
      ["button", "High", "50%"],
      ["button", "Max", "75%"],
      ["button", "Overclock", "100%"],
    ],
  );
  const stopOf = (slug) =>
    screen
      .all("operating-load-stop")
      .find((b) => b.props["data-notch"] === slug).props;
  assert.equal(stopOf("overclock")["aria-pressed"], true);
  assert.match(stopOf("overclock").className, /\bcurrent\b/);
  assert.match(
    stopOf("overclock")["aria-description"],
    /^Overclock: up to \d+ workers/,
  );
  let propagated = true;
  stopOf("medium").onClick({
    stopPropagation() {
      propagated = false;
    },
  });
  assert.equal(propagated, false, "the track's click does not run after it");
  slider = screen.one("operating-load-slider");
  assert.equal(slider["data-notch"], "medium");
  input = screen.one("operating-load-input");
  assert.equal(input["aria-valuenow"], 2);
  stops = screen.all("operating-load-stop");
  assert.deepEqual(
    stops.map((b) => b.props["aria-pressed"]),
    [false, true, false, false, false],
  );
  assert.deepEqual(
    stops.map((b) => /\bcurrent\b/.test(b.props.className)),
    [false, true, false, false, false],
  );
  assert.match(
    screen
      .all("operating-load-notch")
      .find((b) => b.props["data-notch"] === "medium").props.className,
    /active/,
  );
  // Hovering a stop previews its notch (the filled stop moves there), the one in effect staying pressed.
  const track = { getBoundingClientRect: () => ({ left: 0, width: 200 }) };
  screen.one("operating-load-slider").onMouseMove({
    clientX: 150,
    currentTarget: track,
  });
  assert.equal(screen.one("operating-load-slider")["data-notch"], "max");
  assert.match(stopOf("max").className, /\bcurrent\b/);
  assert.equal(stopOf("medium")["aria-pressed"], true);
  screen.one("operating-load-slider").onMouseLeave();
  assert.equal(screen.one("operating-load-slider")["data-notch"], "medium");

  // 4. Colours per notch class
  // Check that all 5 notches correctly reflect their active class and data-notch
  for (const notch of s.NOTCHES) {
    const slug = s.notchSlug(notch);
    await s.loadManager.setNotch(notch);
    slider = screen.one("operating-load-slider");
    assert.equal(slider["data-notch"], slug);
    const active = screen
      .all("operating-load-notch")
      .find((b) => b.props.className.includes("active"));
    assert.equal(active.props["data-notch"], slug);
  }
});

test("operating load: a Get answered after the stream's first message never takes the stream's place (W248)", async () => {
  // The e2e race: the stream sends medium, the notch goes to minimal and back to medium within the stream's beat, so
  // it sends nothing more; the Get, answered in between, read minimal. Kept, it showed minimal for good.
  const N = s.LoadNotch;
  const msg = (notch) => ({
    notch,
    auto: false,
    chosenNotch: notch,
    notchSlots: [],
    slots: 4,
    forcedWorkers: 0,
    lowerPauses: true,
  });
  let answer;
  const fake = {
    clients: {
      load: {
        get: () => new Promise((resolve) => (answer = resolve)),
        set: async ({ notch }) => msg(notch),
      },
    },
    load: {
      subscribe: (callback) => {
        callback(msg(N.MEDIUM));
        return () => {};
      },
    },
  };
  s.loadManager.setSnapshot({ notch: N.HIGH, chosenNotch: N.HIGH });
  // Listened to first: the new djinn restarts the store on it, whatever the tests before left started.
  const unsubscribe = s.loadManager.subscribe(() => {});
  s.loadManager.setDjinn(fake);
  assert.equal(s.loadManager.getSnapshot().notch, N.MEDIUM);
  answer(msg(N.MINIMAL));
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(s.loadManager.getSnapshot().notch, N.MEDIUM);
  unsubscribe();
});

test("operating load forecast: binding resource (memory-bound vs cpu-bound) and consistent two-line numbers", async () => {
  // Case A: Memory-bound machine (e.g. 4 memory slots, 8 cpu slots)
  s.loadManager.setSnapshot({
    notch: s.LoadNotch.MEDIUM,
    auto: false,
    chosenNotch: s.LoadNotch.MEDIUM,
    engagedMemoryBytes: 12n * 1024n * 1024n * 1024n,
    workerMemoryBytes: 4n * 1024n * 1024n * 1024n,
    memoryTotalBytes: 32n * 1024n * 1024n * 1024n,
    memoryAvailableBytes: 24n * 1024n * 1024n * 1024n,
    workerForecastBytes: 3n * 1024n * 1024n * 1024n, // ~3 GiB
    bindingResource: "memory",
    cpuSlots: 8,
    memorySlots: 4,
    baseSlots: 4, // min(cpu, memory)
    runningWorkers: 2,
  });

  let screen = live(s.OperatingLoadForecast, {});
  let binding = screen.one("operating-load-forecast-binding");
  // At Medium (50% of 4 = 2 workers)
  assert.match(
    s.renderToStaticMarkup(binding.children),
    /limited by memory: 2 workers of ~3 GB/,
  );

  let rows = screen.all("operating-load-forecast-row");
  assert.equal(rows.length, 2);
  let cpuText = s.renderToStaticMarkup(rows[0].props.children);
  let memText = s.renderToStaticMarkup(rows[1].props.children);
  assert.match(cpuText, /CPU: 2 of 2 workers run/);
  assert.match(memText, /Memory: 11 GB of 24 GB/); // (24 - 2) * 0.5 = 11 GB engaged

  // Case B: CPU-bound machine (e.g. 4 cpu slots, 16 memory slots)
  s.loadManager.setSnapshot({
    notch: s.LoadNotch.HIGH,
    auto: false,
    chosenNotch: s.LoadNotch.HIGH,
    engagedMemoryBytes: 16n * 1024n * 1024n * 1024n,
    workerMemoryBytes: 3n * 1024n * 1024n * 1024n,
    memoryTotalBytes: 64n * 1024n * 1024n * 1024n,
    memoryAvailableBytes: 48n * 1024n * 1024n * 1024n,
    workerForecastBytes: 1536n * 1024n * 1024n, // ~1.5 GiB
    bindingResource: "cpu",
    cpuSlots: 4,
    memorySlots: 16,
    baseSlots: 4,
    runningWorkers: 1,
  });

  screen = live(s.OperatingLoadForecast, {});
  binding = screen.one("operating-load-forecast-binding");
  // At High (80% of 4 = 3 workers)
  assert.match(
    s.renderToStaticMarkup(binding.children),
    /limited by CPU: 3 workers of ~1\.5 GB/,
  );

  rows = screen.all("operating-load-forecast-row");
  assert.equal(rows.length, 2);
  cpuText = s.renderToStaticMarkup(rows[0].props.children);
  memText = s.renderToStaticMarkup(rows[1].props.children);
  assert.match(cpuText, /CPU: 1 of 3 workers run/);
  assert.match(memText, /Memory: 37 GB of 48 GB/); // (48 - 2) * 0.8 = 36.8 GB rounded to 37 GB
});

test("operating load: each notch shows what the scheduler lets run there, and what moving the bar does (W577)", async () => {
  const N = s.LoadNotch;
  // The server's slots per notch: at Medium the scheduler runs 3, where the old estimate (50 % of 8) said 4.
  const notchSlots = {
    [N.MINIMAL]: 2,
    [N.MEDIUM]: 3,
    [N.HIGH]: 6,
    [N.MAX]: 8,
    [N.OVERCLOCK]: 12,
  };
  const base = {
    notch: N.HIGH,
    auto: false,
    chosenNotch: N.HIGH,
    engagedMemoryBytes: 8n * 1024n * 1024n * 1024n,
    workerMemoryBytes: 6n * 1024n * 1024n * 1024n,
    memoryTotalBytes: 31n * 1024n * 1024n * 1024n,
    memoryAvailableBytes: 14n * 1024n * 1024n * 1024n,
    workerForecastBytes: 1536n * 1024n * 1024n,
    bindingResource: "cpu",
    cpuSlots: 8,
    memorySlots: 10,
    baseSlots: 8,
    slots: 6,
    notchSlots,
    runningWorkers: 6,
    pausedWorkers: 0,
    forcedWorkers: 0,
    lowerPauses: true,
  };
  const cpuRow = () => {
    const screen = live(s.OperatingLoadForecast, {});
    const row = screen.all("operating-load-forecast-row")[0];
    return {
      text: s.renderToStaticMarkup(row.props.children),
      description:
        s
          .renderToStaticMarkup(row.props.children)
          .match(/aria-description="([^"]*)"/)?.[1] ?? "",
    };
  };
  const notchDescription = (slug) =>
    live(s.OperatingLoadSlider, {})
      .all("operating-load-notch")
      .find((b) => b.props["data-notch"] === slug).props["aria-description"];

  s.loadManager.setPreviewNotch(null);
  s.loadManager.setSnapshot(base);
  // Each notch's description names the server's count, never an estimate of its own.
  for (const [slug, n] of [
    ["minimal", N.MINIMAL],
    ["medium", N.MEDIUM],
    ["high", N.HIGH],
    ["max", N.MAX],
    ["overclock", N.OVERCLOCK],
  ])
    assert.match(
      notchDescription(slug),
      new RegExp(`up to ${notchSlots[n]} workers`),
      slug,
    );
  // At High, 6 of 6 run; no worker beyond; the label says what lowering does.
  let row = cpuRow();
  assert.match(row.text, /CPU: 6 of 6 workers run/);
  assert.doesNotMatch(row.text, /operating-load-forecast-over/);
  assert.match(
    row.description,
    /Lowering the load pauses the newest workers above its limit/,
  );

  // Previewing Medium: 3 run there, the 3 newest pause.
  s.loadManager.setPreviewNotch(N.MEDIUM);
  row = cpuRow();
  assert.match(row.text, /CPU: 6 of 3 workers run/);
  assert.match(row.text, /data-over="pause"[^>]*> · here the 3 newest pause/);
  // Where lowering lets them finish (Auto, Windows), it says so.
  s.loadManager.setSnapshot({ lowerPauses: false });
  row = cpuRow();
  assert.match(
    row.text,
    /data-over="finish"[^>]*> · 3 above the limit finish their run/,
  );
  assert.match(
    row.description,
    /lets the workers above its limit finish their run/,
  );
  s.loadManager.setPreviewNotch(null);

  // At Medium, 4 run: 1 started by hand, the other above the limit finishing its run (a gate, a correction).
  s.loadManager.setSnapshot({
    ...base,
    notch: N.MEDIUM,
    chosenNotch: N.MEDIUM,
    slots: 3,
    runningWorkers: 5,
    forcedWorkers: 1,
  });
  row = cpuRow();
  assert.match(row.text, /CPU: 5 of 3 workers run/);
  assert.match(row.text, /data-over="hand"[^>]*> · 1 started by hand/);
  assert.match(
    row.text,
    /data-over="finish"[^>]*> · 1 above the limit finishes its run/,
  );

  // Before the server says, the estimate stands in.
  s.loadManager.setSnapshot({ ...base, notchSlots: {}, runningWorkers: 0 });
  assert.match(notchDescription("medium"), /up to 4 workers/);
  s.loadManager.setSnapshot(base);
});

// The pointer on the track previews the stop nearest it, gaps included: between Max and Overclock it never falls back
// to the level in effect (W37). Nothing applies before a click.
test("operating load: the hover previews the stop nearest the pointer along the whole track (W37)", () => {
  const N = s.LoadNotch;
  s.loadManager.setSnapshot({
    notch: N.HIGH,
    auto: false,
    chosenNotch: N.HIGH,
    memoryTotalBytes: 32n * 1024n * 1024n * 1024n,
    memoryAvailableBytes: 24n * 1024n * 1024n * 1024n,
    baseSlots: 8,
  });
  s.loadManager.setPreviewNotch(null);
  const screen = live(s.OperatingLoadSlider, {});
  const track = { getBoundingClientRect: () => ({ left: 10, width: 200 }) };
  const at = (x) => {
    screen.one("operating-load-slider").onMouseMove({
      clientX: 10 + x,
      currentTarget: track,
    });
    return screen.one("operating-load-slider")["data-notch"];
  };
  try {
    // Stops at 0, 50, 100, 150 and 200: from Max across the gap to Overclock, then back between High and Max.
    const seen = [
      150, 160, 170, 174, 176, 185, 200, 186, 176, 174, 140, 126, 124, 101,
    ].map(at);
    assert.deepEqual(seen, [
      "max",
      "max",
      "max",
      "max",
      "overclock",
      "overclock",
      "overclock",
      "overclock",
      "overclock",
      "max",
      "max",
      "max",
      "high",
      "high",
    ]);
    // Beyond either end, the end stops.
    assert.equal(at(-20), "minimal");
    assert.equal(at(260), "overclock");
    // A label previews its own notch, whatever the stop nearest it.
    screen
      .all("operating-load-notch")
      .find((b) => b.props["data-notch"] === "medium")
      .props.onMouseMove({ stopPropagation() {} });
    assert.equal(screen.one("operating-load-slider")["data-notch"], "medium");
    // Nothing applied while hovering; leaving the track shows the level in effect again.
    assert.equal(s.loadManager.getSnapshot().notch, N.HIGH);
    screen.one("operating-load-slider").onMouseLeave();
    assert.equal(screen.one("operating-load-slider")["data-notch"], "high");
    // The helper itself: the nearest index, none on a box not drawn.
    assert.equal(s.nearestNotch(37, { left: 0, width: 100 }), 1);
    assert.equal(s.nearestNotch(38, { left: 0, width: 100 }), 2);
    assert.equal(s.nearestNotch(10, { left: 0, width: 0 }), null);
  } finally {
    s.loadManager.setPreviewNotch(null);
  }
});

// A real gauge: a solid fill from the track's left end to the level in effect, a striped one on to the previewed
// level, rising or going down (W37).
test("operating load: the fill rises to the level in effect, striped to the previewed one (W37)", () => {
  const N = s.LoadNotch;
  s.loadManager.setSnapshot({
    notch: N.HIGH,
    auto: false,
    chosenNotch: N.HIGH,
  });
  s.loadManager.setPreviewNotch(null);
  const screen = live(s.OperatingLoadSlider, {});
  const fill = () => {
    const solid = screen.one("operating-load-fill");
    const reach = screen.one("operating-load-fill-reach");
    return [
      solid.style.width,
      solid["data-notch"],
      reach.style.width,
      reach["data-notch"],
    ];
  };
  try {
    assert.equal(s.stopAt(0), "calc(0.4375rem + 0 * (100% - 0.875rem))");
    assert.equal(s.stopAt(2), "calc(0.4375rem + 0.5 * (100% - 0.875rem))");
    assert.equal(s.stopAt(4), "calc(0.4375rem + 1 * (100% - 0.875rem))");
    // At rest: both up to High.
    assert.deepEqual(fill(), [s.stopAt(2), "high", s.stopAt(2), "high"]);
    // Previewing Overclock: solid to High, striped on to Overclock in its colour.
    s.loadManager.setPreviewNotch(N.OVERCLOCK);
    assert.deepEqual(fill(), [s.stopAt(2), "high", s.stopAt(4), "overclock"]);
    // Previewing Minimal: solid down to Minimal, striped over what High gives up.
    s.loadManager.setPreviewNotch(N.MINIMAL);
    assert.deepEqual(fill(), [s.stopAt(0), "high", s.stopAt(2), "minimal"]);
    s.loadManager.setPreviewNotch(null);
    // In Auto, the fill stands at the level Auto put in effect.
    s.loadManager.setSnapshot({
      notch: N.MAX,
      auto: true,
      chosenNotch: N.HIGH,
    });
    assert.deepEqual(fill(), [s.stopAt(3), "max", s.stopAt(3), "max"]);
  } finally {
    s.loadManager.setPreviewNotch(null);
    s.loadManager.setSnapshot({
      notch: N.MEDIUM,
      auto: false,
      chosenNotch: N.MEDIUM,
    });
  }
});

// W32 left the Overclock risk and Auto's explanation in aria-description only: the coloured preview shows them (W37).
test("operating load: the preview warns of Overclock's risk and explains Auto (W37)", () => {
  const N = s.LoadNotch;
  const base = {
    notch: N.HIGH,
    auto: false,
    chosenNotch: N.HIGH,
    memoryTotalBytes: 32n * 1024n * 1024n * 1024n,
    memoryAvailableBytes: 24n * 1024n * 1024n * 1024n,
    baseSlots: 8,
  };
  const risk =
    /Risk: Djinn will saturate CPU and memory; other applications may slow down or be terminated\./;
  const auto =
    /Auto: follows the load of the programs outside Djinn, never Djinn&#x27;s own work nor your presence: one level down when that load stays high for 3 minutes, one level up towards High when it stays low for 5\./;
  const notes = () =>
    live(s.OperatingLoadForecast, {})
      .all("operating-load-forecast-note")
      .map((n) => [
        n.props.className,
        s.renderToStaticMarkup(n.props.children),
      ]);
  try {
    s.loadManager.setSnapshot(base);
    s.loadManager.setPreviewNotch(null);
    s.loadManager.setPreviewAuto(false);
    assert.deepEqual(notes(), []);

    // Overclock previewed: the risk, styled as a warning.
    s.loadManager.setPreviewNotch(N.OVERCLOCK);
    let found = notes();
    assert.equal(found.length, 1);
    assert.match(found[0][0], /\boperating-load-forecast-risk\b/);
    assert.match(found[0][1], risk);
    // Overclock set: the risk again; another level previewed over it shows that level's preview, without it.
    s.loadManager.setPreviewNotch(null);
    s.loadManager.setSnapshot({
      ...base,
      notch: N.OVERCLOCK,
      chosenNotch: N.OVERCLOCK,
    });
    assert.match(notes()[0][1], risk);
    s.loadManager.setPreviewNotch(N.MEDIUM);
    assert.deepEqual(notes(), []);
    s.loadManager.setPreviewNotch(null);
    s.loadManager.setSnapshot(base);

    // The Auto button hovered or focused: Auto's explanation, not as a warning.
    const button = () =>
      live(s.OperatingLoadAuto, {})
        .all("operating-load-notch")
        .find((b) => b.props["data-notch"] === "auto").props;
    button().onMouseEnter();
    found = notes();
    assert.equal(found.length, 1);
    assert.doesNotMatch(found[0][0], /risk/);
    assert.match(found[0][1], auto);
    button().onMouseLeave();
    assert.deepEqual(notes(), []);
    button().onFocus();
    assert.match(notes()[0][1], auto);
    button().onBlur();
    assert.deepEqual(notes(), []);
    // Auto on: the explanation stays, with the risk when Auto climbed to Overclock.
    s.loadManager.setSnapshot({
      ...base,
      auto: true,
      notch: N.OVERCLOCK,
      chosenNotch: N.OVERCLOCK,
    });
    found = notes();
    assert.equal(found.length, 2);
    assert.match(found[0][1], risk);
    assert.match(
      found[1][1],
      /one level up towards Overclock when it stays low/,
    );

    // And why auto runs at its notch, in one line (W253).
    s.loadManager.setSnapshot({
      ...base,
      auto: true,
      notch: N.MAX,
      chosenNotch: N.MAX,
      autoReason: {
        outsidePercent: 12,
        memory: false,
        windowSeconds: 300,
        step: s.LoadAutoStep.CEILING,
        notch: N.MAX,
        inSeconds: 0,
      },
    });
    found = notes();
    assert.equal(found.length, 2);
    assert.match(found[1][0], /\boperating-load-auto-reason\b/);
    assert.equal(found[1][1], "Outside load 12 % for 5m: up to Max");
    s.loadManager.setSnapshot({
      ...base,
      auto: true,
      notch: N.MEDIUM,
      chosenNotch: N.MAX,
      autoReason: {
        outsidePercent: 64,
        memory: false,
        windowSeconds: 40,
        step: s.LoadAutoStep.LOWER,
        notch: N.MINIMAL,
        inSeconds: 80,
      },
    });
    assert.equal(
      notes()[1][1],
      "Outside load 64 % for 40s: down to Minimal in 1m20s",
    );
    assert.match(
      s.renderToStaticMarkup(h(s.OperatingLoad)),
      /data-effective="medium" aria-description="Outside load 64 % for 40s: down to Minimal in 1m20s"/,
    );
    s.loadManager.setSnapshot({
      ...base,
      auto: true,
      notch: N.OVERCLOCK,
      chosenNotch: N.OVERCLOCK,
    });

    // Both stay in aria-description, and no title anywhere (W32).
    const html = s.renderToStaticMarkup(h(s.OperatingLoad));
    assert.doesNotMatch(html, /\stitle=/);
    assert.match(
      html,
      /aria-label="Overclock"[^>]*aria-description="Overclock: up to \d+ workers[^"]*Risk: Djinn will saturate CPU and memory/,
    );
    assert.match(
      html,
      /aria-description="Auto: follows the load of the programs outside Djinn/,
    );
  } finally {
    s.loadManager.setPreviewNotch(null);
    s.loadManager.setPreviewAuto(false);
    s.loadManager.setSnapshot({
      ...base,
      notch: N.MEDIUM,
      chosenNotch: N.MEDIUM,
    });
  }
});

// The coloured preview is the gauge's only hover: a title on any of its elements brought the browser's own
// tooltip up late over it (W32). Names stay in aria-label, the explanations in aria-description.
test("operating load: no element of the gauge or its preview carries a title (W32)", () => {
  const N = s.LoadNotch;
  const base = {
    notch: N.HIGH,
    auto: false,
    chosenNotch: N.HIGH,
    engagedMemoryBytes: 8n * 1024n * 1024n * 1024n,
    workerMemoryBytes: 6n * 1024n * 1024n * 1024n,
    memoryTotalBytes: 31n * 1024n * 1024n * 1024n,
    memoryAvailableBytes: 14n * 1024n * 1024n * 1024n,
    workerForecastBytes: 1536n * 1024n * 1024n,
    bindingResource: "cpu",
    cpuSlots: 8,
    memorySlots: 10,
    baseSlots: 8,
    runningWorkers: 6,
    pausedWorkers: 1,
    forcedWorkers: 1,
    lowerPauses: true,
  };
  const states = [
    [base, null],
    [base, N.MINIMAL],
    [{ ...base, auto: true, notch: N.MEDIUM }, null],
  ];
  try {
    for (const [snapshot, preview] of states) {
      s.loadManager.setSnapshot(snapshot);
      s.loadManager.setPreviewNotch(preview);
      const html = s.renderToStaticMarkup(h(s.OperatingLoad));
      const label = `auto ${snapshot.auto}, preview ${preview}`;
      assert.match(html, /class="operating-load-forecast /, label);
      assert.doesNotMatch(html, /\stitle=/, label);
      assert.match(html, /aria-label="Operating load"/, label);
      assert.match(html, /aria-description="[^"]*up to \d+ workers?/, label);
    }
    s.loadManager.setSnapshot(base);
    s.loadManager.setPreviewNotch(null);
    const html = s.renderToStaticMarkup(h(s.OperatingLoad));
    for (const n of ["Minimal", "Medium", "High", "Max", "Overclock"])
      assert.match(html, new RegExp(`aria-label="${n}"`), n);
    assert.match(html, /aria-description="Lowering the load pauses/);
  } finally {
    s.loadManager.setPreviewNotch(null);
  }
});
