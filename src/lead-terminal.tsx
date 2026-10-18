// The terminal pinned at the bottom of the window: real terminals (xterm.js) on pseudo-terminals of djinn up, each
// in a tab. It shows only when djinn serves the page (a DjinnProvider); elsewhere the app renders alone. None opens by
// itself: the lead of the wish shown comes as a tab while it runs, first; you open a terminal with + or Ctrl+Shift+T,
// or one at a project's root (its button in the side panel). Every tab closes with its ×, or when its program ends
// (Ctrl+D, exit), except the lead's: an ended lead keeps its last output there until Lead, or its Restart, starts it
// again. One shows at a time.
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
import { message } from "./data/client";
import { type Djinn, useData, useDjinn, useStore } from "./data/djinn";
import type { TerminalInfo } from "./data/terminal";
import "./lead-terminal.css";
import { t } from "./i18n";
import { EmptyState } from "./empty-art";
import { onTextSize, textScale } from "./text-size";
import { onTheme, token } from "./theme";
import { AddProject } from "./wish-dialogs";

// The terminal of the window: opening it again attaches to it while djinn up runs. The wish shown, or djinn wish
// resume, switches it to the terminal of a wish's lead.
const NAME = "main";
// The terminal of the lead, as djinn up names it.
const LEAD = "lead";
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
      <OpenTerminalContext value={open}>
        <div className="lead-frame-app">{children}</div>
      </OpenTerminalContext>
      <LeadTerminal djinn={djinn} request={request} onNew={() => open()} />
    </div>
  );
}

export type TerminalStatus =
  | { kind: "connecting" }
  | { kind: "running"; info: TerminalInfo }
  | { kind: "exited"; info: TerminalInfo; code: number }
  | { kind: "error"; message: string; code?: number };

function LeadTerminal({
  djinn,
  request,
  onNew,
}: {
  djinn: Djinn;
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
  const [status, setStatus] = useState<TerminalStatus>({ kind: "connecting" });
  const [generation, setGeneration] = useState(0); // A restart opens a new program.
  // The terminal shown, "" while none is open.
  const [name, setName] = useState("");
  // The lead's tab shows a lead that ended: a lead started again comes in its place.
  const endedLead = useRef(false);
  endedLead.current = name === LEAD && status.kind === "exited";
  const [adding, setAdding] = useState(false);
  // Every terminal the panel holds, each in a tab: the leads first, then the others in the order they came.
  const [tabs, setTabs] = useState<Tab[]>([]);
  const projects = useData((s) => s.projects);
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
    ...tabs.filter((x) => x.name === LEAD),
    ...tabs.filter((x) => x.name !== LEAD),
  ].map((x) => x.name);
  // Show the terminal djinn asks for, such as the lead: never start a lead from here.
  useEffect(
    () =>
      djinn.focus.subscribe((focus) => {
        if (!focus.terminal) return;
        void api.running().then((names) => {
          if (!names.includes(focus.terminal)) return;
          // The lead started again (Lead, djinn lead resume): its tab, showing the one that ended, shows the new one.
          if (focus.terminal === LEAD && endedLead.current)
            setGeneration((g) => g + 1);
          ended.current = undefined;
          add({ name: focus.terminal });
          setName(focus.terminal);
          setCollapsed(false);
        });
      }),
    [api, djinn],
  );

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
        const leadTab = names.find((n) => n === LEAD);
        const nonLead = names.find((n) => n !== LEAD);
        const first = leadTab ?? nonLead;
        setName((shown) => shown || first || "");
      },
      () => undefined,
    );
    return () => {
      current = false;
    };
  }, [api]);
  // A lead that ended keeps its tab, after a reload too, once LeadService.Get says so; closed, it stays closed.
  const leadEnded = useData((s) => !!s.leadExit);
  useEffect(() => {
    if (!leadEnded) return;
    add({ name: LEAD });
    setName((shown) => shown || LEAD);
  }, [leadEnded]);
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
    if (tab === LEAD) return t("terminal.lead_tab");
    const project = projects.find((p) => tab.startsWith(`project-${p.id}-`));
    if (project) return project.name;
    return t("terminal.shell_tab", {
      n: tabs.findIndex((x) => x.name === tab) + 1,
    });
  };

  // The emulator, the program, and the links between them.
  useEffect(() => {
    const element = host.current;
    if (!element || !name || collapsed) return;
    // A restart runs again the program that ended here, not one of another terminal.
    const again = ended.current?.name === name ? ended.current : undefined;
    const folder = tabsRef.current.find((x) => x.name === name)?.directory;
    const mounted = mountTerminal(
      element,
      djinn,
      {
        name,
        ...(again
          ? { command: again.command, directory: again.directory }
          : folder
            ? { directory: folder }
            : {}),
      },
      (next) => {
        if (next.kind !== "exited") return setStatus(next);
        // A terminal ends with its program, Ctrl+D or exit: its tab goes. The lead's stays, its output read, unless
        // a lead runs there again already: then it shows that one.
        if (name !== LEAD) return closeTabRef.current(name);
        setStatus(next);
        void api.running().then(
          (names) => names.includes(LEAD) && setGeneration((g) => g + 1),
          () => undefined,
        );
      },
      () => onNewRef.current(),
    );
    termRef.current = mounted.term;
    fitRef.current = mounted.fit;
    return () => {
      mounted.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [api, collapsed, djinn, generation, name]);

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
            onClick={() =>
              name === LEAD
                ? // The lead starts again as Lead starts it, never on its old command line.
                  void djinn.clients.lead.resume({}).then(
                    () => setGeneration((g) => g + 1),
                    (error) =>
                      setStatus({ kind: "error", message: message(error) }),
                  )
                : setGeneration((g) => g + 1)
            }
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
          <EmptyState
            art="terminal"
            after={
              <button className="button accent" onClick={onNew}>
                <Plus size={14} />
                {t("terminal.new_tab")}
              </button>
            }
          >
            {t("terminal.none")}
          </EmptyState>
        </div>
      )}
      {noProject && !collapsed && (
        <div className="lead-terminal-empty">
          <EmptyState
            art="noproject"
            after={
              <button className="button accent" onClick={() => setAdding(true)}>
                <FolderPlus size={14} />
                {t("sidebar.new_project")}
              </button>
            }
          >
            {t("terminal.no_project")}
          </EmptyState>
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

// terminalFontSize is the emulator's font size: 13px at the default text size, scaled with it (text-size.ts) to the
// half pixel.
const terminalFontSize = () => Math.round(26 * textScale()) / 2;

// mountTerminal runs an emulator in element on the terminal of djinn that request opens or attaches to, and tells
// onStatus where its program stands, until dispose. The window's terminal and the agents' setup both use it; onNew,
// when given, takes Ctrl+Shift+T.
export function mountTerminal(
  element: HTMLElement,
  djinn: Djinn,
  request: {
    name: string;
    command?: string[];
    line?: string;
    directory?: string;
  },
  onStatus: (status: TerminalStatus) => void,
  onNew?: () => void,
): { term: Terminal; fit: FitAddon; dispose: () => void } {
  const api = djinn.terminal;
  const term = new Terminal({
    fontFamily: '"IBM Plex Mono", monospace',
    fontSize: terminalFontSize(),
    cursorBlink: true,
    scrollback: 5000,
    allowProposedApi: false,
    theme: terminalTheme(),
  });
  const stopTheme = onTheme(() => (term.options.theme = terminalTheme()));
  const stopTextSize = onTextSize(() => {
    term.options.fontSize = terminalFontSize();
    fitSafely(fit);
  });
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
  fitSafely(fit);

  // Copy and paste as in a terminal: Ctrl+Shift+C and Ctrl+Shift+V (Cmd+C and Cmd+V on macOS work as they are).
  term.attachCustomKeyEventHandler((event) => {
    if (event.type !== "keydown" || !event.ctrlKey || !event.shiftKey)
      return true;
    if (event.code === "KeyT" && onNew) {
      onNew();
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
  const stopInputGuard = guardTerminalInput(term, element);

  const abort = new AbortController();
  let id = "";
  // The output a program wrote before this emulator attached is read again, not answered: its queries (DA1, DECRQM,
  // OSC 11 from agy…) were answered when they came, or their program no longer waits; answers now would be typed
  // into it, or into the shell once it ended. Keys typed in those few milliseconds go too.
  let replaying = false;
  const inputs = [
    term.onData(
      (data) =>
        id && !replaying && void api.write(id, data).catch(() => undefined),
    ),
    term.onBinary((data) => {
      if (!id || replaying) return;
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
      const { terminal, attached, end } = await api.open({
        ...request,
        cols: term.cols,
        rows: term.rows,
      });
      if (abort.signal.aborted) return;
      id = terminal.id;
      onStatus({ kind: "running", info: terminal });
      // What a program this open started wrote meanwhile was never answered: none of it is replayed.
      const replayed = attached ? end : 0n;
      replaying = replayed > 0n;
      // From the start of what is kept; a broken stream resumes where it stopped.
      let offset = 0n;
      for (;;) {
        try {
          const end = await api.read(
            id,
            offset,
            (at, data) => {
              offset = at + BigInt(data.length);
              if (!replaying || offset < replayed) return term.write(data);
              // The end of the replay: the emulator answers again once it has parsed the bytes before it.
              const cut = Number(replayed > at ? replayed - at : 0n);
              term.write(data.subarray(0, cut), () => (replaying = false));
              term.write(data.subarray(cut));
            },
            abort.signal,
          );
          if (abort.signal.aborted) return;
          if (end.exited) {
            term.write(
              `\r\n\x1b[2m[exited with code ${end.exitCode}]\x1b[0m\r\n`,
            );
            onStatus({ kind: "exited", info: terminal, code: end.exitCode });
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
        onStatus({
          kind: "error",
          message: error instanceof Error ? error.message : String(error),
          code: (error as { code?: number }).code,
        });
    }
  })();

  return {
    term,
    fit,
    dispose: () => {
      abort.abort();
      stopTheme();
      stopTextSize();
      stopInputGuard();
      inputs.forEach((d) => d.dispose());
      element.removeEventListener("keydown", keep);
      term.dispose();
    },
  };
}

// guardTerminalInput keeps WebKitGTK's input methods and xterm.js's hidden textarea in step:
// - xterm.js does not preventDefault on keypress (Space, Shift+letter, keyCode 0) or clear Linux mouse selections, so
//   characters and WebKit's non-breaking spaces accumulate in the textarea between compositions;
// - WebKitGTK's IBus and Simple input methods commit direct accented keys (keyCode 229, insertFromComposition,
//   compositionend) without firing compositionstart, so CompositionHelper slices from index 0 of the textarea and
//   sends every accumulated space and accented character again.
export function guardTerminalInput(
  term: Terminal,
  element: HTMLElement,
): () => void {
  const ta = term.textarea;
  if (!ta) return () => {};
  let composing = false;
  let pending = 0;
  const release = () => {
    setTimeout(() => {
      if (pending > 0) pending--;
      if (!composing && pending === 0 && ta.value !== "") ta.value = "";
    }, 0);
  };
  const onElementKeyDown = () => {
    if (!composing && pending === 0 && ta.value !== "") ta.value = "";
  };
  const onTextareaKeyDown = (ev: KeyboardEvent) => {
    if (ev.keyCode === 229) {
      pending++;
      release();
    } else if (
      composing &&
      ev.keyCode !== 16 &&
      ev.keyCode !== 17 &&
      ev.keyCode !== 18 &&
      ev.keyCode !== 20
    ) {
      composing = false;
      pending++;
      release();
    }
  };
  const onBeforeInput = (ev: InputEvent) => {
    if (!composing && ev.inputType === "insertFromComposition") {
      if (pending === 0 && ta.value !== "") ta.value = "";
      composing = true;
      ta.dispatchEvent(new CompositionEvent("compositionstart"));
    }
  };
  const onCompositionStart = () => {
    if (!composing && pending === 0 && ta.value !== "") ta.value = "";
    composing = true;
  };
  const onCompositionEnd = () => {
    composing = false;
    pending++;
    release();
  };
  const onBlur = () => {
    composing = false;
  };
  const onKeyPress = (ev: KeyboardEvent) => {
    ev.preventDefault();
  };
  element.addEventListener("keydown", onElementKeyDown, true);
  ta.addEventListener("keydown", onTextareaKeyDown, true);
  ta.addEventListener("beforeinput", onBeforeInput, true);
  ta.addEventListener("compositionstart", onCompositionStart, true);
  ta.addEventListener("compositionend", onCompositionEnd);
  ta.addEventListener("blur", onBlur);
  ta.addEventListener("keypress", onKeyPress);
  return () => {
    element.removeEventListener("keydown", onElementKeyDown, true);
    ta.removeEventListener("keydown", onTextareaKeyDown, true);
    ta.removeEventListener("beforeinput", onBeforeInput, true);
    ta.removeEventListener("compositionstart", onCompositionStart, true);
    ta.removeEventListener("compositionend", onCompositionEnd);
    ta.removeEventListener("blur", onBlur);
    ta.removeEventListener("keypress", onKeyPress);
  };
}

// fitSafely fits the emulator to its box, unless the box is hidden (collapsed): a zero size would shrink the
// program's terminal to nothing.
export function fitSafely(fit: FitAddon) {
  const size = fit.proposeDimensions();
  if (size && size.cols > 1 && size.rows > 1) fit.fit();
}
