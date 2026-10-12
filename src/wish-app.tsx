// The window, on the services: the side panel (the flight plan, the wishes by rank, the projects), the flight plan of
// the active wishes or the wish shown, and the status bar (whether the page follows djinn, the machine). What stays on
// this page is how it shows: what is shown and the side panel folded.
import { ArrowLeft, PanelLeft, Plus, Settings2, Upload } from "lucide-react";
import { AnimatePresence, LayoutGroup } from "motion/react";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import { Change, type Wish } from "../gen/ts/plan/v1/plan_pb";
import { ProviderState } from "../gen/ts/ui/v1/ui_pb";
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
import { AgentSetup } from "./agent-setup";
import { jump } from "./attention";
import { FlightPlan } from "./flight-plan";
import { Brand, Toast } from "./frame";
import { t } from "./i18n";
import { useTerminalFollows } from "./lead-terminal";
import { platformShortcut, TooltipButton } from "./tooltip";
import { AddProject, MakeWish, ProjectPanel, Settings } from "./wish-dialogs";
import { Docs } from "./docs";
import { WishSidebar } from "./wish-sidebar";
import { type Opening, WishView } from "./wish-view";
import "./wish.css";
import "./review.css";
import "./wish-transition.css";

type Modal = "make" | "project" | "settings" | "agents" | "docs" | null;
type WishCreationTransition = {
  wish: Wish;
  phase: "departing" | "opening" | "dissipating";
};

const WISH_REVEAL_RESET_MS = 900;

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
  const [selected, setSelected] = useState(() => stored("djinn.wish"));
  const selectedRef = useRef(selected);
  selectedRef.current = selected;
  // Keep a just-created wish renderable while the creation smoke expands and the next store list is in flight.
  const [justMade, setJustMade] = useState<Wish>();
  const [creationTransition, setCreationTransition] =
    useState<WishCreationTransition>();
  const [revealingWishId, setRevealingWishId] = useState("");
  const creationRun = useRef(0);
  const creationTransitionRef = useRef<WishCreationTransition | undefined>(
    undefined,
  );
  const pendingMadeWishId = useRef("");
  const focusFrame = useRef<number | undefined>(undefined);
  const focusWishId = useRef("");
  const [collapsed, setCollapsed] = useState(
    () => stored("djinn.sidebar.collapsed") === "1",
  );
  const [modal, setModal] = useState<Modal>(null);
  const modalRef = useRef<Modal>(modal);
  modalRef.current = modal;
  const [making, setMaking] = useState(false);
  const [projectId, setProjectId] = useState("");
  const [toast, setToast] = useState("");
  const [opening, setOpening] = useState<Opening>();
  const makeBackAction = useRef<(() => void) | null>(null);
  const [makeBackInSetup, setMakeBackInSetup] = useState(false);
  const importRef = useRef<HTMLInputElement>(null);
  const creationBusy = making || creationTransition !== undefined;
  const closeToast = useCallback(() => setToast(""), []);
  const closeMake = useCallback(() => {
    creationRun.current += 1;
    if (focusFrame.current !== undefined) {
      cancelAnimationFrame(focusFrame.current);
      focusFrame.current = undefined;
    }
    creationTransitionRef.current = undefined;
    pendingMadeWishId.current = "";
    focusWishId.current = "";
    setCreationTransition(undefined);
    setMaking(false);
    setModal(null);
  }, []);
  const openMake = useCallback(() => {
    if (creationBusy) return;
    closeMake();
    setModal("make");
  }, [closeMake, creationBusy]);
  const toggleSidebar = useCallback(() => setCollapsed((value) => !value), []);
  useEffect(
    () => () => {
      if (focusFrame.current !== undefined)
        cancelAnimationFrame(focusFrame.current);
      creationRun.current += 1;
      creationTransitionRef.current = undefined;
    },
    [],
  );

  useEffect(() => store("djinn.wish", selected), [selected]);
  useEffect(() => {
    if (justMade && wishes.some((item) => item.id === justMade.id))
      setJustMade(undefined);
  }, [justMade, wishes]);
  useEffect(() => {
    if (!revealingWishId) return;
    const timer = window.setTimeout(
      () =>
        setRevealingWishId((wishId) =>
          wishId === revealingWishId ? "" : wishId,
        ),
      WISH_REVEAL_RESET_MS,
    );
    return () => window.clearTimeout(timer);
  }, [revealingWishId]);
  useEffect(
    () => store("djinn.sidebar.collapsed", collapsed ? "1" : ""),
    [collapsed],
  );
  useLayoutEffect(() => {
    const wishId = focusWishId.current;
    if (!wishId || creationTransition) return;
    const run = creationRun.current;
    focusWishId.current = "";
    if (focusFrame.current !== undefined)
      cancelAnimationFrame(focusFrame.current);
    focusFrame.current = requestAnimationFrame(() => {
      focusFrame.current = undefined;
      if (creationRun.current !== run || creationTransitionRef.current) return;
      const entry = document.querySelector<HTMLElement>(
        `[data-wish-entry="wish"][data-wish-id="${wishId}"]`,
      );
      entry?.focus({ preventScroll: true });
    });
  }, [creationTransition]);
  // djinn wish resume, or a click on a notification, asks the window to show a wish, there.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const off = djinn.focus.subscribe((focus) => {
      if (focus.replay && modalRef.current === "make") {
        // A reconnect can replay an older wish while creation is opening; keep the new wish underlay selected.
        return;
      }
      const ownWishId =
        creationTransitionRef.current?.wish.id || pendingMadeWishId.current;
      if (
        focus.wishId &&
        !focus.replay &&
        (modalRef.current === "make" || pendingMadeWishId.current) &&
        focus.wishId !== ownWishId &&
        focus.wishId !== selectedRef.current
      ) {
        // A focus notification is external navigation. Invalidate a response or lead launch that is still in
        // flight so it cannot reopen the creation flow for a wish the user has left behind.
        closeMake();
      }
      if (focus.wishId) setSelected(focus.wishId);
      if (focus.tilasmId)
        setOpening({ wishId: focus.wishId, tilasmId: focus.tilasmId });
      if (focus.unknownLink)
        setToast(t("link.unknown", { link: focus.unknownLink }));
      clearTimeout(timer);
      if (!focus.target) return;
      // The wish renders, then its details load: look for the element a few seconds.
      const until = Date.now() + 5000;
      const look = () => {
        if (document.getElementById(focus.target)) jump(focus.target);
        else if (Date.now() < until) timer = setTimeout(look, 100);
      };
      timer = setTimeout(look, 0);
    });
    return () => {
      off();
      clearTimeout(timer);
    };
  }, [closeMake, djinn]);
  useEffect(
    () => setOpening((o) => (o && o.wishId !== selected ? undefined : o)),
    [selected],
  );
  // At the first start, with no agent ready, the setup of the agents opens by itself; once checked, it waits in the
  // settings.
  useEffect(() => {
    if (stored("djinn.agents.offered")) return;
    let live = true;
    clients.ui
      .getEnvironment({ agents: true })
      .then((res) => {
        if (!live) return;
        store("djinn.agents.offered", "1");
        if (!res.providers.some((p) => p.state === ProviderState.READY))
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
        openMake();
      }
    };
    window.addEventListener("keydown", keys);
    return () => window.removeEventListener("keydown", keys);
  }, [openMake]);
  // Ctrl+\ (Cmd+\) folds the navigation without taking shortcuts from the terminal.
  useEffect(() => {
    const keys = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.repeat ||
        event.isComposing ||
        (modal !== null && modal !== "make") ||
        creationBusy ||
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
  }, [creationBusy, modal, toggleSidebar]);

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
  useTerminalFollows(wish);
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

  // Making an active wish only records it. Start its lead once, here, so the UI owns the decision to launch and a
  // failed start remains visible on the wish through WishService's transient leadError field.
  const launchMadeLead = useCallback(
    async (made: Wish, run: number) => {
      if (!isActive(made)) {
        if (pendingMadeWishId.current === made.id)
          pendingMadeWishId.current = "";
        return;
      }
      try {
        const res = await clients.wishes.resume({
          wishId: made.id,
          provider: made.provider,
        });
        if (creationRun.current !== run) return;
        await data.changed(made.id, [Change.WISH]);
        if (creationRun.current !== run) return;
        await clients.ui.show({ wishId: made.id, terminal: res.terminal });
        if (
          creationRun.current === run &&
          pendingMadeWishId.current === made.id
        )
          pendingMadeWishId.current = "";
      } catch (err) {
        if (creationRun.current !== run) return;
        setToast(message(err));
        await data.changed(made.id, [Change.WISH]).catch(() => undefined);
        if (pendingMadeWishId.current === made.id)
          pendingMadeWishId.current = "";
      }
    },
    [clients, data],
  );
  const completeCreation = useCallback(() => {
    const transition = creationTransitionRef.current;
    if (!transition || transition.phase !== "dissipating") return;
    const wishId = transition.wish.id;
    creationTransitionRef.current = undefined;
    focusWishId.current = wishId;
    setRevealingWishId(wishId);
    setCreationTransition(undefined);
    setMaking(false);
    setModal(null);
  }, []);
  const finishCreation = useCallback(() => {
    const transition = creationTransitionRef.current;
    if (!transition || transition.phase !== "opening") return;
    const dissipating = { ...transition, phase: "dissipating" as const };
    creationTransitionRef.current = dissipating;
    setCreationTransition(dissipating);
  }, []);
  const finishDeparture = useCallback(() => {
    const transition = creationTransitionRef.current;
    if (!transition || transition.phase !== "departing") return;
    const opening = { ...transition, phase: "opening" as const };
    creationTransitionRef.current = opening;
    setCreationTransition(opening);
  }, []);
  const madeWish = useCallback(
    async (made: Wish) => {
      const run = creationRun.current;
      setSelected(made.id);
      setJustMade(made);
      pendingMadeWishId.current = isActive(made) ? made.id : "";
      // The response carries a complete wish, so the pending copy is renderable immediately. Refresh in the
      // background; a slow or failed list refresh must not strand the smoke overlay.
      if (run !== creationRun.current) return;
      const transition = { wish: made, phase: "departing" as const };
      creationTransitionRef.current = transition;
      setCreationTransition(transition);
      void data.changed(made.id, [Change.WISH]).catch(() => undefined);
      void launchMadeLead(made, run);
    },
    [data, launchMadeLead],
  );

  return (
    <div className={`app wish-app ${collapsed ? "sidebar-collapsed" : ""}`}>
      <div
        className="app-toolbar"
        onKeyDown={(event) => {
          if (
            event.key !== "Escape" ||
            event.defaultPrevented ||
            modal !== "make" ||
            creationBusy
          )
            return;
          event.preventDefault();
          makeBackAction.current?.();
        }}
      >
        <TooltipButton
          type="button"
          className="wish-creation-back app-toolbar-back"
          label={makeBackInSetup ? t("agents.back") : t("make.back")}
          tooltip={makeBackInSetup ? t("agents.back") : t("make.back")}
          shortcut={
            modal === "make" && !creationBusy
              ? t("app.shortcut_escape")
              : undefined
          }
          aria-keyshortcuts={
            modal === "make" && !creationBusy ? "Escape" : undefined
          }
          disabled={modal !== "make" || creationBusy}
          onClick={() => makeBackAction.current?.()}
          data-setup-back={makeBackInSetup || undefined}
        >
          <ArrowLeft size={16} aria-hidden="true" />
        </TooltipButton>
        <TooltipButton
          type="button"
          className="sidebar-toggle app-toolbar-toggle"
          onClick={toggleSidebar}
          disabled={creationBusy}
          label={collapsed ? t("app.expand_nav") : t("app.collapse_nav")}
          tooltip={collapsed ? t("app.expand_nav") : t("app.collapse_nav")}
          shortcut={sidebarShortcut.label}
          aria-keyshortcuts={sidebarShortcut.aria}
          aria-expanded={!collapsed}
          aria-controls="djinn-sidebar"
        >
          <PanelLeft size={16} aria-hidden="true" />
        </TooltipButton>
      </div>
      <aside
        id="djinn-sidebar"
        className="sidebar"
        inert={modal === "make" && creationBusy}
      >
        <div className="sidebar-brand">
          <div className="sidebar-brand-main">
            <Brand />
            <span className="version-tag">EARLY ACCESS</span>
          </div>
        </div>
        <div
          className="sidebar-content"
          aria-hidden={collapsed ? "true" : undefined}
          inert={collapsed}
        >
          <TooltipButton
            className="new-mission"
            onClick={openMake}
            label={t("app.new_wish")}
            tooltip={t("app.new_wish")}
            shortcut={newWishShortcut.label}
            aria-keyshortcuts={newWishShortcut.aria}
          >
            <Plus size={16} />
            <span>{t("app.new_wish")}</span>
            <kbd>{newWishShortcut.label}</kbd>
          </TooltipButton>
          <WishSidebar
            wishes={wishes}
            projects={projects}
            counts={counts}
            planSelected={plan}
            onSelectPlan={() => {
              closeMake();
              setSelected(PLAN);
              setModal(null);
            }}
            selectedWishId={wish?.id ?? ""}
            selectedProjectId={projectId}
            collapsed={false}
            onSelectWish={(id) => {
              closeMake();
              setSelected(id);
              setModal(null);
            }}
            onSelectProject={(id) => {
              closeMake();
              setProjectId(id);
            }}
            onMove={move}
            onNewProject={() => {
              closeMake();
              setModal("project");
            }}
          />
          <button
            className="nav-item import-nav"
            onClick={() => {
              closeMake();
              importRef.current?.click();
            }}
            title={t("app.import_wish")}
            disabled={!loaded}
          >
            <Upload size={15} />
            <span>{t("app.import_wish")}</span>
          </button>
          <div className="sidebar-bottom">
            <button
              className="profile"
              onClick={() => {
                closeMake();
                setModal("settings");
              }}
              title={t("app.connections_preferences")}
            >
              <span className="profile-avatar">C</span>
              <span>
                {t("app.personal_space")}
                <small>{t("app.local_app")}</small>
              </span>
              <Settings2 size={15} />
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
              transitioning={creationTransition !== undefined}
              transitionPhase={creationTransition?.phase}
              onDepartureComplete={finishDeparture}
              onTransitionComplete={finishCreation}
              onDissipationComplete={completeCreation}
              onMade={madeWish}
            />
          ) : plan ? (
            <FlightPlan
              wishes={active}
              onOpen={setSelected}
              onToast={setToast}
            />
          ) : !wish ? (
            <HomePlaceholder />
          ) : null}
          {wish &&
          (modal !== "make" || creationTransition?.wish.id === wish.id) ? (
            <div
              key={wish.id}
              className="wish-entry"
              data-wish-entry="wish"
              data-wish-id={wish.id}
              tabIndex={-1}
              data-wish-transition={
                revealingWishId === wish.id
                  ? "revealing"
                  : modal === "make"
                    ? "preparing"
                    : "ready"
              }
              inert={modal === "make" && creationTransition !== undefined}
            >
              <WishView
                wish={wish}
                opening={opening?.wishId === wish.id ? opening : undefined}
                onToast={setToast}
              />
            </div>
          ) : null}
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
        {modal === "agents" && <AgentSetup onClose={() => setModal(null)} />}
        {modal === "docs" && <Docs onClose={() => setModal(null)} />}
        {project && modal !== "make" && (
          <ProjectPanel project={project} onClose={() => setProjectId("")} />
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
    <div className="wish-home-placeholder">
      <h1>{t("home.title")}</h1>
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
