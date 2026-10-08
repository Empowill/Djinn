// window.djinn over Connect, for the interface served by the djinn command (native window or browser).
// Electron's preload defines window.djinn first: this shim then leaves it alone.
import {
  Code,
  ConnectError,
  type Transport,
  createClient,
} from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import { UiService } from "../gen/ts/ui/v1/ui_pb";
import type { DjinnBridge, Project, RuntimeEvent } from "../src/types";
import { createFocus } from "./focus";
import { legacyExchange } from "./legacy-bridge";
import { createTerminal } from "./terminal";

// Methods the Go side does not serve yet. Each rejects with a clear error the interface shows.
const notAvailable = [
  "discoverProject",
  "cancelProjectDiscovery",
  "renderVisualization",
  "getProviderModels",
  "getMissionJournalPage",
  "selectDirectory",
  "startRun",
  "cancelRun",
  "steerRun",
  "getActions",
  "performAction",
  "loginProvider",
  "saveArtifact",
  "respondPermission",
] as const;
// getRuntimeSnapshot, getPendingPermissions and getMissionInteractions are left undefined, as the interface
// expects of a bridge without them: if they rejected, the startup would fail and suspend autosave.

function bridgeError(code: string, message: string): Error {
  return Object.assign(new Error(message), { code });
}

// The same errors as the preload: an Error with a message to show and a string code.
async function call<T>(run: () => Promise<T>): Promise<T> {
  try {
    return await run();
  } catch (error) {
    if (!(error instanceof ConnectError)) throw error;
    const code = Code[error.code].replace(
      /[A-Z]/g,
      (c, i) => (i ? "_" : "") + c.toLowerCase(),
    );
    throw bridgeError(code, error.rawMessage);
  }
}

export function createDjinn(
  transport: Transport,
  pickFile?: () => Promise<File | null>,
): DjinnBridge {
  const ui = createClient(UiService, transport);
  const listeners = new Set<(event: RuntimeEvent) => void>();
  const missing = Object.fromEntries(
    notAvailable.map((name) => [
      name,
      () =>
        Promise.reject(
          bridgeError("not_available", `${name} is not available yet`),
        ),
    ]),
  ) as Record<(typeof notAvailable)[number], () => Promise<never>>;

  const exchange = legacyExchange(transport, bridgeError, pickFile);

  return Object.freeze({
    ...missing,
    // Import and export of a wish, through a bridge to the interface's mission model until it reads the Go
    // services itself.
    importSession: () => call(exchange.importSession),
    exportSession: (session: unknown) =>
      call(() => exchange.exportSession(session)),
    getEnvironment: () =>
      call(async () => {
        const env = await ui.getEnvironment({});
        return {
          platform: env.platform,
          appVersion: env.version,
          providers: env.providers.map((p) => ({
            id: p.id as "codex" | "claude",
            name: p.name,
            available: p.available,
            authenticated: null,
            command: p.command,
          })),
        };
      }),
    loadState: () =>
      call(async () => {
        const { stateJson } = await ui.loadState({});
        if (!stateJson) return null;
        try {
          return JSON.parse(stateJson);
        } catch {
          throw bridgeError(
            "invalid_state",
            "The saved workspace is malformed; automatic saving is suspended",
          );
        }
      }),
    saveState: (state: unknown) =>
      call(async () => {
        await ui.saveState({ stateJson: JSON.stringify(state) });
        return { saved: true };
      }),
    validateProject: (project: Project) =>
      call(async () => {
        const { directory } = await ui.validateProject({
          directory: project.directory,
        });
        return { ...project, directory };
      }),
    openExternal: (url: string) =>
      call(async () => {
        const opened = await ui.openExternal({ url });
        return { opened: true, url: opened.url };
      }),
    notifyQuestion: (input: {
      taskId: string;
      questionId: string;
      title: string;
      body: string;
    }) =>
      call(async () => {
        const { taskId, questionId, title, body } = input;
        const { shown } = await ui.notifyQuestion({
          taskId,
          questionId,
          title,
          body,
        });
        return shown ? { shown } : { shown, reason: "unsupported" };
      }),
    // Nothing is sent yet: the events will come from UiService.Watch.
    onEvent: (callback: (event: RuntimeEvent) => void) => {
      if (typeof callback !== "function") return () => undefined;
      listeners.add(callback);
      return () => void listeners.delete(callback);
    },
  } satisfies DjinnBridge);
}

// djinnTransport reaches the djinn server at baseUrl in the binary Protobuf format: against JSON, it encodes and
// decodes a saved workspace about three times faster on both ends, and halves the round trip of a large one in the
// window (docs/transport.md). fetch replaces the global one, for the tests.
export function djinnTransport(
  baseUrl: string,
  fetch?: typeof globalThis.fetch,
): Transport {
  return createConnectTransport({ baseUrl, useBinaryFormat: true, fetch });
}

// Only the built interface, which the djinn command serves: the Vite dev server keeps the browser preview, which
// the end-to-end tests drive. The API is on the same origin, and fetch sends the session cookie on its own.
if (import.meta.env.PROD && typeof window !== "undefined") {
  // Electron's preload has no terminals: only a page djinn serves gets one.
  if (!window.djinn) {
    const transport = djinnTransport(window.location.origin);
    window.djinn = createDjinn(transport);
    window.djinnTerminal = createTerminal(transport);
    window.djinnFocus = createFocus(transport);
  }
}
