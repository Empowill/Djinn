// The terminal pinned at the bottom of the window: a real terminal (xterm.js) on a pseudo-terminal of djinn up,
// running the user's shell or the command djinn up was given (--terminal), such as the lead agent. It shows only
// when djinn serves the page (a DjinnProvider); elsewhere the app renders alone. One terminal shows at a time: the
// lead of the wish the app shows while it runs, else the window's own.
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { type ITheme, Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import {
  ChevronDown,
  ChevronUp,
  FolderPlus,
  RotateCcw,
  SquareTerminal,
} from "lucide-react";
import {
  type ReactNode,
  createContext,
  useContext,
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
const ShowWish = createContext<(wishId: string) => void>(() => undefined);
export const useShowWish = () => useContext(ShowWish);

// LeadTerminalFrame lays the app out above the terminal, when there is one.
export function LeadTerminalFrame({ children }: { children: ReactNode }) {
  const djinn = useDjinn();
  const [wishId, setWishId] = useState<string>();
  if (!djinn) return <>{children}</>;
  return (
    <div className="lead-frame">
      <ShowWish.Provider value={setWishId}>
        <div className="lead-frame-app">{children}</div>
      </ShowWish.Provider>
      <LeadTerminal djinn={djinn} wishId={wishId} />
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
}: {
  djinn: Djinn;
  wishId: string | undefined;
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
  const [name, setName] = useState(NAME);
  const [adding, setAdding] = useState(false);
  const data = useStore();
  // Whether a project has a folder here: the terminal opens in the first one.
  const hasFolder = useData((s) => s.projects.some((p) => p.directory !== ""));
  // The program a restart starts again: the one that ended, not the default of djinn up.
  const ended = useRef<TerminalInfo | undefined>(undefined);
  const host = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);

  useEffect(
    () => store("djinn.terminal.collapsed", collapsed ? "1" : "0"),
    [collapsed],
  );
  useEffect(() => store("djinn.terminal.height", String(height)), [height]);
  // Show the terminal djinn asks for, such as a wish's lead.
  useEffect(
    () =>
      djinn.focus.subscribe((focus) => {
        if (!focus.terminal) return;
        ended.current = undefined;
        setName(focus.terminal);
        setCollapsed(false);
      }),
    [djinn],
  );
  // Show the lead of the wish shown while it runs, else the window's terminal: never start a lead from here.
  useEffect(() => {
    if (wishId === undefined) return;
    if (!wishId) {
      setName(NAME);
      return;
    }
    let current = true;
    api.running().then(
      (names) =>
        current && setName(names.includes(lead(wishId)) ? lead(wishId) : NAME),
      () => undefined, // djinn up restarts: the terminal shown stays, and says so.
    );
    return () => {
      current = false;
    };
  }, [api, wishId]);

  // The emulator, the program, and the links between them.
  useEffect(() => {
    const element = host.current;
    if (!element) return;
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
        const { terminal } = await api.open({
          name,
          cols: term.cols,
          rows: term.rows,
          ...(again
            ? { command: again.command, directory: again.directory }
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
      <div className="lead-terminal-screen" ref={host} />
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
