// The machine and worker series the resources panel draws, kept live: the window reads the whole window once
// (MachineService.Series), then only the points measured since its last one, and appends them here.
import { type Timestamp, timestampDate } from "@bufbuild/protobuf/wkt";

import type {
  MachineServiceSeriesResponse,
  WorkerSeries,
} from "../../gen/ts/machine/v1/machine_pb";

export function timeOf(ts?: Timestamp): number {
  return ts ? timestampDate(ts).getTime() : 0;
}

// lastPointTime is the time of the newest point of the series, machine's or a worker's; undefined when none.
export function lastPointTime(
  data: MachineServiceSeriesResponse,
): Timestamp | undefined {
  let last: Timestamp | undefined;
  let at = 0;
  const see = (ts?: Timestamp) => {
    const t = timeOf(ts);
    if (t > at) {
      at = t;
      last = ts;
    }
  };
  for (const p of data.machine?.points ?? []) see(p.time);
  for (const w of data.workers) for (const p of w.points) see(p.time);
  return last;
}

// append keeps the points of was at or after cutoff, then those of next newer than its last.
function append<P extends { time?: Timestamp }>(
  was: readonly P[],
  next: readonly P[],
  cutoff: number,
): P[] {
  const kept = was.filter((p) => timeOf(p.time) >= cutoff);
  const last = kept.length ? timeOf(kept[kept.length - 1].time) : -Infinity;
  return [...kept, ...next.filter((p) => timeOf(p.time) > last)];
}

function sameWorker(a: WorkerSeries, b: WorkerSeries): boolean {
  return (
    a.title === b.title &&
    a.paused === b.paused &&
    a.peakCpuPercent === b.peakCpuPercent &&
    a.peakMemoryBytes === b.peakMemoryBytes &&
    a.gates.length === b.gates.length &&
    a.gates.every(
      (g, i) => timeOf(g.releaseTime) === timeOf(b.gates[i].releaseTime),
    )
  );
}

// mergeSeries adds next, read since the last point of was, to was: the points older than cutoff (ms) go, the new
// ones are appended. The workers are next's (a worker that ended is gone); a worker with nothing new stays the same
// object, so its row does not render again. The gate intervals are next's.
export function mergeSeries(
  was: MachineServiceSeriesResponse,
  next: MachineServiceSeriesResponse,
  cutoff: number,
): MachineServiceSeriesResponse {
  const before = new Map(was.workers.map((w) => [w.taskId, w]));
  const workers = next.workers.map((w) => {
    const old = before.get(w.taskId);
    if (!old) return w;
    const points = append(old.points, w.points, cutoff);
    if (
      points.length === old.points.length &&
      points[0] === old.points[0] &&
      sameWorker(old, w)
    )
      return old;
    return { ...w, points };
  });
  const machine = next.machine
    ? {
        ...next.machine,
        points: append(was.machine?.points ?? [], next.machine.points, cutoff),
      }
    : was.machine;
  return { ...next, machine, workers };
}
