// The Hourglass (Decision Q253): the home of every live metric, the work as a function of time. Each section reads its
// live data only while the page shows: leaving the page unmounts its sections, and their polls and timers stop with
// them. The machine comes first (consumption-panel.tsx); a later metric is one more entry of sections, and the flying
// carpet keeps no live graph.
import { type ComponentType, useEffect, useMemo } from "react";

import type { Wish } from "../gen/ts/plan/v1/plan_pb";
import { jump } from "./attention";
import { ConsumptionPanel } from "./consumption-panel";
import { useData, useDjinn, useWishDetails } from "./data/djinn";
import { t } from "./i18n";
import { OperatingLoad } from "./operating-load";
import { PageHeader } from "./page-header";
import { formatValue } from "./resource-chart";
import "./hourglass.css";

// What the window shows instead of a wish: the Hourglass. A wish's id is a UUID: never this.
export const HOURGLASS = "hourglass";

// A section of the Hourglass: given the active wishes, it reads its own live data while it is mounted.
interface Section {
  id: string;
  Section: ComponentType<{ wishes: readonly Wish[] }>;
}

// The machine: its CPU and memory, each running worker (its row is worker-<task id>, a link's target), and where the
// commands' time goes in the projects of the active wishes.
function MachineSection({ wishes }: { wishes: readonly Wish[] }) {
  const projects = useData((s) => s.projects);
  const wishIds = useMemo(() => wishes.map((w) => w.id), [wishes]);
  const details = useWishDetails(wishIds);
  const tasks = useMemo(
    () => wishes.flatMap((w) => details[w.id]?.tasks ?? []),
    [wishes, details],
  );
  const taskById = useMemo(
    () => new Map(tasks.map((task) => [task.id, task])),
    [tasks],
  );
  const projectIds = useMemo(
    () =>
      new Set([
        ...wishes.flatMap((w) => w.projectIds),
        ...tasks.map((task) => task.projectId).filter(Boolean),
      ]),
    [wishes, tasks],
  );
  return (
    <ConsumptionPanel
      tasks={taskById}
      projects={projects}
      projectIds={projectIds}
    />
  );
}

const sections: readonly Section[] = [
  { id: "hourglass-machine", Section: MachineSection },
];

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
  // The row shows once its series are read: tried again a while, then given up.
  useEffect(() => {
    if (!target || jump(target.target)) return;
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
        <HourglassSections wishes={wishes} />
      </div>
    </div>
  );
}

// HourglassSections are the page's sections, one under the other: mounted, each reads its live data; unmounted, it
// stops.
export function HourglassSections({ wishes }: { wishes: readonly Wish[] }) {
  return (
    <div className="overview-content hourglass-sections">
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
