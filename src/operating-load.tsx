import React, { useEffect, useSyncExternalStore } from "react";

import {
  type LoadAutoReason,
  LoadAutoStep,
  LoadNotch,
} from "../gen/ts/djinn/v1/load_pb";
import { IfritState } from "../gen/ts/plan/v1/plan_pb";
import type { Clients } from "./data/client";
import { type Djinn, useDjinn, useOptionalData } from "./data/djinn";
import { ifritLit } from "./data/ifrit";
import { span } from "./data/format";
import { t } from "./i18n";
import { ToneIcon } from "./status";
import { still } from "./stillness";
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
  // What the scheduler lets run now at the notch in effect, and at each notch (W577): the counts the bar shows.
  slots: number;
  notchSlots: Partial<Record<LoadNotch, number>>;
  // Workers among runningWorkers started or resumed by hand, beyond the slots.
  forcedWorkers: number;
  // Whether lowering the notch pauses the newest workers above it, or lets them finish their run.
  lowerPauses: boolean;
  // Why auto mode runs at its notch (W253); undefined outside auto mode.
  autoReason?: LoadAutoReason;
}

// The fields the server's Get and Watch both send about the workers.
interface WorkerFields {
  slots: number;
  notchSlots: { notch: LoadNotch; slots: number }[];
  forcedWorkers: number;
  lowerPauses: boolean;
  autoReason?: LoadAutoReason;
}

// workerFields reads them, keeping what the page knew when the server sends no slot (an older one).
function workerFields(
  res: WorkerFields,
  data: LoadData,
): Pick<
  LoadData,
  "slots" | "notchSlots" | "forcedWorkers" | "lowerPauses" | "autoReason"
> {
  const notchSlots: Partial<Record<LoadNotch, number>> = {};
  for (const n of res.notchSlots) notchSlots[n.notch] = n.slots;
  return {
    slots: res.slots || data.slots,
    notchSlots: res.notchSlots.length ? notchSlots : data.notchSlots,
    forcedWorkers: res.forcedWorkers,
    lowerPauses: res.lowerPauses,
    autoReason: res.autoReason,
  };
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
    slots: 0,
    notchSlots: {},
    forcedWorkers: 0,
    lowerPauses: true,
  };
  private previewNotch: LoadNotch | null = null;
  // The Auto button hovered or focused: the preview explains Auto.
  private previewAuto = false;
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

  getPreviewAuto = (): boolean => this.previewAuto;

  setPreviewAuto(on: boolean) {
    if (this.previewAuto === on) return;
    this.previewAuto = on;
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
    // Once the stream has spoken, it alone says the load: it sends only what changed since its last message, so a Get
    // answered after it and kept would stay on screen. A Get read minimal after the stream's medium, the stream then
    // sent nothing when the notch went back to medium, and the window showed minimal for good (W248).
    let streamed = false;

    if (this.clients) {
      void this.clients.load
        .get({}, { signal })
        .then((res) => {
          if (signal.aborted || streamed) return;
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
            ...workerFields(res, this.data),
          };
          this.notify();
        })
        .catch(() => {});
    }

    if (this.djinn?.load) {
      this.unsubscribeLoad = this.djinn.load.subscribe((res) => {
        if (signal.aborted) return;
        streamed = true;
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
          ...workerFields(res, this.data),
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
            streamed = true;
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
              ...workerFields(res, this.data),
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

// workersAt is the most workers a notch lets run, as the scheduler weighs it (the server's notch slots): the window
// never computes it apart (W577). Before the server says, an estimate from the base slots.
function workersAt(
  data: Pick<LoadData, "notchSlots" | "baseSlots">,
  notch: LoadNotch,
): number {
  return data.notchSlots[notch] ?? computeWorkers(notch, data.baseSlots);
}

// overLimit says how many workers run beyond what a notch lets run, and why: started by hand (they stay), the rest
// finishing their run, or pausing when the bar moves there and lowering pauses (previewed). ifrits is how many lit
// ifrits are at work: Djinn starts them beyond the slots and counts them among the forced workers, and the gauge
// counts them apart.
function overLimit(
  data: Pick<
    LoadData,
    "runningWorkers" | "forcedWorkers" | "notchSlots" | "baseSlots"
  >,
  notch: LoadNotch,
  ifrits = 0,
): { byHand: number; others: number } {
  const over = Math.max(
    0,
    data.runningWorkers - ifrits - workersAt(data, notch),
  );
  const byHand = Math.min(Math.max(0, data.forcedWorkers - ifrits), over);
  return { byHand, others: over - byHand };
}

// The lit ifrits of the wishes read, and how many of them are at work on a turn (taking a slot).
function useIfrits(): { lit: number; working: number } {
  const lit = useOptionalData(
    (s) =>
      Object.values(s.details).reduce(
        (n, d) => n + d.tasks.filter(ifritLit).length,
        0,
      ),
    0,
  );
  const working = useOptionalData(
    (s) =>
      Object.values(s.details).reduce(
        (n, d) =>
          n +
          d.tasks.filter(
            (task) =>
              ifritLit(task) && task.ifrit?.state !== IfritState.LISTENING,
          ).length,
        0,
      ),
    0,
  );
  return { lit, working };
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
  data: Pick<LoadData, "notchSlots" | "baseSlots">,
  chosenNotch: LoadNotch,
): string {
  switch (notch) {
    case LoadNotch.MINIMAL:
      return t("load.tooltip.minimal", { workers: workersAt(data, notch) });
    case LoadNotch.MEDIUM:
      return t("load.tooltip.medium", { workers: workersAt(data, notch) });
    case LoadNotch.HIGH:
      return t("load.tooltip.high", { workers: workersAt(data, notch) });
    case LoadNotch.MAX:
      return t("load.tooltip.max", { workers: workersAt(data, notch) });
    case LoadNotch.OVERCLOCK:
      return `${t("load.tooltip.overclock", { workers: workersAt(data, notch) })} ${t("load.overclock_risk")}`;
    case LoadNotch.AUTO:
      return t("load.tooltip.auto", { chosen: notchLabel(chosenNotch) });
    default:
      return "";
  }
}

// autoReasonLine says in one line why auto runs at its notch: the load outside Djinn, how long it stayed on its side
// of the marks, and the step it leads to (W253).
export function autoReasonLine(reason: LoadAutoReason | undefined): string {
  if (!reason) return "";
  const notch = notchLabel(reason.notch);
  if (!reason.windowSeconds) return t("load.auto_reason.unmeasured", { notch });
  const params = {
    what: t(reason.memory ? "load.auto_reason.memory" : "load.auto_reason.cpu"),
    pct: Math.round(reason.outsidePercent),
    time: span(reason.windowSeconds * 1000),
    wait: span(reason.inSeconds * 1000),
    notch,
  };
  switch (reason.step) {
    case LoadAutoStep.LOWER:
      return t("load.auto_reason.lower", params);
    case LoadAutoStep.RAISE:
      return t("load.auto_reason.raise", params);
    case LoadAutoStep.CEILING:
      return t("load.auto_reason.ceiling", params);
    case LoadAutoStep.FLOOR:
      return t("load.auto_reason.floor", params);
    default:
      return t("load.auto_reason.hold", params);
  }
}

// nearestNotch is the index of the stop nearest a pointer at clientX over box, the stops' row: the stops stand at
// even steps from its left end to its right end.
function nearestNotch(clientX: number, box: { left: number; width: number }) {
  if (box.width <= 0) return null;
  const ratio = Math.max(0, Math.min(1, (clientX - box.left) / box.width));
  return Math.round(ratio * (NOTCHES.length - 1));
}

// stopAt is the distance from the track's left end to the stop at index i: the stops' row is inset by the thumb's
// half, 0.4375rem, at each end (operating-load.css).
function stopAt(i: number): string {
  return `calc(0.4375rem + ${i / (NOTCHES.length - 1)} * (100% - 0.875rem))`;
}

// useAnimatedNumber glides to target when glide changes with it: the notch the developer moves to. Any other move
// takes its new value at once: the memory available moves every second, and gliding through each move drew frames
// all the time (W513).
function useAnimatedNumber(
  target: number,
  glide: unknown,
  durationMs = 300,
): number {
  const glideRef = React.useRef(glide);
  const [current, setCurrent] = React.useState(target);
  const currentRef = React.useRef(current);
  currentRef.current = current;
  const frameRef = React.useRef<number | null>(null);
  const startValRef = React.useRef(target);
  const startTimeRef = React.useRef<number | null>(null);

  React.useEffect(() => {
    const from = currentRef.current;
    const moved = glideRef.current !== glide;
    glideRef.current = glide;
    if (from === target) return;
    if (
      !moved ||
      typeof window === "undefined" ||
      window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ||
      document.documentElement.dataset.motion === "reduced" ||
      still()
    ) {
      setCurrent(target);
      return;
    }

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
  }, [target, glide, durationMs]);

  return current;
}

function useAnimatedBigInt(
  target: bigint,
  glide: unknown,
  durationMs = 300,
): bigint {
  const numTarget = Number(target);
  const animated = useAnimatedNumber(numTarget, glide, durationMs);
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
  const allowedWorkers = workersAt(data, displayNotch);

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
    ? notchTooltip(LoadNotch.AUTO, data, chosen)
    : notchTooltip(displayNotch, data, chosen);
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

  // The stop nearest the pointer, measured on the stops' row (the slider's own box before it is drawn).
  const stopsRef = React.useRef<HTMLDivElement>(null);
  const nearest = (e: React.MouseEvent<HTMLDivElement>) => {
    const box = (
      stopsRef.current ?? e.currentTarget
    )?.getBoundingClientRect?.();
    const idx = box ? nearestNotch(e.clientX, box) : null;
    return idx === null ? null : (NOTCHES[idx] ?? null);
  };

  // A click anywhere on the track sets the nearest stop.
  const handleTrackClick = (e: React.MouseEvent<HTMLDivElement>) => {
    if ((e.target as HTMLElement)?.tagName === "BUTTON") return;
    const notch = nearest(e);
    if (notch === null) return;
    loadManager.setPreviewNotch(null);
    void loadManager.setNotch(notch);
  };

  // The pointer anywhere on the track, gaps included, previews the stop nearest it: the preview changes only when
  // that stop changes, never falling back to the level in effect between two stops (W37).
  const handleTrackMove = (e: React.MouseEvent<HTMLDivElement>) => {
    const notch = nearest(e);
    if (notch !== null) loadManager.setPreviewNotch(notch);
  };

  // select sets a notch from a label or a stop: the same move from either.
  const select = (notch: LoadNotch) => {
    loadManager.setPreviewNotch(null);
    void loadManager.setNotch(notch);
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

  // The gauge's fill: solid up to the level in effect, striped on to the previewed level when it is higher, or over
  // the part a lower previewed level would give up.
  const activeIndex = Math.max(0, NOTCHES.indexOf(activeNotch));
  return (
    <div
      className="operating-load-slider"
      data-notch={notchSlug(displayNotch)}
      onClick={handleTrackClick}
      onMouseMove={handleTrackMove}
      onMouseLeave={() => loadManager.setPreviewNotch(null)}
    >
      <div className="operating-load-track">
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
          aria-description={sliderTooltip}
          className="operating-load-input"
        />
        <div
          className="operating-load-fill-reach"
          data-notch={notchSlug(displayNotch)}
          style={{ width: stopAt(Math.max(activeIndex, sliderIndex)) }}
        />
        <div
          className="operating-load-fill"
          data-notch={notchSlug(activeNotch)}
          style={{ width: stopAt(Math.min(activeIndex, sliderIndex)) }}
        />
        {/* A round stop per notch on the track, a button each: the one shown filled (it stands for the cursor), the
            one in effect let through to the cursor beneath, which it drags. */}
        <div className="operating-load-stops" ref={stopsRef}>
          {NOTCHES.map((n, i) => {
            const inEffect = !data.auto && activeNotch === n;
            return (
              <button
                key={n}
                type="button"
                data-notch={notchSlug(n)}
                className={`operating-load-stop${i === sliderIndex ? " current" : ""}${inEffect ? " active" : ""}`}
                style={{ left: `${(i / (NOTCHES.length - 1)) * 100}%` }}
                aria-label={notchLabel(n)}
                aria-pressed={inEffect}
                onFocus={() => loadManager.setPreviewNotch(n)}
                onBlur={() => loadManager.setPreviewNotch(null)}
                onClick={(e) => {
                  e.stopPropagation();
                  select(n);
                }}
                aria-description={notchTooltip(n, data, chosen)}
              />
            );
          })}
        </div>
      </div>
      <div className="operating-load-notches">
        {NOTCHES.map((n) => (
          <button
            key={n}
            type="button"
            tabIndex={-1}
            data-notch={notchSlug(n)}
            className={`operating-load-notch ${!data.auto && activeNotch === n ? "active" : ""}`}
            // A label previews its own notch: spread edge to edge, the end labels sit off their stops.
            onMouseMove={(e) => {
              e.stopPropagation();
              loadManager.setPreviewNotch(n);
            }}
            onClick={(e) => {
              e.stopPropagation();
              select(n);
            }}
            aria-description={notchTooltip(n, data, chosen)}
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
        onMouseEnter={() => loadManager.setPreviewAuto(true)}
        onMouseLeave={() => loadManager.setPreviewAuto(false)}
        onFocus={() => loadManager.setPreviewAuto(true)}
        onBlur={() => loadManager.setPreviewAuto(false)}
        aria-description={notchTooltip(LoadNotch.AUTO, data, chosen)}
      >
        {t("load.notch.auto")}
      </button>
      {data.auto && (
        <span
          className="operating-load-effective"
          data-effective={notchSlug(data.notch)}
          aria-description={
            autoReasonLine(data.autoReason) ||
            notchTooltip(LoadNotch.AUTO, data, chosen)
          }
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

  const previewAuto = useSyncExternalStore(
    loadManager.subscribe,
    loadManager.getPreviewAuto,
    loadManager.getPreviewAuto,
  );

  const displayNotch = preview ?? data.notch;
  const allowedWorkers = workersAt(data, displayNotch);
  const ifrits = useIfrits();
  const over = overLimit(data, displayNotch, ifrits.working);
  // Overclock previewed, or in effect with no other level previewed: its risk, as a warning. Auto hovered or on: what
  // it does.
  const risk = displayNotch === LoadNotch.OVERCLOCK;
  const explainAuto = previewAuto || data.auto;
  // Previewing a notch below the one in effect: what moving there does to the workers above its limit.
  const previewing =
    preview !== null && workersAt(data, preview) < workersAt(data, data.notch);
  const lowerText = data.lowerPauses
    ? t("load.lower_pauses")
    : t("load.lower_finishes");
  const limitMemory = computeEngagedLimit(
    displayNotch,
    data.memoryAvailableBytes,
    data.memoryTotalBytes,
  );

  const animatedWorkers = useAnimatedNumber(allowedWorkers, displayNotch);
  const animatedEngaged = useAnimatedBigInt(limitMemory, displayNotch);

  if (data.memoryTotalBytes === 0n && data.memoryAvailableBytes === 0n)
    return null;

  const priorityText = notchLowPriority(displayNotch)
    ? t("load.priority_low")
    : t("load.priority_standard");

  const maxPossibleWorkers = Math.max(1, workersAt(data, LoadNotch.OVERCLOCK));
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

  // Laid over the page under the gauge, shown while the gauge is hovered or has the keyboard (operating-load.css).
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
        <span
          className="operating-load-forecast-label"
          aria-description={lowerText}
        >
          {t("load.forecast_cpu_running", {
            running: data.runningWorkers,
            workers: Math.round(animatedWorkers),
            priority: priorityText,
          })}
          {data.pausedWorkers > 0 && (
            <span className="operating-load-forecast-paused">
              {" · "}
              {t("load.forecast_paused", { count: data.pausedWorkers })}
            </span>
          )}
          {ifrits.lit > 0 && (
            <span className="operating-load-forecast-ifrit tone-ifrit">
              {" · "}
              <ToneIcon tone="ifrit" size={12} />
              {t("load.forecast_ifrits", { count: ifrits.lit })}
            </span>
          )}
          {over.byHand > 0 && (
            <span className="operating-load-forecast-over" data-over="hand">
              {" · "}
              {t("load.forecast_by_hand", { count: over.byHand })}
            </span>
          )}
          {over.others > 0 && (
            <span
              className="operating-load-forecast-over"
              data-over={previewing && data.lowerPauses ? "pause" : "finish"}
            >
              {" · "}
              {previewing && data.lowerPauses
                ? t("load.forecast_will_pause", { count: over.others })
                : t("load.forecast_finishing", { count: over.others })}
            </span>
          )}
        </span>
        <div className="operating-load-bar">
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
        <div className="operating-load-bar">
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
      {risk && (
        <div className="operating-load-forecast-note operating-load-forecast-risk">
          {t("load.overclock_risk")}
        </div>
      )}
      {explainAuto && (
        <div className="operating-load-forecast-note">
          {notchTooltip(
            LoadNotch.AUTO,
            data,
            data.chosenNotch || LoadNotch.MEDIUM,
          )}
        </div>
      )}
      {data.auto && data.autoReason && (
        <div className="operating-load-forecast-note operating-load-auto-reason">
          {autoReasonLine(data.autoReason)}
        </div>
      )}
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

  // No element of the gauge carries a title: the browser's own tooltip came up late over the coloured preview, a
  // hover on a hover (W32). Names stay in aria-label, the longer explanations in aria-description.
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
  workersAt,
  overLimit,
  computeEngagedLimit,
  nearestNotch,
  stopAt,
};
