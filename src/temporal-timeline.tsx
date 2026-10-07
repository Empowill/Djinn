import { useEffect, useMemo, useState } from "react";
import {
  Activity,
  ChevronDown,
  Clock3,
  GitBranch,
  Maximize2,
  MessageSquare,
  Minus,
  Plus,
  UserRound,
} from "lucide-react";
import type { Agent, Task } from "./types";
import { agentColor, agentOrbState, Orb } from "./visuals";
import {
  agentById,
  allocateIntervalRows,
  buildTemporalLayout,
  formatClock,
  formatElapsed,
  intervalGeometry,
  pointPosition,
  temporalCanvasWidth,
  type HumanIntervention,
  type TemporalActivityPoint,
  type TemporalInterval,
  type TemporalLane,
  type TemporalTimelineModel,
} from "./temporal-layout";
import "./temporal-timeline.css";

export interface TimelineProps {
  task: Task;
  onAgent: (agent: Agent) => void;
}

const zoomLevels = [0.7, 1, 1.5, 2, 3];

function shortRunId(runId?: string): string {
  if (!runId) return "Run non renseigné";
  return runId.length > 22 ? `${runId.slice(0, 10)}…${runId.slice(-8)}` : runId;
}

function humanKindLabel(kind: HumanIntervention["kind"]): string {
  if (kind === "decision") return "Décision";
  if (kind === "feedback") return "Review";
  return "Indication";
}

function activityLabel(activity: TemporalActivityPoint): string {
  if (activity.lifecycle === "started") return "Déclenchement";
  if (activity.lifecycle === "completed") return "Fin enregistrée";
  if (activity.lifecycle === "blocked") return "Blocage enregistré";
  return activity.type === "tool" ? "Outil" : "Activité enregistrée";
}

function AgentLabel({
  task,
  lane,
  index,
  onAgent,
}: {
  task: Task;
  lane: TemporalLane;
  index: number;
  onAgent: (agent: Agent) => void;
}) {
  const agent = agentById(task, lane.agentId);
  const isMissionLane = lane.agentId === "__mission__";
  const color = agentColor(
    lane.agentId,
    agent ? task.agents.indexOf(agent) : index,
  );
  const status = agent?.status || "queued";
  const content = (
    <>
      {isMissionLane ? (
        <span className="temporal-mission-icon">
          <Activity size={14} />
        </span>
      ) : (
        <Orb
          status={status}
          size={30}
          color={color}
          animation={agentOrbState(
            lane.agentId,
            agent ? task.agents.indexOf(agent) : index,
          )}
        />
      )}
      <span className="temporal-agent-copy">
        <strong style={{ color }}>
          {agent?.name ||
            (isMissionLane ? "Mission" : lane.agentId) ||
            "Mission"}
        </strong>
        <small>{agent?.role || "Activité enregistrée"}</small>
      </span>
    </>
  );
  if (!agent || isMissionLane)
    return <div className="temporal-agent-label">{content}</div>;
  return (
    <button
      className="temporal-agent-label"
      onClick={() => onAgent(agent)}
      aria-label={`Ouvrir le chat de ${agent.name}`}
      title={`Ouvrir le chat de ${agent.name}`}
    >
      {content}
      <MessageSquare size={13} aria-hidden="true" />
    </button>
  );
}

function IntervalDetails({
  interval,
  agent,
}: {
  interval: TemporalInterval;
  agent?: Agent;
}) {
  return (
    <div className="temporal-popover-body">
      <span className="temporal-popover-kind">
        {interval.endKind === "running"
          ? "En cours"
          : interval.endKind === "blocked"
            ? "Bloqué"
            : "Run"}
      </span>
      <strong>{interval.title}</strong>
      <p>{interval.detail || "Aucun détail enregistré."}</p>
      <dl>
        <div>
          <dt>Début</dt>
          <dd>
            {formatClock(interval.start)}
            {interval.endMs !== undefined &&
              ` · ${formatElapsed(interval.endMs - interval.startMs)}`}
          </dd>
        </div>
        {interval.end && interval.endMs !== undefined && (
          <div>
            <dt>Fin</dt>
            <dd>{formatClock(interval.end)}</dd>
          </div>
        )}
        {interval.runId && (
          <div>
            <dt>Run</dt>
            <dd>{shortRunId(interval.runId)}</dd>
          </div>
        )}
        {interval.worktree && (
          <div>
            <dt>Worktree</dt>
            <dd>{interval.worktree}</dd>
          </div>
        )}
        {interval.branch && (
          <div>
            <dt>Branche</dt>
            <dd>{interval.branch}</dd>
          </div>
        )}
        {agent && (
          <div>
            <dt>Agent</dt>
            <dd>{agent.name}</dd>
          </div>
        )}
      </dl>
    </div>
  );
}

function ActivityDetails({ activity }: { activity: TemporalActivityPoint }) {
  return (
    <div className="temporal-popover-body">
      <span className="temporal-popover-kind">{activityLabel(activity)}</span>
      <strong>{activity.title}</strong>
      <p>{activity.detail || "Aucun détail enregistré."}</p>
      <dl>
        <div>
          <dt>Heure</dt>
          <dd>{formatClock(activity.time)}</dd>
        </div>
        {activity.runId && (
          <div>
            <dt>Run</dt>
            <dd>{shortRunId(activity.runId)}</dd>
          </div>
        )}
        {activity.worktree && (
          <div>
            <dt>Worktree</dt>
            <dd>{activity.worktree}</dd>
          </div>
        )}
      </dl>
    </div>
  );
}

function HumanDetails({ item }: { item: HumanIntervention }) {
  return (
    <div className="temporal-popover-body">
      <span className="temporal-popover-kind">{humanKindLabel(item.kind)}</span>
      <strong>{item.title}</strong>
      <p>{item.detail}</p>
      <dl>
        <div>
          <dt>Heure exacte</dt>
          <dd>{formatClock(item.time)}</dd>
        </div>
        {item.resolved !== undefined && (
          <div>
            <dt>État</dt>
            <dd>{item.resolved ? "Résolu" : "À traiter"}</dd>
          </div>
        )}
      </dl>
    </div>
  );
}

function IntervalBar({
  interval,
  model,
  lane,
  task,
  row,
  openId,
  setOpenId,
}: {
  interval: TemporalInterval;
  model: TemporalTimelineModel;
  lane: TemporalLane;
  task: Task;
  row: number;
  openId: string | null;
  setOpenId: (id: string | null) => void;
}) {
  const agent = agentById(task, lane.agentId);
  const agentIndex = agent ? task.agents.indexOf(agent) : 0;
  const color = agentColor(lane.agentId, agentIndex);
  const geometry = intervalGeometry(interval, model.axis);
  const point = geometry.width === 0;
  const open = openId === interval.id;
  return (
    <div
      className={`temporal-interval-wrap ${point ? "is-point" : ""}`}
      style={{
        left: `${geometry.left}%`,
        width: point ? "8px" : `${Math.max(0.2, geometry.width)}%`,
        top: `${8 + row * 25}px`,
      }}
    >
      <button
        className={`temporal-interval ${interval.endKind} ${open ? "is-open" : ""}`}
        style={{
          borderColor: `${color}aa`,
          background: `linear-gradient(90deg, ${color}30, ${color}13)`,
          color,
        }}
        onClick={() => setOpenId(open ? null : interval.id)}
        aria-label={`${interval.title}, ${formatClock(interval.start)}`}
        aria-expanded={open}
        title={`${interval.title} · ${formatClock(interval.start)}`}
      >
        {interval.endKind === "running" && <i aria-label="En cours" />}
      </button>
      {open && (
        <div className="temporal-popover temporal-popover-interval">
          <IntervalDetails interval={interval} agent={agent} />
        </div>
      )}
    </div>
  );
}

function ActivityMarker({
  activity,
  model,
  openId,
  setOpenId,
}: {
  activity: TemporalActivityPoint;
  model: TemporalTimelineModel;
  openId: string | null;
  setOpenId: (id: string | null) => void;
}) {
  const open = openId === activity.id;
  return (
    <span
      className="temporal-activity-wrap"
      style={{ left: `${pointPosition(activity.timeMs, model.axis)}%` }}
    >
      <button
        className={`temporal-activity-marker ${open ? "is-open" : ""}`}
        onClick={() => setOpenId(open ? null : activity.id)}
        aria-label={`${activity.title}, ${formatClock(activity.time)}`}
        aria-expanded={open}
        title={`${activity.title} · ${formatClock(activity.time)}`}
      />
      {open && (
        <div className="temporal-popover temporal-popover-activity">
          <ActivityDetails activity={activity} />
        </div>
      )}
    </span>
  );
}

function LaneRow({
  task,
  lane,
  index,
  model,
  onAgent,
  openId,
  setOpenId,
}: {
  task: Task;
  lane: TemporalLane;
  index: number;
  model: TemporalTimelineModel;
  onAgent: (agent: Agent) => void;
  openId: string | null;
  setOpenId: (id: string | null) => void;
}) {
  const rows = allocateIntervalRows(lane.intervals);
  const rowCount = Math.max(
    1,
    ...lane.intervals.map((item) => (rows.get(item.id) || 0) + 1),
  );
  const trackHeight = Math.max(
    48,
    20 + rowCount * 25 + (lane.activities.length ? 18 : 0),
  );
  return (
    <div className="temporal-lane">
      <AgentLabel task={task} lane={lane} index={index} onAgent={onAgent} />
      <div className="temporal-track" style={{ minHeight: trackHeight }}>
        {model.axis.ticks.map((tick, tickIndex) => (
          <i
            className="temporal-gridline"
            key={`${lane.agentId}-grid-${tickIndex}`}
            style={{ left: `${tick.ratio * 100}%` }}
          />
        ))}
        {lane.intervals.map((interval) => (
          <IntervalBar
            key={interval.id}
            interval={interval}
            model={model}
            lane={lane}
            task={task}
            row={rows.get(interval.id) || 0}
            openId={openId}
            setOpenId={setOpenId}
          />
        ))}
        {(lane.intervals.length ? [] : lane.activities).map((activity) => (
          <ActivityMarker
            key={activity.id}
            activity={activity}
            model={model}
            openId={openId}
            setOpenId={setOpenId}
          />
        ))}
        {!lane.intervals.length && !lane.activities.length && (
          <span className="temporal-empty-lane">
            Aucune activité enregistrée
          </span>
        )}
      </div>
    </div>
  );
}

function HumanRow({
  model,
  openId,
  setOpenId,
}: {
  model: TemporalTimelineModel;
  openId: string | null;
  setOpenId: (id: string | null) => void;
}) {
  return (
    <div className="temporal-lane temporal-human-lane">
      <div className="temporal-agent-label temporal-human-label">
        <span className="temporal-human-icon">
          <UserRound size={14} />
        </span>
        <span className="temporal-agent-copy">
          <strong>Vous</strong>
          <small>Interventions enregistrées</small>
        </span>
      </div>
      <div className="temporal-track temporal-human-track">
        {model.axis.ticks.map((tick, tickIndex) => (
          <i
            className="temporal-gridline"
            key={`human-grid-${tickIndex}`}
            style={{ left: `${tick.ratio * 100}%` }}
          />
        ))}
        {model.humanInterventions.map((item) => {
          const open = openId === item.id;
          return (
            <span
              className={`temporal-human-marker ${item.kind} ${open ? "is-open" : ""}`}
              key={item.id}
              style={{ left: `${pointPosition(item.timeMs, model.axis)}%` }}
            >
              <button
                onClick={() => setOpenId(open ? null : item.id)}
                aria-label={`${humanKindLabel(item.kind)} : ${item.title}, ${formatClock(item.time)}`}
                aria-expanded={open}
                title={`${item.title} · ${formatClock(item.time)}`}
              >
                <span />
              </button>
              {open && (
                <div className="temporal-popover temporal-popover-human">
                  <HumanDetails item={item} />
                </div>
              )}
            </span>
          );
        })}
        {!model.humanInterventions.length && (
          <span className="temporal-empty-lane">
            Aucune intervention enregistrée
          </span>
        )}
      </div>
    </div>
  );
}

export function TemporalTimeline({ task, onAgent }: TimelineProps) {
  const selectedStepId = task.selectedStepId || task.activeStepId;
  const live =
    task.status === "running" && selectedStepId === task.activeStepId;
  const [clockMs, setClockMs] = useState<number | undefined>(
    live ? Date.now() : undefined,
  );
  const [scale, setScale] = useState(1);
  const [openId, setOpenId] = useState<string | null>(null);
  useEffect(() => {
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpenId(null);
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, []);
  useEffect(() => {
    setScale(1);
    setOpenId(null);
    setClockMs(live ? Date.now() : undefined);
    if (!live) return;
    const timer = window.setInterval(() => setClockMs(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [task.id, selectedStepId, live]);
  const model = useMemo(
    () =>
      buildTemporalLayout(live ? task : { ...task, status: "done" }, {
        currentTime: live ? clockMs : undefined,
      }),
    [task, clockMs, live],
  );
  const width = temporalCanvasWidth(model.axis.durationMs, scale);
  const currentZoomIndex = zoomLevels.reduce(
    (best, value, index) =>
      Math.abs(value - scale) < Math.abs(zoomLevels[best] - scale)
        ? index
        : best,
    0,
  );

  return (
    <div className="temporal-timeline-content">
      <div className="temporal-toolbar">
        <div className="temporal-toolbar-copy">
          <span>
            <Clock3 size={13} />
            {model.hasRecordedTimes
              ? `${model.axis.startMs !== undefined ? formatClock(model.axis.startMs) : ""} → ${model.axis.endMs !== undefined ? formatClock(model.axis.endMs) : ""} · ${formatElapsed(model.axis.durationMs)}`
              : "Pas encore d’activité"}
          </span>
          {live && model.currentTimeMs !== undefined && (
            <span className="temporal-live-label">
              <i /> En cours
            </span>
          )}
        </div>
        <div
          className="temporal-toolbar-actions"
          aria-label="Réglage de l’échelle"
        >
          <button
            className="temporal-tool-button"
            onClick={() =>
              setScale(zoomLevels[Math.max(0, currentZoomIndex - 1)])
            }
            disabled={currentZoomIndex === 0}
            aria-label="Réduire l’échelle"
            title="Réduire l’échelle"
          >
            <Minus size={13} />
          </button>
          <span className="temporal-zoom-readout">
            {Math.round(scale * 100)}%
          </span>
          <button
            className="temporal-tool-button"
            onClick={() =>
              setScale(
                zoomLevels[
                  Math.min(zoomLevels.length - 1, currentZoomIndex + 1)
                ],
              )
            }
            disabled={currentZoomIndex === zoomLevels.length - 1}
            aria-label="Augmenter l’échelle"
            title="Augmenter l’échelle"
          >
            <Plus size={13} />
          </button>
          <button
            className="temporal-fit-button"
            onClick={() => setScale(1)}
            title="Ajuster l’échelle"
          >
            <Maximize2 size={12} /> Ajuster
          </button>
        </div>
      </div>

      {!model.hasRecordedTimes ? (
        <div className="temporal-empty-state">
          <Activity size={22} />
          <strong>Pas encore d’activité</strong>
          <p>Les événements apparaîtront ici dès qu’ils seront enregistrés.</p>
        </div>
      ) : (
        <div
          className="temporal-scroll"
          onClick={(event) => {
            if (
              event.target instanceof Element &&
              !event.target.closest(
                ".temporal-interval, .temporal-activity-marker, .temporal-human-marker button, .temporal-popover",
              )
            )
              setOpenId(null);
          }}
        >
          <div
            className="temporal-canvas"
            style={{ width: `max(${scale * 100}%, ${width}px)` }}
          >
            <div className="temporal-axis">
              <div className="temporal-axis-label">
                <span>Temps écoulé</span>
                <small>
                  {model.axis.startMs !== undefined
                    ? formatClock(model.axis.startMs)
                    : ""}
                </small>
              </div>
              <div className="temporal-axis-track">
                {model.axis.ticks.map((tick, index) => (
                  <span
                    className="temporal-axis-tick"
                    key={`axis-${index}`}
                    style={{ left: `${tick.ratio * 100}%` }}
                  >
                    <i />
                    <b>{formatElapsed(tick.elapsedMs)}</b>
                  </span>
                ))}
              </div>
            </div>
            <div className="temporal-lanes">
              {model.lanes.map((lane, index) => (
                <LaneRow
                  key={lane.agentId}
                  task={task}
                  lane={lane}
                  index={index}
                  model={model}
                  onAgent={onAgent}
                  openId={openId}
                  setOpenId={setOpenId}
                />
              ))}
              <HumanRow model={model} openId={openId} setOpenId={setOpenId} />
            </div>
          </div>
        </div>
      )}
      <div className="temporal-legend">
        <span>
          <i className="legend-interval" /> Travail
        </span>
        <span>
          <i className="legend-activity" /> Activité
        </span>
        <span>
          <i className="legend-human" /> Vous
        </span>
      </div>
    </div>
  );
}

/** Keeps the old panel contract while exposing the chart for parent wiring. */
export function Timeline({ task, onAgent }: TimelineProps) {
  return (
    <div className="panel-content temporal-timeline">
      <div className="panel-heading">
        <h1>Timeline</h1>
      </div>
      <TemporalTimeline task={task} onAgent={onAgent} />
    </div>
  );
}
