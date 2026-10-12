// The window, on the services: the side panel (the flight plan, the wishes by rank, the projects), the flight plan of
// the active wishes or the wish shown, and the status bar (whether the page follows djinn, the machine). What stays on
// this page is how it shows: what is shown and the side panel folded.
import { ArrowLeft, PanelLeft, Plus, Settings2, Upload } from "lucide-react";
import { AnimatePresence, LayoutGroup } from "motion/react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Change, type Wish } from "../gen/ts/plan/v1/plan_pb";
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
import { isActive, runningOf, waitsForYou } from "./data/format";
import { AgentSetup, needsSetup } from "./agent-setup";
import { FlightPlan } from "./flight-plan";
import { Brand, Toast } from "./frame";
import { t } from "./i18n";
import { platformShortcut, TooltipButton } from "./tooltip";
import { AddProject, MakeWish, ProjectPanel, Settings } from "./wish-dialogs";
import { Docs } from "./docs";
import { WishSidebar } from "./wish-sidebar";
import { startWishSmokePrewarm } from "./wish-smoke";
import { type Opening, type Reaching, WishView } from "./wish-view";
import { OperatingLoad } from "./operating-load";
import { PageHeader } from "./page-header";
import "./wish.css";
import "./review.css";

type Modal = "make" | "project" | "settings" | "docs" | "agents" | null;

// What the window shows instead of a wish: the flight plan of the active wishes. A wish's id is a UUID: never this.
export const PLAN = "plan";

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
  const inbox = useData((s) => s.inbox.length);
  const [selected, setSelected] = useState(() => stored("djinn.wish"));
  // Keep a just-created wish renderable for the same commit as MakeWish leaves, so the shared action surface can
  // animate into the wish view while the next store list is still in flight.
  const [justMade, setJustMade] = useState<Wish>();
  const [collapsed, setCollapsed] = useState(
    () => stored("djinn.sidebar.collapsed") === "1",
  );
  // The sidebar glides only once folded or unfolded by hand, never as the page opens.
  const [glide, setGlide] = useState(false);
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
  const importRef = useRef<HTMLInputElement>(null);
  const closeToast = useCallback(() => setToast(""), []);
  const closeMake = useCallback(() => setModal(null), []);
  const closeDocs = useCallback(() => setModal(null), []);
  const toggleSidebar = useCallback(() => {
    setGlide(true);
    setCollapsed((value) => !value);
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
        if (focus.tilasmId)
          setOpening({ wishId: focus.wishId, tilasmId: focus.tilasmId });
        if (focus.target)
          setReaching({ wishId: focus.wishId, target: focus.target });
        if (focus.unknownLink)
          setToast(t("link.unknown", { link: focus.unknownLink }));
      }),
    [djinn],
  );
  // Another wish shown: a tilasm asked for earlier is not opened again on coming back.
  useEffect(() => {
    setOpening((o) => (o && o.wishId !== selected ? undefined : o));
    setReaching((r) => (r && r.wishId !== selected ? undefined : r));
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

  // The active wishes are always read: the flight plan and the side panel count what waits in each.
  const active = wishes.filter(isActive);
  const details = useWishDetails(active.map((w) => w.id));
  const counts = Object.fromEntries(
    active.map((w) => [
      w.id,
      {
        questions: details[w.id]?.questions.filter(waitsForYou).length ?? 0,
        ...runningOf(details[w.id]?.tasks ?? []),
      },
    ]),
  );
  // What shows: the wish chosen, else the flight plan while a wish is active, else the first wish.
  const chosen = wishes.find((w) => w.id === selected);
  const pendingWish = justMade?.id === selected ? justMade : undefined;
  const plan =
    !chosen && !pendingWish && (selected === PLAN || active.length > 0);
  const wish = plan ? undefined : (chosen ?? pendingWish ?? wishes[0]);
  const shownId = wish?.id ?? "";
  const newWishShortcut = platformShortcut("N");
  const sidebarShortcut = platformShortcut("\\");
  // djinn sends no notification for the wish shown while the window is in front.
  useEffect(() => {
    void clients.ui.view({ wishId: shownId }).catch(() => undefined);
  }, [clients, shownId]);
  const project = projects.find((p) => p.id === projectId);

  const move = (wishId: string, to: number) =>
    void clients.wishes
      .move({ wishId, to })
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

  const pushProject = useCallback(
    (id: string) => {
      clients.projects
        .push({ project: id })
        .then(() => {
          void data.changed("", [Change.PROJECT]);
        })
        .catch((err) => setToast(message(err)));
    },
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
            <Brand small={collapsed} />
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
            onSelectPlan={() => {
              setSelected(PLAN);
              setModal(null);
            }}
            selectedWishId={wish?.id ?? ""}
            selectedProjectId={projectId}
            collapsed={collapsed}
            onSelectWish={(id) => {
              setSelected(id);
              setModal(null);
            }}
            onSelectProject={setProjectId}
            onMove={move}
            onNewProject={() => setModal("project")}
            onPush={pushProject}
          />
          <button
            className="nav-item import-nav"
            onClick={() => importRef.current?.click()}
            title={t("app.import_wish")}
            disabled={!loaded}
          >
            <Upload size={15} />
            {!collapsed && <span>{t("app.import_wish")}</span>}
          </button>
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
          ) : plan ? (
            <FlightPlan
              wishes={active}
              onOpen={setSelected}
              onToast={setToast}
            />
          ) : wish ? (
            <WishView
              key={wish.id}
              wish={wish}
              opening={opening?.wishId === wish.id ? opening : undefined}
              reaching={reaching?.wishId === wish.id ? reaching : undefined}
              onToast={setToast}
              onSetupAgents={() => setModal("agents")}
            />
          ) : (
            <HomePlaceholder />
          )}
          <StatusBar live={live} error={error} />
        </LayoutGroup>
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
        {modal === "settings" && (
          <Settings
            onClose={() => setModal(null)}
            onDocs={() => setModal("docs")}
          />
        )}
        {modal === "docs" && <Docs onClose={closeDocs} />}
        {modal === "agents" && <AgentSetup onClose={() => setModal(null)} />}
        {project && modal !== "make" && (
          <ProjectPanel
            project={project}
            onClose={() => setProjectId("")}
            onToast={setToast}
          />
        )}
      </AnimatePresence>
      <Toast text={toast} onClose={closeToast} />
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

function HomePlaceholder() {
  return (
    <div className="wish-view">
      <PageHeader middle={<OperatingLoad />} />
      <div className="wish-home-placeholder">
        <h1>{t("home.title")}</h1>
      </div>
    </div>
  );
}

// StatusBar says whether the page follows djinn, and how the machine is doing.
function StatusBar({ live, error }: { live: boolean; error: string }) {
  const machine = useMachine();
  const gates = useData((s) => s.gates);
  const held = gates.filter((g) => g.holder).length;
  return (
    <footer className="app-statusbar">
      <span title={error || undefined}>
        <span className={`status-dot ${live ? "green" : "neutral"}`} />
        {live ? t("status.live") : t("status.reconnecting")}
      </span>
      <span>
        {machine &&
          t("status.machine", {
            running: machine.running,
            workers: machine.workers,
          })}
        {machine?.pressure && ` · ${machine.pressure}`}
      </span>
      <span>{held > 0 && t("status.gates", { count: held })}</span>
    </footer>
  );
}
