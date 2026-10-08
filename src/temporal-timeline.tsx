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
import type { Agent, MissionWorkItem, Task } from "./types";
import { agentColor, agentOrbState, Orb } from "./visuals";
import { agentDisplayState } from "./agent-state";
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
import { t } from "./i18n";

export interface TimelineProps {
  task: Task;
  onAgent: (agent: Agent) => void;
}

const zoomLevels = [0.7, 1, 1.5, 2, 3];

function shortRunId(runId?: string): string {
  if (!runId) return t("timeline.run_unknown");
  return runId.length > 22 ? `${runId.slice(0, 10)}…${runId.slice(-8)}` : runId;
}

function humanKindLabel(kind: HumanIntervention["kind"]): string {
  if (kind === "decision") return t("timeline.kind_decision");
  if (kind === "feedback") return t("timeline.kind_feedback");
  return t("timeline.instruction");
}

const workStatusLabels: Record<MissionWorkItem["status"], string> = {
  pending: t("timeline.work_pending"),
  running: t("timeline.running"),
  blocked: t("timeline.blocked"),
  ready: t("timeline.work_ready"),
  done: t("timeline.work_done"),
};

function workItemStatusLabel(
  task: Task,
  item: MissionWorkItem,
  agent?: Agent,
): string {
  const attention = agent ? agentDisplayState(task, agent) : undefined;
  if (attention?.permission) return t("timeline.awaiting_permission");
  if (attention?.question) return t("timeline.blocked_question");
  if (agent && item.status === "running" && agent.status !== "running")
    return agentDisplayState(task, agent).label;
  return workStatusLabels[item.status];
}

function workItemsForTask(task: Task): MissionWorkItem[] {
  const selectedStepId = task.selectedStepId || task.activeStepId;
  const scoped = (task.workItems || []).filter(
    (item) => !item.stepId || !selectedStepId || item.stepId === selectedStepId,
  );
  if (scoped.length) return scoped;
  return task.agents.map((agent) => ({
    id: `agent:${agent.id}`,
    title: agent.activity || agent.summary || agent.role || agent.name,
    status:
      agent.status === "queued"
        ? "pending"
        : agent.status === "error"
          ? "blocked"
          : agent.status,
    detail: agent.summary,
    agentId: agent.id,
    worktree: agent.worktree,
    branch: agent.branch,
    stepId: selectedStepId,
    runId: agent.runId,
    updatedAt: task.createdAt,
  }));
}

function MissionWorkItemRow({
  task,
  item,
  onAgent,
}: {
  task: Task;
  item: MissionWorkItem;
  onAgent: (agent: Agent) => void;
}) {
  const agent = item.agentId
    ? task.agents.find((candidate) => candidate.id === item.agentId)
    : undefined;
  const display = agent ? agentDisplayState(task, agent) : undefined;
  const status = workItemStatusLabel(task, item, agent);
  const hasDetails = Boolean(
    item.detail || item.ticket || item.worktree || item.branch || item.runId,
  );
  return (
    <li className={`mission-work-item is-${item.status}`}>
      <span
        className={`mission-work-status ${display?.status || item.status}`}
        aria-label={status}
        title={status}
      />
      <div className="mission-work-copy">
        <div className="mission-work-title-row">
          <strong>{item.title}</strong>
          {item.ticket && (
            <span className="mission-work-ticket">{item.ticket}</span>
          )}
        </div>
        <span className="mission-work-state">{status}</span>
        {hasDetails && (
          <details className="mission-work-details">
            <summary>
              {t("timeline.details")}
              <ChevronDown size={12} />
            </summary>
            <div>
              {item.detail && <p>{item.detail}</p>}
              {item.worktree && (
                <span>
                  {t("timeline.worktree")} <code>{item.worktree}</code>
                </span>
              )}
              {item.branch && (
                <span>
                  {t("timeline.branch")} <code>{item.branch}</code>
                </span>
              )}
              {item.runId && (
                <span>
                  {t("timeline.run")} {shortRunId(item.runId)}
                </span>
              )}
            </div>
          </details>
        )}
      </div>
      {agent && (
        <button
          className="mission-work-agent"
          type="button"
          onClick={() => onAgent(agent)}
          aria-label={t("timeline.open_chat", { name: agent.name })}
          title={t("timeline.open_chat", { name: agent.name })}
        >
          <Orb
            status={display?.status || agent.status}
            size={22}
            color={agentColor(agent.id, task.agents.indexOf(agent))}
            animation={agentOrbState(agent.id, task.agents.indexOf(agent))}
          />
          <span>{agent.name}</span>
          <MessageSquare size={12} aria-hidden="true" />
        </button>
      )}
    </li>
  );
}

/** Compact work summary shown above the elapsed-time chart. */
function MissionWorkList({
  task,
  onAgent,
}: {
  task: Task;
  onAgent: (agent: Agent) => void;
}) {
  const items = workItemsForTask(task);
  if (!items.length) return null;
  return (
    <section
      className="mission-work-list"
      aria-label={t("timeline.wish_tasks")}
    >
      <div className="mission-work-heading">
        <div>
          <span className="eyebrow">{t("timeline.work_eyebrow")}</span>
          <h2>{t("timeline.step_tasks")}</h2>
        </div>
        <span className="mission-work-count">{items.length}</span>
      </div>
      <ul>
        {items.map((item) => (
          <MissionWorkItemRow
            key={item.id}
            task={task}
            item={item}
            onAgent={onAgent}
          />
        ))}
      </ul>
    </section>
  );
}

function activityLabel(activity: TemporalActivityPoint): string {
  if (activity.lifecycle === "started") return t("timeline.activity_started");
  if (activity.lifecycle === "completed")
    return t("timeline.activity_completed");
  if (activity.lifecycle === "blocked") return t("timeline.activity_blocked");
  return activity.type === "tool"
    ? t("timeline.activity_tool")
    : t("timeline.activity_recorded");
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
  const display = agent ? agentDisplayState(task, agent) : undefined;
  const color = agentColor(
    lane.agentId,
    agent ? task.agents.indexOf(agent) : index,
  );
  const status = display?.status || agent?.status || "queued";
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
            (isMissionLane ? t("timeline.wish_lane") : lane.agentId) ||
            t("timeline.wish_lane")}
        </strong>
        <small>
          {agent?.role || t("timeline.activity_recorded")}
          {display && ` · ${display.label}`}
        </small>
      </span>
    </>
  );
  if (!agent || isMissionLane)
    return <div className="temporal-agent-label">{content}</div>;
  return (
    <button
      className="temporal-agent-label"
      onClick={() => onAgent(agent)}
      aria-label={t("timeline.open_chat", { name: agent.name })}
      title={t("timeline.open_chat", { name: agent.name })}
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
          ? t("timeline.running")
          : interval.endKind === "blocked"
            ? t("timeline.blocked")
            : t("timeline.run")}
      </span>
      <strong>{interval.title}</strong>
      <p>{interval.detail || t("timeline.no_detail")}</p>
      <dl>
        <div>
          <dt>{t("timeline.start")}</dt>
          <dd>
            {formatClock(interval.start)}
            {interval.endMs !== undefined &&
              ` · ${formatElapsed(interval.endMs - interval.startMs)}`}
          </dd>
        </div>
        {interval.end && interval.endMs !== undefined && (
          <div>
            <dt>{t("timeline.end")}</dt>
            <dd>{formatClock(interval.end)}</dd>
          </div>
        )}
        {interval.runId && (
          <div>
            <dt>{t("timeline.run")}</dt>
            <dd>{shortRunId(interval.runId)}</dd>
          </div>
        )}
        {interval.worktree && (
          <div>
            <dt>{t("timeline.worktree")}</dt>
            <dd>{interval.worktree}</dd>
          </div>
        )}
        {interval.branch && (
          <div>
            <dt>{t("timeline.branch")}</dt>
            <dd>{interval.branch}</dd>
          </div>
        )}
        {agent && (
          <div>
            <dt>{t("timeline.agent")}</dt>
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
      <p>{activity.detail || t("timeline.no_detail")}</p>
      <dl>
        <div>
          <dt>{t("timeline.time")}</dt>
          <dd>{formatClock(activity.time)}</dd>
        </div>
        {activity.runId && (
          <div>
            <dt>{t("timeline.run")}</dt>
            <dd>{shortRunId(activity.runId)}</dd>
          </div>
        )}
        {activity.worktree && (
          <div>
            <dt>{t("timeline.worktree")}</dt>
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
          <dt>{t("timeline.exact_time")}</dt>
          <dd>{formatClock(item.time)}</dd>
        </div>
        {item.resolved !== undefined && (
          <div>
            <dt>{t("timeline.state")}</dt>
            <dd>
              {item.resolved
                ? t("timeline.resolved")
                : t("timeline.to_address")}
            </dd>
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
        {interval.endKind === "running" && (
          <i aria-label={t("timeline.running")} />
        )}
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
            {t("timeline.no_activity")}
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
          <strong>{t("timeline.you")}</strong>
          <small>{t("timeline.interventions")}</small>
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
                aria-label={t("timeline.intervention_label", {
                  kind: humanKindLabel(item.kind),
                  title: item.title,
                  time: formatClock(item.time),
                })}
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
            {t("timeline.no_intervention")}
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
      <MissionWorkList task={task} onAgent={onAgent} />
      <div className="temporal-toolbar">
        <div className="temporal-toolbar-copy">
          <span>
            <Clock3 size={13} />
            {model.hasRecordedTimes
              ? `${model.axis.startMs !== undefined ? formatClock(model.axis.startMs) : ""} → ${model.axis.endMs !== undefined ? formatClock(model.axis.endMs) : ""} · ${formatElapsed(model.axis.durationMs)}`
              : t("timeline.no_activity_yet")}
          </span>
          {live && model.currentTimeMs !== undefined && (
            <span className="temporal-live-label">
              <i /> {t("timeline.running")}
            </span>
          )}
        </div>
        <div
          className="temporal-toolbar-actions"
          aria-label={t("timeline.scale")}
        >
          <button
            className="temporal-tool-button"
            onClick={() =>
              setScale(zoomLevels[Math.max(0, currentZoomIndex - 1)])
            }
            disabled={currentZoomIndex === 0}
            aria-label={t("timeline.zoom_out")}
            title={t("timeline.zoom_out")}
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
            aria-label={t("timeline.zoom_in")}
            title={t("timeline.zoom_in")}
          >
            <Plus size={13} />
          </button>
          <button
            className="temporal-fit-button"
            onClick={() => setScale(1)}
            title={t("timeline.fit_scale")}
          >
            <Maximize2 size={12} /> {t("timeline.fit")}
          </button>
        </div>
      </div>

      {!model.hasRecordedTimes ? (
        <div className="temporal-empty-state">
          <Activity size={22} />
          <strong>{t("timeline.no_activity_yet")}</strong>
          <p>{t("timeline.empty_hint")}</p>
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
                <span>{t("timeline.elapsed")}</span>
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
          <i className="legend-interval" /> {t("timeline.legend_work")}
        </span>
        <span>
          <i className="legend-activity" /> {t("timeline.legend_activity")}
        </span>
        <span>
          <i className="legend-human" /> {t("timeline.you")}
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
        <h1>{t("timeline.title")}</h1>
      </div>
      <TemporalTimeline task={task} onAgent={onAgent} />
    </div>
  );
}
