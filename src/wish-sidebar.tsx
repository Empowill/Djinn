// The side panel's lists, read from the services: the flying carpet's entry with what waits for you there (questions,
// new inbox items), the wishes, the active ones by rank and how many of the three places they take, then the paused
// ones and the granted ones, each list in its own order; the projects, each with its branches out of sync with their
// remote (project-sync.tsx) and its gear, which opens its panel and turns red with a small flame when its
// configuration has something for you to fix (Project.to_fix); the Hourglass's entry at the bottom (the live metrics,
// hourglass.tsx), and under it the Grimoire's, the documentation (docs.tsx), a page like the Hourglass, before the profile. The wishes' list shows the active ones, each with its counts on a second line, and three rows more;
// it scrolls under the pointer. Every wish is dragged by its row, or moved with Alt+Arrow, within its list; while it is
// dragged, a thin line shows where it would land. A paused or granted wish dropped among the active
// ones, or sent first with its button, becomes active there (Q01): three being active, the last one is paused, first
// among the paused ones.
import {
  Armchair,
  ArrowUpToLine,
  BookMarked,
  ChevronDown,
  CirclePause,
  ChevronRight,
  Compass,
  Eye,
  Flame,
  Folder,
  FolderOpen,
  GitMerge,
  Hourglass,
  Inbox,
  type LucideIcon,
  Plane,
  Plus,
  Settings,
  Sparkles,
  SquareTerminal,
  Upload,
  Wrench,
} from "lucide-react";
import {
  type DragEvent,
  type KeyboardEvent,
  type ReactNode,
  useRef,
  useState,
} from "react";

import type { Project, ProjectSync, Wish } from "../gen/ts/plan/v1/plan_pb";
import { WishState } from "../gen/ts/plan/v1/plan_pb";
import {
  MAX_ACTIVE,
  type Tone,
  type Waits,
  isActive,
  waitCount,
  waitText,
} from "./data/format";
import { t } from "./i18n";
import { useOpenTerminal } from "./lead-terminal";
import { CARPET_DIGIT, HOURGLASS_DIGIT, usePageShortcut } from "./page-keys";
import { SyncLines } from "./project-sync";
import { CountPill, IfritFlame, WaitingHand } from "./status";
import { WishIcon } from "./wish-icon";

// The wishes' lists: a wish is dragged within its own, and from any into the active one.
type Zone = "active" | "paused" | "granted";

const zoneOf = (wish: Wish): Zone =>
  isActive(wish)
    ? "active"
    : wish.state === WishState.GRANTED
      ? "granted"
      : "paused";

// Where the dragged wish would land: its list, the gap it falls into (0 before the first wish, the list's length after
// the last), its rank or place once there, and the height of the line that shows it in the wishes' list.
interface Landing {
  zone: Zone;
  gap: number;
  to: number;
  top: number;
}

// What the side panel counts in a wish.
export interface WishCounts {
  questions: number;
  // Workers at work, watchers waiting for their command, and finished work waiting to merge, apart (runningOf).
  running: number;
  // Investigators at work on a question after Enlighten me (runningOf), apart from the running ones: shown violet.
  investigating?: number;
  watching: number;
  merging: number;
  // Failed merges a correction task fixes (runningOf): Djinn's move, shown orange.
  fixing?: number;
  // Lit ifrits (runningOf), apart from the running ones: a vermilion flame, and the wish's tone.
  ifrits?: number;
  // Workers that wait, by what holds them (waitsOf): a place, the branch's push checks, a dependency, a usage limit or
  // the network, a question. Grey, with a seat: their number, and each reason's in its tooltip.
  waits?: Waits;
  // Workers paused, their work kept until they go on (runningOf, workerPhase): grey, with a circled pause.
  paused?: number;
}

export function WishSidebar({
  wishes,
  projects,
  counts = {},
  inbox = 0,
  planSelected = false,
  onSelectPlan,
  hourglassSelected = false,
  onSelectHourglass,
  grimoireSelected = false,
  onSelectGrimoire,
  onOpenSummoningCircle,
  selectedWishId,
  selectedProjectId,
  collapsed,
  onSelectWish,
  onSelectProject,
  onMove,
  onReorder,
  onNewProject,
  onPush,
  onPull,
  onImportWish,
  importDisabled = false,
}: {
  wishes: Wish[];
  projects: Project[];
  // What each active wish holds, by id: questions that wait for you, workers that run.
  counts?: Readonly<Record<string, WishCounts>>;
  // How many inbox items wait for your answer: the flying carpet shows them.
  inbox?: number;
  // The flying carpet of the active wishes shows.
  planSelected?: boolean;
  onSelectPlan?: () => void;
  // The Hourglass, the live metrics, shows.
  hourglassSelected?: boolean;
  onSelectHourglass?: () => void;
  // The Grimoire, the documentation, shows.
  grimoireSelected?: boolean;
  onSelectGrimoire?: () => void;
  // Opens the Summoning Circle: the default agent and model per project.
  onOpenSummoningCircle?: () => void;
  selectedWishId: string;
  selectedProjectId: string;
  collapsed: boolean;
  onSelectWish: (id: string) => void;
  onSelectProject: (id: string) => void;
  // Moves a wish to a rank, from 1: a paused or granted one becomes active there.
  onMove: (wishId: string, to: number) => void;
  // Moves a paused or granted wish to a place in its own list, from 1.
  onReorder: (wishId: string, to: number) => void;
  onNewProject: () => void;
  // Pushes one branch of the project, out of sync with its remote.
  onPush?: (projectId: string, sync: ProjectSync) => Promise<unknown> | void;
  // Pulls a branch behind its remote, a fast-forward only; a refusal says why on its line.
  onPull?: (projectId: string, sync: ProjectSync) => Promise<unknown> | void;
  onImportWish?: () => void;
  importDisabled?: boolean;
}) {
  const active = wishes.filter(isActive);
  const paused = wishes.filter((w) => w.state === WishState.PAUSED);
  const granted = wishes.filter((w) => w.state === WishState.GRANTED);
  const [showGranted, setShowGranted] = useState(false);
  // Where djinn serves the page, a project opens a terminal at its root, in a tab of its own.
  const openTerminal = useOpenTerminal();
  const lists: Record<Zone, Wish[]> = { active, paused, granted };
  const [dragged, setDragged] = useState("");
  const [landing, setLanding] = useState<Landing>();
  // Each row of a wish by its id, and the line that says no wish is active: where a drop line goes.
  const rows = useRef(new Map<string, HTMLElement>());
  const noActive = useRef<HTMLParagraphElement>(null);
  // The pages' shortcuts, named in their entries' tooltips as this keyboard writes them (Ctrl & on AZERTY).
  const carpetKey = usePageShortcut(CARPET_DIGIT);
  const hourglassKey = usePageShortcut(HOURGLASS_DIGIT);

  // Where the dragged wish lands in the gap of zone, or nothing when it cannot go there or would not move: a wish goes
  // into the active list or within its own, at the third place at most among the active ones.
  const land = (zone: Zone, gap: number): Landing | undefined => {
    const from = wishes.find((w) => w.id === dragged);
    if (!from || (zone !== "active" && zone !== zoneOf(from))) return;
    const list = lists[zone];
    const at = list.findIndex((w) => w.id === dragged);
    gap = Math.min(gap, list.length);
    if (at < 0 && zone === "active") gap = Math.min(gap, MAX_ACTIVE - 1);
    if (at >= 0 && (gap === at || gap === at + 1)) return;
    // The line sits on the edge between two rows: the top of the row after the gap, or the bottom of the last one; never
    // above the list's top, which would cut it.
    const after = list[gap] && rows.current.get(list[gap].id);
    const before = gap > 0 ? rows.current.get(list[gap - 1].id) : undefined;
    const top = after
      ? after.offsetTop
      : before
        ? before.offsetTop + before.offsetHeight
        : (noActive.current?.offsetTop ?? 0);
    return {
      zone,
      gap,
      to: at >= 0 && gap > at ? gap : gap + 1,
      top: Math.max(top, 1),
    };
  };
  // Over a row, the pointer's half says which gap it points at: above the row, or below it.
  const over = (event: DragEvent<HTMLElement>, zone: Zone, index: number) => {
    if (!dragged) return;
    event.stopPropagation();
    const box = event.currentTarget.getBoundingClientRect();
    const next = land(
      zone,
      index + (event.clientY > box.top + box.height / 2 ? 1 : 0),
    );
    if (
      next?.zone !== landing?.zone ||
      next?.gap !== landing?.gap ||
      next?.top !== landing?.top
    )
      setLanding(next);
    if (!next) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
  };
  const drop = (event: DragEvent) => {
    event.preventDefault();
    if (dragged && landing) {
      if (landing.zone === "active") onMove(dragged, landing.to);
      else onReorder(dragged, landing.to);
    }
    setDragged("");
    setLanding(undefined);
  };

  const item = (wish: Wish, rank?: number) => {
    const zone = zoneOf(wish);
    const index = lists[zone].indexOf(wish);
    const count = counts[wish.id] ?? {
      questions: 0,
      running: 0,
      watching: 0,
      merging: 0,
    };
    // Each wish shows its own icon of the bank, or its grey mark (L09 v6), at the same place and size folded or
    // unfolded: the row is built around it, and only its states move when the menu folds (wish.css). A paused or
    // granted wish's icon gives up its colours, one grey. Unfolded, an active wish takes a second line under its title,
    // in one order: its lit ifrits' flame, the small hand and the questions that wait for you, what runs by kind (work,
    // merges, investigations, repairs), what is paused, what waits whatever holds it, what watches, then ready to grant,
    // a count at zero left out, and its rank at the end. Folded to the rail (or narrowed by its width alone, styles.css,
    // 820px, the second line hidden), the same states sit on the icon's corners, without background: the hand and the
    // questions' count on its upper right, a lit ifrit's flame on its upper left; under it a blue dot while workers run
    // or merge, a violet one while an investigation runs, never a number; the workers that wait hang on its lower right
    // corner, the paused ones on its lower left.
    const counted = !!rank && !collapsed;
    const working = count.running + count.merging;
    const ifritLabel = t("wish.ifrits_lit", { count: count.ifrits ?? 0 });
    const questionsLabel = t("wish.questions_wait", {
      count: count.questions,
    });
    const actsLabel = [
      count.running > 0 && t("plan.running", { count: count.running }),
      count.merging > 0 && t("plan.merging_detail", { count: count.merging }),
      !!count.investigating &&
        t("plan.investigating_detail", { count: count.investigating }),
    ]
      .filter(Boolean)
      .join(" · ");
    const waiting = waitCount(count.waits);
    const waitLabel = count.waits ? waitText(count.waits) : "";
    const pausedLabel = t("plan.paused", { count: count.paused ?? 0 });
    // Alt+Arrow moves a wish one step within its list: its rank, or its place among the paused or granted ones.
    const keys = (event: KeyboardEvent) => {
      if (!event.altKey) return;
      const move = rank ? onMove : onReorder;
      const length = lists[zone].length;
      if (event.key === "ArrowUp" && index > 0) move(wish.id, index);
      else if (event.key === "ArrowDown" && index < length - 1)
        move(wish.id, index + 2);
      else return;
      event.preventDefault();
    };
    // The row that takes the drag: the paused wish's, with its button, or the wish's own.
    const target = {
      ref: (el: HTMLElement | null) => {
        if (el) rows.current.set(wish.id, el);
        else rows.current.delete(wish.id);
      },
      onDragOver: (event: DragEvent<HTMLElement>) => over(event, zone, index),
    };
    const wrapped = wish.state === WishState.PAUSED && !collapsed;
    const row = (
      <button
        key={wish.id}
        {...(wrapped ? {} : target)}
        className={`mission-nav wish-nav ${wish.id === selectedWishId ? "selected" : ""} ${dragged === wish.id ? "dragged" : ""} ${rank ? "ranked" : "rest"} ${counted ? "counted" : ""}`}
        onClick={() => onSelectWish(wish.id)}
        onKeyDown={keys}
        title={
          rank
            ? t("sidebar.wish_rank", { rank, title: wish.title })
            : t("sidebar.wish_place", { title: wish.title })
        }
        draggable
        onDragStart={(event) => {
          event.dataTransfer.setData("text/plain", wish.id);
          event.dataTransfer.effectAllowed = "move";
          setDragged(wish.id);
        }}
        onDragEnd={() => {
          setDragged("");
          setLanding(undefined);
        }}
      >
        <span className="wish-emblem">
          <WishIcon wish={wish} size={24} />
          {!!rank && !!count.ifrits && (
            <span
              className="wish-flame-corner"
              title={ifritLabel}
              aria-label={ifritLabel}
              role="img"
            >
              <IfritFlame />
            </span>
          )}
          {!!rank && count.questions > 0 && (
            <span
              className="wish-hand-corner"
              title={questionsLabel}
              aria-label={questionsLabel}
              role="img"
            >
              <WaitingHand />
              <b>{count.questions}</b>
            </span>
          )}
          {!!rank && (working > 0 || !!count.investigating) && (
            <span
              className="wish-acts"
              title={actsLabel}
              aria-label={actsLabel}
              role="img"
            >
              {working > 0 && <span className="wish-act tone-running" />}
              {!!count.investigating && (
                <span className="wish-act tone-investigating" />
              )}
            </span>
          )}
          {!!rank && !!waiting && (
            <span
              className="wish-place-corner"
              title={waitLabel}
              aria-label={waitLabel}
              role="img"
            >
              <Armchair size={8} aria-hidden="true" />
              <b>{waiting}</b>
            </span>
          )}
          {!!rank && !!count.paused && (
            <span
              className="wish-paused-corner"
              title={pausedLabel}
              aria-label={pausedLabel}
              role="img"
            >
              <CirclePause size={8} aria-hidden="true" />
              <b>{count.paused}</b>
            </span>
          )}
        </span>
        {!collapsed && (
          <>
            <span className="wish-title">{wish.title}</span>
            {counted && (
              <span className="wish-counts">
                {!!count.ifrits && (
                  <Sign tone="ifrit" label={ifritLabel}>
                    <IfritFlame />
                    {count.ifrits > 1 && <b>{count.ifrits}</b>}
                  </Sign>
                )}
                {count.questions > 0 && (
                  <Sign tone="waiting" label={questionsLabel}>
                    <WaitingHand />
                    <b>{count.questions}</b>
                  </Sign>
                )}
                {count.running > 0 && (
                  <Sign
                    tone="running"
                    count={count.running}
                    label={t("plan.running", { count: count.running })}
                  />
                )}
                {count.merging > 0 && (
                  <Sign
                    tone="running"
                    icon={GitMerge}
                    count={count.merging}
                    label={t("plan.merging_detail", { count: count.merging })}
                  />
                )}
                {!!count.investigating && (
                  <Sign
                    tone="investigating"
                    count={count.investigating}
                    label={t("plan.investigating_detail", {
                      count: count.investigating,
                    })}
                  />
                )}
                {!!count.fixing && (
                  <Sign
                    tone="fixing"
                    icon={Wrench}
                    count={count.fixing}
                    label={t("plan.fixing_detail", { count: count.fixing })}
                  />
                )}
                {!!count.paused && (
                  <Sign
                    tone="paused"
                    icon={CirclePause}
                    count={count.paused}
                    label={pausedLabel}
                  />
                )}
                {!!waiting && (
                  <Sign
                    tone="planned"
                    icon={Armchair}
                    count={waiting}
                    label={waitLabel}
                  />
                )}
                {count.watching > 0 && (
                  <Sign
                    tone="watching"
                    icon={Eye}
                    count={count.watching}
                    label={t("plan.watching", { count: count.watching })}
                  />
                )}
                {wish.ready && (
                  <span
                    className="wish-ready tone-proof"
                    title={t("wish.ready_title")}
                  >
                    {t("sidebar.ready")}
                  </span>
                )}
                {/* A rank, never a count: "#3" after the signs, which count (W507). */}
                <small className="wish-rank">#{rank}</small>
              </span>
            )}
          </>
        )}
      </button>
    );
    if (!wrapped) return row;
    return (
      <div key={wish.id} className="wish-row" {...target}>
        {row}
        <button
          className="icon-button wish-first"
          title={t("sidebar.to_first")}
          aria-label={t("sidebar.to_first")}
          onClick={() => onMove(wish.id, 1)}
        >
          <ArrowUpToLine size={13} />
        </button>
      </div>
    );
  };

  const waits = active.reduce(
    (sum, w) => sum + (counts[w.id]?.questions ?? 0),
    0,
  );
  const carpetLabel = [
    waits > 0 && t("wish.questions_wait", { count: waits }),
    inbox > 0 && t("sidebar.inbox_new", { count: inbox }),
  ]
    .filter(Boolean)
    .join(" · ");
  return (
    <>
      {onSelectPlan && (active.length > 0 || inbox > 0) && (
        <button
          className={`nav-item plan-nav ${planSelected ? "selected" : ""}`}
          onClick={onSelectPlan}
          title={t("sidebar.page_shortcut", {
            page: t("plan.title"),
            shortcut: carpetKey.label,
          })}
          aria-keyshortcuts={carpetKey.aria}
        >
          {collapsed ? (
            // Folded, what waits for you there is the small hand and its count on the plane's corner (L09 v6).
            <span className="wish-emblem">
              <Plane size={15} />
              {waits + inbox > 0 && (
                <span
                  className="wish-hand-corner"
                  title={carpetLabel}
                  aria-label={carpetLabel}
                  role="img"
                >
                  <WaitingHand />
                  <b>{waits + inbox}</b>
                </span>
              )}
            </span>
          ) : (
            <>
              <Plane size={15} />
              <span>{t("plan.title")}</span>
              {waits > 0 && (
                <Sign
                  tone="waiting"
                  label={t("wish.questions_wait", { count: waits })}
                >
                  <WaitingHand />
                  <b>{waits}</b>
                </Sign>
              )}
              {inbox > 0 && (
                <CountPill
                  tone="waiting"
                  icon={Inbox}
                  count={inbox}
                  label={t("sidebar.inbox_new", { count: inbox })}
                />
              )}
            </>
          )}
        </button>
      )}
      <div className="sidebar-section-label">
        {!collapsed && <Sparkles size={15} />}
        {!collapsed && <span>{t("sidebar.wishes")}</span>}
        {!collapsed && (
          <span
            className={`nav-counter ${active.length >= MAX_ACTIVE ? "full" : ""}`}
            title={t("sidebar.active_detail", { max: MAX_ACTIVE })}
          >
            {t("sidebar.active_count", {
              count: active.length,
              max: MAX_ACTIVE,
            })}
          </span>
        )}
      </div>
      <div
        className="mission-list wish-list"
        onDragOver={(event) => {
          if (dragged && landing) event.preventDefault();
        }}
        onDragLeave={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node))
            setLanding(undefined);
        }}
        onDrop={drop}
      >
        {active.map((wish, i) => item(wish, wish.rank || i + 1))}
        {!active.length && !collapsed && (
          <p
            ref={noActive}
            className="project-empty"
            onDragOver={(event) => over(event, "active", 0)}
          >
            {t("sidebar.no_active")}
          </p>
        )}
        {/* Folded, the sub-lists' labels keep their place, empty: no wish's icon moves when the menu folds. */}
        {paused.length > 0 && (
          <>
            <div className="sidebar-section-label sub">
              {!collapsed && <span>{t("sidebar.paused")}</span>}
            </div>
            {paused.map((wish) => item(wish))}
          </>
        )}
        {granted.length > 0 && (
          <>
            {collapsed ? (
              <div className="sidebar-section-label sub" />
            ) : (
              <button
                className="sidebar-section-label sub"
                onClick={() => setShowGranted(!showGranted)}
                aria-expanded={showGranted}
              >
                {showGranted ? (
                  <ChevronDown size={12} />
                ) : (
                  <ChevronRight size={12} />
                )}
                <span>{t("sidebar.granted", { count: granted.length })}</span>
              </button>
            )}
            {showGranted && granted.map((wish) => item(wish))}
          </>
        )}
        {landing && (
          <div
            className="wish-drop-line"
            style={{ top: landing.top }}
            aria-hidden="true"
          />
        )}
      </div>
      <div className="sidebar-section-label project-section-label">
        {!collapsed && <Folder size={15} />}
        {!collapsed && <span>{t("sidebar.projects")}</span>}
        <button
          onClick={onNewProject}
          title={t("sidebar.new_project")}
          aria-label={t("sidebar.new_project")}
        >
          <Plus size={15} />
        </button>
      </div>
      <div className="mission-list project-list">
        {projects.map((project) => (
          <div
            key={project.id}
            className={`project-nav ${selectedProjectId === project.id ? "selected" : ""}`}
          >
            <div className="project-nav-main">
              <button
                className="project-select"
                title={
                  (project.directory || t("project.no_folder")) +
                  (project.toFix
                    ? ` · ${t("project.to_fix", { count: project.toFix })}`
                    : "")
                }
                onClick={() => onSelectProject(project.id)}
              >
                <span className="project-folder">
                  <FolderOpen size={collapsed ? 16 : 15} />
                  {/* Folded to the rail, the gear is gone: its flame passes onto the folder. */}
                  {collapsed && project.toFix > 0 && <ProjectFlame />}
                </span>
                {!collapsed && <span>{project.name}</span>}
              </button>
              {openTerminal && project.directory && !collapsed && (
                <button
                  className="icon-button project-terminal"
                  onClick={() => openTerminal(project)}
                  title={t("sidebar.project_terminal", {
                    project: project.name,
                  })}
                  aria-label={t("sidebar.project_terminal", {
                    project: project.name,
                  })}
                >
                  <SquareTerminal size={13} />
                </button>
              )}
              {!collapsed && (
                <ProjectGear project={project} onOpen={onSelectProject} />
              )}
            </div>
            {!collapsed && (
              <SyncLines
                syncs={project.syncs}
                onPush={onPush && ((sync) => onPush(project.id, sync))}
                onPull={onPull && ((sync) => onPull(project.id, sync))}
              />
            )}
          </div>
        ))}
      </div>
      {onImportWish && (
        <button
          className="nav-item import-nav"
          onClick={onImportWish}
          title={t("app.import_wish")}
          disabled={importDisabled}
        >
          <Upload size={15} />
          {!collapsed && <span>{t("app.import_wish")}</span>}
        </button>
      )}
      {onSelectHourglass && (
        <button
          className={`nav-item hourglass-nav ${hourglassSelected ? "selected" : ""}`}
          onClick={onSelectHourglass}
          title={t("sidebar.page_shortcut", {
            page: t("hourglass.title"),
            shortcut: hourglassKey.label,
          })}
          aria-keyshortcuts={hourglassKey.aria}
        >
          <Hourglass size={15} />
          {!collapsed && <span>{t("hourglass.title")}</span>}
        </button>
      )}
      {/* Its tooltip says Documentation in every language: folded to its book, it still reads as the documentation. */}
      {onSelectGrimoire && (
        <button
          className={`nav-item grimoire-nav ${grimoireSelected ? "selected" : ""}`}
          onClick={onSelectGrimoire}
          title={t("docs.subtitle")}
          aria-label={t("sidebar.grimoire")}
        >
          <BookMarked size={15} />
          {!collapsed && <span>{t("sidebar.grimoire")}</span>}
        </button>
      )}
      {onOpenSummoningCircle && (
        <button
          className="nav-item summoning-nav"
          onClick={onOpenSummoningCircle}
          title={t("summoning.subtitle")}
          aria-label={t("sidebar.summoning_circle")}
        >
          <Compass size={15} />
          {!collapsed && <span>{t("sidebar.summoning_circle")}</span>}
        </button>
      )}
    </>
  );
}

// Sign is a state on a wish's second line, or the flying carpet's: its glyph and its number in its state's colour
// (review.css), no pill around it, and its words for whoever cannot see the colour. A full dot when no glyph is given:
// what runs.
function Sign({
  tone,
  label,
  count,
  icon: Icon,
  children,
}: {
  tone: Tone;
  label: string;
  count?: number;
  icon?: LucideIcon;
  // The glyph and the number, when they are not a lucide icon and a count: the hand, the flame.
  children?: ReactNode;
}) {
  return (
    <span
      className={`wish-sign tone-${tone}`}
      title={label}
      aria-label={label}
      role="img"
    >
      {children ?? (
        <>
          {Icon ? (
            <Icon size={12} aria-hidden="true" />
          ) : (
            <span className="wish-sign-dot" />
          )}
          <b>{count}</b>
        </>
      )}
    </span>
  );
}

// ProjectFlame is the small flame of a project whose configuration has something for you to fix: a full round on the
// corner of its gear, or of its folder once the side panel is folded. It flickers by transform and opacity only, and
// stands still in a window at rest (styles.css, html[data-still]).
export function ProjectFlame() {
  return (
    <span className="project-flame" aria-hidden="true">
      <span className="project-flame-glyph">
        <Flame size={8} fill="currentColor" />
      </span>
    </span>
  );
}

// ProjectGear opens a project's panel, as its name does; grey while all is well, red with its flame while its
// configuration has something for you to fix, which its tooltip counts.
export function ProjectGear({
  project,
  onOpen,
}: {
  project: Project;
  onOpen: (id: string) => void;
}) {
  const label = project.toFix
    ? t("sidebar.project_gear_fix", {
        project: project.name,
        count: project.toFix,
      })
    : t("sidebar.project_gear", { project: project.name });
  return (
    <button
      className={`icon-button project-gear ${project.toFix ? "hot" : ""}`}
      onClick={() => onOpen(project.id)}
      title={label}
      aria-label={label}
    >
      <Settings size={14} />
      {project.toFix > 0 && <ProjectFlame />}
    </button>
  );
}
