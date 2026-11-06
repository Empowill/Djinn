// The colours a chart draws with, read from the theme's tokens (theme.css, styles.css, review.css) as the page has
// them now: the dark and the light theme each have their own steps, never a flip of the other's.
import { token } from "../theme";

// The series' hues, in their fixed order: the first is the lead. Five, then "Others" in --chart-other: a chart never
// shows more than six series (L53, Will's lint).
export const SERIES_TOKENS = [
  "chart-1",
  "chart-2",
  "chart-3",
  "chart-4",
  "chart-5",
] as const;

export interface ChartTheme {
  // The series in order, then the colour of "Others".
  palette: string[];
  other: string;
  // Text on the plot: values and names, then axes' names and ticks.
  ink: string;
  inkSoft: string;
  // Grid lines and axes, recessive.
  grid: string;
  // The plot's ground: the gap between stacked segments, the tooltip's ground.
  surface: string;
}

// chartTheme reads the theme on the page now.
export function chartTheme(): ChartTheme {
  return {
    palette: SERIES_TOKENS.map(token),
    other: token("chart-other"),
    ink: token("text"),
    inkSoft: token("muted"),
    grid: token("line"),
    surface: token("surface"),
  };
}
