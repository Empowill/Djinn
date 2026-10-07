import {
  Archive,
  ArrowDownToLine,
  Check,
  CheckCircle2,
  ChevronRight,
  CircleDot,
  Clock3,
  FileCode2,
  FileImage,
  FileText,
  Filter,
  FolderOpen,
  LayoutDashboard,
  MessageCircle,
  Move,
  Network,
  Plus,
  Search,
  Send,
  SlidersHorizontal,
  Sparkles,
  Trash2,
  Upload,
  Users,
  X,
} from "lucide-react";
import type * as React from "react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { event as createEvent, now, uid } from "./data";
import type { Artifact, Feedback, Task } from "./types";
import "./artifact-workspace.css";
import { MarkdownBody } from "./markdown-body";
import { VisualizationFrame } from "./visualization-frame";
import { invalidateDependentSteps } from "./workflow";

type DiagramNode = {
  id: string;
  label: string;
  sublabel: string;
  x: number;
  y: number;
};

type DiagramModel = {
  nodes: DiagramNode[];
  edges: [string, string][];
};

type PreviewProject = {
  id: string;
  name: string;
  owner: string;
  status: "Planifié" | "En cours" | "Review" | "Archivé";
  team: string;
  progress: number;
  accent: string;
};

type WireframeModel = {
  heading: string;
  layout: "status" | "team";
  archive: "dedicated" | "filter";
  projects?: PreviewProject[];
  filter?: string;
  view?: "active" | "archive";
};

type PinPosition = { x: number; y: number };
type DragState = {
  id: string;
  offsetX: number;
  offsetY: number;
  pointerId: number;
};

type ArtifactWorkspaceProps = {
  task: Task;
  onUpdate: (next: Task) => void;
  onToast: (message: string) => void;
  review?: boolean;
  requestedArtifactId?: string;
  requestVersion?: number;
};

const DIAGRAM_WIDTH = 1000;
const DIAGRAM_HEIGHT = 420;
const MAX_SCREENSHOT_BYTES = 4 * 1024 * 1024;
// Electron accepts a 12 MB session. Leave room for the rest of the task list
// and reject a capture before it creates state that cannot be saved.
const MAX_TASK_PERSISTED_BYTES = 10 * 1024 * 1024;
// Retain unsaved human drafts when the tab/step temporarily unmounts this view.
// These remain local to this renderer session and are keyed by mission + support.
const HUMAN_DRAFTS = new Map<
  string,
  { text: string; dirty: boolean; base: string }
>();

const FALLBACK_DIAGRAM: DiagramModel = { nodes: [], edges: [] };

const DEFAULT_PROJECTS: PreviewProject[] = [
  {
    id: "atlas",
    name: "Atlas",
    owner: "Camille",
    status: "En cours",
    team: "Produit",
    progress: 68,
    accent: "#d1d1d1",
  },
  {
    id: "nova",
    name: "Nova",
    owner: "Marc",
    status: "Review",
    team: "Design",
    progress: 91,
    accent: "#bebebe",
  },
  {
    id: "echo",
    name: "Echo",
    owner: "Lina",
    status: "Planifié",
    team: "Qualité",
    progress: 18,
    accent: "#b0b0b0",
  },
  {
    id: "lumen",
    name: "Lumen",
    owner: "Nora",
    status: "En cours",
    team: "Produit",
    progress: 44,
    accent: "#afafaf",
  },
  {
    id: "archive-1",
    name: "Northstar",
    owner: "Ilyes",
    status: "Archivé",
    team: "Ops",
    progress: 100,
    accent: "#8e8e8e",
  },
];

const STATUS_FILTERS = ["Tous", "Planifié", "En cours", "Review"] as const;

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}

function parseJson<T>(content: string, fallback: T): T {
  try {
    const value: unknown = JSON.parse(content);
    return value !== null && typeof value === "object"
      ? (value as T)
      : fallback;
  } catch {
    return fallback;
  }
}

function parseDiagram(content: string): DiagramModel {
  const raw = parseJson<Record<string, unknown>>(content, {});
  const rawNodes = Array.isArray(raw.nodes) ? raw.nodes : [];
  const nodes = rawNodes
    .filter((node): node is Record<string, unknown> =>
      Boolean(node && typeof node === "object"),
    )
    .map((node, index) => ({
      id:
        typeof node.id === "string" && node.id ? node.id : `node-${index + 1}`,
      label: typeof node.label === "string" ? node.label : "Nouveau nœud",
      sublabel: typeof node.sublabel === "string" ? node.sublabel : "",
      x: Number.isFinite(node.x)
        ? clamp(Number(node.x), 90, DIAGRAM_WIDTH - 90)
        : 120 + index * 190,
      y: Number.isFinite(node.y)
        ? clamp(Number(node.y), 55, DIAGRAM_HEIGHT - 55)
        : 100 + (index % 2) * 180,
    }));
  const ids = new Set(nodes.map((node) => node.id));
  const rawEdges = Array.isArray(raw.edges) ? raw.edges : [];
  const edges: [string, string][] = rawEdges
    .filter(
      (edge): edge is unknown[] => Array.isArray(edge) && edge.length >= 2,
    )
    .map((edge) => [String(edge[0]), String(edge[1])] as [string, string])
    .filter(([from, to]) => ids.has(from) && ids.has(to));
  return { nodes, edges };
}

function hasDiagramShape(content: string) {
  const raw = parseJson<Record<string, unknown>>(content, {});
  return Array.isArray(raw.nodes);
}

function isProject(value: unknown): value is PreviewProject {
  if (!value || typeof value !== "object") return false;
  const project = value as Partial<PreviewProject>;
  return (
    typeof project.id === "string" &&
    typeof project.name === "string" &&
    typeof project.owner === "string" &&
    (project.status === "Planifié" ||
      project.status === "En cours" ||
      project.status === "Review" ||
      project.status === "Archivé") &&
    typeof project.team === "string" &&
    Number.isFinite(project.progress) &&
    typeof project.accent === "string"
  );
}

function parseWireframe(content: string): WireframeModel {
  const raw = parseJson<Partial<WireframeModel>>(content, {});
  const projects = Array.isArray(raw.projects)
    ? raw.projects.filter(isProject)
    : undefined;
  return {
    heading:
      typeof raw.heading === "string" && raw.heading.trim()
        ? raw.heading
        : "Vos projets",
    layout: raw.layout === "team" ? "team" : "status",
    archive: raw.archive === "filter" ? "filter" : "dedicated",
    ...(projects && projects.length ? { projects } : {}),
    ...(typeof raw.filter === "string" ? { filter: raw.filter } : {}),
    view: raw.view === "archive" ? "archive" : "active",
  };
}

function serialize(value: unknown) {
  return JSON.stringify(value, null, 2);
}

function relativeTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "à l’instant";
  const seconds = Math.max(0, Math.round((Date.now() - date.getTime()) / 1000));
  if (seconds < 60) return "à l’instant";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `il y a ${minutes} min`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `il y a ${hours} h`;
  return `il y a ${Math.round(hours / 24)} j`;
}

function typeLabel(type: Artifact["type"]) {
  return {
    diagram: "Diagramme",
    wireframe: "Support de cadrage",
    document: "Document",
    code: "Texte",
    screenshot: "Capture",
    visualization: "Visualisation",
  }[type];
}

function typeIcon(type: Artifact["type"], size = 16) {
  if (type === "diagram") return <Network size={size} strokeWidth={1.6} />;
  if (type === "wireframe")
    return <LayoutDashboard size={size} strokeWidth={1.6} />;
  if (type === "screenshot") return <FileImage size={size} strokeWidth={1.6} />;
  if (type === "code") return <FileCode2 size={size} strokeWidth={1.6} />;
  return <FileText size={size} strokeWidth={1.6} />;
}

function artifactFileName(artifact: Artifact) {
  const base =
    (artifact.title || "support")
      .normalize("NFKD")
      .replace(/[\u0300-\u036f]/g, "")
      .replace(/[^a-zA-Z0-9]+/g, "-")
      .replace(/^-|-$/g, "")
      .toLowerCase() || "support";
  if (artifact.type === "document") return `${base}.md`;
  if (artifact.type === "visualization") return `${base}.html`;
  if (artifact.type === "diagram" || artifact.type === "wireframe")
    return `${base}.json`;
  if (artifact.type === "screenshot") {
    const match = artifact.content.match(/^data:image\/([a-zA-Z0-9.+-]+);/);
    return `${base}.${match?.[1] === "jpeg" ? "jpg" : match?.[1] || "png"}`;
  }
  return `${base}.txt`;
}

function dataUrlBlob(value: string) {
  const match = value.match(/^data:([^;,]+)?(;base64)?,(.*)$/s);
  if (!match) return new Blob([value], { type: "text/plain;charset=utf-8" });
  const mime = match[1] || "application/octet-stream";
  if (!match[2])
    return new Blob([decodeURIComponent(match[3])], { type: mime });
  const binary = atob(match[3]);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1)
    bytes[index] = binary.charCodeAt(index);
  return new Blob([bytes], { type: mime });
}

function StatusDot({ status }: { status: PreviewProject["status"] }) {
  return (
    <span
      className={`art-status-dot art-status-dot--${status.toLowerCase().replace(/\s/g, "-")}`}
      aria-hidden="true"
    />
  );
}

export default function ArtifactWorkspace({
  task,
  onUpdate,
  onToast,
  review = false,
  requestedArtifactId,
  requestVersion,
}: ArtifactWorkspaceProps) {
  const [selectedId, setSelectedId] = useState(
    requestedArtifactId ?? task.artifacts[0]?.id ?? "",
  );
  useEffect(() => {
    if (
      requestedArtifactId &&
      task.artifacts.some((artifact) => artifact.id === requestedArtifactId)
    )
      setSelectedId(requestedArtifactId);
  }, [requestedArtifactId, requestVersion, task.id]);
  const [artifactQuery, setArtifactQuery] = useState("");
  const [documentDraft, setDocumentDraft] = useState("");
  const [documentDirty, setDocumentDirty] = useState(false);
  const [documentMode, setDocumentMode] = useState<"read" | "edit" | "preview">(
    "read",
  );
  const [draftBase, setDraftBase] = useState("");
  const draftOwner = useRef("");
  const draftCache = useRef(HUMAN_DRAFTS);
  const [diagram, setDiagram] = useState<DiagramModel | null>(null);
  const [selectedNodeId, setSelectedNodeId] = useState("");
  const [wireframe, setWireframe] = useState<WireframeModel | null>(null);
  const [wireframeFilter, setWireframeFilter] = useState("Tous");
  const [wireframeSearch, setWireframeSearch] = useState("");
  const [feedbackText, setFeedbackText] = useState("");
  const [pendingPin, setPendingPin] = useState<PinPosition | null>(null);
  const [activeFeedbackId, setActiveFeedbackId] = useState<string | null>(null);
  const [drag, setDrag] = useState<DragState | null>(null);
  const [uploading, setUploading] = useState(false);
  const uploadRef = useRef<HTMLInputElement>(null);
  const diagramRef = useRef<DiagramModel | null>(null);
  const svgRef = useRef<SVGSVGElement>(null);

  const selectedArtifact =
    task.artifacts.find((artifact) => artifact.id === selectedId) ??
    task.artifacts[0];
  const selectedFeedback = useMemo(
    () =>
      selectedArtifact
        ? task.feedback.filter(
            (feedback) => feedback.artifactId === selectedArtifact.id,
          )
        : [],
    [selectedArtifact, task.feedback],
  );
  const visibleArtifacts = useMemo(() => {
    const query = artifactQuery.trim().toLowerCase();
    if (!query) return task.artifacts;
    return task.artifacts.filter((artifact) =>
      `${artifact.title} ${typeLabel(artifact.type)}`
        .toLowerCase()
        .includes(query),
    );
  }, [artifactQuery, task.artifacts]);

  useEffect(() => {
    if (
      selectedId &&
      task.artifacts.some((artifact) => artifact.id === selectedId)
    )
      return;
    setSelectedId(task.artifacts[0]?.id ?? "");
  }, [selectedId, task.artifacts]);

  useEffect(() => {
    if (!selectedArtifact) {
      setDiagram(null);
      setWireframe(null);
      setDocumentDraft("");
      setDocumentDirty(false);
      return;
    }
    const owner = `${task.id}:${selectedArtifact.id}`;
    const changedArtifact = draftOwner.current !== owner;
    if (changedArtifact && draftOwner.current)
      draftCache.current.set(draftOwner.current, {
        text: documentDraft,
        dirty: documentDirty,
        base: draftBase,
      });
    draftOwner.current = owner;
    if (
      !changedArtifact &&
      documentDirty &&
      ["document", "code", "visualization", "diagram"].includes(
        selectedArtifact.type,
      )
    )
      return;
    const cached = changedArtifact ? draftCache.current.get(owner) : undefined;
    setPendingPin(null);
    setActiveFeedbackId(null);
    setDocumentDraft(cached?.dirty ? cached.text : selectedArtifact.content);
    setDocumentDirty(cached?.dirty || false);
    setDraftBase(cached?.dirty ? cached.base : selectedArtifact.content);
    if (changedArtifact) setDocumentMode("read");
    if (selectedArtifact.type === "diagram") {
      const parsed = parseDiagram(selectedArtifact.content);
      setDiagram(parsed);
      diagramRef.current = parsed;
      setSelectedNodeId(parsed.nodes[0]?.id ?? "");
    } else {
      setDiagram(null);
    }
    if (selectedArtifact.type === "wireframe") {
      const parsed = parseWireframe(selectedArtifact.content);
      setWireframe(parsed);
      setWireframeFilter(
        parsed.filter &&
          STATUS_FILTERS.includes(
            parsed.filter as (typeof STATUS_FILTERS)[number],
          )
          ? parsed.filter
          : "Tous",
      );
      setWireframeSearch("");
    } else {
      setWireframe(null);
    }
  }, [
    task.id,
    selectedArtifact?.content,
    selectedArtifact?.id,
    selectedArtifact?.type,
  ]);

  const draftConflict = Boolean(
    documentDirty && selectedArtifact && draftBase !== selectedArtifact.content,
  );
  const proposalOriginal = selectedArtifact?.id.includes(":proposal:")
    ? task.artifacts.find(
        (a) =>
          a.id ===
          selectedArtifact.id.slice(
            0,
            selectedArtifact.id.lastIndexOf(":proposal:"),
          ),
      )
    : undefined;

  const appendEvent = useCallback(
    (
      nextTask: Task,
      title: string,
      detail: string,
      type: "note" | "review" = "note",
      interventionId?: string,
    ) => {
      const entry = {
        ...createEvent(type, title, detail),
        actor: "human" as const,
        interventionId,
        stepId: nextTask.selectedStepId || nextTask.activeStepId,
      };
      const feedbackTime = nextTask.feedback.find(
        (f) => f.id === interventionId,
      )?.createdAt;
      if (feedbackTime) entry.time = feedbackTime;
      return { ...nextTask, events: [...nextTask.events, entry] };
    },
    [],
  );

  const patchArtifact = useCallback(
    (
      artifactId: string,
      content: string,
      options?: {
        eventTitle?: string;
        eventDetail?: string;
        eventType?: "note" | "review";
      },
    ) => {
      const artifact = task.artifacts.find((item) => item.id === artifactId);
      if (!artifact) return;
      let nextTask: Task = {
        ...task,
        artifacts: task.artifacts.map((item) =>
          item.id === artifactId
            ? {
                ...item,
                content,
                updatedAt: now(),
                revision: (item.revision || 1) + 1,
                editedBy: "human",
                revisions: [
                  ...(item.revisions || []),
                  {
                    revision: item.revision || 1,
                    content: item.content,
                    updatedAt: item.updatedAt,
                    editedBy: item.editedBy || "agent",
                  },
                ].slice(-20),
              }
            : item,
        ),
      };
      nextTask = invalidateDependentSteps(
        nextTask,
        artifact.stepId || task.activeStepId || "",
      );
      if (options?.eventTitle) {
        nextTask = appendEvent(
          nextTask,
          options.eventTitle,
          options.eventDetail || artifact.title,
          options.eventType || "note",
        );
      }
      onUpdate(nextTask);
    },
    [appendEvent, onUpdate, task],
  );

  const saveTextArtifact = useCallback(() => {
    const diagramFallback =
      selectedArtifact?.type === "diagram" &&
      !hasDiagramShape(selectedArtifact.content);
    if (
      !selectedArtifact ||
      (!["document", "code", "visualization"].includes(selectedArtifact.type) &&
        !diagramFallback)
    )
      return;
    if (draftConflict) {
      onToast(
        "La version enregistrée a changé. Comparez les versions avant de conserver votre brouillon.",
      );
      return;
    }
    patchArtifact(selectedArtifact.id, documentDraft, {
      eventTitle: "Support enregistré",
      eventDetail: selectedArtifact.title,
    });
    setDocumentDirty(false);
    setDraftBase(documentDraft);
    draftCache.current.delete(draftOwner.current);
    onToast("Support enregistré");
  }, [documentDraft, draftConflict, onToast, patchArtifact, selectedArtifact]);

  const preserveDraftRevision = () => {
    if (!selectedArtifact || !draftConflict) return;
    const proposal: Artifact = {
      ...selectedArtifact,
      id: `${selectedArtifact.id}:proposal:human-${uid()}`,
      title: `${selectedArtifact.title} — proposition de révision humaine`,
      content: documentDraft,
      updatedAt: now(),
      editedBy: "human",
      revision: 1,
      baseRevision: selectedArtifact.revision || 1,
      revisions: [],
    };
    onUpdate(
      appendEvent(
        { ...task, artifacts: [...task.artifacts, proposal] },
        "Révision humaine conservée",
        proposal.title,
      ),
    );
    draftCache.current.delete(draftOwner.current);
    setDocumentDirty(false);
    setSelectedId(proposal.id);
    onToast("Brouillon conservé dans une révision distincte");
  };

  const saveDiagram = useCallback(
    (next: DiagramModel, detail = "Diagramme mis à jour") => {
      setDiagram(next);
      diagramRef.current = next;
      if (selectedArtifact?.type === "diagram") {
        patchArtifact(selectedArtifact.id, serialize(next), {
          eventTitle: "Architecture enregistrée",
          eventDetail: detail,
        });
      }
    },
    [patchArtifact, selectedArtifact],
  );

  const saveWireframe = useCallback(
    (next: WireframeModel, detail?: string) => {
      setWireframe(next);
      if (selectedArtifact?.type === "wireframe") {
        patchArtifact(
          selectedArtifact.id,
          serialize(next),
          detail
            ? {
                eventTitle: "Support de cadrage mis à jour",
                eventDetail: detail,
              }
            : undefined,
        );
      }
    },
    [patchArtifact, selectedArtifact],
  );

  const toDiagramPoint = useCallback(
    (event: React.PointerEvent<SVGSVGElement>) => {
      const rect = svgRef.current?.getBoundingClientRect();
      if (!rect) return { x: 0, y: 0 };
      return {
        x: clamp(
          ((event.clientX - rect.left) / rect.width) * DIAGRAM_WIDTH,
          70,
          DIAGRAM_WIDTH - 70,
        ),
        y: clamp(
          ((event.clientY - rect.top) / rect.height) * DIAGRAM_HEIGHT,
          42,
          DIAGRAM_HEIGHT - 42,
        ),
      };
    },
    [],
  );

  const handleDiagramPointerDown = useCallback(
    (event: React.PointerEvent<SVGGElement>, node: DiagramNode) => {
      if (review) return;
      event.stopPropagation();
      const point = toDiagramPoint(
        event as unknown as React.PointerEvent<SVGSVGElement>,
      );
      setSelectedNodeId(node.id);
      setDrag({
        id: node.id,
        offsetX: point.x - node.x,
        offsetY: point.y - node.y,
        pointerId: event.pointerId,
      });
      svgRef.current?.setPointerCapture(event.pointerId);
    },
    [review, toDiagramPoint],
  );

  const handleDiagramPointerMove = useCallback(
    (event: React.PointerEvent<SVGSVGElement>) => {
      if (!drag || review || event.pointerId !== drag.pointerId) return;
      const point = toDiagramPoint(event);
      const current = diagramRef.current;
      if (!current) return;
      const next: DiagramModel = {
        ...current,
        nodes: current.nodes.map((node) =>
          node.id === drag.id
            ? {
                ...node,
                x: clamp(point.x - drag.offsetX, 90, DIAGRAM_WIDTH - 90),
                y: clamp(point.y - drag.offsetY, 55, DIAGRAM_HEIGHT - 55),
              }
            : node,
        ),
      };
      diagramRef.current = next;
      setDiagram(next);
    },
    [drag, review, toDiagramPoint],
  );

  const handleDiagramPointerUp = useCallback(
    (event: React.PointerEvent<SVGSVGElement>) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      const current = diagramRef.current;
      if (current && selectedArtifact?.type === "diagram") {
        const moved = current.nodes.find((node) => node.id === drag.id);
        patchArtifact(selectedArtifact.id, serialize(current), {
          eventTitle: "Architecture enregistrée",
          eventDetail: moved?.label || "Nœud déplacé",
        });
      }
      setDrag(null);
    },
    [drag, patchArtifact, selectedArtifact],
  );

  const updateNode = useCallback(
    (field: "label" | "sublabel", value: string) => {
      if (!diagram || !selectedNodeId) return;
      const next = {
        ...diagram,
        nodes: diagram.nodes.map((node) =>
          node.id === selectedNodeId ? { ...node, [field]: value } : node,
        ),
      };
      diagramRef.current = next;
      setDiagram(next);
    },
    [diagram, selectedNodeId],
  );

  const handleReviewCanvasClick = useCallback(
    (event: React.MouseEvent<HTMLDivElement>) => {
      if (
        !review ||
        !selectedArtifact ||
        !["diagram", "wireframe", "screenshot"].includes(selectedArtifact.type)
      )
        return;
      const target = event.target as HTMLElement;
      if (
        target.closest(
          'button, input, textarea, select, a, [data-review-ignore="true"]',
        )
      )
        return;
      const rect = event.currentTarget.getBoundingClientRect();
      if (!rect.width || !rect.height) return;
      setPendingPin({
        x: clamp(((event.clientX - rect.left) / rect.width) * 100, 0, 100),
        y: clamp(((event.clientY - rect.top) / rect.height) * 100, 0, 100),
      });
      setActiveFeedbackId(null);
    },
    [review, selectedArtifact],
  );

  const submitFeedback = useCallback(() => {
    if (!pendingPin || !selectedArtifact || !feedbackText.trim()) return;
    const feedback: Feedback = {
      id: uid(),
      x: pendingPin.x,
      y: pendingPin.y,
      text: feedbackText.trim(),
      resolved: false,
      artifactId: selectedArtifact.id,
      createdAt: new Date().toISOString(),
    };
    let nextTask: Task = { ...task, feedback: [...task.feedback, feedback] };
    nextTask = appendEvent(
      nextTask,
      "Commentaire épinglé",
      `${selectedArtifact.title} · ${feedback.text}`,
      "review",
      feedback.id,
    );
    onUpdate(nextTask);
    setPendingPin(null);
    setFeedbackText("");
    setActiveFeedbackId(feedback.id);
    onToast("Commentaire ajouté à la review");
  }, [
    appendEvent,
    feedbackText,
    onToast,
    onUpdate,
    pendingPin,
    selectedArtifact,
    task,
  ]);

  const toggleFeedback = useCallback(
    (feedback: Feedback) => {
      let nextTask: Task = {
        ...task,
        feedback: task.feedback.map((item) =>
          item.id === feedback.id
            ? { ...item, resolved: !item.resolved }
            : item,
        ),
      };
      nextTask = appendEvent(
        nextTask,
        feedback.resolved ? "Commentaire rouvert" : "Commentaire résolu",
        feedback.text,
        "review",
      );
      onUpdate(nextTask);
    },
    [appendEvent, onUpdate, task],
  );

  const deleteFeedback = useCallback(
    (feedback: Feedback) => {
      let nextTask: Task = {
        ...task,
        feedback: task.feedback.filter((item) => item.id !== feedback.id),
      };
      nextTask = appendEvent(
        nextTask,
        "Commentaire supprimé",
        feedback.text,
        "review",
      );
      onUpdate(nextTask);
      setActiveFeedbackId(null);
    },
    [appendEvent, onUpdate, task],
  );

  const addDocument = useCallback(() => {
    const artifact: Artifact = {
      id: `document-${uid()}`,
      title: "Nouveau document",
      type: "document",
      content: "# Nouveau document\n\n",
      updatedAt: now(),
      stepId: task.selectedStepId || task.activeStepId,
      revision: 1,
      editedBy: "human",
    };
    onUpdate(
      appendEvent(
        { ...task, artifacts: [...task.artifacts, artifact] },
        "Document ajouté",
        artifact.title,
      ),
    );
    setSelectedId(artifact.id);
    onToast("Nouveau document ajouté");
  }, [appendEvent, onToast, onUpdate, task]);

  const removeArtifact = useCallback(() => {
    if (!selectedArtifact) return;
    const index = task.artifacts.findIndex(
      (artifact) => artifact.id === selectedArtifact.id,
    );
    const remaining = task.artifacts.filter(
      (artifact) => artifact.id !== selectedArtifact.id,
    );
    const nextSelected =
      remaining[Math.min(index, Math.max(0, remaining.length - 1))]?.id ?? "";
    const nextTask = appendEvent(
      {
        ...task,
        artifacts: remaining,
        feedback: task.feedback.filter(
          (feedback) => feedback.artifactId !== selectedArtifact.id,
        ),
      },
      "Support supprimé",
      selectedArtifact.title,
    );
    onUpdate(nextTask);
    setSelectedId(nextSelected);
    onToast("Support supprimé");
  }, [appendEvent, onToast, onUpdate, selectedArtifact, task]);

  const handleUpload = useCallback(
    (event: React.ChangeEvent<HTMLInputElement>) => {
      const file = event.target.files?.[0];
      event.target.value = "";
      if (!file) return;
      if (!file.type.startsWith("image/")) {
        onToast("Choisissez une image");
        return;
      }
      if (file.size > MAX_SCREENSHOT_BYTES) {
        onToast("La capture doit faire 4 Mo ou moins");
        return;
      }
      setUploading(true);
      const reader = new FileReader();
      reader.onload = () => {
        if (typeof reader.result !== "string") {
          setUploading(false);
          onToast("Impossible de lire cette capture");
          return;
        }
        const artifact: Artifact = {
          id: `screenshot-${uid()}`,
          title: file.name.replace(/\.[^.]+$/, "") || "Capture",
          type: "screenshot",
          content: reader.result,
          updatedAt: now(),
          stepId: task.selectedStepId || task.activeStepId,
          revision: 1,
          editedBy: "human",
        };
        const nextTask = appendEvent(
          { ...task, artifacts: [...task.artifacts, artifact] },
          "Capture ajoutée",
          artifact.title,
        );
        if (JSON.stringify(nextTask).length > MAX_TASK_PERSISTED_BYTES) {
          setUploading(false);
          onToast(
            "Cette capture dépasserait la limite de sauvegarde de la mission",
          );
          return;
        }
        onUpdate(nextTask);
        setSelectedId(artifact.id);
        setUploading(false);
        onToast("Capture ajoutée");
      };
      reader.onerror = () => {
        setUploading(false);
        onToast("Impossible de lire cette capture");
      };
      reader.readAsDataURL(file);
    },
    [appendEvent, onToast, onUpdate, task],
  );

  const exportArtifact = useCallback(async () => {
    if (!selectedArtifact) return;
    const name = artifactFileName(selectedArtifact);
    if (selectedArtifact.type === "screenshot") {
      const blob = dataUrlBlob(selectedArtifact.content);
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = name;
      anchor.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 0);
      onToast("Capture téléchargée");
      return;
    }
    try {
      if (window.djinn?.saveArtifact) {
        await window.djinn.saveArtifact({
          name,
          content: selectedArtifact.content,
        });
        onToast("Support exporté");
        return;
      }
    } catch {
      // The browser download below remains the portable fallback.
    }
    const blob = new Blob([selectedArtifact.content], {
      type:
        selectedArtifact.type === "document"
          ? "text/markdown;charset=utf-8"
          : selectedArtifact.type === "code"
            ? "text/plain;charset=utf-8"
            : "application/json;charset=utf-8",
    });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = name;
    anchor.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
    onToast("Support téléchargé");
  }, [onToast, selectedArtifact]);

  const updateWireframe = useCallback(
    (next: WireframeModel, detail?: string) => {
      saveWireframe(next, detail);
    },
    [saveWireframe],
  );

  const renderPins = (artifactId: string) => {
    if (!review) return null;
    return (
      <>
        {task.feedback
          .filter((feedback) => feedback.artifactId === artifactId)
          .map((feedback, index) => (
            <button
              className={`art-feedback-pin ${feedback.resolved ? "is-resolved" : ""} ${activeFeedbackId === feedback.id ? "is-active" : ""}`}
              key={feedback.id}
              style={{ left: `${feedback.x}%`, top: `${feedback.y}%` }}
              onClick={(event) => {
                event.stopPropagation();
                setActiveFeedbackId(feedback.id);
                setPendingPin(null);
              }}
              aria-label={`Commentaire ${index + 1}: ${feedback.text}`}
              title={feedback.text}
            >
              {feedback.resolved ? (
                <Check size={12} strokeWidth={2.4} />
              ) : (
                index + 1
              )}
            </button>
          ))}
        {pendingPin && (
          <span
            className="art-feedback-pin art-feedback-pin--pending"
            style={{ left: `${pendingPin.x}%`, top: `${pendingPin.y}%` }}
            aria-hidden="true"
          >
            <CircleDot size={13} />
          </span>
        )}
      </>
    );
  };

  const renderDiagram = () => {
    const model = diagram || FALLBACK_DIAGRAM;
    const nodeById = new Map(model.nodes.map((node) => [node.id, node]));
    return (
      <div className="art-diagram-wrap">
        <div className="art-stage-caption">
          <span>
            <span className="art-caption-mark" /> architecture éditable
          </span>
          <span>glisser les nœuds pour réorganiser</span>
        </div>
        {model.nodes.length ? (
          <svg
            ref={svgRef}
            className="art-diagram"
            viewBox={`0 0 ${DIAGRAM_WIDTH} ${DIAGRAM_HEIGHT}`}
            role="img"
            aria-label="Diagramme éditable"
            onPointerMove={handleDiagramPointerMove}
            onPointerUp={handleDiagramPointerUp}
            onPointerCancel={handleDiagramPointerUp}
          >
            <defs>
              <pattern
                id="art-grid"
                width="28"
                height="28"
                patternUnits="userSpaceOnUse"
              >
                <path
                  d="M 28 0 L 0 0 0 28"
                  fill="none"
                  stroke="#333333"
                  strokeWidth="0.55"
                  opacity="0.65"
                />
              </pattern>
              <marker
                id="art-arrow"
                markerWidth="8"
                markerHeight="8"
                refX="7"
                refY="4"
                orient="auto"
              >
                <path d="M0,0 L8,4 L0,8 Z" fill="#a3a3a3" opacity="0.86" />
              </marker>
            </defs>
            <rect
              width={DIAGRAM_WIDTH}
              height={DIAGRAM_HEIGHT}
              fill="url(#art-grid)"
            />
            <g className="art-diagram-edges">
              {model.edges.map(([fromId, toId], index) => {
                const from = nodeById.get(fromId);
                const to = nodeById.get(toId);
                if (!from || !to) return null;
                const midpoint = (from.x + to.x) / 2;
                return (
                  <path
                    key={`${fromId}-${toId}-${index}`}
                    d={`M ${from.x} ${from.y} C ${midpoint} ${from.y}, ${midpoint} ${to.y}, ${to.x} ${to.y}`}
                    markerEnd="url(#art-arrow)"
                  />
                );
              })}
            </g>
            <g className="art-diagram-nodes">
              {model.nodes.map((node) => (
                <g
                  className={`art-diagram-node ${selectedNodeId === node.id ? "is-selected" : ""}`}
                  key={node.id}
                  transform={`translate(${node.x - 82} ${node.y - 34})`}
                  onPointerDown={(event) =>
                    handleDiagramPointerDown(event, node)
                  }
                  onClick={() => setSelectedNodeId(node.id)}
                  role="button"
                  tabIndex={0}
                  aria-label={`Modifier ${node.label}`}
                >
                  <rect width="164" height="68" rx="8" />
                  <rect
                    className="art-node-accent"
                    width="3"
                    height="68"
                    rx="1.5"
                  />
                  <text className="art-node-label" x="16" y="27">
                    {node.label.slice(0, 28)}
                  </text>
                  <text className="art-node-sublabel" x="16" y="47">
                    {node.sublabel.slice(0, 32)}
                  </text>
                  <circle className="art-node-port" cx="164" cy="34" r="3" />
                  <circle className="art-node-port" cx="0" cy="34" r="3" />
                </g>
              ))}
            </g>
          </svg>
        ) : (
          <div className="art-empty-stage">
            <Network size={30} />
            <span>Ce diagramme ne contient aucun nœud valide.</span>
          </div>
        )}
      </div>
    );
  };

  const renderWireframe = () => {
    const model = wireframe || parseWireframe("{}");
    const projects = model.projects?.length ? model.projects : DEFAULT_PROJECTS;
    const activeView = model.view || "active";
    const list = projects.filter((project) =>
      activeView === "archive"
        ? project.status === "Archivé"
        : project.status !== "Archivé",
    );
    const normalizedFilter =
      wireframeFilter === "Tous" ? null : wireframeFilter;
    const searched = list.filter((project) => {
      const text =
        `${project.name} ${project.owner} ${project.team}`.toLowerCase();
      return (
        (!normalizedFilter || project.status === normalizedFilter) &&
        (!wireframeSearch.trim() ||
          text.includes(wireframeSearch.trim().toLowerCase()))
      );
    });
    const byTeam = searched.reduce<Record<string, PreviewProject[]>>(
      (groups, project) => {
        groups[project.team] ||= [];
        groups[project.team].push(project);
        return groups;
      },
      {},
    );
    const changeFilter = (filter: string) => {
      setWireframeFilter(filter);
      updateWireframe(
        { ...model, filter, view: activeView },
        `Filtre ${filter.toLowerCase()}`,
      );
    };
    const changeView = (view: "active" | "archive") => {
      setWireframeFilter("Tous");
      updateWireframe(
        { ...model, view, filter: "Tous" },
        view === "archive" ? "Archives affichées" : "Projets actifs affichés",
      );
    };
    const cycleStatus = (project: PreviewProject) => {
      const statuses: PreviewProject["status"][] = [
        "Planifié",
        "En cours",
        "Review",
      ];
      const nextStatus =
        statuses[(statuses.indexOf(project.status) + 1) % statuses.length];
      const next = {
        ...model,
        projects: projects.map((item) =>
          item.id === project.id ? { ...item, status: nextStatus } : item,
        ),
      };
      updateWireframe(next, `${project.name} · ${nextStatus}`);
    };
    const addPreview = () => {
      const item: PreviewProject = {
        id: `preview-${uid()}`,
        name: "Nouveau projet",
        owner: "Vous",
        status: "Planifié",
        team: "Produit",
        progress: 12,
        accent: "#ebebeb",
      };
      updateWireframe(
        { ...model, projects: [...projects, item], view: "active" },
        "Projet de démonstration ajouté",
      );
      setWireframeFilter("Tous");
      onToast("Projet de démonstration ajouté au support");
    };
    return (
      <div className="art-wireframe-wrap">
        <div className="art-wireframe-frame">
          <aside className="art-mini-sidebar">
            <div className="art-mini-brand">
              <span className="art-brand-orb">D</span>
              <span>djinn</span>
            </div>
            <div className="art-mini-nav-label">Espace projet</div>
            <button
              className={`art-mini-nav-item ${activeView === "active" ? "is-current" : ""}`}
              onClick={() => changeView("active")}
            >
              <LayoutDashboard size={14} /> Projets{" "}
              <span>
                {
                  projects.filter((project) => project.status !== "Archivé")
                    .length
                }
              </span>
            </button>
            <button
              className={`art-mini-nav-item ${activeView === "archive" ? "is-current" : ""}`}
              onClick={() => changeView("archive")}
            >
              <Archive size={14} /> Archives{" "}
              <span>
                {
                  projects.filter((project) => project.status === "Archivé")
                    .length
                }
              </span>
            </button>
            <div className="art-mini-sidebar-bottom">
              <span className="art-mini-avatar">Y</span>
              <span>Votre espace</span>
            </div>
          </aside>
          <div className="art-mini-main">
            <div className="art-mini-header">
              <div>
                <span className="art-mini-eyebrow">SUPPORT DE CADRAGE</span>
                <h3>{model.heading}</h3>
              </div>
              <button className="art-mini-add" onClick={addPreview}>
                <Plus size={14} /> Ajouter
              </button>
            </div>
            <div className="art-mini-toolbar">
              <div
                className="art-mini-layout-toggle"
                aria-label="Organisation du support"
              >
                <button
                  className={model.layout === "status" ? "is-current" : ""}
                  onClick={() =>
                    updateWireframe(
                      { ...model, layout: "status" },
                      "Vue par statut",
                    )
                  }
                >
                  <SlidersHorizontal size={13} /> Statut
                </button>
                <button
                  className={model.layout === "team" ? "is-current" : ""}
                  onClick={() =>
                    updateWireframe(
                      { ...model, layout: "team" },
                      "Vue par équipe",
                    )
                  }
                >
                  <Users size={13} /> Équipe
                </button>
              </div>
              <label className="art-mini-search">
                <Search size={14} />
                <input
                  value={wireframeSearch}
                  onChange={(event) => setWireframeSearch(event.target.value)}
                  placeholder="Rechercher"
                  aria-label="Rechercher un projet"
                />
              </label>
            </div>
            <div className="art-mini-filter-row">
              <div className="art-mini-filters">
                {STATUS_FILTERS.map((filter) => (
                  <button
                    key={filter}
                    className={wireframeFilter === filter ? "is-active" : ""}
                    onClick={() => changeFilter(filter)}
                  >
                    {filter}
                  </button>
                ))}
              </div>
              <div
                className="art-mini-archive-mode"
                aria-label="Organisation des archives"
              >
                <span>Archives</span>
                <button
                  className={model.archive === "dedicated" ? "is-active" : ""}
                  onClick={() =>
                    updateWireframe(
                      { ...model, archive: "dedicated" },
                      "Archives dédiées",
                    )
                  }
                >
                  dédiées
                </button>
                <button
                  className={model.archive === "filter" ? "is-active" : ""}
                  onClick={() =>
                    updateWireframe(
                      { ...model, archive: "filter" },
                      "Archives comme filtre",
                    )
                  }
                >
                  filtre
                </button>
              </div>
              {activeView === "archive" && (
                <button
                  className="art-mini-back"
                  onClick={() => changeView("active")}
                >
                  <ChevronRight size={13} /> Projets actifs
                </button>
              )}
            </div>
            {model.layout === "team" ? (
              <div className="art-mini-groups">
                {Object.entries(byTeam).map(([team, teamProjects]) => (
                  <div className="art-mini-group" key={team}>
                    <div className="art-mini-group-heading">
                      <span>{team}</span>
                      <span>
                        {teamProjects.length.toString().padStart(2, "0")}
                      </span>
                    </div>
                    {teamProjects.map((project) => (
                      <ProjectCard
                        key={project.id}
                        project={project}
                        onCycle={() => cycleStatus(project)}
                      />
                    ))}
                  </div>
                ))}
              </div>
            ) : (
              <div className="art-mini-project-list">
                {searched.map((project) => (
                  <ProjectCard
                    key={project.id}
                    project={project}
                    onCycle={() => cycleStatus(project)}
                  />
                ))}
              </div>
            )}
            {!searched.length && (
              <div className="art-mini-empty">
                <Filter size={18} />
                <span>Aucun projet pour ce filtre.</span>
                <button onClick={() => changeFilter("Tous")}>
                  Réinitialiser
                </button>
              </div>
            )}
            <div className="art-mini-footnote">
              <Sparkles size={12} /> Interface de démonstration · les
              interactions modifient ce support de cadrage.
            </div>
          </div>
        </div>
      </div>
    );
  };

  const renderArtifactSurface = () => {
    if (!selectedArtifact)
      return (
        <div className="art-no-artifacts">
          <FolderOpen size={30} />
          <h3>Aucun support pour le moment</h3>
          <p>Ajoutez un document ou une capture pour commencer la review.</p>
          <button
            className="art-button art-button--primary"
            onClick={addDocument}
          >
            <Plus size={15} /> Ajouter un document
          </button>
        </div>
      );
    if (selectedArtifact.type === "diagram")
      return hasDiagramShape(selectedArtifact.content) ? (
        renderDiagram()
      ) : (
        <div className="art-text-preview">
          <div className="art-stage-caption">
            <span>
              <span className="art-caption-mark" /> texte de secours
            </span>
            <span>le JSON du diagramme est invalide</span>
          </div>
          <pre>{selectedArtifact.content}</pre>
        </div>
      );
    if (selectedArtifact.type === "document")
      return (
        <div className="art-text-preview">
          <MarkdownBody
            text={
              documentMode === "preview"
                ? documentDraft
                : selectedArtifact.content
            }
          />
        </div>
      );
    if (selectedArtifact.type === "visualization")
      return <VisualizationFrame artifact={selectedArtifact} />;
    if (selectedArtifact.type === "wireframe") return renderWireframe();
    if (selectedArtifact.type === "screenshot") {
      return selectedArtifact.content.startsWith("data:image/") ? (
        <div className="art-screenshot-wrap">
          <img src={selectedArtifact.content} alt={selectedArtifact.title} />
        </div>
      ) : (
        <div className="art-empty-stage">
          <FileImage size={30} />
          <span>Cette capture ne peut pas être affichée.</span>
        </div>
      );
    }
    return (
      <div className="art-text-preview">
        <div className="art-stage-caption">
          <span>
            <span className="art-caption-mark" /> aperçu texte brut
          </span>
          <span>aucun code n’est exécuté dans cette vue</span>
        </div>
        <pre>{selectedArtifact.content}</pre>
      </div>
    );
  };

  const renderEditor = () => {
    const diagramFallback =
      selectedArtifact?.type === "diagram" &&
      !hasDiagramShape(selectedArtifact.content);
    if (
      !selectedArtifact ||
      (!["document", "code", "visualization"].includes(selectedArtifact.type) &&
        !diagramFallback)
    )
      return null;
    return (
      <section className="art-editor-panel">
        <div className="art-panel-heading">
          <div>
            <span className="art-kicker">ÉDITEUR</span>
            <h3>
              {selectedArtifact.type === "document"
                ? "Modifier le document"
                : diagramFallback
                  ? "Réparer le diagramme"
                  : "Modifier le texte"}
            </h3>
          </div>
          <span className={`art-dirty ${documentDirty ? "is-dirty" : ""}`}>
            {documentDirty ? "Modifications non enregistrées" : "Enregistré"}
          </span>
        </div>
        <textarea
          value={documentDraft}
          onChange={(event) => {
            setDocumentDraft(event.target.value);
            setDocumentDirty(true);
            draftCache.current.set(draftOwner.current, {
              text: event.target.value,
              dirty: true,
              base: draftBase,
            });
          }}
          spellCheck={false}
          aria-label="Contenu du support"
        />
        <div className="art-editor-actions">
          <span>
            {selectedArtifact.type === "document"
              ? "Source Markdown"
              : selectedArtifact.type === "visualization"
                ? "Source HTML locale"
                : "Texte source"}{" "}
            · les modifications humaines sont conservées
          </span>
          <button
            className="art-button art-button--primary"
            onClick={saveTextArtifact}
            disabled={!documentDirty || draftConflict}
          >
            <Check size={15} /> Enregistrer
          </button>
        </div>
      </section>
    );
  };

  const renderDiagramInspector = () => {
    if (!selectedArtifact || selectedArtifact.type !== "diagram" || !diagram)
      return null;
    const selectedNode = diagram.nodes.find(
      (node) => node.id === selectedNodeId,
    );
    if (!selectedNode)
      return (
        <div className="art-inspector-empty">
          <Move size={16} />
          <span>Sélectionnez un nœud pour modifier son libellé.</span>
        </div>
      );
    return (
      <div className="art-inspector">
        <div className="art-panel-heading">
          <div>
            <span className="art-kicker">NŒUD SÉLECTIONNÉ</span>
            <h3>Propriétés</h3>
          </div>
          <Move size={15} />
        </div>
        <label>
          Libellé
          <input
            value={selectedNode.label}
            onChange={(event) => updateNode("label", event.target.value)}
          />
        </label>
        <label>
          Sous-libellé
          <input
            value={selectedNode.sublabel}
            onChange={(event) => updateNode("sublabel", event.target.value)}
          />
        </label>
        <button
          className="art-button art-button--secondary"
          onClick={() => saveDiagram(diagram, selectedNode.label)}
        >
          <Check size={14} /> Enregistrer le nœud
        </button>
      </div>
    );
  };

  const renderReviewPanel = () => {
    if (!selectedArtifact || !review) return null;
    const activeFeedback = selectedFeedback.find(
      (feedback) => feedback.id === activeFeedbackId,
    );
    return (
      <aside className="art-review-panel">
        <div className="art-panel-heading">
          <div>
            <span className="art-kicker">ANNOTATIONS</span>
            <h3>
              {selectedFeedback.length
                ? `${selectedFeedback.length} commentaire${selectedFeedback.length > 1 ? "s" : ""}`
                : "Aucun commentaire"}
            </h3>
          </div>
          <MessageCircle size={16} />
        </div>
        {pendingPin ? (
          <div className="art-pin-composer">
            <div className="art-pin-composer-title">
              <span className="art-pin-number">+</span>
              <span>Nouveau point de review</span>
              <button
                onClick={() => setPendingPin(null)}
                aria-label="Annuler le point"
              >
                <X size={14} />
              </button>
            </div>
            <textarea
              autoFocus
              value={feedbackText}
              onChange={(event) => setFeedbackText(event.target.value)}
              placeholder="Décrivez ce qui doit évoluer…"
              rows={3}
            />
            <button
              className="art-button art-button--primary"
              onClick={submitFeedback}
              disabled={!feedbackText.trim()}
            >
              <Send size={14} /> Épingler le commentaire
            </button>
          </div>
        ) : (
          <div className="art-pin-hint">
            <CircleDot size={15} />
            <span>Cliquez sur le support pour ajouter un point.</span>
          </div>
        )}
        {activeFeedback && (
          <div className="art-active-feedback">
            <div className="art-feedback-meta">
              <span>Point sélectionné</span>
              <button
                onClick={() => setActiveFeedbackId(null)}
                aria-label="Fermer"
              >
                <X size={13} />
              </button>
            </div>
            <p>{activeFeedback.text}</p>
            <div className="art-feedback-actions">
              <button onClick={() => toggleFeedback(activeFeedback)}>
                {activeFeedback.resolved ? (
                  <CircleDot size={13} />
                ) : (
                  <CheckCircle2 size={13} />
                )}{" "}
                {activeFeedback.resolved ? "Rouvrir" : "Résoudre"}
              </button>
              <button
                className="is-danger"
                onClick={() => deleteFeedback(activeFeedback)}
              >
                <Trash2 size={13} /> Supprimer
              </button>
            </div>
          </div>
        )}
        {selectedFeedback.length > 0 && (
          <div className="art-comment-list">
            {selectedFeedback.map((feedback, index) => (
              <button
                key={feedback.id}
                className={`art-comment-row ${activeFeedbackId === feedback.id ? "is-active" : ""} ${feedback.resolved ? "is-resolved" : ""}`}
                onClick={() => setActiveFeedbackId(feedback.id)}
              >
                <span className="art-comment-number">
                  {feedback.resolved ? <Check size={12} /> : index + 1}
                </span>
                <span className="art-comment-copy">
                  <span>{feedback.text}</span>
                  <small>
                    {feedback.resolved ? "Résolu" : "À traiter"} ·{" "}
                    {Math.round(feedback.x)}% × {Math.round(feedback.y)}%
                  </small>
                </span>
                <ChevronRight size={14} />
              </button>
            ))}
          </div>
        )}
      </aside>
    );
  };

  const supportedForReview = Boolean(
    selectedArtifact &&
    ["diagram", "wireframe", "screenshot"].includes(selectedArtifact.type),
  );

  return (
    <section
      className={`art-workspace ${review ? "is-review" : ""}`}
      aria-label={
        review ? "Espace de review des supports" : "Espace des supports"
      }
    >
      <header className="art-workspace-header">
        <div className="art-header-title">
          <div className="art-header-mark">
            <Sparkles size={15} />
          </div>
          <div>
            <span className="art-kicker">{review ? "REVIEW" : "SUPPORTS"}</span>
            <h2>{review ? "Review" : "Supports"}</h2>
          </div>
        </div>
        <div className="art-header-actions">
          <span className="art-header-count">
            {task.artifacts.length.toString().padStart(2, "0")} support
            {task.artifacts.length > 1 ? "s" : ""}
          </span>
          <button
            className="art-button art-button--secondary"
            onClick={exportArtifact}
            disabled={!selectedArtifact}
          >
            <ArrowDownToLine size={15} /> Exporter
          </button>
          <button
            className="art-icon-button art-icon-button--danger"
            onClick={removeArtifact}
            disabled={!selectedArtifact}
            title="Supprimer ce support"
            aria-label="Supprimer ce support"
          >
            <Trash2 size={15} />
          </button>
        </div>
      </header>
      {review && (
        <div className="art-review-banner">
          <span className="art-live-dot" />
          <strong>Review</strong>
          <span>
            {supportedForReview
              ? "Cliquez pour annoter."
              : "Annotations sur les supports visuels."}
          </span>
        </div>
      )}
      <div className="art-layout">
        <aside className="art-sidebar">
          <div className="art-sidebar-heading">
            <div>
              <span className="art-kicker">MISSION / SUPPORTS</span>
              <h3>Bibliothèque</h3>
            </div>
            <span className="art-sidebar-index">01</span>
          </div>
          <label className="art-sidebar-search">
            <Search size={14} />
            <input
              value={artifactQuery}
              onChange={(event) => setArtifactQuery(event.target.value)}
              placeholder="Filtrer les supports"
              aria-label="Filtrer les supports"
            />
          </label>
          <div className="art-artifact-list">
            {visibleArtifacts.map((artifact, index) => (
              <button
                key={artifact.id}
                className={`art-artifact-card ${selectedArtifact?.id === artifact.id ? "is-selected" : ""}`}
                onClick={() => setSelectedId(artifact.id)}
              >
                <span className="art-artifact-icon">
                  {typeIcon(artifact.type, 17)}
                </span>
                <span className="art-artifact-copy">
                  <strong>{artifact.title}</strong>
                  <em>
                    {artifact.sourceOfTruth ? "Source de vérité · " : ""}
                    {relativeTime(artifact.updatedAt)}
                  </em>
                </span>
                <span className="art-artifact-index">
                  {String(index + 1).padStart(2, "0")}
                </span>
              </button>
            ))}
          </div>
          {!visibleArtifacts.length && (
            <div className="art-sidebar-empty">
              Aucun support ne correspond à cette recherche.
            </div>
          )}
          <div className="art-sidebar-footer">
            <button className="art-sidebar-action" onClick={addDocument}>
              <Plus size={15} /> Nouveau document
            </button>
            <label className="art-sidebar-action">
              <Upload size={15} />{" "}
              {uploading ? "Lecture…" : "Importer une capture"}
              <input
                ref={uploadRef}
                type="file"
                accept="image/*"
                onChange={handleUpload}
                disabled={uploading}
              />
            </label>
          </div>
        </aside>
        <main className="art-main">
          <nav className="art-tabs" aria-label="Onglets des supports">
            {task.artifacts.map((artifact) => (
              <button
                key={artifact.id}
                className={
                  selectedArtifact?.id === artifact.id ? "is-selected" : ""
                }
                onClick={() => setSelectedId(artifact.id)}
              >
                {typeIcon(artifact.type, 14)}
                <span>{artifact.title}</span>
              </button>
            ))}
          </nav>
          {selectedArtifact ? (
            <>
              <div className="art-main-heading">
                <div>
                  <h1>{selectedArtifact.title}</h1>
                </div>
                <div className="art-main-meta">
                  <button
                    type="button"
                    className="art-button"
                    aria-pressed={selectedArtifact.sourceOfTruth === true}
                    onClick={() => {
                      const canonical = selectedArtifact.sourceOfTruth !== true;
                      onUpdate(
                        appendEvent(
                          {
                            ...task,
                            artifacts: task.artifacts.map((a) =>
                              a.id === selectedArtifact.id
                                ? { ...a, sourceOfTruth: canonical }
                                : a,
                            ),
                          },
                          canonical
                            ? "Source de vérité définie par vous"
                            : "Source de vérité retirée par vous",
                          selectedArtifact.title,
                        ),
                      );
                    }}
                  >
                    {selectedArtifact.sourceOfTruth
                      ? "Source de vérité"
                      : "Définir comme source de vérité"}
                  </button>
                  <span>
                    {typeIcon(selectedArtifact.type, 15)}{" "}
                    {typeLabel(selectedArtifact.type)}
                  </span>
                  <span>
                    <Clock3 size={14} />{" "}
                    {relativeTime(selectedArtifact.updatedAt)}
                  </span>
                </div>
              </div>
              <div
                className={`art-content-grid ${review ? "has-review-panel" : ""}`}
              >
                <div className="art-main-column">
                  {selectedArtifact.type === "document" && (
                    <div
                      className="art-document-modes"
                      role="group"
                      aria-label="Mode du document"
                    >
                      {(
                        [
                          ["read", "Lecture"],
                          ["edit", "Édition"],
                          ["preview", "Aperçu"],
                        ] as const
                      ).map(([mode, label]) => (
                        <button
                          key={mode}
                          type="button"
                          aria-pressed={documentMode === mode}
                          onClick={() => setDocumentMode(mode)}
                        >
                          {label}
                        </button>
                      ))}
                      <span>
                        {documentDirty
                          ? "Brouillon non enregistré"
                          : `Révision ${selectedArtifact.revision || 1} · ${selectedArtifact.editedBy === "human" ? "modification humaine" : "agent"}`}
                      </span>
                    </div>
                  )}
                  {proposalOriginal && (
                    <section
                      className="art-revision-notice"
                      aria-label="Proposition de révision"
                    >
                      <strong>
                        Proposition de révision · original conservé
                      </strong>
                      <p>
                        Cette proposition est un support distinct. Consultez les
                        deux versions avant de valider le résultat de l’étape.
                      </p>
                      <button
                        className="art-button"
                        onClick={() => setSelectedId(proposalOriginal.id)}
                      >
                        Consulter l’original
                      </button>
                      <details>
                        <summary>Comparer les sources</summary>
                        <div className="art-revision-comparison">
                          <pre>{proposalOriginal.content}</pre>
                          <pre>{selectedArtifact.content}</pre>
                        </div>
                      </details>
                    </section>
                  )}
                  {draftConflict && (
                    <section className="art-revision-notice" role="alert">
                      <strong>
                        Une nouvelle version est arrivée pendant votre édition
                      </strong>
                      <p>
                        Votre brouillon est conservé. Enregistrez-le comme
                        révision distincte ou repartez explicitement de la
                        version enregistrée.
                      </p>
                      <details>
                        <summary>
                          Comparer la version enregistrée et mon brouillon
                        </summary>
                        <div className="art-revision-comparison">
                          <pre>{selectedArtifact.content}</pre>
                          <pre>{documentDraft}</pre>
                        </div>
                      </details>
                      <div className="art-revision-actions">
                        <button
                          className="art-button"
                          onClick={preserveDraftRevision}
                        >
                          Conserver mon brouillon comme révision
                        </button>
                        <button
                          className="art-button"
                          onClick={() => {
                            setDocumentDraft(selectedArtifact.content);
                            setDraftBase(selectedArtifact.content);
                            setDocumentDirty(false);
                            draftCache.current.delete(draftOwner.current);
                          }}
                        >
                          Repartir de la version enregistrée
                        </button>
                      </div>
                    </section>
                  )}
                  {selectedArtifact.revisions?.length ? (
                    <details className="art-revision-history">
                      <summary>
                        Historique · {selectedArtifact.revisions.length}{" "}
                        révision(s) conservée(s)
                      </summary>
                      {[...selectedArtifact.revisions]
                        .reverse()
                        .map((revision, index) => (
                          <details key={`${revision.revision}-${index}`}>
                            <summary>
                              Révision {revision.revision} ·{" "}
                              {revision.editedBy === "human" ? "vous" : "agent"}{" "}
                              · {relativeTime(revision.updatedAt)}
                            </summary>
                            <pre>{revision.content}</pre>
                          </details>
                        ))}
                    </details>
                  ) : null}
                  {(selectedArtifact.type !== "document" ||
                    documentMode !== "edit") && (
                    <div
                      className={`art-canvas-shell ${review && supportedForReview ? "is-reviewable" : ""}`}
                    >
                      <div
                        className={`art-canvas-surface ${selectedArtifact.type === "screenshot" ? "is-image-canvas" : ""}`}
                        onClick={
                          review && supportedForReview
                            ? handleReviewCanvasClick
                            : undefined
                        }
                      >
                        {renderArtifactSurface()}
                        {renderPins(selectedArtifact.id)}
                      </div>
                    </div>
                  )}
                  {(selectedArtifact.type !== "document" ||
                    documentMode === "edit") &&
                    renderEditor()}
                  {renderDiagramInspector()}
                </div>
                {review ? renderReviewPanel() : null}
              </div>
            </>
          ) : (
            <div className="art-empty-workspace">
              <FolderOpen size={32} />
              <h3>La bibliothèque est prête.</h3>
              <p>Ajoutez un document ou une capture.</p>
              <button
                className="art-button art-button--primary"
                onClick={addDocument}
              >
                <Plus size={15} /> Ajouter un document
              </button>
            </div>
          )}
        </main>
      </div>
    </section>
  );
}

function ProjectCard({
  project,
  onCycle,
}: {
  project: PreviewProject;
  onCycle: () => void;
}) {
  return (
    <article className="art-mini-project">
      <div className="art-mini-project-top">
        <span
          className="art-project-initial"
          style={{ background: `${project.accent}20`, color: project.accent }}
        >
          {project.name.slice(0, 1)}
        </span>
        <div className="art-mini-project-name">
          <strong>{project.name}</strong>
          <span>
            {project.team} · {project.owner}
          </span>
        </div>
        <button className="art-mini-status" onClick={onCycle}>
          <StatusDot status={project.status} /> {project.status}
        </button>
      </div>
      <div className="art-mini-progress">
        <span>
          <i
            style={{
              width: `${clamp(project.progress, 0, 100)}%`,
              background: project.accent,
            }}
          />
        </span>
        <small>{project.progress}%</small>
      </div>
    </article>
  );
}
