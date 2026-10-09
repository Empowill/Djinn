// The window, on the services: the side panel (the flight plan, the wishes by rank, the projects), the flight plan of
// the active wishes or the wish shown, and the status bar (whether the page follows djinn, the machine). What stays on
// this page is how it shows: what is shown and the side panel folded.
import { Plus, Settings2, Upload } from "lucide-react";
import { AnimatePresence } from "motion/react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Change, TaskStatus } from "../gen/ts/plan/v1/plan_pb";
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
import { isActive, waitsForYou } from "./data/format";
import { FlightPlan } from "./flight-plan";
import { Brand, Toast } from "./frame";
import { t } from "./i18n";
import { useShowWish } from "./lead-terminal";
import { AddProject, MakeWish, ProjectPanel, Settings } from "./wish-dialogs";
import { Docs } from "./docs";
import { WishSidebar } from "./wish-sidebar";
import { type Opening, WishView } from "./wish-view";
import "./wish.css";
import "./review.css";

type Modal = "make" | "project" | "settings" | "docs" | null;

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
  const [collapsed, setCollapsed] = useState(
    () => stored("djinn.sidebar.collapsed") === "1",
  );
  const [modal, setModal] = useState<Modal>(null);
  const [projectId, setProjectId] = useState("");
  const [toast, setToast] = useState("");
  // The tilasm a djinn:// link asks to show, in its wish; a new object at each request.
  const [opening, setOpening] = useState<Opening>();
  const importRef = useRef<HTMLInputElement>(null);
  const closeToast = useCallback(() => setToast(""), []);

  useEffect(() => store("djinn.wish", selected), [selected]);
  useEffect(
    () => store("djinn.sidebar.collapsed", collapsed ? "1" : ""),
    [collapsed],
  );
  // djinn wish resume asks the window to show a wish; a djinn:// link, a wish or a tilasm in its wish, or says Djinn
  // does not know it.
  useEffect(
    () =>
      djinn.focus.subscribe((focus) => {
        if (focus.wishId) setSelected(focus.wishId);
        if (focus.tilasmId)
          setOpening({ wishId: focus.wishId, tilasmId: focus.tilasmId });
        if (focus.unknownLink)
          setToast(t("link.unknown", { link: focus.unknownLink }));
      }),
    [djinn],
  );
  // Another wish shown: a tilasm asked for earlier is not opened again on coming back.
  useEffect(
    () => setOpening((o) => (o && o.wishId !== selected ? undefined : o)),
    [selected],
  );
  // Ctrl+N (Cmd+N) makes a wish.
  useEffect(() => {
    const keys = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "n") {
        event.preventDefault();
        setModal("make");
      }
    };
    window.addEventListener("keydown", keys);
    return () => window.removeEventListener("keydown", keys);
  }, []);

  // The active wishes are always read: the flight plan and the side panel count what waits in each.
  const active = wishes.filter(isActive);
  const details = useWishDetails(active.map((w) => w.id));
  const counts = Object.fromEntries(
    active.map((w) => [
      w.id,
      {
        questions: details[w.id]?.questions.filter(waitsForYou).length ?? 0,
        running:
          details[w.id]?.tasks.filter((x) => x.status === TaskStatus.RUNNING)
            .length ?? 0,
      },
    ]),
  );
  // What shows: the wish chosen, else the flight plan while a wish is active, else the first wish.
  const chosen = wishes.find((w) => w.id === selected);
  const plan = !chosen && (selected === PLAN || active.length > 0);
  const wish = plan ? undefined : (chosen ?? wishes[0]);
  const project = projects.find((p) => p.id === projectId);
  // The terminal below shows the lead of the wish shown while it runs.
  const showWish = useShowWish();
  const shown = wish?.id ?? "";
  useEffect(() => showWish(shown), [showWish, shown]);

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

  return (
    <div className={`app wish-app ${collapsed ? "sidebar-collapsed" : ""}`}>
      <aside className="sidebar">
        <div className="sidebar-brand">
          <button
            onClick={() => setCollapsed(!collapsed)}
            title={collapsed ? t("app.expand_nav") : t("app.collapse_nav")}
          >
            <Brand small={collapsed} />
          </button>
          {!collapsed && <span className="version-tag">EARLY ACCESS</span>}
        </div>
        <button
          className="new-mission"
          onClick={() => setModal("make")}
          title={t("app.new_wish")}
        >
          <Plus size={16} />
          {!collapsed && (
            <>
              <span>{t("app.new_wish")}</span>
              <kbd>⌘ N</kbd>
            </>
          )}
        </button>
        <WishSidebar
          wishes={wishes}
          projects={projects}
          counts={counts}
          inbox={inbox}
          planSelected={plan}
          onSelectPlan={() => setSelected(PLAN)}
          selectedWishId={wish?.id ?? ""}
          selectedProjectId={projectId}
          collapsed={collapsed}
          onSelectWish={setSelected}
          onSelectProject={setProjectId}
          onMove={move}
          onNewProject={() => setModal("project")}
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
      </aside>
      <main className="main-shell">
        {plan ? (
          <FlightPlan wishes={active} onOpen={setSelected} onToast={setToast} />
        ) : wish ? (
          <WishView
            key={wish.id}
            wish={wish}
            opening={opening?.wishId === wish.id ? opening : undefined}
            onToast={setToast}
          />
        ) : (
          <Welcome
            loaded={loaded}
            onMake={() => setModal("make")}
            onImport={() => importRef.current?.click()}
          />
        )}
        <StatusBar live={live} error={error} />
      </main>
      <AnimatePresence>
        {modal === "make" && (
          <MakeWish
            projects={projects}
            active={wishes.filter(isActive).length}
            onClose={() => setModal(null)}
            onMade={(id) => {
              setModal(null);
              setSelected(id);
              void data.changed(id, [Change.WISH]);
            }}
          />
        )}
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
        {modal === "docs" && <Docs onClose={() => setModal(null)} />}
        {project && (
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

// Welcome is the window without a wish: make one, or import one.
function Welcome({
  loaded,
  onMake,
  onImport,
}: {
  loaded: boolean;
  onMake: () => void;
  onImport: () => void;
}) {
  return (
    <div className="mission-scroll wish-welcome">
      <div className="hero">
        <div className="hero-copy">
          <h1>{t("welcome.title")}</h1>
          <p>{loaded ? t("welcome.detail") : t("common.loading")}</p>
          <div className="hero-meta">
            <button className="button accent" onClick={onMake}>
              <Plus size={14} />
              {t("app.new_wish")}
            </button>
            <button
              className="button secondary"
              onClick={onImport}
              disabled={!loaded}
            >
              <Upload size={14} />
              {t("app.import_wish")}
            </button>
          </div>
          <p className="form-tip">
            {t("welcome.cli", { command: 'djinn wish make "…"' })}
          </p>
        </div>
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
