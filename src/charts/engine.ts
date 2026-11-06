// ECharts, loaded on demand: echarts.ts and what it imports go in a chunk of their own, which only a screen that draws
// a chart reads, out of the window's first bundle (L59).
export type Engine = typeof import("./echarts");

let engine: Promise<Engine> | undefined;

// loadEcharts loads ECharts once; a failed load is tried again at the next call.
export function loadEcharts(): Promise<Engine> {
  engine ??= import("./echarts").catch((err: unknown) => {
    engine = undefined;
    throw err;
  });
  return engine;
}
