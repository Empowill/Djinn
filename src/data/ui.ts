// What the window is asked to show (UiService.Watch), whether a newer Djinn waits,
// and the operating load, united in a single stream.
import { Code, type Transport, createClient } from "@connectrpc/connect";

import {
  LoadService,
  type LoadServiceWatchResponse,
} from "../../gen/ts/djinn/v1/load_pb";
import { type DjinnFocus, type Focus, none as noneFocus } from "./focus";
import { type DjinnUpdate, type UpdateState } from "./update";
import {
  UiService,
  type UiServiceWatchShowResponse,
  type UiServiceWatchUpdateResponse,
} from "../../gen/ts/ui/v1/ui_pb";

const RECENT = 60_000;

export interface DjinnLoad {
  subscribe(callback: (load: LoadServiceWatchResponse) => void): () => void;
}

export interface DjinnUi {
  focus: DjinnFocus;
  update: DjinnUpdate;
  load: DjinnLoad;
}

export function createUi(transport: Transport, retry = 1000): DjinnUi {
  const ui = createClient(UiService, transport);

  const focusListeners = new Set<(focus: Focus) => void>();
  let lastFocus: { focus: Focus; at: number } | undefined;

  const updateListeners = new Set<(state: UpdateState) => void>();
  let lastUpdate: UpdateState | undefined;

  const loadListeners = new Set<(load: LoadServiceWatchResponse) => void>();
  let lastLoad: LoadServiceWatchResponse | undefined;

  let started = false;

  function handleShow(res: UiServiceWatchShowResponse) {
    if (!res.wishId && !res.terminal && !res.unknownLink) return;
    const focus: Focus = {
      wishId: res.wishId,
      terminal: res.terminal,
      tilasmId: res.tilasmId,
      unknownLink: res.unknownLink,
    };
    lastFocus = { focus, at: Date.now() };
    focusListeners.forEach((listener) => listener(focus));
  }

  function handleUpdate(res: UiServiceWatchUpdateResponse) {
    const state: UpdateState = {
      current: res.current,
      ready: res.ready,
      notResumed: [...res.notResumed],
      notesUrl: res.notesUrl,
      build: res.build && {
        wishTitle: res.build.wishTitle,
        project: res.build.project,
        branch: res.build.branch,
        sha: res.build.sha,
        tasks: [...res.build.tasks],
        changes: [...res.build.changes],
        summaries: res.build.summaries.map((s) => ({
          code: s.code,
          title: s.title,
          summary: s.summary,
        })),
      },
      installing: res.installing && {
        sha: res.installing.sha,
        step: res.installing.step,
        waiting: res.installing.waiting,
      },
    };
    lastUpdate = state;
    updateListeners.forEach((listener) => listener(state));
  }

  function handleLoad(res: LoadServiceWatchResponse) {
    lastLoad = res;
    loadListeners.forEach((listener) => listener(res));
  }

  async function watchUnified() {
    for (;;) {
      try {
        for await (const res of ui.watch({})) {
          if (res.show) {
            handleShow(res.show);
          }
          if (res.update) {
            handleUpdate(res.update);
          }
          if (res.load) {
            handleLoad(res.load);
          }
        }
      } catch (err: unknown) {
        const error = err as { code?: unknown; message?: string };
        if (
          error?.code === Code.Unimplemented ||
          error?.code === "unimplemented" ||
          String(err).includes("unimplemented") ||
          String(err).includes("not implemented")
        ) {
          await watchSeparate();
          return;
        }
      }
      await new Promise((r) => setTimeout(r, retry));
    }
  }

  async function watchSeparate() {
    const watchShow = async () => {
      for (;;) {
        if (focusListeners.size === 0) return;
        try {
          for await (const res of ui.watchShow({})) {
            handleShow(res);
          }
        } catch (err: unknown) {
          const error = err as { code?: unknown };
          if (error?.code === Code.Unimplemented) return;
        }
        await new Promise((r) => setTimeout(r, retry));
      }
    };

    const watchUpdate = async () => {
      for (;;) {
        if (updateListeners.size === 0) return;
        try {
          for await (const res of ui.watchUpdate({})) {
            handleUpdate(res);
          }
        } catch (err: unknown) {
          const error = err as { code?: unknown };
          if (error?.code === Code.Unimplemented) return;
        }
        await new Promise((r) => setTimeout(r, retry));
      }
    };

    const loadClient = createClient(LoadService, transport);
    const watchLoad = async () => {
      for (;;) {
        if (loadListeners.size === 0) return;
        try {
          for await (const res of loadClient.watch({})) {
            handleLoad(res);
          }
        } catch (err: unknown) {
          const error = err as { code?: unknown };
          if (error?.code === Code.Unimplemented) return;
        }
        await new Promise((r) => setTimeout(r, retry));
      }
    };

    if (focusListeners.size > 0) void watchShow();
    if (updateListeners.size > 0) void watchUpdate();
    if (loadListeners.size > 0) void watchLoad();
  }

  function ensureStarted() {
    if (!started) {
      started = true;
      void watchUnified();
    }
  }

  const focus: DjinnFocus = Object.freeze({
    subscribe: (callback: (focus: Focus) => void) => {
      focusListeners.add(callback);
      if (lastFocus && Date.now() - lastFocus.at < RECENT) {
        callback(lastFocus.focus);
      }
      ensureStarted();
      return () => void focusListeners.delete(callback);
    },
    show: (f: Partial<Focus>) => {
      const full = { ...noneFocus, ...f };
      focusListeners.forEach((listener) => listener(full));
    },
  });

  const update: DjinnUpdate = Object.freeze({
    subscribe: (callback: (state: UpdateState) => void) => {
      updateListeners.add(callback);
      if (lastUpdate) callback(lastUpdate);
      ensureStarted();
      return () => void updateListeners.delete(callback);
    },
    update: async (build?: string) => {
      const res = await ui.update({ build: build ?? "" });
      return {
        version: res.version,
        terminals: res.terminals,
        installed: res.installed,
      };
    },
  });

  const load: DjinnLoad = Object.freeze({
    subscribe: (callback: (load: LoadServiceWatchResponse) => void) => {
      loadListeners.add(callback);
      if (lastLoad) callback(lastLoad);
      ensureStarted();
      return () => void loadListeners.delete(callback);
    },
  });

  return Object.freeze({ focus, update, load });
}
