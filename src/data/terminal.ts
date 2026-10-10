// The terminals of djinn up (TerminalService), for the terminal pinned at the bottom of the
// window. The output comes as a server stream; the keys go as small unary requests, one at a time so they keep their
// order. A browser cannot stream from client to server over HTTP/1.1, and a request takes well under a millisecond
// on this machine, far below the key repeat interval (about 30 ms): see docs/transport.md.
import {
  ConnectError,
  type Transport,
  createClient,
} from "@connectrpc/connect";

import { TerminalService } from "../../gen/ts/terminal/v1/terminal_pb";

export interface TerminalInfo {
  id: string;
  name: string;
  command: string[];
  directory: string;
  cols: number;
  rows: number;
  exited: boolean;
  exitCode: number;
}

export interface TerminalEnd {
  exited: boolean;
  exitCode: number;
}

export interface DjinnTerminal {
  // Opens the terminal of name, or attaches to it if it runs: its output so far, up to end, is read again from
  // offset 0. A command and a directory start that program instead of the default of djinn up.
  open(input: {
    name: string;
    cols: number;
    rows: number;
    command?: string[];
    directory?: string;
  }): Promise<{ terminal: TerminalInfo; attached: boolean; end: bigint }>;
  // Sends typed bytes. Writes leave in the order they are made, one request at a time; the promise settles once
  // the terminal has them.
  write(id: string, data: string | Uint8Array): Promise<void>;
  resize(id: string, cols: number, rows: number): Promise<void>;
  // Calls onData with each piece of output from offset on, until the program ends or signal aborts.
  read(
    id: string,
    offset: bigint,
    onData: (offset: bigint, data: Uint8Array) => void,
    signal: AbortSignal,
  ): Promise<TerminalEnd>;
  // The names of the terminals whose program runs, such as lead-<wish id>. Starts none.
  running(): Promise<string[]>;
  // Hangs up: the program gets SIGHUP.
  close(id: string): Promise<void>;
}

// Above this many writes waiting, they leave together: a burst (a paste in pieces, a stalled link) catches up in one
// request. Below, each keeps its own request, as a terminal delivers each key on its own.
const backlog = 16;

export function createTerminal(transport: Transport): DjinnTerminal {
  const client = createClient(TerminalService, transport);
  const encoder = new TextEncoder();
  type Pending = {
    id: string;
    data: Uint8Array;
    done: Array<{ resolve: () => void; reject: (e: unknown) => void }>;
  };
  const queue: Pending[] = [];
  let sending = false;

  async function drain() {
    sending = true;
    while (queue.length) {
      const next = queue.shift()!;
      while (
        queue.length >= backlog &&
        queue[0].id === next.id &&
        next.data.length < 1 << 16
      ) {
        const more = queue.shift()!;
        const joined = new Uint8Array(next.data.length + more.data.length);
        joined.set(next.data);
        joined.set(more.data, next.data.length);
        next.data = joined;
        next.done.push(...more.done);
      }
      try {
        await client.write({ id: next.id, data: next.data });
        next.done.forEach((d) => d.resolve());
      } catch (error) {
        next.done.forEach((d) => d.reject(error));
      }
    }
    sending = false;
  }

  return Object.freeze({
    open: async (input: {
      name: string;
      cols: number;
      rows: number;
      command?: string[];
      directory?: string;
    }) => {
      const res = await client.open(input);
      const t = res.terminal!;
      return {
        attached: res.attached,
        end: res.endOffset,
        terminal: {
          id: t.id,
          name: t.name,
          command: t.command,
          directory: t.directory,
          cols: t.cols,
          rows: t.rows,
          exited: t.exited,
          exitCode: t.exitCode,
        },
      };
    },
    write: (id: string, data: string | Uint8Array) =>
      new Promise<void>((resolve, reject) => {
        const bytes = typeof data === "string" ? encoder.encode(data) : data;
        queue.push({ id, data: bytes, done: [{ resolve, reject }] });
        if (!sending) void drain();
      }),
    resize: async (id: string, cols: number, rows: number) => {
      await client.resize({ id, cols, rows });
    },
    read: async (
      id: string,
      offset: bigint,
      onData: (offset: bigint, data: Uint8Array) => void,
      signal: AbortSignal,
    ) => {
      try {
        for await (const res of client.read(
          { id, fromOffset: offset },
          { signal },
        )) {
          if (res.data.length) onData(res.offset, res.data);
          if (res.exited) return { exited: true, exitCode: res.exitCode };
        }
      } catch (error) {
        if (!signal.aborted || !(error instanceof ConnectError)) throw error;
      }
      return { exited: false, exitCode: 0 };
    },
    running: async () => {
      const res = await client.list({});
      return res.terminals.map((t) => t.name);
    },
    close: async (id: string) => {
      await client.close({ id });
    },
  } satisfies DjinnTerminal);
}
