// The Hourglass (Decision Q253): the home of every live metric, the work as a function of time. Each section reads its
// live data only while the page shows: leaving the page unmounts its sections, and their polls and timers stop with
// them. The machine comes first (consumption-panel.tsx); a later metric is one more entry of sections, and the flying
// carpet keeps no live graph. Its second tab, Gates (gates-tab.tsx, L05 v3, Q12 B), says what the gates do now and what
// the commands admitted under them cost. Its third, Statistics (statistics.tsx, L53 A), reads the past: one query per
// card, as the tab opens, never polled. Each tab bears its icon of the wishes' bank (wish-icon.tsx), drawn as a wish's.
import {
  type ComponentType,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";

import type { Task, Wish } from "../gen/ts/plan/v1/plan_pb";
import { jump } from "./attention";
import { ConsumptionPanel } from "./consumption-panel";
import { useData, useDjinn, useWishDetails } from "./data/djinn";
import { GatesTab } from "./gates-tab";
import { type TextKey, t } from "./i18n";
import { OperatingLoad } from "./operating-load";
import { PageHeader } from "./page-header";
import { formatValue } from "./resource-chart";
import { Statistics } from "./statistics";
import { WishIcon } from "./wish-icon";
import "./hourglass.css";

// What the window shows instead of a wish: the Hourglass. A wish's id is a UUID: never this.
export const HOURGLASS = "hourglass";

// A section of the Hourglass: given the active wishes, it reads its own live data while it is mounted.
interface Section {
  id: string;
  Section: ComponentType<{ wishes: readonly Wish[] }>;
}

// useActiveTasks is the tasks of the active wishes, by id: a worker's row, a gate's holder, a command's run say their
// task's facts.
function useActiveTasks(wishes: readonly Wish[]): ReadonlyMap<string, Task> {
  const wishIds = useMemo(() => wishes.map((w) => w.id), [wishes]);
  const details = useWishDetails(wishIds);
  return useMemo(
    () =>
      new Map(
        wishes
          .flatMap((w) => details[w.id]?.tasks ?? [])
          .map((task) => [task.id, task]),
      ),
    [wishes, details],
  );
}

// The ways from one tab to another: a link to a gate (gate-<name>) shows the Gates tab, to a worker (worker-<task id>)
// Machine.
function useShow() {
  const djinn = useDjinn();
  const gate = useCallback(
    (name: string) =>
      djinn?.focus.show({ page: HOURGLASS, target: `gate-${name}` }),
    [djinn],
  );
  const worker = useCallback(
    (taskId: string) =>
      djinn?.focus.show({ page: HOURGLASS, target: `worker-${taskId}` }),
    [djinn],
  );
  const task = useCallback(
    (task: Task) =>
      djinn?.focus.show({ wishId: task.wishId, target: `task-${task.id}` }),
    [djinn],
  );
  return { gate, worker, task };
}

// The machine: its CPU and memory, and each running worker (its row is worker-<task id>, a link's target); the gate a
// worker holds leads to the Gates tab.
function MachineSection({ wishes }: { wishes: readonly Wish[] }) {
  const projects = useData((s) => s.projects);
  const tasks = useActiveTasks(wishes);
  const show = useShow();
  return (
    <ConsumptionPanel
      wishes={wishes}
      tasks={tasks}
      projects={projects}
      onGate={show.gate}
    />
  );
}

// The Gates tab, with the tasks of the active wishes: a holder leads to its worker's row, a run to its task.
function GatesSection({ wishes }: { wishes: readonly Wish[] }) {
  const tasks = useActiveTasks(wishes);
  const show = useShow();
  return <GatesTab tasks={tasks} onWorker={show.worker} onTask={show.task} />;
}

const sections: readonly Section[] = [
  { id: "hourglass-machine", Section: MachineSection },
];

// The tabs of the Hourglass: what runs now, the machine, the gates, and what ran, the statistics.
export type HourglassTab = "machine" | "gates" | "statistics";

// Each tab, its label and its icon of the wishes' bank.
const tabs: readonly [HourglassTab, TextKey, string][] = [
  ["machine", "hourglass.tab_machine", "server"],
  ["gates", "hourglass.tab_gates", "arch"],
  ["statistics", "hourglass.tab_statistics", "chart-bar"],
];

// tabOf is the tab a link's target is on: a gate's card on Gates, anything else (a worker's row) on Machine.
function tabOf(target: string): HourglassTab {
  return target.startsWith("gate-") ? "gates" : "machine";
}

// The tab shown last: back on the Hourglass, the window shows it again.
let shownTab: HourglassTab = "machine";

// How long a link to a row waits for it: the series are read as the page opens.
const SETTLE = 3000;

export function Hourglass({
  wishes,
  target,
}: {
  // The active wishes, by rank.
  wishes: Wish[];
  // An element to bring into sight, a worker's row as a link asks (worker-<task id>); a new object at each request.
  target?: { target: string };
}) {
  const [tab, setTab] = useState<HourglassTab>(() =>
    target ? tabOf(target.target) : shownTab,
  );
  const show = (next: HourglassTab) => {
    shownTab = next;
    setTab(next);
  };
  // The row shows once its series are read: tried again a while, then given up. A worker's row is on the machine, a
  // gate's card on Gates.
  useEffect(() => {
    if (!target) return;
    shownTab = tabOf(target.target);
    setTab(shownTab);
    if (jump(target.target)) return;
    const timer = setInterval(() => {
      if (jump(target.target)) clearInterval(timer);
    }, 100);
    const stop = setTimeout(() => clearInterval(timer), SETTLE);
    return () => {
      clearInterval(timer);
      clearTimeout(stop);
    };
  }, [target]);
  return (
    <div className="wish-view hourglass">
      <PageHeader middle={<OperatingLoad />}>
        <strong>{t("hourglass.title")}</strong>
      </PageHeader>
      <div className="mission-scroll">
        <div className="hero mission-header">
          <div className="hero-copy">
            <span className="eyebrow">{t("hourglass.eyebrow")}</span>
            <h1>{t("hourglass.title")}</h1>
            <p>{t("hourglass.detail")}</p>
          </div>
        </div>
        <div className="overview-content hourglass-body">
          <div className="view-tabs hourglass-tabs" role="tablist">
            {tabs.map(([id, label, icon]) => (
              <button
                key={id}
                type="button"
                role="tab"
                id={`hourglass-tab-${id}`}
                aria-selected={tab === id}
                className={tab === id ? "active" : ""}
                onClick={() => show(id)}
              >
                <WishIcon wish={{ id: `hourglass-${id}`, icon }} size={20} />
                {t(label)}
              </button>
            ))}
          </div>
          <div role="tabpanel" aria-labelledby={`hourglass-tab-${tab}`}>
            {tab === "machine" ? (
              <HourglassSections wishes={wishes} />
            ) : tab === "gates" ? (
              <GatesSection wishes={wishes} />
            ) : (
              <Statistics />
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

// HourglassSections are the page's sections, one under the other: mounted, each reads its live data; unmounted, it
// stops.
export function HourglassSections({ wishes }: { wishes: readonly Wish[] }) {
  return (
    <div className="hourglass-sections">
      {sections.map(({ id, Section }) => (
        <div key={id} id={id} className="hourglass-section">
          <Section wishes={wishes} />
        </div>
      ))}
    </div>
  );
}

// HourglassLine is the machine's load in one line, where the Hourglass is not shown (the flying carpet), with the way
// to it. It reads nothing of its own: the machine the status bar keeps read (useMachine).
export function HourglassLine() {
  const djinn = useDjinn();
  const machine = useData((s) => s.machine);
  if (!machine) return null;
  const total = Number(machine.memoryTotalBytes);
  const used = total - Number(machine.memoryAvailableBytes);
  return (
    <p className="hourglass-line muted-text">
      {t("hourglass.line", {
        running: machine.running,
        workers: machine.workers,
        memory: formatValue("memory", used, total),
      })}{" "}
      <button
        type="button"
        className="text-link hourglass-line-open"
        onClick={() => djinn?.focus.show({ page: HOURGLASS })}
      >
        {t("hourglass.open")}
      </button>
    </p>
  );
}
