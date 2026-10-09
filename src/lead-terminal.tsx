// The terminal pinned at the bottom of the window: a real terminal (xterm.js) on a pseudo-terminal of djinn up,
// running the user's shell or the command djinn up was given (--terminal), such as the lead agent. It shows only
// when djinn serves the page (a DjinnProvider); elsewhere the app renders alone.
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import {
  Bot,
  ChevronDown,
  ChevronUp,
  RotateCcw,
  SquareTerminal,
} from "lucide-react";
import {
  type ReactNode,
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";

import type { Wish } from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { type Djinn, useDjinn } from "./data/djinn";
import type { TerminalInfo } from "./data/terminal";
import "./lead-terminal.css";
import { t } from "./i18n";

// The terminal of the window: opening it again attaches to it while djinn up runs. djinn wish resume switches it
// to the terminal of a wish's lead.
const NAME = "main";
const MIN_HEIGHT = 120;

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

// leadTerminal is the name of the terminal of a wish's lead, as djinn names it (plan.LeadTerminal).
export function leadTerminal(wishId: string): string {
  return "lead-" + wishId.toLowerCase();
}

// The wish the window shows, as the terminal follows it: its lead, if it has one.
interface Shown {
  id: string;
  lead: boolean; // A lead session is recorded, so following may resume its known session.
  running: boolean; // The lead's terminal runs.
  exited: boolean; // The lead's terminal ended, so following attaches without starting it again.
}

const ShowWish = createContext<(shown: Shown | undefined) => void>(
  () => undefined,
);

// useTerminalFollows makes the terminal follow wish, the one the window shows (none for the flight plan): it shows
// the wish's lead.
export function useTerminalFollows(wish: Wish | undefined) {
  const show = useContext(ShowWish);
  const id = wish?.id ?? "";
  const lead = !!wish?.lead || !!wish?.leadRunning;
  const running = !!wish?.leadRunning;
  const exited = !!wish?.leadExit;
  useEffect(
    () => show(id ? { id, lead, running, exited } : undefined),
    [show, id, lead, running, exited],
  );
}

// LeadTerminalFrame lays the app out above the terminal, when there is one.
export function LeadTerminalFrame({ children }: { children: ReactNode }) {
  const djinn = useDjinn();
  const [shown, setShown] = useState<Shown | undefined>(undefined);
  const show = useCallback(
    (next: Shown | undefined) =>
      setShown((prev) =>
        prev?.id === next?.id &&
        prev?.lead === next?.lead &&
        prev?.running === next?.running &&
        prev?.exited === next?.exited
          ? prev
          : next,
      ),
    [],
  );
  if (!djinn) return <>{children}</>;
  return (
    <div className="lead-frame">
      <ShowWish.Provider value={show}>
        <div className="lead-frame-app">{children}</div>
      </ShowWish.Provider>
      <LeadTerminal djinn={djinn} shown={shown} />
    </div>
  );
}

export type TerminalStatus =
  | { kind: "connecting" }
  | { kind: "running"; info: TerminalInfo }
  | { kind: "exited"; info: TerminalInfo; code: number }
  | { kind: "error"; message: string };

function LeadTerminal({
  djinn,
  shown,
}: {
  djinn: Djinn;
  shown: Shown | undefined;
}) {
  const [collapsed, setCollapsed] = useState(
    () => stored("djinn.terminal.collapsed") === "1",
  );
  const [height, setHeight] = useState(() =>
    Math.max(MIN_HEIGHT, Number(stored("djinn.terminal.height")) || 300),
  );
  const [status, setStatus] = useState<TerminalStatus>({ kind: "connecting" });
  const [generation, setGeneration] = useState(0); // A restart opens a new program.
  const [name, setName] = useState(NAME);
  // What the terminal could not show of the wish, such as a lead that does not resume.
  const [notice, setNotice] = useState("");
  // The wish whose terminal the window took: another one shown takes its own.
  const followed = useRef("");
  const shownRef = useRef(shown);
  shownRef.current = shown;
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
        // Attach again even under the same name: a new lead may run there now, its program a new one.
        setGeneration((g) => g + 1);
        setCollapsed(false);
      }),
    [djinn],
  );

  // Show the lead of the wish the window shows, once the terminal is open. An ended lead attaches to its retained
  // output without starting it again. A recorded session with no current terminal keeps the old follow behavior and
  // resumes its known session; a wish without a lead keeps the window's terminal until the user starts it.
  const follow = useCallback(
    (wish: Shown, start = false) => {
      const key = `${wish.id}:`;
      const target = wish.running || wish.exited ? leadTerminal(wish.id) : NAME;
      followed.current = key + target;
      setNotice("");
      const take = (terminal: string) => {
        if (!followed.current.startsWith(key)) return;
        ended.current = undefined;
        followed.current = key + terminal;
        setName(terminal);
      };
      if (wish.running || wish.exited) take(leadTerminal(wish.id));
      else if (!wish.lead && !start) take(NAME);
      else
        djinn.clients.wishes
          .resume({ wishId: wish.id })
          .then((res) => take(res.terminal))
          .catch(
            (error) =>
              followed.current.startsWith(key) && setNotice(message(error)),
          );
    },
    [djinn],
  );
  useEffect(() => {
    if (collapsed || !shown) return;
    const target =
      shown.running || shown.exited ? leadTerminal(shown.id) : NAME;
    if (followed.current === `${shown.id}:${target}`) return;
    follow(shown);
  }, [collapsed, shown, follow]);

  // The emulator, the program, and the links between them.
  useEffect(() => {
    const element = host.current;
    if (!element) return;
    // The recorded command is safe to replay for the window shell. A lead retry belongs to WishService.Resume, so a
    // lead terminal is always reattached with its retained output instead of starting its old command directly.
    const again = ended.current && name === NAME ? ended.current : undefined;
    const mounted = mountTerminal(
      element,
      djinn,
      again
        ? { name, command: again.command, directory: again.directory }
        : { name },
      (next) => {
        if (next.kind === "exited") ended.current = next.info;
        setStatus(next);
      },
    );
    termRef.current = mounted.term;
    fitRef.current = mounted.fit;
    return () => {
      mounted.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [djinn, generation, name]);

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
        {status.kind === "error" && (
          <span className="lead-terminal-state error">{status.message}</span>
        )}
        {notice && <span className="lead-terminal-state error">{notice}</span>}
        {shown && name !== leadTerminal(shown.id) && (
          <button
            className="lead-terminal-switch"
            onClick={() => {
              const next = shownRef.current ?? shown;
              follow(next, !(next.lead || next.running || next.exited));
            }}
            title={
              shown.lead || shown.running || shown.exited
                ? t("terminal.lead_detail")
                : t("wish.resume_detail")
            }
          >
            <Bot size={13} />
            <span>
              {shown.lead || shown.running
                ? t("terminal.lead")
                : t("terminal.resume")}
            </span>
          </button>
        )}
        {name !== NAME && (
          <button
            className="lead-terminal-switch"
            onClick={() => {
              ended.current = undefined;
              setNotice("");
              const current = shownRef.current;
              if (current) followed.current = `${current.id}:${NAME}`;
              setName(NAME);
            }}
            title={t("terminal.shell_detail")}
          >
            <SquareTerminal size={13} />
            <span>{t("terminal.shell")}</span>
          </button>
        )}
        {(status.kind === "exited" || status.kind === "error") &&
          name === NAME && (
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
    </section>
  );
}

// mountTerminal runs an emulator in element on the terminal of djinn that request opens or attaches to, and tells
// onStatus where its program stands, until dispose. The window's terminal and the agents' setup both use it.
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
): { term: Terminal; fit: FitAddon; dispose: () => void } {
  const api = djinn.terminal;
  const term = new Terminal({
    fontFamily: '"IBM Plex Mono", monospace',
    fontSize: 13,
    cursorBlink: true,
    scrollback: 5000,
    allowProposedApi: false,
    theme: {
      background: "#111111",
      foreground: "#e7e7e7",
      cursor: "#ebebeb",
    },
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
      const { terminal } = await api.open({
        ...request,
        cols: term.cols,
        rows: term.rows,
      });
      if (abort.signal.aborted) return;
      id = terminal.id;
      onStatus({ kind: "running", info: terminal });
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
        });
    }
  })();

  return {
    term,
    fit,
    dispose: () => {
      abort.abort();
      inputs.forEach((d) => d.dispose());
      element.removeEventListener("keydown", keep);
      term.dispose();
    },
  };
}

// fitSafely fits the emulator to its box, unless the box is hidden (collapsed): a zero size would shrink the
// program's terminal to nothing.
export function fitSafely(fit: FitAddon) {
  const size = fit.proposeDimensions();
  if (size && size.cols > 1 && size.rows > 1) fit.fit();
}
