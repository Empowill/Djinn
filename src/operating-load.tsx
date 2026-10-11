import React, { useEffect, useSyncExternalStore } from "react";

import { LoadNotch } from "../gen/ts/djinn/v1/load_pb";
import type { Clients } from "./data/client";
import { type Djinn, useDjinn } from "./data/djinn";
import { t } from "./i18n";
import { memory } from "./usage";
import "./operating-load.css";

interface LoadData {
  notch: LoadNotch;
  auto: boolean;
  chosenNotch: LoadNotch;
  engagedMemoryBytes: bigint;
  workerMemoryBytes: bigint;
  memoryTotalBytes: bigint;
  memoryAvailableBytes: bigint;
  workerForecastBytes: bigint;
  bindingResource: string;
  cpuSlots: number;
  memorySlots: number;
  baseSlots: number;
  runningWorkers: number;
  pausedWorkers: number;
}

class LoadManager {
  private data: LoadData = {
    notch: LoadNotch.MEDIUM,
    auto: false,
    chosenNotch: LoadNotch.MEDIUM,
    engagedMemoryBytes: 0n,
    workerMemoryBytes: 0n,
    memoryTotalBytes: 0n,
    memoryAvailableBytes: 0n,
    workerForecastBytes: 0n,
    bindingResource: "cpu",
    cpuSlots: 1,
    memorySlots: 1,
    baseSlots: 1,
    runningWorkers: 0,
    pausedWorkers: 0,
  };
  private previewNotch: LoadNotch | null = null;
  private listeners = new Set<() => void>();
  private abort: AbortController | null = null;
  private clients: Clients | null = null;
  private djinn: Djinn | null = null;
  private unsubscribeLoad: (() => void) | null = null;
  private stopTimer: ReturnType<typeof setTimeout> | null = null;

  setDjinn(djinn: Djinn) {
    if (this.djinn === djinn) return;
    this.djinn = djinn;
    this.clients = djinn.clients;
    if (this.stopTimer) {
      clearTimeout(this.stopTimer);
      this.stopTimer = null;
    }
    if (this.listeners.size > 0) {
      this.restart();
    }
  }

  setClients(clients: Clients) {
    if (this.clients === clients) return;
    this.clients = clients;
    if (this.stopTimer) {
      clearTimeout(this.stopTimer);
      this.stopTimer = null;
    }
    if (this.listeners.size > 0) {
      this.restart();
    }
  }

  setSnapshot(partial: Partial<LoadData>) {
    this.data = { ...this.data, ...partial };
    this.notify();
  }

  getSnapshot = (): LoadData => this.data;

  getPreviewNotch = (): LoadNotch | null => this.previewNotch;

  setPreviewNotch(notch: LoadNotch | null) {
    if (this.previewNotch === notch) return;
    this.previewNotch = notch;
    this.notify();
  }

  subscribe = (listener: () => void) => {
    if (this.stopTimer) {
      clearTimeout(this.stopTimer);
      this.stopTimer = null;
    }
    this.listeners.add(listener);
    if (this.listeners.size === 1 && !this.abort) {
      this.start();
    }
    return () => {
      this.listeners.delete(listener);
      if (this.listeners.size === 0) {
        this.stopTimer = setTimeout(() => {
          this.stopTimer = null;
          if (this.listeners.size === 0) {
            this.stop();
          }
        }, 1000);
      }
    };
  };

  private restart() {
    this.stop();
    this.start();
  }

  private start() {
    if (!this.clients && !this.djinn) return;
    this.abort = new AbortController();
    const signal = this.abort.signal;

    if (this.clients) {
      void this.clients.load
        .get({}, { signal })
        .then((res) => {
          if (signal.aborted) return;
          const auto = Boolean(res.auto);
          const notch = res.notch || this.data.notch;
          const chosenNotch = res.chosenNotch || this.data.chosenNotch || notch;
          const baseSlots = res.baseSlots || this.data.baseSlots;
          const runningWorkers = res.runningWorkers ?? this.data.runningWorkers;
          const pausedWorkers = res.pausedWorkers ?? this.data.pausedWorkers;
          const memoryTotalBytes =
            res.memoryTotalBytes || this.data.memoryTotalBytes;
          const memoryAvailableBytes =
            res.memoryAvailableBytes || this.data.memoryAvailableBytes;
          const workerForecastBytes =
            res.workerForecastBytes || this.data.workerForecastBytes;
          const bindingResource =
            res.bindingResource || this.data.bindingResource;
          const cpuSlots = res.cpuSlots || this.data.cpuSlots;
          const memorySlots = res.memorySlots || this.data.memorySlots;
          this.data = {
            ...this.data,
            notch,
            auto,
            chosenNotch,
            baseSlots,
            runningWorkers,
            pausedWorkers,
            memoryTotalBytes,
            memoryAvailableBytes,
            workerForecastBytes,
            bindingResource,
            cpuSlots,
            memorySlots,
          };
          this.notify();
        })
        .catch(() => {});
    }

    if (this.djinn?.load) {
      this.unsubscribeLoad = this.djinn.load.subscribe((res) => {
        if (signal.aborted) return;
        this.data = {
          notch: res.notch || this.data.notch,
          auto: Boolean(res.auto),
          chosenNotch: res.chosenNotch || this.data.chosenNotch || res.notch,
          engagedMemoryBytes: res.engagedMemoryBytes,
          workerMemoryBytes: res.workerMemoryBytes,
          memoryTotalBytes: res.memoryTotalBytes,
          memoryAvailableBytes: res.memoryAvailableBytes,
          workerForecastBytes:
            res.workerForecastBytes || this.data.workerForecastBytes,
          bindingResource: res.bindingResource || this.data.bindingResource,
          cpuSlots: res.cpuSlots || this.data.cpuSlots,
          memorySlots: res.memorySlots || this.data.memorySlots,
          baseSlots: res.baseSlots || this.data.baseSlots,
          runningWorkers: res.runningWorkers ?? this.data.runningWorkers,
          pausedWorkers: res.pausedWorkers ?? this.data.pausedWorkers,
        };
        this.notify();
      });
      return;
    }

    void (async () => {
      while (!signal.aborted) {
        try {
          for await (const res of this.clients!.load.watch({}, { signal })) {
            if (signal.aborted) return;
            this.data = {
              notch: res.notch || this.data.notch,
              auto: Boolean(res.auto),
              chosenNotch:
                res.chosenNotch || this.data.chosenNotch || res.notch,
              engagedMemoryBytes: res.engagedMemoryBytes,
              workerMemoryBytes: res.workerMemoryBytes,
              memoryTotalBytes: res.memoryTotalBytes,
              memoryAvailableBytes: res.memoryAvailableBytes,
              workerForecastBytes:
                res.workerForecastBytes || this.data.workerForecastBytes,
              bindingResource: res.bindingResource || this.data.bindingResource,
              cpuSlots: res.cpuSlots || this.data.cpuSlots,
              memorySlots: res.memorySlots || this.data.memorySlots,
              baseSlots: res.baseSlots || this.data.baseSlots,
              runningWorkers: res.runningWorkers ?? this.data.runningWorkers,
              pausedWorkers: res.pausedWorkers ?? this.data.pausedWorkers,
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
    if (this.stopTimer) {
      clearTimeout(this.stopTimer);
      this.stopTimer = null;
    }
    if (this.unsubscribeLoad) {
      this.unsubscribeLoad();
      this.unsubscribeLoad = null;
    }
    this.abort?.abort();
    this.abort = null;
  }

  private notify() {
    for (const l of this.listeners) {
      l();
    }
  }

  async setNotch(notch: LoadNotch) {
    if (notch === LoadNotch.AUTO) {
      if (this.data.auto) return;
      this.data = { ...this.data, auto: true };
    } else {
      if (!this.data.auto && notch === this.data.notch) return;
      this.data = { ...this.data, notch, chosenNotch: notch, auto: false };
    }
    this.notify();
    if (this.clients) {
      try {
        const res = await this.clients.load.set({ notch });
        if (res.notch) {
          this.data = {
            ...this.data,
            notch: res.notch,
            chosenNotch: res.chosenNotch || this.data.chosenNotch,
            auto: Boolean(res.auto),
          };
          this.notify();
        }
      } catch {
        // Ignored; stream update will reconcile.
      }
    }
  }
}

const loadManager = new LoadManager();

const NOTCHES: readonly LoadNotch[] = [
  LoadNotch.MINIMAL,
  LoadNotch.MEDIUM,
  LoadNotch.HIGH,
  LoadNotch.MAX,
  LoadNotch.OVERCLOCK,
];

function notchLabel(notch: LoadNotch): string {
  switch (notch) {
    case LoadNotch.MINIMAL:
      return t("load.notch.minimal");
    case LoadNotch.MEDIUM:
      return t("load.notch.medium");
    case LoadNotch.HIGH:
      return t("load.notch.high");
    case LoadNotch.MAX:
      return t("load.notch.max");
    case LoadNotch.OVERCLOCK:
      return t("load.notch.overclock");
    case LoadNotch.AUTO:
      return t("load.notch.auto");
    default:
      return "";
  }
}

function notchSlug(notch: LoadNotch): string {
  switch (notch) {
    case LoadNotch.MINIMAL:
      return "minimal";
    case LoadNotch.MEDIUM:
      return "medium";
    case LoadNotch.HIGH:
      return "high";
    case LoadNotch.MAX:
      return "max";
    case LoadNotch.OVERCLOCK:
      return "overclock";
    case LoadNotch.AUTO:
      return "auto";
    default:
      return "medium";
  }
}

function notchShare(notch: LoadNotch): number {
  switch (notch) {
    case LoadNotch.MINIMAL:
      return 0.2;
    case LoadNotch.MEDIUM:
      return 0.5;
    case LoadNotch.HIGH:
      return 0.8;
    case LoadNotch.MAX:
      return 1.0;
    case LoadNotch.OVERCLOCK:
      return 1.5;
    default:
      return 0.5;
  }
}

function notchLowPriority(notch: LoadNotch): boolean {
  return notch === LoadNotch.MINIMAL || notch === LoadNotch.MEDIUM;
}

function computeWorkers(notch: LoadNotch, baseSlots: number): number {
  const share = notchShare(notch);
  return Math.max(1, Math.round(share * (baseSlots || 1)));
}

const MEMORY_RESERVE = 2n * 1024n * 1024n * 1024n; // 2 GiB

function computeEngagedLimit(
  notch: LoadNotch,
  availableBytes: bigint,
  totalBytes: bigint,
): bigint {
  const ref = availableBytes > 0n ? availableBytes : totalBytes;
  if (ref <= MEMORY_RESERVE) return 0n;
  const usable = ref - MEMORY_RESERVE;
  const share = notchShare(notch);
  let limit = BigInt(Math.round(Number(usable) * share));
  if (limit > ref) {
    limit = ref;
  }
  return limit;
}

function notchTooltip(
  notch: LoadNotch,
  baseSlots: number,
  chosenNotch: LoadNotch,
): string {
  switch (notch) {
    case LoadNotch.MINIMAL:
      return t("load.tooltip.minimal", {
        workers: computeWorkers(LoadNotch.MINIMAL, baseSlots),
      });
    case LoadNotch.MEDIUM:
      return t("load.tooltip.medium", {
        workers: computeWorkers(LoadNotch.MEDIUM, baseSlots),
      });
    case LoadNotch.HIGH:
      return t("load.tooltip.high", {
        workers: computeWorkers(LoadNotch.HIGH, baseSlots),
      });
    case LoadNotch.MAX:
      return t("load.tooltip.max", {
        workers: computeWorkers(LoadNotch.MAX, baseSlots),
      });
    case LoadNotch.OVERCLOCK:
      return t("load.tooltip.overclock", {
        workers: computeWorkers(LoadNotch.OVERCLOCK, baseSlots),
      });
    case LoadNotch.AUTO:
      return t("load.tooltip.auto", { chosen: notchLabel(chosenNotch) });
    default:
      return "";
  }
}

function useAnimatedNumber(target: number, durationMs = 300): number {
  const [current, setCurrent] = React.useState(target);
  const currentRef = React.useRef(current);
  currentRef.current = current;
  const frameRef = React.useRef<number | null>(null);
  const startValRef = React.useRef(target);
  const startTimeRef = React.useRef<number | null>(null);

  React.useEffect(() => {
    if (
      typeof window === "undefined" ||
      window.matchMedia?.("(prefers-reduced-motion: reduce)").matches
    ) {
      setCurrent(target);
      return;
    }
    const from = currentRef.current;
    if (from === target) return;

    startValRef.current = from;
    startTimeRef.current = null;

    const step = (time: number) => {
      if (startTimeRef.current === null) startTimeRef.current = time;
      const elapsed = time - startTimeRef.current;
      const progress = Math.min(1, elapsed / durationMs);
      const ease = 1 - Math.pow(1 - progress, 2);
      const next = startValRef.current + (target - startValRef.current) * ease;
      setCurrent(next);
      if (progress < 1) {
        frameRef.current = requestAnimationFrame(step);
      }
    };

    frameRef.current = requestAnimationFrame(step);
    return () => {
      if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
    };
  }, [target, durationMs]);

  return current;
}

function useAnimatedBigInt(target: bigint, durationMs = 300): bigint {
  const numTarget = Number(target);
  const animated = useAnimatedNumber(numTarget, durationMs);
  return BigInt(Math.round(animated));
}

const OperatingLoadSlider = React.memo(function OperatingLoadSlider() {
  const data = useSyncExternalStore(
    loadManager.subscribe,
    loadManager.getSnapshot,
    loadManager.getSnapshot,
  );
  const preview = useSyncExternalStore(
    loadManager.subscribe,
    loadManager.getPreviewNotch,
    loadManager.getPreviewNotch,
  );

  const activeNotch = data.auto ? data.notch : data.notch;
  const displayNotch = preview ?? activeNotch;
  const sliderIndex = Math.max(0, NOTCHES.indexOf(displayNotch));
  const chosen = data.chosenNotch || LoadNotch.MEDIUM;
  const allowedWorkers = computeWorkers(displayNotch, data.baseSlots);

  const valuetext = data.auto
    ? t("load.aria_valuetext", {
        notch: t("load.effective", { notch: notchLabel(data.notch) }),
        pct: Math.round(notchShare(data.notch) * 100),
        count: allowedWorkers,
      })
    : t("load.aria_valuetext", {
        notch: notchLabel(displayNotch),
        pct: Math.round(notchShare(displayNotch) * 100),
        count: allowedWorkers,
      });

  const baseTooltip = data.auto
    ? notchTooltip(LoadNotch.AUTO, data.baseSlots, chosen)
    : notchTooltip(displayNotch, data.baseSlots, chosen);
  const sliderTooltip =
    data.memoryTotalBytes > 0n || data.memoryAvailableBytes > 0n
      ? t("load.tooltip_detail", {
          base: baseTooltip,
          engaged: memory(data.engagedMemoryBytes),
          limit: memory(
            computeEngagedLimit(
              displayNotch,
              data.memoryAvailableBytes,
              data.memoryTotalBytes,
            ),
          ),
          forecast: memory(
            data.workerForecastBytes > 0n
              ? data.workerForecastBytes
              : 1536n * 1024n * 1024n,
          ),
        })
      : baseTooltip;

  const handleTrackClick = (e: React.MouseEvent<HTMLDivElement>) => {
    if ((e.target as HTMLElement)?.tagName === "BUTTON") return;
    const rect = e.currentTarget?.getBoundingClientRect?.() ?? {
      left: 0,
      width: 0,
    };
    if (rect.width <= 0) return;
    const ratio = Math.max(
      0,
      Math.min(1, (e.clientX - rect.left) / rect.width),
    );
    const idx = Math.min(4, Math.max(0, Math.round(ratio * 4)));
    loadManager.setPreviewNotch(null);
    void loadManager.setNotch(NOTCHES[idx] ?? LoadNotch.MEDIUM);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    let nextIdx: number;
    if (e.key === "ArrowRight" || e.key === "ArrowUp") {
      nextIdx = Math.min(4, sliderIndex + 1);
    } else if (e.key === "ArrowLeft" || e.key === "ArrowDown") {
      nextIdx = Math.max(0, sliderIndex - 1);
    } else if (e.key === "Home") {
      nextIdx = 0;
    } else if (e.key === "End") {
      nextIdx = 4;
    } else {
      return;
    }
    e.preventDefault();
    loadManager.setPreviewNotch(null);
    void loadManager.setNotch(NOTCHES[nextIdx] ?? LoadNotch.MEDIUM);
  };

  return (
    <div
      className="operating-load-slider"
      data-notch={notchSlug(displayNotch)}
      onClick={handleTrackClick}
    >
      <input
        type="range"
        role="slider"
        min={1}
        max={5}
        step={1}
        value={sliderIndex + 1}
        onInput={(e) => {
          const idx = Number(e.currentTarget.value) - 1;
          loadManager.setPreviewNotch(NOTCHES[idx] ?? LoadNotch.MEDIUM);
        }}
        onChange={(e) => {
          const idx = Number(e.currentTarget.value) - 1;
          loadManager.setPreviewNotch(null);
          void loadManager.setNotch(NOTCHES[idx] ?? LoadNotch.MEDIUM);
        }}
        onKeyDown={handleKeyDown}
        onBlur={() => loadManager.setPreviewNotch(null)}
        aria-label={t("load.label")}
        aria-valuemin={1}
        aria-valuemax={5}
        aria-valuenow={sliderIndex + 1}
        aria-valuetext={valuetext}
        title={sliderTooltip}
        className="operating-load-input"
      />
      <div className="operating-load-notches">
        {NOTCHES.map((n) => (
          <button
            key={n}
            type="button"
            tabIndex={-1}
            data-notch={notchSlug(n)}
            className={`operating-load-notch ${!data.auto && activeNotch === n ? "active" : ""}`}
            onMouseEnter={() => loadManager.setPreviewNotch(n)}
            onMouseLeave={() => loadManager.setPreviewNotch(null)}
            onClick={(e) => {
              e.stopPropagation();
              loadManager.setPreviewNotch(null);
              void loadManager.setNotch(n);
            }}
            title={notchTooltip(n, data.baseSlots, chosen)}
          >
            {notchLabel(n)}
          </button>
        ))}
      </div>
    </div>
  );
});

const OperatingLoadAuto = React.memo(function OperatingLoadAuto() {
  const data = useSyncExternalStore(
    loadManager.subscribe,
    loadManager.getSnapshot,
    loadManager.getSnapshot,
  );
  const chosen = data.chosenNotch || LoadNotch.MEDIUM;

  return (
    <div className="operating-load-auto">
      <button
        type="button"
        tabIndex={-1}
        data-notch="auto"
        className={`operating-load-notch ${data.auto ? "active" : ""}`}
        onClick={() => void loadManager.setNotch(LoadNotch.AUTO)}
        title={notchTooltip(LoadNotch.AUTO, data.baseSlots, chosen)}
      >
        {t("load.notch.auto")}
      </button>
      {data.auto && (
        <span
          className="operating-load-effective"
          data-effective={notchSlug(data.notch)}
          title={notchTooltip(LoadNotch.AUTO, data.baseSlots, chosen)}
        >
          ({notchLabel(data.notch)})
        </span>
      )}
    </div>
  );
});

const OperatingLoadForecast = React.memo(function OperatingLoadForecast() {
  const data = useSyncExternalStore(
    loadManager.subscribe,
    loadManager.getSnapshot,
    loadManager.getSnapshot,
  );
  const preview = useSyncExternalStore(
    loadManager.subscribe,
    loadManager.getPreviewNotch,
    loadManager.getPreviewNotch,
  );

  const displayNotch = preview ?? data.notch;
  const allowedWorkers = computeWorkers(displayNotch, data.baseSlots);
  const limitMemory = computeEngagedLimit(
    displayNotch,
    data.memoryAvailableBytes,
    data.memoryTotalBytes,
  );

  const animatedWorkers = useAnimatedNumber(allowedWorkers, 300);
  const animatedEngaged = useAnimatedBigInt(limitMemory, 300);

  if (data.memoryTotalBytes === 0n && data.memoryAvailableBytes === 0n)
    return null;

  const priorityText = notchLowPriority(displayNotch)
    ? t("load.priority_low")
    : t("load.priority_standard");

  const maxPossibleWorkers = Math.max(
    1,
    Math.round((data.baseSlots || 1) * 1.5),
  );
  const cpuLimitPct = Math.min(
    100,
    Math.max(0, Math.round((allowedWorkers / maxPossibleWorkers) * 100)),
  );
  const cpuUsedPct = Math.min(
    100,
    Math.max(0, Math.round((data.runningWorkers / maxPossibleWorkers) * 100)),
  );

  const refMem =
    data.memoryAvailableBytes > 0n
      ? data.memoryAvailableBytes
      : data.memoryTotalBytes;
  const maxScaleBytes = (refMem * 3n) / 2n;
  const memLimitPct =
    maxScaleBytes > 0n
      ? Math.min(100, Number((limitMemory * 100n) / maxScaleBytes))
      : 0;
  const memUsedPct =
    maxScaleBytes > 0n
      ? Math.min(100, Number((data.workerMemoryBytes * 100n) / maxScaleBytes))
      : 0;

  const forecastBytes =
    data.workerForecastBytes > 0n
      ? data.workerForecastBytes
      : 1536n * 1024n * 1024n; // 1.5 GiB default

  const isMemoryBound =
    data.bindingResource === "memory" ||
    (data.memorySlots > 0 &&
      data.cpuSlots > 0 &&
      data.memorySlots < data.cpuSlots);

  const bindingKey = isMemoryBound
    ? "load.limited_by_memory"
    : "load.limited_by_cpu";

  return (
    <div
      className="operating-load-forecast operating-load-memory"
      data-notch={notchSlug(displayNotch)}
      role="status"
      aria-live="polite"
    >
      <div className="operating-load-forecast-binding">
        {t(bindingKey, {
          count: Math.round(animatedWorkers),
          forecast: memory(forecastBytes),
        })}
      </div>
      <div className="operating-load-forecast-row">
        <span className="operating-load-forecast-label">
          {t("load.forecast_cpu", {
            workers: Math.round(animatedWorkers),
            priority: priorityText,
          })}
          {data.pausedWorkers > 0 && (
            <span className="operating-load-forecast-paused">
              {" · "}
              {t("load.forecast_paused", { count: data.pausedWorkers })}
            </span>
          )}
        </span>
        <div
          className="operating-load-bar"
          title={`CPU: ${data.runningWorkers} / ${allowedWorkers}${data.pausedWorkers > 0 ? ` (${data.pausedWorkers} paused)` : ""}`}
        >
          <div
            className="operating-load-bar-limit"
            style={{ width: `${cpuLimitPct}%` }}
          />
          <div
            className="operating-load-bar-used"
            style={{ width: `${cpuUsedPct}%` }}
          />
        </div>
      </div>
      <div className="operating-load-forecast-row">
        <span className="operating-load-forecast-label">
          {t("load.forecast_memory", {
            engaged: memory(animatedEngaged),
            total: memory(refMem),
            used: memory(data.workerMemoryBytes),
          })}
        </span>
        <div
          className="operating-load-bar"
          title={`Memory: ${memory(data.workerMemoryBytes)} / ${memory(limitMemory)}`}
        >
          <div
            className="operating-load-bar-limit"
            style={{ width: `${memLimitPct}%` }}
          />
          <div
            className="operating-load-bar-used"
            style={{ width: `${memUsedPct}%` }}
          />
        </div>
      </div>
    </div>
  );
});

export const OperatingLoad = React.memo(function OperatingLoad() {
  const djinn = useDjinn();

  if (djinn) {
    loadManager.setDjinn(djinn);
  }

  useEffect(() => {
    if (djinn) {
      loadManager.setDjinn(djinn);
    }
  }, [djinn]);

  return (
    <div className="operating-load" role="group" aria-label={t("load.label")}>
      <OperatingLoadSlider />
      <OperatingLoadAuto />
      <OperatingLoadForecast />
    </div>
  );
});

export {
  OperatingLoadSlider,
  OperatingLoadAuto,
  OperatingLoadForecast,
  loadManager,
  NOTCHES,
  notchLabel,
  notchSlug,
  notchShare,
  computeWorkers,
  computeEngagedLimit,
};
