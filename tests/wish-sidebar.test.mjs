// The side panel (src/wish-sidebar.tsx): the wishes, their counts, the projects and their gears, the pages. Run with
// `go tool task test-ui`.
import test from "node:test";
import assert from "node:assert/strict";

import { screens, wish } from "./screens.mjs";

const { s, h } = await screens();

test("the side panel ranks the active wishes, counts the three places, and folds the granted ones", () => {
  const html = s.renderToStaticMarkup(
    h(s.WishSidebar, {
      wishes: [
        wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1),
        wish("w2", "Polish the brass", s.WishState.ACTIVE, 2, { ready: true }),
        wish("w3", "Trim the wick", s.WishState.PAUSED, 0),
        wish("w4", "Light it", s.WishState.GRANTED, 0),
      ],
      projects: [{ id: "p1", name: "lamp", directory: "/tmp/lamp" }],
      selectedWishId: "w2",
      selectedProjectId: "",
      collapsed: false,
      onSelectWish() {},
      onSelectProject() {},
      onMove() {},
      onNewProject() {},
    }),
  );
  assert.match(
    html,
    /<div class="sidebar-section-label"><svg[^>]*lucide-sparkles[^>]*>[^]*?<\/svg><span>Wishes<\/span><span class="nav-counter "/,
  );
  assert.match(
    html,
    /<div class="sidebar-section-label project-section-label"><svg[^>]*lucide-folder[^>]*>[^]*?<\/svg><span>Projects<\/span><button title="Create a project"/,
  );
  assert.match(html, />2\/3</);
  assert.ok(html.indexOf("Ship the lamp") < html.indexOf("Polish the brass"));
  assert.match(html, /Paused/);
  assert.match(html, /1 granted/);
  // Granted wishes stay folded until asked.
  assert.doesNotMatch(html, /Light it/);
  // Every wish shown is dragged by its row, no grip on it: the active ones to a new rank, a paused one among them or
  // among the paused ones (Q01). The row starts with the wish's icon, 24px, in place of the grip and the state's icon.
  assert.equal(html.match(/draggable="true"/g).length, 3);
  assert.doesNotMatch(html, /wish-grip|lucide-grip-vertical|wish-tone/);
  assert.equal(
    html.match(
      /draggable="true"><span class="wish-emblem"><svg class="lucide wish-icon[^"]*" width="24" height="24"/g,
    ).length,
    3,
  );
  // A paused or granted wish rests: its icon in one grey (wish.css), no second line.
  assert.match(
    html,
    /class="mission-nav wish-nav[^"]*\brest\b[^"]*" title="Trim the wick/,
  );
  assert.match(
    html,
    /title="Trim the wick \(drag, or Alt\+Arrow, to change its place/,
  );
  // A paused wish goes first with its button.
  assert.equal(html.match(/Make it the first active wish/g).length / 2, 1);
  assert.match(html, /mission-nav wish-nav selected/);
  // An active wish takes a second line under its title, its rank at the end; a paused one keeps a single line.
  assert.equal(html.match(/class="wish-counts"/g).length, 2);
  assert.match(
    html,
    /<span class="wish-title">Polish the brass<\/span><span class="wish-counts">/,
  );
  assert.match(
    html,
    /<span class="wish-title">Trim the wick<\/span><\/button>/,
  );
  // A wish ready to grant says it there, before its rank.
  assert.match(html, />ready to grant<\/span><small class="wish-rank">/);
  // Each wish says at a glance what waits and what runs: an icon and a count, with their words, on its second line.
  const sidebar = (
    collapsed,
    count = { questions: 2, running: 1, watching: 1, merging: 0 },
  ) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1)],
        projects: [],
        counts: { w1: count },
        onSelectPlan() {},
        selectedWishId: "",
        selectedProjectId: "",
        collapsed,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
      }),
    );
  const counted = sidebar(false);
  const row = counted.slice(counted.indexOf("wish-nav"));
  // The questions: the small hand and their count, no bubble, no pill; what runs, a full dot and its count, no live
  // ring (no infinite animation).
  assert.match(
    row,
    /<span class="wish-sign tone-waiting" title="2 questions wait for your answer." aria-label="2 questions wait for your answer." role="img"><svg class="lucide waiting-hand"[^>]*>.*?<\/svg><b>2<\/b><\/span>/,
  );
  assert.match(
    row,
    /<span class="wish-sign tone-running" title="1 running" aria-label="1 running" role="img"><span class="wish-sign-dot"><\/span><b>1<\/b><\/span>/,
  );
  assert.doesNotMatch(
    row,
    /live-dot|count-pill|lucide-message-circle-question/,
  );
  // A watcher is counted apart, its eye: it waits for its command to print, it does not work.
  assert.match(
    row,
    /wish-sign tone-watching" title="1 watching"[^>]*><svg[^>]*lucide-eye/,
  );
  // What waits for you first, then what works, then what watches, then the rank.
  const at = (text) => row.indexOf(text);
  assert.ok(at('title="2 questions') < at('title="1 running"'));
  assert.ok(at('title="1 running"') < at('title="1 watching"'));
  assert.ok(at('title="1 watching"') < at("wish-rank"));
  // The wish's own icon is drawn unfolded too, its marks with it, which only the folded rail and a window under 820px
  // wide show (wish.css): the wish's mark (it has no icon yet), the small hand and its count on its corner, a blue dot
  // under it.
  assert.match(
    row,
    /<span class="wish-emblem"><svg[^>]*class="lucide wish-icon wish-mark"[^>]*>(?:(?!<\/svg>).)*<\/svg>/,
  );
  assert.match(
    row,
    /<span class="wish-hand-corner" title="2 questions wait for your answer." aria-label="2 questions wait for your answer." role="img"><svg class="lucide waiting-hand"[^>]*>.*?<\/svg><b>2<\/b><\/span>/,
  );
  assert.match(
    row,
    /<span class="wish-acts" title="1 running" aria-label="1 running" role="img"><span class="wish-act tone-running"><\/span><\/span>/,
  );
  // A wish with no question carries no hand.
  assert.doesNotMatch(
    sidebar(false, { questions: 0, running: 1, watching: 0, merging: 0 }),
    /wish-hand-corner|waiting-hand/,
  );
  // Folded to the rail, the wish's icon is all that shows, the same, with its hand and its dot: no second line.
  const rail = sidebar(true);
  assert.match(
    rail,
    /wish-emblem"><svg class="lucide wish-icon wish-mark" width="24" height="24"/,
  );
  assert.match(
    counted,
    /wish-emblem"><svg class="lucide wish-icon wish-mark" width="24" height="24"/,
  );
  assert.match(rail, /wish-hand-corner/);
  assert.match(rail, /wish-act tone-running/);
  assert.doesNotMatch(rail, /wish-counts/);
  assert.doesNotMatch(rail, /wish-grip/);
  // A wish with an icon of the bank draws it in its duo's colours, its line apart from its gilding; a name the bank
  // does not hold keeps the mark.
  const drawn = (icon) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1, { icon })],
        projects: [],
        selectedWishId: "w1",
        selectedProjectId: "",
        collapsed: true,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
      }),
    );
  const lamp = drawn("lamp");
  assert.match(
    lamp,
    /<svg[^>]*class="lucide wish-icon wish-duo-l"[^>]*stroke-width="1.25"[^>]*><g class="wish-icon-line"><g><path d="M4 12h10l7-3.5/,
  );
  assert.match(lamp, /<g stroke-width="1"><path d="M6.5 14.2h7"\/?>/);
  assert.match(lamp, /<g class="wish-icon-gild"><path[^>]*fill="currentColor"/);
  assert.doesNotMatch(lamp, / id="/);
  assert.match(drawn("no-such-icon"), /wish-icon wish-mark/);
  // The workers that wait, whatever holds them: a grey seat and their number, after what works and before what
  // watches, its tooltip split by reason; on the rail, where no count shows, they hang on the corner of the wish's icon.
  // None waits: neither shows.
  const placed = sidebar(false, {
    questions: 0,
    running: 1,
    watching: 1,
    merging: 0,
    waits: { place: 1, push: 0, dependency: 1, limit: 0, question: 0 },
  });
  const placeRow = placed.slice(placed.indexOf("wish-nav"));
  const placeTitle =
    "2 workers wait: 1 for a place (a free slot, the machine&#x27;s load or memory, a gate); 1 for a dependency";
  assert.match(
    placeRow,
    new RegExp(
      `<span class="wish-sign tone-planned" title="${placeTitle.replace(/[()]/g, "\\$&")}" aria-label="[^"]*" role="img"><svg[^>]*lucide-armchair[^>]*>.*?</svg><b>2</b></span>`,
    ),
  );
  const placeAt = (text) => placeRow.indexOf(text);
  assert.ok(placeAt('title="1 running"') < placeAt("wish-sign tone-planned"));
  assert.ok(placeAt("wish-sign tone-planned") < placeAt('title="1 watching"'));
  const placedRail = sidebar(true, {
    questions: 0,
    running: 1,
    watching: 0,
    merging: 0,
    waits: { place: 0, push: 3, dependency: 0, limit: 0, question: 0 },
  });
  assert.doesNotMatch(placedRail, /wish-counts/);
  assert.match(
    placedRail,
    /<span class="wish-place-corner" title="3 workers wait: 3 for the push checks [^"]*" aria-label="3 workers wait: [^"]*" role="img"><svg[^>]*lucide-armchair[^>]*>.*?<\/svg><b>3<\/b><\/span><\/span>/,
  );
  for (const collapsed of [false, true])
    assert.doesNotMatch(
      sidebar(collapsed, {
        questions: 0,
        running: 1,
        watching: 0,
        merging: 0,
        waits: { place: 0, push: 0, dependency: 0, limit: 0, question: 0 },
      }),
      /lucide-armchair|wish-place-corner|workers? waits?/,
    );
  // A wish whose only process is a watcher is watching, not running.
  const watched = s.renderToStaticMarkup(
    h(s.WishSidebar, {
      wishes: [wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1)],
      projects: [],
      counts: { w1: { questions: 0, running: 0, watching: 1 } },
      onSelectPlan() {},
      selectedWishId: "",
      selectedProjectId: "",
      collapsed: false,
      onSelectWish() {},
      onSelectProject() {},
      onMove() {},
      onNewProject() {},
    }),
  );
  assert.match(watched, /wish-sign tone-watching/);
  assert.doesNotMatch(watched, /running/);
  assert.match(html, />lamp</);
});

test("the side panel counts the new inbox items on the flying carpet's entry, even with no active wish", () => {
  const sidebar = (wishes, inbox) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes,
        projects: [],
        inbox,
        onSelectPlan() {},
        selectedWishId: "",
        selectedProjectId: "",
        collapsed: false,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
      }),
    );
  const paused = wish("w1", "Trim the wick", s.WishState.PAUSED, 0);
  // Nothing active, nothing new: no flying carpet.
  assert.doesNotMatch(sidebar([paused], 0), /plan-nav/);
  // Two new items: the flying carpet shows them, its entry counts them.
  const html = sidebar([paused], 2);
  assert.match(html, /plan-nav/);
  assert.match(
    html,
    /aria-label="2 new items in the inbox"[^>]*><svg[^>]*lucide-inbox[^>]*>.*?<b>2<\/b>/,
  );
  assert.match(
    sidebar([wish("w2", "Ship the lamp", s.WishState.ACTIVE, 1)], 1),
    /aria-label="1 new item in the inbox"/,
  );
  // Folded to the rail: no pill, the small hand and the count on the plane's corner.
  const rail = s.renderToStaticMarkup(
    h(s.WishSidebar, {
      wishes: [paused],
      projects: [],
      inbox: 2,
      onSelectPlan() {},
      selectedWishId: "",
      selectedProjectId: "",
      collapsed: true,
      onSelectWish() {},
      onSelectProject() {},
      onMove() {},
      onNewProject() {},
    }),
  );
  assert.doesNotMatch(rail, /count-pill/);
  assert.match(
    rail,
    /<span class="wish-emblem"><svg[^>]*lucide-plane[^>]*>.*?<\/svg><span class="wish-hand-corner" title="2 new items in the inbox" aria-label="2 new items in the inbox" role="img"><svg class="lucide waiting-hand"[^>]*>.*?<\/svg><b>2<\/b><\/span><\/span>/,
  );
});

test("a project's gear opens its panel; red with a small flame while it has something to fix, which its tooltip counts; folded, the flame passes onto the folder", () => {
  const sidebar = (collapsed) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [],
        projects: [
          { id: "p1", name: "lamp", directory: "/tmp/lamp", toFix: 0 },
          { id: "p2", name: "dolmen", directory: "/tmp/dolmen", toFix: 2 },
        ],
        selectedWishId: "",
        selectedProjectId: "",
        collapsed,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
      }),
    );
  const html = sidebar(false);
  // All well: a grey gear, no flame.
  assert.match(
    html,
    /<button class="icon-button project-gear " title="Configuration of lamp" aria-label="Configuration of lamp"><svg[^>]*lucide-settings/,
  );
  // Something to fix: red, a flame on its corner, the count in its tooltip and on the folder's.
  assert.match(
    html,
    /<button class="icon-button project-gear hot" title="Configuration of dolmen: 2 things to fix" aria-label="Configuration of dolmen: 2 things to fix"><svg[^>]*lucide-settings[^]*?<\/svg><span class="project-flame" aria-hidden="true"><span class="project-flame-glyph"><svg[^>]*lucide-flame/,
  );
  assert.match(html, /title="\/tmp\/dolmen · 2 things to fix"/);
  assert.equal(html.split('project-flame"').length - 1, 1);
  // Folded to the rail: no gear, the flame on the red project's folder alone.
  const rail = sidebar(true);
  assert.doesNotMatch(rail, /project-gear/);
  assert.equal(rail.split('project-flame"').length - 1, 1);
  assert.match(
    rail,
    /title="\/tmp\/dolmen · 2 things to fix"><span class="project-folder"><svg[^>]*lucide-folder-open[^]*?<\/svg><span class="project-flame"/,
  );
  // The gear opens the same panel as the name.
  const opened = [];
  s.ProjectGear({
    project: { id: "p2", name: "dolmen", toFix: 2 },
    onOpen: (id) => opened.push(id),
  }).props.onClick();
  assert.deepEqual(opened, ["p2"]);
});

test("the side panel's Hourglass sits at the bottom of the menu, after the wishes, projects and import, and stays with no active wish", (t) => {
  globalThis.__DJINN_MAC__ = false;
  t.after(() => delete globalThis.__DJINN_MAC__);
  const sidebar = (wishes, selected) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes,
        projects: [{ id: "p1", name: "lamp", directory: "/tmp/lamp" }],
        onSelectPlan() {},
        hourglassSelected: selected,
        onSelectHourglass() {},
        selectedWishId: "",
        selectedProjectId: "",
        collapsed: false,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
        onImportWish() {},
      }),
    );
  const html = sidebar(
    [wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1)],
    true,
  );
  const carpet = html.indexOf("plan-nav");
  const wishes = html.indexOf("Ship the lamp");
  const projects = html.indexOf("project-list");
  const importBtn = html.indexOf("import-nav");
  const glass = html.indexOf("hourglass-nav");
  assert.ok(
    carpet >= 0 &&
      wishes > carpet &&
      projects > wishes &&
      importBtn > projects &&
      glass > importBtn,
    html,
  );
  assert.match(
    html,
    /<button class="nav-item hourglass-nav selected" title="Hourglass \(Ctrl 2\)" aria-keyshortcuts="Control\+2"><svg[^>]*lucide-hourglass[^>]*>[^]*?<\/svg><span>Hourglass<\/span><\/button>/,
  );
  assert.match(
    html,
    /class="nav-item plan-nav " title="Flying carpet \(Ctrl 1\)" aria-keyshortcuts="Control\+1"/,
  );
  // No active wish: no flying carpet, the Hourglass still.
  const calm = sidebar(
    [wish("w1", "Trim the wick", s.WishState.PAUSED, 0)],
    false,
  );
  assert.doesNotMatch(calm, /plan-nav/);
  assert.match(
    calm,
    /class="nav-item hourglass-nav " title="Hourglass \(Ctrl 2\)"/,
  );
});

// The Grimoire, the documentation (W89), sits just under the Hourglass, before the profile; its tooltip says
// Documentation, and folded to its book it keeps its name and its tooltip. It is a page like the Hourglass (W165):
// selected while it shows.
test("the side panel's Grimoire sits under the Hourglass, shows the documentation as a page, and reads folded too", (t) => {
  globalThis.__DJINN_MAC__ = false;
  t.after(() => delete globalThis.__DJINN_MAC__);
  const sidebar = (collapsed, onSelectGrimoire, grimoireSelected = false) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1)],
        projects: [],
        onSelectPlan() {},
        onSelectHourglass() {},
        grimoireSelected,
        onSelectGrimoire,
        selectedWishId: "",
        selectedProjectId: "",
        collapsed,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
        onImportWish() {},
      }),
    );
  const open = sidebar(false, () => {});
  const glass = open.indexOf("hourglass-nav");
  const grimoire = open.indexOf("grimoire-nav");
  assert.ok(glass >= 0 && grimoire > glass, open);
  assert.match(
    open,
    /<button class="nav-item grimoire-nav " title="Documentation" aria-label="Grimoire"><svg[^>]*lucide-book-marked[^>]*>[^]*?<\/svg><span>Grimoire<\/span><\/button>$/,
  );
  // Shown, it is the entry selected.
  assert.match(
    sidebar(false, () => {}, true),
    /<button class="nav-item grimoire-nav selected" title="Documentation"/,
  );
  // Folded: the book alone, its name and its tooltip kept.
  assert.match(
    sidebar(true, () => {}),
    /<button class="nav-item grimoire-nav " title="Documentation" aria-label="Grimoire"><svg[^>]*lucide-book-marked[^>]*>[^]*?<\/svg><\/button>$/,
  );
  // No way to the documentation, no entry.
  assert.doesNotMatch(sidebar(false), /grimoire-nav/);
});

test("the Summoning Circle sits below the Grimoire at the bottom of the side panel", () => {
  const render = (collapsed, onOpenSummoningCircle) =>
    s.renderToStaticMarkup(
      h(s.WishSidebar, {
        wishes: [wish("w1", "Ship the lamp", s.WishState.ACTIVE, 1)],
        projects: [],
        onSelectPlan() {},
        onSelectHourglass() {},
        onSelectGrimoire() {},
        onOpenSummoningCircle,
        selectedWishId: "",
        selectedProjectId: "",
        collapsed,
        onSelectWish() {},
        onSelectProject() {},
        onMove() {},
        onNewProject() {},
        onImportWish() {},
      }),
    );
  const open = render(false, () => {});
  const grimoire = open.indexOf("grimoire-nav");
  const summoning = open.indexOf("summoning-nav");
  assert.ok(grimoire >= 0 && summoning > grimoire, open);
  assert.match(
    open,
    /<button class="nav-item summoning-nav" title="Choose for each kind of worker which model answers across all projects" aria-label="Summoning Circle"><svg[^>]*lucide-compass[^>]*>[^]*?<\/svg><span>Summoning Circle<\/span><\/button>$/,
  );
  assert.match(
    render(true, () => {}),
    /<button class="nav-item summoning-nav" title="Choose for each kind of worker which model answers across all projects" aria-label="Summoning Circle"><svg[^>]*lucide-compass[^>]*>[^]*?<\/svg><\/button>$/,
  );
});
