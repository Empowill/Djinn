// The part of ECharts the window draws with: the series and components src/charts/option.ts uses, and the SVG
// renderer, whose text is the page's, crisp at every text size. Named imports let the build leave the rest out; only
// engine.ts imports this module, by import(), so it lands in a chunk of its own.
import { BarChart, LineChart, ScatterChart } from "echarts/charts";
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from "echarts/components";
import { init, use } from "echarts/core";
import { SVGRenderer } from "echarts/renderers";

use([
  BarChart,
  LineChart,
  ScatterChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  SVGRenderer,
]);

export { init };
