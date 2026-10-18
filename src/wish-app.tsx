// The window, on the services: the side panel (the flying carpet, the wishes by rank, the projects, the Hourglass, the
// Grimoire), the flying carpet of the active wishes, the Hourglass's live metrics, the Grimoire or the wish shown, and
// the status bar (whether the page follows djinn, the machine). What stays on this page is how it shows: what is shown
// and the side panel folded.
import { ArrowLeft, PanelLeft, Plus, Settings2 } from "lucide-react";
import { AnimatePresence, LayoutGroup } from "motion/react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Change, type ProjectSync, type Wish } from "../gen/ts/plan/v1/plan_pb";
import { ShownTheme } from "../gen/ts/ui/v1/ui_pb";
import { useShownTheme } from "./theme";
import { message } from "./data/client";
import {
  useClients,
  useData,
  useDjinn,
  useMachine,
  useStore,
  useWishDetails,
} from "./data/djinn";
import { importWish } from "./data/exchange";
import { followPresence } from "./data/presence";
import {
  date,
  gateWaiters,
  isActive,
  runningOf,
  waitsForYou,
  waitsOf,
} from "./data/format";
import { AgentSetup, needsSetup } from "./agent-setup";
import { FlightPlan } from "./flight-plan";
import { Brand, Toast } from "./frame";
import { HOURGLASS, Hourglass } from "./hourglass";
import { HeldGates } from "./ifrit";
import { language, t } from "./i18n";
import { EmptyState } from "./empty-art";
import { platformShortcut, TooltipButton } from "./tooltip";
import { useInboxCount } from "./inbox";
import { ProjectPanel } from "./project-panel";
import { SummoningCircle } from "./summoning-circle";
import { AddProject, MakeWish, Settings } from "./wish-dialogs";
import { Docs, GRIMOIRE } from "./docs";
import { WishSidebar } from "./wish-sidebar";
import { startWishSmokePrewarm } from "./wish-smoke";
import { TilasmOver } from "./tilasm-over";
import {
  type Landing,
  type Opening,
  type Reaching,
  WishView,
} from "./wish-view";
import { DjinnEye } from "./djinn-eye";
import { OperatingLoad } from "./operating-load";
import { PageHeader } from "./page-header";
import { CARPET_DIGIT, HOURGLASS_DIGIT, pageDigit } from "./page-keys";
import "./wish.css";
import "./review.css";

type Modal = "make" | "project" | "settings" | "agents" | "summoning" | null;

// What the window shows instead of a wish: the flying carpet of the active wishes. A wish's id is a UUID: never this.
export const PLAN = "plan";

// The pages a shortcut shows, by its digit (page-keys.ts).
const PAGES: Record<number, string> = {
  [CARPET_DIGIT]: PLAN,
  [HOURGLASS_DIGIT]: HOURGLASS,
};

function stored(key: string): string {
  try {
    return window.localStorage.getItem(key) ?? "";
  } catch {
    return "";
  }
}
function store(key: string, value: string) {
  try {
    if (value) window.localStorage.setItem(key, value);
    else window.localStorage.removeItem(key);
  } catch {
    // Private window or blocked storage: the preference lasts for this page only.
  }
}

export function WishApp() {
  const djinn = useDjinn()!;
  const clients = useClients();
  const data = useStore();
  const wishes = useData((s) => s.wishes);
  const projects = useData((s) => s.projects);
  const loaded = useData((s) => s.loaded);
  const live = useData((s) => s.live);
  const error = useData((s) => s.error);
  const inbox = useInboxCount();
  const [selected, setSelected] = useState(() => stored("djinn.wish"));
  // Keep a just-created wish renderable for the same commit as MakeWish leaves, so the shared action surface can
  // animate into the wish view while the next store list is still in flight.
  const [justMade, setJustMade] = useState<Wish>();
  const [collapsed, setCollapsed] = useState(
    () => stored("djinn.sidebar.collapsed") === "1",
  );
  // The sidebar glides only once folded or unfolded by hand, never as the page opens, and for the glide's time only:
  // a wish's states coming later appear without it. A count of the folds, so that a second fold keeps the glide.
  const [glide, setGlide] = useState(0);
  const [modal, setModal] = useState<Modal>(null);
  const [making, setMaking] = useState(false);
  const [projectId, setProjectId] = useState("");
  const [toast, setToast] = useState("");
  const makeBackAction = useRef<(() => void) | null>(null);
  const [makeBackInSetup, setMakeBackInSetup] = useState(false);
  // The tilasm a djinn:// link asks to show, in its wish; a new object at each request.
  const [opening, setOpening] = useState<Opening>();
  // The element a line of the attention bar asks to bring into sight, in its wish; a new object at each request.
  const [reaching, setReaching] = useState<Reaching>();
  // The wish clicked in the side panel, even the one shown: its first tab, the questions, and nothing over it; a new
  // object at each click.
  const [landing, setLanding] = useState<Landing>();
  const importRef = useRef<HTMLInputElement>(null);
  const closeToast = useCallback(() => setToast(""), []);
  const closeMake = useCallback(() => setModal(null), []);
  const toggleSidebar = useCallback(() => {
    setGlide((n) => n + 1);
    setCollapsed((value) => !value);
  }, []);
  useEffect(() => {
    if (!glide) return;
    const timer = window.setTimeout(() => setGlide(0), 400);
    return () => window.clearTimeout(timer);
  }, [glide]);
  // showPage shows a page of the window instead of a wish: the flying carpet (PLAN), the Hourglass (HOURGLASS) or the
  // Grimoire (GRIMOIRE), by the side panel or by the keyboard (PAGES).
  const showPage = useCallback((page: string) => {
    setSelected(page);
    setModal(null);
  }, []);

  useEffect(() => store("djinn.wish", selected), [selected]);
  useEffect(() => {
    if (justMade && wishes.some((item) => item.id === justMade.id))
      setJustMade(undefined);
  }, [justMade, wishes]);
  useEffect(
    () => store("djinn.sidebar.collapsed", collapsed ? "1" : ""),
    [collapsed],
  );
  // djinn lead resume --wish-id asks the window to show a wish, and so does a request routed to a new wish, or a click
  // on a notification, there; a djinn:// link, a wish or a tilasm in its wish, or says Djinn does not know it.
  useEffect(
    () =>
      djinn.focus.subscribe((focus) => {
        if (focus.wishId) setSelected(focus.wishId);
        // A page a link asks for (a worker's row in the Hourglass): its target is reached there as in a wish.
        if (focus.page) setSelected(focus.page);
        if (focus.tilasmId)
          setOpening({ wishId: focus.wishId, tilasmId: focus.tilasmId });
        if (focus.target)
          setReaching({
            wishId: focus.page || focus.wishId,
            target: focus.target,
          });
        if (focus.unknownLink)
          setToast(t("link.unknown", { link: focus.unknownLink }));
      }),
    [djinn],
  );
  // Another wish shown: a tilasm asked for earlier is not opened again on coming back.
  useEffect(() => {
    setOpening((o) => (o && o.wishId !== selected ? undefined : o));
    setReaching((r) => (r && r.wishId !== selected ? undefined : r));
    setLanding((l) => (l && l.wishId !== selected ? undefined : l));
  }, [selected]);
  // The window opens with no agent ready (none installed and signed in, or of unknown sign-in): the setup of the agents
  // opens by itself. Otherwise it waits in the settings and the lead's menu.
  useEffect(() => {
    let live = true;
    clients.ui
      .getEnvironment({ agents: true })
      .then((res) => {
        if (live && res.providers.length > 0 && res.providers.every(needsSetup))
          setModal((m) => m ?? "agents");
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [clients]);
  // Ctrl+N (Cmd+N) makes a wish.
  useEffect(() => {
    const keys = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.repeat || event.isComposing) return;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "n") {
        event.preventDefault();
        setModal("make");
      }
    };
    window.addEventListener("keydown", keys);
    return () => window.removeEventListener("keydown", keys);
  }, []);
  // Ctrl+\ (Cmd+\) folds the navigation without taking shortcuts from the terminal.
  useEffect(() => {
    const keys = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.repeat ||
        event.isComposing ||
        (modal !== null && modal !== "make") ||
        making ||
        !(event.metaKey || event.ctrlKey) ||
        (event.key !== "\\" && event.code !== "Backslash")
      )
        return;
      if (event.target instanceof HTMLElement && event.target.closest(".xterm"))
        return;
      event.preventDefault();
      toggleSidebar();
    };
    window.addEventListener("keydown", keys);
    return () => window.removeEventListener("keydown", keys);
  }, [making, modal, toggleSidebar]);
  // Ctrl+1 (Cmd+1) shows the flying carpet, Ctrl+2 (Cmd+2) the Hourglass, by the key's place: on AZERTY, Ctrl+& and
  // Ctrl with the key of e acute; the numpad's digits too. In a text field too: Ctrl and a digit type and edit nothing
  // there. Never from a terminal, whose keys stay its own (Ctrl+3 to Ctrl+8 type Escape and other control codes
  // there), nor over a dialog, as Ctrl+\.
  useEffect(() => {
    const keys = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.repeat ||
        event.isComposing ||
        (modal !== null && modal !== "make") ||
        making
      )
        return;
      const page = PAGES[pageDigit(event)];
      if (!page) return;
      if (event.target instanceof HTMLElement && event.target.closest(".xterm"))
        return;
      event.preventDefault();
      showPage(page);
    };
    window.addEventListener("keydown", keys);
    return () => window.removeEventListener("keydown", keys);
  }, [making, modal, showPage]);

  // The active wishes are always read: the flying carpet and the side panel count what waits in each.
  const active = wishes.filter(isActive);
  const details = useWishDetails(active.map((w) => w.id));
  const gateWaiting = useData((s) => gateWaiters(s.gates));
  const counts = Object.fromEntries(
    active.map((w) => [
      w.id,
      {
        questions: details[w.id]?.questions.filter(waitsForYou).length ?? 0,
        ...runningOf(details[w.id]?.tasks ?? []),
        waits: waitsOf(details[w.id]?.tasks ?? [], gateWaiting),
      },
    ]),
  );
  // What shows: the wish chosen, or the Hourglass, or the Grimoire, else the flying carpet while a wish is active, else
  // the first wish.
  const chosen = wishes.find((w) => w.id === selected);
  const pendingWish = justMade?.id === selected ? justMade : undefined;
  const hourglass = !chosen && !pendingWish && selected === HOURGLASS;
  const grimoire = !chosen && !pendingWish && selected === GRIMOIRE;
  const plan =
    !hourglass &&
    !grimoire &&
    !chosen &&
    !pendingWish &&
    (selected === PLAN || active.length > 0);
  const wish =
    plan || hourglass || grimoire
      ? undefined
      : (chosen ?? pendingWish ?? wishes[0]);
  // Until the wishes are read, no page is chosen: the home page would show, then give way to the flying carpet or a wish
  // with a header of its own, and a gauge focused or measured in between would be gone. A failed read shows home.
  const settling = !loaded && !error;
  const shownId = wish?.id ?? "";
  const newWishShortcut = platformShortcut("N");
  const sidebarShortcut = platformShortcut("\\");
  // djinn sends no notification for the wish shown while the window is in front; the theme shown, the system's
  // resolved, sets the Djinn's tone.
  const theme = useShownTheme();
  useEffect(() => {
    void clients.ui
      .view({
        wishId: shownId,
        theme: theme === "light" ? ShownTheme.LIGHT : ShownTheme.DARK,
      })
      .catch(() => undefined);
  }, [clients, shownId, theme]);
  // A key, a click or a scroll in the window in front says the developer is there: it keeps their ifrits lit.
  useEffect(
    () =>
      followPresence(
        () => clients.ui.touch({}),
        window,
        () => document.hasFocus(),
      ),
    [clients],
  );
  const project = projects.find((p) => p.id === projectId);

  const move = (wishId: string, to: number) =>
    void clients.wishes
      .move({ wishId, to })
      .catch((err) => setToast(message(err)))
      .finally(() => void data.changed(wishId, [Change.WISH]));
  const reorder = (wishId: string, to: number) =>
    void clients.wishes
      .reorder({ wishId, to })
      .catch((err) => setToast(message(err)))
      .finally(() => void data.changed(wishId, [Change.WISH]));

  const importFile = async (file: File) => {
    try {
      const res = await importWish(clients, file);
      await data.changed(res.wishId, [Change.WISH, Change.PROJECT]);
      setSelected(res.wishId);
      setToast(
        res.already ? t("import.already") : res.note || t("import.done"),
      );
    } catch (err) {
      setToast(message(err));
    }
  };

  // A push says how it went on its sync line, never in a toast: the line reads its answer, or its error.
  const pushProject = useCallback(
    (id: string, sync: ProjectSync) =>
      clients.projects
        .push({ project: id, wish: sync.wishId, branch: sync.branch })
        .finally(() => void data.changed("", [Change.PROJECT])),
    [clients, data],
  );

  const pullProject = useCallback(
    (id: string, sync: ProjectSync) =>
      clients.projects
        .pull({ project: id, branch: sync.branch })
        .finally(() => void data.changed("", [Change.PROJECT])),
    [clients, data],
  );

  return (
    <div
      className={`app wish-app ${collapsed ? "sidebar-collapsed" : ""} ${glide ? "sidebar-glide" : ""}`}
    >
      <div
        className="app-toolbar"
        onKeyDown={(event) => {
          if (
            event.key !== "Escape" ||
            event.defaultPrevented ||
            modal !== "make" ||
            making
          )
            return;
          event.preventDefault();
          makeBackAction.current?.();
        }}
      >
        <TooltipButton
          type="button"
          className="sidebar-toggle app-toolbar-toggle"
          onClick={toggleSidebar}
          disabled={making}
          label={collapsed ? t("app.expand_nav") : t("app.collapse_nav")}
          tooltip={collapsed ? t("app.expand_nav") : t("app.collapse_nav")}
          shortcut={sidebarShortcut.label}
          aria-keyshortcuts={sidebarShortcut.aria}
          aria-expanded={!collapsed}
          aria-controls="djinn-sidebar"
        >
          <PanelLeft size={16} aria-hidden="true" />
        </TooltipButton>
        {/* Back shows on the page that needs it, after the toggle, which never moves. */}
        {modal === "make" && (
          <TooltipButton
            type="button"
            className="wish-creation-back app-toolbar-back"
            label={makeBackInSetup ? t("agents.back") : t("make.back")}
            tooltip={makeBackInSetup ? t("agents.back") : t("make.back")}
            shortcut={making ? undefined : t("app.shortcut_escape")}
            aria-keyshortcuts={making ? undefined : "Escape"}
            disabled={making}
            onClick={() => makeBackAction.current?.()}
            data-setup-back={makeBackInSetup || undefined}
          >
            <ArrowLeft size={16} aria-hidden="true" />
          </TooltipButton>
        )}
      </div>
      <aside
        id="djinn-sidebar"
        className="sidebar"
        inert={modal === "make" && making}
      >
        <div className="sidebar-brand">
          <div className="sidebar-brand-main">
            <Brand small={collapsed} eye />
            {!collapsed && <span className="version-tag">EARLY ACCESS</span>}
          </div>
        </div>
        {/* Folded, the sidebar is a rail of icons that keeps each wish's counts (Q03). */}
        <div className="sidebar-content">
          {/* The creation page's smoke warms up when the person reaches for New wish, never at start (report §3). */}
          <TooltipButton
            className="new-mission"
            onClick={() => setModal("make")}
            onPointerEnter={startWishSmokePrewarm}
            onFocus={startWishSmokePrewarm}
            label={t("app.new_wish")}
            tooltip={t("app.new_wish")}
            shortcut={newWishShortcut.label}
            aria-keyshortcuts={newWishShortcut.aria}
          >
            <Plus size={16} />
            {!collapsed && (
              <>
                <span>{t("app.new_wish")}</span>
                <kbd>{newWishShortcut.label}</kbd>
              </>
            )}
          </TooltipButton>
          <WishSidebar
            wishes={wishes}
            projects={projects}
            counts={counts}
            inbox={inbox}
            planSelected={plan}
            onSelectPlan={() => showPage(PLAN)}
            hourglassSelected={hourglass}
            onSelectHourglass={() => showPage(HOURGLASS)}
            grimoireSelected={grimoire}
            onSelectGrimoire={() => showPage(GRIMOIRE)}
            onOpenSummoningCircle={() => setModal("summoning")}
            selectedWishId={wish?.id ?? ""}
            selectedProjectId={projectId}
            collapsed={collapsed}
            onSelectWish={(id) => {
              setSelected(id);
              setModal(null);
              setOpening(undefined);
              setReaching(undefined);
              setLanding({ wishId: id });
            }}
            onSelectProject={setProjectId}
            onMove={move}
            onReorder={reorder}
            onNewProject={() => setModal("project")}
            onPush={pushProject}
            onPull={pullProject}
            onImportWish={() => importRef.current?.click()}
            importDisabled={!loaded}
          />
          <div className="sidebar-bottom">
            <button
              className="profile"
              onClick={() => setModal("settings")}
              title={t("app.connections_preferences")}
            >
              <span className="profile-avatar">C</span>
              {!collapsed && (
                <>
                  <span>
                    {t("app.personal_space")}
                    <small>{t("app.local_app")}</small>
                  </span>
                  <Settings2 size={15} />
                </>
              )}
            </button>
          </div>
        </div>
      </aside>
      <main className="main-shell">
        {/* A tilasm a question names opens over the view shown, which stays as it is under it. */}
        <TilasmOver
          view={modal === "make" ? "make" : selected}
          landing={landing}
          onToast={setToast}
        >
          <LayoutGroup id="workspace-action-group">
            {modal === "make" ? (
              <MakeWish
                projects={projects}
                active={active.length}
                onBusyChange={setMaking}
                backActionRef={makeBackAction}
                onBackSetupChange={setMakeBackInSetup}
                onClose={closeMake}
                onMade={(made, warning) => {
                  setModal(null);
                  setSelected(made.id);
                  setJustMade(made);
                  void data.changed(made.id, [Change.WISH]);
                  if (warning) setToast(warning);
                }}
              />
            ) : settling ? (
              <div className="wish-view" aria-busy="true" />
            ) : hourglass ? (
              <Hourglass
                wishes={active}
                target={reaching?.wishId === HOURGLASS ? reaching : undefined}
              />
            ) : grimoire ? (
              <Docs />
            ) : plan ? (
              <FlightPlan
                wishes={active}
                onOpen={setSelected}
                onOpenProject={setProjectId}
                onToast={setToast}
                onSetupAgents={() => setModal("agents")}
              />
            ) : wish ? (
              <WishView
                key={wish.id}
                wish={wish}
                opening={opening?.wishId === wish.id ? opening : undefined}
                reaching={reaching?.wishId === wish.id ? reaching : undefined}
                landing={landing?.wishId === wish.id ? landing : undefined}
                onToast={setToast}
                onSetupAgents={() => setModal("agents")}
              />
            ) : (
              <HomePlaceholder projects={projects.length} />
            )}
            <StatusBar live={live} error={error} />
          </LayoutGroup>
        </TilasmOver>
      </main>
      <AnimatePresence>
        {modal === "project" && (
          <AddProject
            onClose={() => setModal(null)}
            onAdded={(added) => {
              setModal(null);
              void data.changed("", [Change.PROJECT]);
              setToast(t("project.added", { name: added.name }));
            }}
          />
        )}
        {modal === "settings" && <Settings onClose={() => setModal(null)} />}
        {modal === "agents" && <AgentSetup onClose={() => setModal(null)} />}
        {modal === "summoning" && (
          <SummoningCircle projects={projects} onClose={() => setModal(null)} />
        )}
        {project && modal !== "make" && (
          <ProjectPanel
            project={project}
            onClose={() => setProjectId("")}
            onToast={setToast}
          />
        )}
      </AnimatePresence>
      <Toast text={toast} onClose={closeToast} />
      <DjinnEye live={live} />
      <input
        type="file"
        accept=".json,.djinn"
        hidden
        ref={importRef}
        aria-hidden="true"
        onChange={(e) => {
          const file = e.target.files?.[0];
          if (file) void importFile(file);
          e.target.value = "";
        }}
      />
    </div>
  );
}

// HomePlaceholder is the window with no wish at all: the lamp waits, for a first project, or for a first wish.
function HomePlaceholder({ projects }: { projects: number }) {
  return (
    <div className="wish-view">
      <PageHeader middle={<OperatingLoad />} />
      <div className="wish-home-placeholder">
        <h1>{t("home.title")}</h1>
        <EmptyState art="home">
          {projects ? t("home.no_wish") : t("home.no_project")}
        </EmptyState>
      </div>
    </div>
  );
}

// StatusBar says whether the page follows djinn, how the machine is doing, quietly whether the network is down (W285),
// and the gates held (HeldGates).
function StatusBar({ live, error }: { live: boolean; error: string }) {
  const machine = useMachine();
  const gates = useData((s) => s.gates);
  const down = date(machine?.networkDownSince);
  return (
    <footer className="app-statusbar">
      <span title={error || undefined}>
        <span className={`status-dot ${live ? "green" : "neutral"}`} />
        {live ? t("status.live") : t("status.reconnecting")}
      </span>
      <span title={down ? t("status.network_down_hint") : undefined}>
        {machine &&
          t("status.machine", {
            running: machine.running,
            workers: machine.workers,
          })}
        {machine?.pressure && ` · ${machine.pressure}`}
        {down &&
          ` · ${t("status.network_down", {
            time: down.toLocaleTimeString(language, {
              hour: "2-digit",
              minute: "2-digit",
            }),
          })}`}
      </span>
      <span>
        <HeldGates gates={gates} />
      </span>
    </footer>
  );
}
