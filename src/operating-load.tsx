import React, { useEffect, useSyncExternalStore } from "react";

import { LoadNotch } from "../gen/ts/djinn/v1/load_pb";
import type { Clients } from "./data/client";
import { useClients } from "./data/djinn";
import { t } from "./i18n";
import { memory } from "./usage";
import "./operating-load.css";

interface LoadData {
  notch: LoadNotch;
  engagedMemoryBytes: bigint;
  workerMemoryBytes: bigint;
  memoryTotalBytes: bigint;
  memoryAvailableBytes: bigint;
}

class LoadManager {
  private data: LoadData = {
    notch: LoadNotch.MEDIUM,
    engagedMemoryBytes: 0n,
    workerMemoryBytes: 0n,
    memoryTotalBytes: 0n,
    memoryAvailableBytes: 0n,
  };
  private listeners = new Set<() => void>();
  private abort: AbortController | null = null;
  private clients: Clients | null = null;

  setClients(clients: Clients) {
    if (this.clients === clients) return;
    this.clients = clients;
    if (this.listeners.size > 0) {
      this.restart();
    }
  }

  getNotch = (): LoadNotch => this.data.notch;

  getMemoryKey = (): string =>
    `${this.data.workerMemoryBytes}/${this.data.engagedMemoryBytes}/${this.data.memoryTotalBytes}`;

  getMemoryData = (): { worker: bigint; engaged: bigint; total: bigint } => ({
    worker: this.data.workerMemoryBytes,
    engaged: this.data.engagedMemoryBytes,
    total: this.data.memoryTotalBytes,
  });

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    if (this.listeners.size === 1) {
      this.start();
    }
    return () => {
      this.listeners.delete(listener);
      if (this.listeners.size === 0) {
        this.stop();
      }
    };
  };

  private restart() {
    this.stop();
    this.start();
  }

  private start() {
    if (!this.clients) return;
    this.abort = new AbortController();
    const signal = this.abort.signal;

    void this.clients.load
      .get({}, { signal })
      .then((res) => {
        if (signal.aborted) return;
        if (res.notch && res.notch !== this.data.notch) {
          this.data = { ...this.data, notch: res.notch };
          this.notify();
        }
      })
      .catch(() => {});

    void (async () => {
      while (!signal.aborted) {
        try {
          for await (const res of this.clients!.load.watch({}, { signal })) {
            if (signal.aborted) return;
            this.data = {
              notch: res.notch || this.data.notch,
              engagedMemoryBytes: res.engagedMemoryBytes,
              workerMemoryBytes: res.workerMemoryBytes,
              memoryTotalBytes: res.memoryTotalBytes,
              memoryAvailableBytes: res.memoryAvailableBytes,
            };
            this.notify();
          }
        } catch {
          if (signal.aborted) return;
        }
        await new Promise((r) => setTimeout(r, 1000));
      }
    })();
  }

  private stop() {
    this.abort?.abort();
    this.abort = null;
  }

  private notify() {
    for (const l of this.listeners) {
      l();
    }
  }

  async setNotch(notch: LoadNotch) {
    if (notch === this.data.notch) return;
    this.data = { ...this.data, notch };
    this.notify();
    if (this.clients) {
      try {
        await this.clients.load.set({ notch });
      } catch {
        // Ignored; stream update will reconcile.
      }
    }
  }
}

const loadManager = new LoadManager();

const NOTCHES: readonly LoadNotch[] = [
  LoadNotch.MINIMAL,
  LoadNotch.LIGHT,
  LoadNotch.MEDIUM,
  LoadNotch.HIGH,
  LoadNotch.MAX,
];

function notchLabel(notch: LoadNotch): string {
  switch (notch) {
    case LoadNotch.MINIMAL:
      return t("load.notch.minimal");
    case LoadNotch.LIGHT:
      return t("load.notch.light");
    case LoadNotch.MEDIUM:
      return t("load.notch.medium");
    case LoadNotch.HIGH:
      return t("load.notch.high");
    case LoadNotch.MAX:
      return t("load.notch.max");
    default:
      return "";
  }
}

function notchTooltip(notch: LoadNotch): string {
  switch (notch) {
    case LoadNotch.MINIMAL:
      return t("load.tooltip.minimal");
    case LoadNotch.LIGHT:
      return t("load.tooltip.light");
    case LoadNotch.MEDIUM:
      return t("load.tooltip.medium");
    case LoadNotch.HIGH:
      return t("load.tooltip.high");
    case LoadNotch.MAX:
      return t("load.tooltip.max");
    default:
      return "";
  }
}

function notchSlug(notch: LoadNotch): string {
  switch (notch) {
    case LoadNotch.MINIMAL:
      return "minimal";
    case LoadNotch.LIGHT:
      return "light";
    case LoadNotch.MEDIUM:
      return "medium";
    case LoadNotch.HIGH:
      return "high";
    case LoadNotch.MAX:
      return "max";
    default:
      return "";
  }
}

const OperatingLoadSlider = React.memo(function OperatingLoadSlider() {
  const notch = useSyncExternalStore(
    loadManager.subscribe,
    loadManager.getNotch,
    loadManager.getNotch,
  );

  return (
    <div className="operating-load-slider">
      <input
        type="range"
        min={1}
        max={5}
        step={1}
        value={notch}
        onChange={(e) =>
          void loadManager.setNotch(Number(e.target.value) as LoadNotch)
        }
        aria-label={t("load.label")}
        aria-valuetext={notchLabel(notch)}
        title={notchTooltip(notch)}
        className="operating-load-input"
      />
      <div className="operating-load-notches">
        {NOTCHES.map((n) => (
          <button
            key={n}
            type="button"
            tabIndex={-1}
            data-notch={notchSlug(n)}
            className={`operating-load-notch ${notch === n ? "active" : ""}`}
            onClick={() => void loadManager.setNotch(n)}
            title={notchTooltip(n)}
          >
            {notchLabel(n)}
          </button>
        ))}
      </div>
    </div>
  );
});

const OperatingLoadMemory = React.memo(function OperatingLoadMemory() {
  useSyncExternalStore(
    loadManager.subscribe,
    loadManager.getMemoryKey,
    loadManager.getMemoryKey,
  );
  const { worker, engaged, total } = loadManager.getMemoryData();
  if (total === 0n) return null;

  return (
    <div
      className="operating-load-memory"
      title={t("load.memory_title", {
        worker: memory(worker),
        engaged: memory(engaged),
        total: memory(total),
      })}
    >
      <span>
        {t("load.memory", {
          worker: memory(worker),
          engaged: memory(engaged),
          total: memory(total),
        })}
      </span>
    </div>
  );
});

export const OperatingLoad = React.memo(function OperatingLoad() {
  const clients = useClients();

  useEffect(() => {
    loadManager.setClients(clients);
  }, [clients]);

  return (
    <div className="operating-load" role="group" aria-label={t("load.label")}>
      <OperatingLoadSlider />
      <OperatingLoadMemory />
    </div>
  );
});
