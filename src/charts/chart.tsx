// <Chart frame spec label />: a chart of the window, drawn by ECharts once its chunk is loaded (engine.ts). Its
// option is built again when the frame, the theme or the text size changes; it never animates, so a window at rest
// draws no frame. The box is an image named by its label: the tooltip and the legend carry the values and the names.
import { useEffect, useRef, useState } from "react";
import type { EChartsType } from "echarts/core";

import { useShownTheme } from "../theme";
import { useTextScale } from "../text-size";
import { loadEcharts } from "./engine";
import type { Frame } from "./frame";
import { type ChartSpec, chartOption } from "./option";
import { chartTheme } from "./theme";

export interface ChartProps {
  frame: Frame;
  spec: ChartSpec;
  // What the chart shows, said to a screen reader.
  label: string;
  // The height at the default text size, in px; it follows the text size.
  height?: number;
}

export function Chart({ frame, spec, label, height = 220 }: ChartProps) {
  const box = useRef<HTMLDivElement>(null);
  const [chart, setChart] = useState<EChartsType>();
  const theme = useShownTheme();
  const scale = useTextScale();

  useEffect(() => {
    let alive = true;
    let made: EChartsType | undefined;
    let observer: ResizeObserver | undefined;
    loadEcharts().then(
      (core) => {
        const el = box.current;
        if (!alive || !el) return;
        made = core.init(el, null, { renderer: "svg" });
        observer = new ResizeObserver(() =>
          made?.resize({ animation: { duration: 0 } }),
        );
        observer.observe(el);
        setChart(made);
      },
      (err: unknown) => console.error("charts: ECharts did not load", err),
    );
    return () => {
      alive = false;
      observer?.disconnect();
      made?.dispose();
    };
  }, []);

  useEffect(() => {
    const el = box.current;
    if (!chart || !el) return;
    const look = {
      theme: chartTheme(),
      scale,
      font: getComputedStyle(el).fontFamily || undefined,
    };
    chart.setOption(chartOption(frame, spec, look), { notMerge: true });
  }, [chart, frame, spec, theme, scale]);

  return (
    <div
      ref={box}
      role="img"
      aria-label={label}
      style={{ height: `${height / 16}rem` }}
    />
  );
}
