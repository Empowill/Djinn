// The terminal pinned at the bottom of the window: real terminals (xterm.js) on pseudo-terminals of djinn up, each
// in a tab. It shows only when djinn serves the page (a DjinnProvider); elsewhere the app renders alone. None opens by
// itself: the lead of the wish shown comes as a tab while it runs, first; you open a terminal with + or Ctrl+Shift+T,
// or one at a project's root (its button in the side panel). Every tab closes with its ×, or when its program ends
// (Ctrl+D, exit); a lead's comes back from the Lead button. One shows at a time.
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { type ITheme, Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import {
  ChevronDown,
  ChevronUp,
  FolderPlus,
  Plus,
  RotateCcw,
  SquareTerminal,
  X,
} from "lucide-react";
import {
  type ReactNode,
  createContext,
  use,
  useEffect,
  useRef,
  useState,
} from "react";

import { Change } from "../gen/ts/plan/v1/plan_pb";
import { type Djinn, useData, useDjinn, useStore } from "./data/djinn";
import type { TerminalInfo } from "./data/terminal";
import "./lead-terminal.css";
import { t } from "./i18n";
import { onTheme, token } from "./theme";
import { AddProject } from "./wish-dialogs";

// The terminal of the window: opening it again attaches to it while djinn up runs. The wish shown, or djinn wish
// resume, switches it to the terminal of a wish's lead.
const NAME = "main";
// The terminal of a wish's lead, as djinn up names it.
const lead = (wishId: string) => `lead-${wishId}`;
const MIN_HEIGHT = 120;
// The Connect code of a terminal that has no folder to open in: djinn up opens the window's terminal in the first
// project's folder, never in the home folder, and refuses with failed_precondition while no project has one.
const FAILED_PRECONDITION = 9;

// The emulator's colours, from the --term-* tokens of theme.css: the theme on the page now.
const COLOURS = {
  background: "bg",
  foreground: "fg",
  cursor: "cursor",
  cursorAccent: "cursor-accent",
  selectionBackground: "selection",
  black: "black",
  red: "red",
  green: "green",
  yellow: "yellow",
  blue: "blue",
  magenta: "magenta",
  cyan: "cyan",
  white: "white",
  brightBlack: "bright-black",
  brightRed: "bright-red",
  brightGreen: "bright-green",
  brightYellow: "bright-yellow",
  brightBlue: "bright-blue",
  brightMagenta: "bright-magenta",
  brightCyan: "bright-cyan",
  brightWhite: "bright-white",
} as const;
function terminalTheme(): ITheme {
  const theme: ITheme = {};
  for (const [key, name] of Object.entries(COLOURS)) {
    const value = token(`term-${name}`);
    if (value) theme[key as keyof typeof COLOURS] = value;
  }
  return theme;
}

function stored(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}
function store(key: string, value: string) {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Private window or blocked storage: the setting lasts for this page only.
  }
}

// The app tells the terminal which wish it shows: empty for none, such as the flight plan.
const ShowWishContext = createContext<(wishId: string) => void>(
  () => undefined,
);
export const useShowWish = () => use(ShowWishContext);

// A terminal you opened, in a tab of its own: a shell in the first project's folder (shell-…), or in a project's
// root (project-<id>-…). The names say which, so that the tabs come back from djinn up after a reload.
interface Tab {
  name: string;
  directory?: string;
}
// The app asks the terminal for a new tab: in a project's root when given a folder, else a plain shell.
const OpenTerminalContext = createContext<
  ((project?: { id: string; directory: string }) => void) | undefined
>(undefined);
export const useOpenTerminal = () => use(OpenTerminalContext);

// LeadTerminalFrame lays the app out above the terminal, when there is one.
export function LeadTerminalFrame({ children }: { children: ReactNode }) {
  const djinn = useDjinn();
  const [wishId, setWishId] = useState<string>();
  const [request, setRequest] = useState<Tab>();
  if (!djinn) return <>{children}</>;
  const open = (project?: { id: string; directory: string }) =>
    setRequest({
      name: project
        ? `project-${project.id}-${Date.now().toString(36)}`
        : `shell-${Date.now().toString(36)}`,
      directory: project?.directory,
    });
  return (
    <div className="lead-frame">
      <ShowWishContext value={setWishId}>
        <OpenTerminalContext value={open}>
          <div className="lead-frame-app">{children}</div>
        </OpenTerminalContext>
      </ShowWishContext>
      <LeadTerminal
        djinn={djinn}
        wishId={wishId}
        request={request}
        onNew={() => open()}
      />
    </div>
  );
}

type Status =
  | { kind: "connecting" }
  | { kind: "running"; info: TerminalInfo }
  | { kind: "exited"; info: TerminalInfo; code: number }
  | { kind: "error"; message: string; code?: number };

function LeadTerminal({
  djinn,
  wishId,
  request,
  onNew,
}: {
  djinn: Djinn;
  wishId: string | undefined;
  // A tab the app asked for, opened at once.
  request: Tab | undefined;
  onNew: () => void;
}) {
  const api = djinn.terminal;
  const [collapsed, setCollapsed] = useState(
    () => stored("djinn.terminal.collapsed") === "1",
  );
  const [height, setHeight] = useState(() =>
    Math.max(MIN_HEIGHT, Number(stored("djinn.terminal.height")) || 300),
  );
  const [status, setStatus] = useState<Status>({ kind: "connecting" });
  const [generation, setGeneration] = useState(0); // A restart opens a new program.
  // The terminal shown, "" while none is open.
  const [name, setName] = useState("");
  const [adding, setAdding] = useState(false);
  // Every terminal the panel holds, each in a tab: the leads first, then the others in the order they came.
  const [tabs, setTabs] = useState<Tab[]>([]);
  const projects = useData((s) => s.projects);
  const wishes = useData((s) => s.wishes);
  const data = useStore();
  // Whether a project has a folder here: the terminal opens in the first one.
  const hasFolder = useData((s) => s.projects.some((p) => p.directory !== ""));
  // The program a restart starts again: the one that ended, not the default of djinn up.
  const ended = useRef<TerminalInfo | undefined>(undefined);
  const host = useRef<HTMLDivElement>(null);
  const onNewRef = useRef(onNew);
  onNewRef.current = onNew;
  // Ctrl+Shift+T opens a terminal from anywhere in the window too.
  useEffect(() => {
    const keys = (event: KeyboardEvent) => {
      if (event.ctrlKey && event.shiftKey && event.code === "KeyT") {
        event.preventDefault();
        onNewRef.current();
      }
    };
    window.addEventListener("keydown", keys);
    return () => window.removeEventListener("keydown", keys);
  }, []);
  const termRef = useRef<Terminal | null>(null);
  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;
  const fitRef = useRef<FitAddon | null>(null);

  useEffect(
    () => store("djinn.terminal.collapsed", collapsed ? "1" : "0"),
    [collapsed],
  );
  useEffect(() => store("djinn.terminal.height", String(height)), [height]);
  // A terminal joins the tabs once, where it belongs: the leads first.
  const add = (tab: Tab) =>
    setTabs((have) =>
      have.some((x) => x.name === tab.name) ? have : [...have, tab],
    );
  const ordered = [
    ...tabs.filter((x) => x.name.startsWith("lead-")),
    ...tabs.filter((x) => !x.name.startsWith("lead-")),
  ].map((x) => x.name);
  // Show the terminal djinn asks for, such as a wish's lead.
  useEffect(
    () =>
      djinn.focus.subscribe((focus) => {
        if (!focus.terminal) return;
        ended.current = undefined;
        add({ name: focus.terminal });
        setName(focus.terminal);
        setCollapsed(false);
      }),
    [djinn],
  );
  // Show the lead of the wish shown while it runs: never start a lead from here.
  useEffect(() => {
    if (!wishId) return;
    let current = true;
    api.running().then(
      (names) => {
        if (!current || !names.includes(lead(wishId))) return;
        add({ name: lead(wishId) });
        setName(lead(wishId));
      },
      () => undefined, // djinn up restarts: the terminal shown stays, and says so.
    );
    return () => {
      current = false;
    };
  }, [api, wishId]);

  // The terminals that run come back as tabs after a reload: djinn up keeps them while it runs. None opens by itself.
  useEffect(() => {
    let current = true;
    api.running().then(
      (names) => {
        if (!current) return;
        setTabs((have) => [
          ...have,
          ...names
            .filter((n) => !have.some((x) => x.name === n))
            .map((n) => ({ name: n })),
        ]);
        const first = names.find((n) => n.startsWith("lead-")) ?? names[0];
        setName((shown) => shown || first || "");
      },
      () => undefined,
    );
    return () => {
      current = false;
    };
  }, [api]);
  useEffect(() => {
    if (!request) return;
    ended.current = undefined;
    add(request);
    setName(request.name);
    setCollapsed(false);
  }, [request]);
  // A tab closes, by its × or when its program ends (Ctrl+D, exit): its program hangs up if it still runs, and the
  // next tab shows; none left, the panel offers a new terminal. A lead's tab comes back from the Lead button.
  const closeTab = async (tab: string) => {
    const rest = ordered.filter((x) => x !== tab);
    setTabs((have) => have.filter((x) => x.name !== tab));
    if (name === tab) setName(rest[0] ?? "");
    try {
      if (!(await api.running()).includes(tab)) return;
      const { terminal } = await api.open({ name: tab, cols: 80, rows: 24 });
      await api.close(terminal.id);
    } catch {
      // Gone already: nothing to hang up.
    }
  };
  const closeTabRef = useRef(closeTab);
  closeTabRef.current = closeTab;
  const label = (tab: string) => {
    if (tab === NAME) return t("terminal.title");
    if (tab.startsWith("lead-")) {
      const wish = wishes.find((w) => lead(w.id) === tab);
      return t("terminal.lead_tab", { wish: wish?.title ?? "" });
    }
    const project = projects.find((p) => tab.startsWith(`project-${p.id}-`));
    if (project) return project.name;
    return t("terminal.shell_tab", {
      n: tabs.findIndex((x) => x.name === tab) + 1,
    });
  };

  // The emulator, the program, and the links between them.
  useEffect(() => {
    const element = host.current;
    if (!element || !name) return;
    const term = new Terminal({
      fontFamily: '"IBM Plex Mono", monospace',
      fontSize: 13,
      cursorBlink: true,
      scrollback: 5000,
      allowProposedApi: false,
      theme: terminalTheme(),
    });
    const stopTheme = onTheme(() => (term.options.theme = terminalTheme()));
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(
      new WebLinksAddon((_event, uri) => {
        void djinn.clients.ui
          .openExternal({ url: uri })
          .catch(() => window.open(uri, "_blank", "noopener"));
      }),
    );
    term.open(element);
    termRef.current = term;
    fitRef.current = fit;
    fitSafely(fit);

    // Copy and paste as in a terminal: Ctrl+Shift+C and Ctrl+Shift+V (Cmd+C and Cmd+V on macOS work as they are).
    term.attachCustomKeyEventHandler((event) => {
      if (event.type !== "keydown" || !event.ctrlKey || !event.shiftKey)
        return true;
      if (event.code === "KeyT") {
        onNewRef.current();
        event.preventDefault();
        return false;
      }
      if (event.code === "KeyC") {
        const text = term.getSelection();
        if (text)
          void navigator.clipboard
            ?.writeText(text)
            .catch(() => document.execCommand("copy"));
        event.preventDefault();
        return false;
      }
      // Leave Ctrl+Shift+V to the browser, which pastes into the emulator.
      return event.code !== "KeyV";
    });
    // The app's own shortcuts (Escape, Ctrl+K…) must not take the keys typed into the terminal.
    const keep = (event: KeyboardEvent) => event.stopPropagation();
    element.addEventListener("keydown", keep);

    const abort = new AbortController();
    let id = "";
    const inputs = [
      term.onData(
        (data) => id && void api.write(id, data).catch(() => undefined),
      ),
      term.onBinary((data) => {
        if (!id) return;
        const bytes = Uint8Array.from(data, (c) => c.charCodeAt(0) & 0xff);
        void api.write(id, bytes).catch(() => undefined);
      }),
      term.onResize(
        ({ cols, rows }) =>
          id && void api.resize(id, cols, rows).catch(() => undefined),
      ),
    ];

    (async () => {
      try {
        // A restart runs again the program that ended here, not one of another terminal.
        const again = ended.current?.name === name ? ended.current : undefined;
        const folder = tabsRef.current.find((x) => x.name === name)?.directory;
        const { terminal } = await api.open({
          name,
          cols: term.cols,
          rows: term.rows,
          ...(again
            ? { command: again.command, directory: again.directory }
            : folder
              ? { directory: folder }
              : {}),
        });
        if (abort.signal.aborted) return;
        id = terminal.id;
        setStatus({ kind: "running", info: terminal });
        // From the start of what is kept; a broken stream resumes where it stopped.
        let offset = 0n;
        for (;;) {
          try {
            const end = await api.read(
              id,
              offset,
              (at, data) => {
                term.write(data);
                offset = at + BigInt(data.length);
              },
              abort.signal,
            );
            if (abort.signal.aborted) return;
            if (end.exited) {
              // A terminal ends with its program, Ctrl+D or exit: its tab goes.
              closeTabRef.current(name);
              return;
            }
            if (end.exited) {
              term.write(
                `\r\n\x1b[2m[exited with code ${end.exitCode}]\x1b[0m\r\n`,
              );
              ended.current = terminal;
              setStatus({ kind: "exited", info: terminal, code: end.exitCode });
              return;
            }
          } catch (error) {
            if (abort.signal.aborted) return;
            if ((error as { code?: number }).code === 5) throw error; // Not found: djinn up restarted.
          }
          // eslint-disable-next-line @eslint-react/web-api-no-leaked-timeout -- a pause in a loop the cleanup aborts
          await new Promise((r) => setTimeout(r, 500));
        }
      } catch (error) {
        if (!abort.signal.aborted)
          setStatus({
            kind: "error",
            message: error instanceof Error ? error.message : String(error),
            code: (error as { code?: number }).code,
          });
      }
    })();

    return () => {
      abort.abort();
      stopTheme();
      inputs.forEach((d) => d.dispose());
      element.removeEventListener("keydown", keep);
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [api, djinn, generation, name]);

  // A terminal with no folder to open in starts once a project has one, made here or from the command line.
  const noFolder =
    status.kind === "error" && status.code === FAILED_PRECONDITION;
  const waiting = useRef(false);
  waiting.current = noFolder;
  useEffect(() => {
    if (hasFolder && waiting.current) setGeneration((g) => g + 1);
  }, [hasFolder]);
  const noProject = noFolder && !hasFolder;

  // Fit the emulator to its box whenever the box changes.
  useEffect(() => {
    const element = host.current;
    if (!element) return;
    const observer = new ResizeObserver(() => {
      if (fitRef.current) fitSafely(fitRef.current);
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (!collapsed) termRef.current?.focus();
  }, [collapsed]);

  // Drag the top edge to change the height.
  const startDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    const startY = event.clientY;
    const startHeight = height;
    const move = (e: PointerEvent) => {
      const max = window.innerHeight - 160;
      setHeight(
        Math.round(
          Math.min(max, Math.max(MIN_HEIGHT, startHeight + startY - e.clientY)),
        ),
      );
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };

  const info =
    status.kind === "running" || status.kind === "exited"
      ? status.info
      : undefined;
  return (
    <section
      className={`lead-terminal ${collapsed ? "collapsed" : ""}`}
      style={collapsed ? undefined : { height }}
      aria-label={t("terminal.title")}
    >
      {!collapsed && (
        <div className="lead-terminal-grip" onPointerDown={startDrag} />
      )}
      <header className="lead-terminal-bar">
        <button
          className="lead-terminal-title"
          onClick={() => setCollapsed(!collapsed)}
          title={collapsed ? t("terminal.expand") : t("terminal.collapse")}
        >
          <SquareTerminal size={13} />
          <span>{t("terminal.title")}</span>
          {info && (
            <span className="lead-terminal-command">
              {info.command.join(" ")}
            </span>
          )}
          {info && (
            <span className="lead-terminal-command">{info.directory}</span>
          )}
        </button>
        <div className="lead-terminal-tabs" role="tablist">
          {ordered.map((tab) => (
            <span
              key={tab}
              className={`lead-terminal-tab ${tab === name ? "selected" : ""}`}
            >
              <button
                role="tab"
                aria-selected={tab === name}
                onClick={() => {
                  ended.current = undefined;
                  setName(tab);
                  setCollapsed(false);
                }}
              >
                {label(tab)}
              </button>
              <button
                className="lead-terminal-tab-close"
                onClick={() => void closeTab(tab)}
                title={t("terminal.close_tab")}
                aria-label={t("terminal.close_tab")}
              >
                <X size={11} />
              </button>
            </span>
          ))}
          <button
            className="lead-terminal-tab-new"
            onClick={onNew}
            title={t("terminal.new_tab")}
            aria-label={t("terminal.new_tab")}
          >
            <Plus size={13} />
          </button>
        </div>
        {status.kind === "exited" && (
          <span className="lead-terminal-state">
            {t("terminal.exited", { code: status.code })}
          </span>
        )}
        {status.kind === "error" && !noProject && (
          <span className="lead-terminal-state error">{status.message}</span>
        )}
        {(status.kind === "exited" ||
          (status.kind === "error" && !noProject)) && (
          <button
            onClick={() => setGeneration((g) => g + 1)}
            title={t("terminal.restart")}
          >
            <RotateCcw size={13} />
          </button>
        )}
        <button
          onClick={() => setCollapsed(!collapsed)}
          title={collapsed ? t("terminal.expand") : t("terminal.collapse")}
        >
          {collapsed ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
        </button>
      </header>
      <div className="lead-terminal-screen" ref={host} hidden={!name} />
      {!name && !collapsed && (
        <div className="lead-terminal-empty">
          <p>{t("terminal.none")}</p>
          <button className="button accent" onClick={onNew}>
            <Plus size={14} />
            {t("terminal.new_tab")}
          </button>
        </div>
      )}
      {noProject && !collapsed && (
        <div className="lead-terminal-empty">
          <p>{t("terminal.no_project")}</p>
          <button className="button accent" onClick={() => setAdding(true)}>
            <FolderPlus size={14} />
            {t("sidebar.new_project")}
          </button>
        </div>
      )}
      {adding && (
        <AddProject
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            void data.changed("", [Change.PROJECT]);
          }}
        />
      )}
    </section>
  );
}

// fitSafely fits the emulator to its box, unless the box is hidden (collapsed): a zero size would shrink the
// program's terminal to nothing.
function fitSafely(fit: FitAddon) {
  const size = fit.proposeDimensions();
  if (size && size.cols > 1 && size.rows > 1) fit.fit();
}
