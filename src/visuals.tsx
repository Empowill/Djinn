import { ThinkingOrb, type OrbState } from "thinking-orbs";
import { useId, useState, useEffect } from "react";
import { t } from "./i18n";

type AgentStatus = "queued" | "running" | "blocked" | "done" | "error";
export const agentColor = (id: string, index = 0) =>
  id === "lead"
    ? "#bbc0c8"
    : ["#af9de3", "#88b4e5", "#dea487", "#96c2b8", "#d1a1c7", "#a6b5d5"][
        Math.max(0, index - 1) % 6
      ];
export const agentOrbState = (id: string, index = 0): OrbState =>
  id === "lead"
    ? "connecting"
    : id === "design"
      ? "composing"
      : id === "build"
        ? "solving"
        : id === "review"
          ? "searching"
          : (
              [
                "shaping",
                "weaving",
                "searching",
                "composing",
                "solving",
                "listening",
              ] as OrbState[]
            )[Math.max(0, index - 1) % 6];
export function Orb({
  status = "running",
  size = 44,
  color = "#d6d6d6",
  animation = "connecting",
}: {
  status?: AgentStatus;
  size?: number;
  color?: string;
  animation?: OrbState;
}) {
  const [reduced, setReduced] = useState(
    document.documentElement.dataset.motion === "reduced",
  );
  useEffect(() => {
    const observer = new MutationObserver(() =>
      setReduced(document.documentElement.dataset.motion === "reduced"),
    );
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-motion"],
    });
    return () => observer.disconnect();
  }, []);
  return (
    <span
      className={`orb orb-${status} thinking-avatar`}
      style={{ width: size, height: size }}
      aria-hidden="true"
    >
      <ThinkingOrb
        state={animation}
        size={size < 30 ? 20 : 64}
        theme="auto"
        color={color}
        speed={status === "running" ? 0.85 : status === "queued" ? 0.35 : 0.45}
        paused={reduced || status === "done"}
        style={{ width: size, height: size }}
      />
      {status === "done" && <span className="orb-finished">✓</span>}
    </span>
  );
}
export function Machine({ active = true }: { active?: boolean }) {
  const id = useId().replace(/:/g, "");
  return (
    <svg
      className={`machine ${active ? "active" : ""}`}
      viewBox="0 0 560 340"
      fill="none"
      aria-label={t("visuals.team_illustration")}
    >
      <defs>
        <linearGradient
          id={`body${id}`}
          x1="150"
          y1="80"
          x2="350"
          y2="260"
          gradientUnits="userSpaceOnUse"
        >
          <stop style={{ stopColor: "var(--n-2a)" }} />
          <stop offset="1" style={{ stopColor: "var(--n-14)" }} />
        </linearGradient>
        <radialGradient id={`glow${id}`}>
          <stop style={{ stopColor: "var(--n-de)" }} stopOpacity=".18" />
          <stop
            offset="1"
            style={{ stopColor: "var(--n-de)" }}
            stopOpacity="0"
          />
        </radialGradient>
        <linearGradient id={`screen${id}`} x1="0" y1="0" x2="1" y2="1">
          <stop style={{ stopColor: "var(--n-2a)" }} />
          <stop offset="1" style={{ stopColor: "var(--n-14)" }} />
        </linearGradient>
      </defs>
      <ellipse cx="280" cy="215" rx="205" ry="100" fill={`url(#glow${id})`} />
      <g style={{ stroke: "var(--n-38)" }} strokeWidth=".6" opacity=".55">
        {Array.from({ length: 13 }, (_, i) => (
          <g key={i}>
            <path d={`M${15 + i * 27} ${150 + i * 14}l230 -125`} />
            <path d={`M${240 + i * 25} ${21 + i * 14}l-230 125`} />
          </g>
        ))}
      </g>
      <path
        d="m110 234 170-98 181 103-170 98Z"
        style={{ fill: "var(--n-19)", stroke: "var(--n-47)" }}
      />
      <path
        d="m110 234 0 9 181 104 170-99v-9"
        style={{ stroke: "var(--n-38)" }}
      />
      <path
        d="m174 231 0-138 101-58 111 64v139l-101 59Z"
        fill={`url(#body${id})`}
        style={{ stroke: "var(--n-5e)" }}
        strokeWidth="1.1"
      />
      <path
        d="m174 93 111 65 101-59M285 158v139"
        style={{ stroke: "var(--n-57)" }}
      />
      <path
        d="m188 110 80 46v89l-80-46Z"
        style={{ fill: "var(--n-11)", stroke: "var(--n-51)" }}
      />
      <path
        d="m197 128 63 36v61l-63-36Z"
        fill={`url(#screen${id})`}
        style={{ stroke: "var(--n-47)" }}
      />
      <g
        className="screen-symbol"
        style={{ stroke: "var(--n-e7)" }}
        strokeWidth="1.5"
      >
        <path d="m214 165 8 10-8 1m15 3 12 7" />
        <path d="m202 188 51 29" opacity=".25" />
      </g>
      <g
        className="scanline"
        style={{ stroke: "var(--n-b6)" }}
        strokeWidth=".7"
        opacity=".3"
      >
        <path d="m199 145 58 33" />
        <path d="m199 149 58 33" />
        <path d="m199 153 58 33" />
      </g>
      <path d="m305 173 61-35v73l-61 35Z" style={{ stroke: "var(--n-38)" }} />
      <g style={{ stroke: "var(--n-47)" }} strokeWidth="1">
        {Array.from({ length: 9 }, (_, i) => (
          <path key={i} d={`m${308 + i * 6} ${170 - i * 3.5}v55`} />
        ))}
      </g>
      <g style={{ stroke: "var(--n-6f)" }}>
        <path d="m202 235 34 19m-34-15 34 19" />
        <path d="m301 255 65-37" />
      </g>
      <circle
        cx="252"
        cy="252"
        r="2"
        style={{ fill: "var(--n-e7)" }}
        className="machine-led"
      />
      <path
        d="m280 65 70 40-16 9-70-40Z"
        style={{ fill: "var(--n-1c)", stroke: "var(--n-4c)" }}
      />
      <path
        d="m190 268 45-26 94 54-45 26Z"
        style={{ fill: "var(--n-25)", stroke: "var(--n-5e)" }}
      />
      <g style={{ stroke: "var(--n-5e)" }} strokeWidth=".7">
        {Array.from({ length: 4 }, (_, i) => (
          <path key={i} d={`m${198 + i * 9} ${268 - i * 5} 84 48`} />
        ))}
        {Array.from({ length: 8 }, (_, i) => (
          <path key={i} d={`m${205 + i * 10} ${276 + i * 5.7} 30-18`} />
        ))}
      </g>
      <g
        className="signal-paths"
        style={{ stroke: "var(--n-b6)" }}
        strokeWidth=".8"
      >
        <path d="M386 174l43 25v44l50 28" strokeDasharray="3 5" />
        <path d="M174 170l-45-26-55 32" strokeDasharray="3 5" />
        <path d="M281 35V19l50-10" strokeDasharray="3 5" />
      </g>
      <g className="satellite sat-a">
        <path
          d="m440 274 28-16 29 17-28 16Z"
          style={{ fill: "var(--n-28)", stroke: "var(--n-83)" }}
        />
        <path
          d="m451 273 17-10 18 11-17 10Z"
          style={{ stroke: "var(--n-d1)" }}
        />
        <circle cx="469" cy="274" r="2" style={{ fill: "var(--n-eb)" }} />
      </g>
      <g className="satellite sat-b">
        <path
          d="m39 182 29-16 28 16-29 17Z"
          style={{ fill: "var(--n-22)", stroke: "var(--n-6f)" }}
        />
        <path d="m55 182 12-7 12 7-12 7Z" style={{ stroke: "var(--n-c4)" }} />
      </g>
      <g className="satellite sat-c">
        <path
          d="m327 13 22-13 24 14-23 13Z"
          style={{ fill: "var(--n-28)", stroke: "var(--n-83)" }}
        />
        <circle cx="350" cy="14" r="3" style={{ fill: "var(--n-de)" }} />
      </g>
      <text
        x="403"
        y="308"
        style={{ fill: "var(--n-83)" }}
        fontSize="8"
        fontFamily="monospace"
      >
        SYSTEM / DJINN
      </text>
      <text
        x="403"
        y="322"
        style={{ fill: "var(--n-c4)" }}
        fontSize="8"
        fontFamily="monospace"
      >
        CONNECTED_
      </text>
    </svg>
  );
}
export function Wave({ paused = false }: { paused?: boolean }) {
  return (
    <svg
      viewBox="0 0 400 140"
      className={`wave ${paused ? "paused" : ""}`}
      fill="none"
      aria-hidden="true"
    >
      <g stroke="#353535" strokeWidth=".5">
        {Array.from({ length: 11 }, (_, i) => (
          <path
            key={i}
            d={`m${20 + i * 18} ${60 + i * 5} 175-65M${195 + i * 18} ${-5 + i * 5} -175 65`}
          />
        ))}
      </g>
      {Array.from({ length: 9 }, (_, i) => (
        <path
          className="wave-line"
          key={i}
          style={{ animationDelay: `${i * -0.17}s` }}
          d={`M${60 + i * 12} ${90 + i * 3} C${110 + i * 12} ${73 + i * 3},${104 + i * 12} ${12 + i * 3},${147 + i * 12} ${23 + i * 3}S${211 + i * 12} ${77 + i * 3},${254 + i * 12} ${44 + i * 3}`}
          stroke={i === 0 || i === 8 ? "#cccccc" : "#6f6f6f"}
          strokeWidth=".8"
          opacity={0.4 + i * 0.05}
        />
      ))}
    </svg>
  );
}
