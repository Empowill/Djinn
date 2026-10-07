export type StepType =
  | "discussion"
  | "exploration"
  | "reflection"
  | "specification"
  | "prototype"
  | "implementation"
  | "review"
  | "delivery";
export type StepStatus =
  | "pending"
  | "running"
  | "awaiting_human"
  | "completed"
  | "blocked"
  | "paused"
  | "error";
/** How a stage's successful result is validated before the next stage starts. */
export type StepValidation = "human" | "automatic";
export interface StepScope {
  stepId?: string;
  runId?: string;
}
export interface MissionStep {
  id: string;
  type: StepType;
  title: string;
  objective: string;
  status: StepStatus;
  exitCriteria: string[];
  expectedArtifacts: string[];
  skills: string[];
  /** Missing values retain the legacy human-validation behavior. */
  validation?: StepValidation;
  startedAt?: string;
  completedAt?: string;
  approvedAt?: string;
  approvedBy?: "human";
  summary?: string;
  report?: StepResult;
  needsRevalidation?: boolean;
}
export type WorkflowProposalStep = {
  type: Exclude<StepType, "discussion">;
  title: string;
  objective: string;
  exitCriteria?: string[];
  expectedArtifacts?: string[];
  skills?: string[];
  /** Missing values retain the legacy human-validation behavior. */
  validation?: StepValidation;
};
export type WorkflowAmendmentStep = WorkflowProposalStep | MissionStep;
export interface WorkflowAmendment {
  steps: WorkflowAmendmentStep[];
  reason: string;
  /** Current step whose identity and result must remain untouched. */
  currentStepId?: string;
}
export interface WorkflowProposal {
  steps: WorkflowProposalStep[];
  reason: string;
  /** The discussion slot that is waiting for this proposal. */
  stepId?: string;
}
export interface WorkflowTemplate {
  id: string;
  title: string;
  steps: MissionStep[];
}
export interface ProjectPreferences {
  provider?: ProviderId;
  model?: string;
  concurrency?: number;
}
export interface SourceOfTruth {
  id: string;
  title: string;
  path: string;
  description: string;
}
export interface Project {
  id: string;
  name: string;
  directory: string;
  conventions: string;
  locations: Record<string, string>;
  workflows: WorkflowTemplate[];
  /** Defaults applied when a new mission is created in this project. */
  preferences?: ProjectPreferences;
  workflowPolicy?: "flexible" | "enforced";
  sourcesOfTruth?: SourceOfTruth[];
  updatedAt: string;
}
export interface ProjectDiscoveryReport {
  directory: string;
  name: string;
  scannedAt: string;
  filesScanned: number;
  historyCount: number;
  summary: string;
  evidence: {
    path: string;
    kind:
      "instructions" | "skill" | "automation" | "prototype" | "documentation";
    excerpt: string;
  }[];
  workflows: WorkflowTemplate[];
  sourcesOfTruth: SourceOfTruth[];
  locations: Record<string, string>;
  conventions: string;
  notes: string[];
  analysis: {
    provider?: ProviderId;
    status: "local" | "agent" | "fallback";
    detail?: string;
  };
}
export interface ProjectSnapshot extends Project {
  capturedAt: string;
}
export type Phase = "brief" | "execution" | "review" | "delivery";
export type ProviderId = "codex" | "claude";
export type AgentStatus = "queued" | "running" | "blocked" | "done" | "error";
export interface Option {
  id: string;
  label: string;
  description: string;
}
export interface Question extends StepScope {
  id: string;
  title: string;
  context: string;
  recommendation: string;
  options: Option[];
  blocking: boolean;
  blockingScope?: "agent" | "mission";
  workItemId?: string;
  unlocks: string;
  agentId?: string;
  answer?: string;
  answeredAt?: string;
  theme?: string;
}
export interface Agent extends StepScope {
  /** A provider-created child is observed, never queued as a new Djinn worker. */
  origin?: "codex";
  live?: boolean;
  provider?: ProviderId;
  providerThreadId?: string;
  parentAgentId?: string;
  activity?: string;
  writeScope?: string[];
  dependsOn?: string[];
  isolation?: "shared" | "worktree";
  resources?: ResourceProfile;
  readOnly?: boolean;
  waitReason?: string;
  waitingForAgentIds?: string[];
  id: string;
  name: string;
  role: string;
  model: string;
  status: AgentStatus;
  summary: string;
  progress: number;
  prompt?: string;
  worktree?: string;
  branch?: string;
}
export interface ResourceProfile {
  cpu?: number;
  memoryMb?: number;
  labels?: string[];
  requires?: string[];
  excludes?: string[];
  exclusive?: string[];
}
export interface FlightEvent {
  id: string;
  time: string;
  type: "note" | "agent" | "decision" | "tool" | "error" | "phase" | "review";
  title: string;
  detail: string;
  agentId?: string;
  lifecycle?: "started" | "completed" | "blocked";
  runId?: string;
  worktree?: string;
  branch?: string;
  actor?: "human" | "agent";
  interventionId?: string;
  stepId?: string;
}
export interface Artifact extends StepScope {
  id: string;
  title: string;
  type:
    | "diagram"
    | "wireframe"
    | "document"
    | "code"
    | "screenshot"
    | "visualization";
  content: string;
  updatedAt: string;
  sourceOfTruth?: boolean;
  revision?: number;
  editedBy?: "human" | "agent";
  baseRevision?: number;
  needsRevalidation?: boolean;
  revisions?: {
    revision: number;
    content: string;
    updatedAt: string;
    editedBy: "human" | "agent";
  }[];
}
export interface Feedback extends StepScope {
  id: string;
  x: number;
  y: number;
  text: string;
  resolved: boolean;
  artifactId: string;
  createdAt?: string;
}
export interface TaskAction extends StepScope {
  id: string;
  kind: "server" | "link" | "manual";
  title: string;
  detail?: string;
  status: "pending" | "running" | "ready" | "done" | "error" | "stopped";
  createdAt: string;
  updatedAt: string;
  agentId?: string;
  workItemId?: string;
  target?: string;
  url?: string;
  script?: string;
  directory?: string;
  error?: string;
  testInstructions?: string[];
  expectedResult?: string;
  testStartedAt?: string;
  testResult?: {
    status: "passed" | "problem" | "deferred";
    detail?: string;
    recordedAt: string;
  };
}
export type PermissionDecision = "accept" | "acceptForSession" | "decline";
export interface PermissionRequest extends StepScope {
  id: string;
  taskId: string;
  runId: string;
  agentId: string;
  agentName?: string;
  provider: ProviderId;
  method: string;
  title: string;
  reason?: string;
  command?: string;
  cwd?: string;
  paths?: string[];
  status: "pending" | "accepted" | "declined" | "cancelled";
  createdAt: string;
  updatedAt?: string;
  canAcceptForSession: boolean;
  questions?: {
    id: string;
    question: string;
    optional?: boolean;
    options?: { label: string; description: string }[];
  }[];
}
export interface StepResult extends StepScope {
  status: "ready" | "blocked" | "needs_input";
  summary: string;
  criteria?: { criterion: string; met: boolean; evidence?: string }[];
  reason?: string;
  nextAction?: string;
  completed?: string[];
  remaining?: string[];
  evidence?: string[];
  reportedAt?: string;
}
export interface MissionWorkItem extends StepScope {
  id: string;
  title: string;
  status: "pending" | "running" | "blocked" | "ready" | "done";
  detail?: string;
  agentId?: string;
  ticket?: string;
  worktree?: string;
  branch?: string;
  updatedAt: string;
}
export interface MissionReport extends StepResult {
  id: string;
  agentId?: string;
  updatedAt: string;
}
export interface NativeRuntimeRun {
  taskId: string;
  runId: string;
  stepId?: string;
  mode: "plan" | "execute" | "review";
  startedAt: string;
  status: "running" | "stopping";
  phase: "workers" | "lead";
  lastActivityAt?: string;
  activeAgents: {
    id: string;
    name: string;
    task: string;
    runId: string;
    readOnly: boolean;
    origin?: "codex";
    provider?: ProviderId;
    providerThreadId?: string;
    parentAgentId?: string;
    activity?: string;
    model?: string;
    status?: AgentStatus;
  }[];
  events: RuntimeEvent[];
  structuredEvents?: RuntimeEvent[];
  observedAgents?: Agent[];
}
export interface Task {
  id: string;
  title: string;
  brief: string;
  project: string;
  agentHistory?: Record<string, Agent[]>;
  providerSessions?: Record<string, string>;
  permissions?: PermissionRequest[];
  stepResult?: StepResult;
  workItems?: MissionWorkItem[];
  reports?: MissionReport[];
  runtimeEventIds?: string[];
  activity?: {
    lead: "supervises" | "responds" | "integrates";
    activeAgents: { id: string; task: string; runId: string }[];
    lastActivityAt?: string;
  };
  projectId?: string;
  projectSnapshot?: ProjectSnapshot;
  steps?: MissionStep[];
  activeStepId?: string;
  selectedStepId?: string;
  progressionPolicy?: "manual";
  workflowMode?: "flexible" | "fixed";
  /** New missions let the lead define the timeline from the initial intent. */
  workflowOrigin?: "agent" | "legacy";
  /** The proposal being qualified; it is cleared when the timeline is applied. */
  initialWorkflowProposal?: WorkflowProposal & { stepId: string };
  /** Durable record of the timeline selected during qualification. */
  workflowProposal?: WorkflowProposal & { stepId: string };
  /** Lead amendment waiting for the owning run to finish before application. */
  workflowAmendment?: WorkflowAmendment & { stepId: string; runId?: string };
  nextStepProposal?: {
    type: StepType;
    title: string;
    objective: string;
    reason: string;
    stepId: string;
    validation?: StepValidation;
  };
  /** Lead-only proposal that classifies the initial automatic discussion. */
  initialStepProposal?: {
    type: Exclude<StepType, "discussion" | "review" | "delivery">;
    title: string;
    objective: string;
    reason: string;
    stepId: string;
    validation?: StepValidation;
  };
  titleSource?: "placeholder" | "agent" | "human";
  titleGeneratedAt?: string;
  titleEditedAt?: string;
  legacyHistory?: boolean;
  provider: ProviderId;
  model: string;
  phase: Phase;
  status: "idle" | "running" | "waiting" | "paused" | "done" | "error";
  createdAt: string;
  questions: Question[];
  agents: Agent[];
  events: FlightEvent[];
  artifacts: Artifact[];
  feedback: Feedback[];
  actions?: TaskAction[];
  instructions?: {
    id: string;
    text: string;
    time: string;
    appliedAt?: string;
    status?: "queued" | "transmitted" | "consumed" | "prevented";
    reason?: string;
    stepId?: string;
    runId?: string;
    agentId?: string;
  }[];
  demo?: boolean;
  runId?: string;
  runMode?: "plan" | "execute" | "review";
  planCompleted?: boolean;
  reviewApprovedAt?: string;
  configuration: {
    prototype: string;
    review: string;
    deliverables: string[];
    concurrency: number;
  };
}
export interface AppState {
  version: 1 | 2;
  projects?: Project[];
  tasks: Task[];
  selectedId: string;
  settings: {
    provider: ProviderId;
    model: string;
    reduceMotion: boolean;
    sound: boolean;
  };
}
export interface RuntimeEvent {
  eventId?: string;
  stepId?: string;
  runId: string;
  taskId: string;
  type: string;
  timestamp: string;
  data: Record<string, unknown>;
}
/** Native envelope alias used by paged runtime journals and notification clicks. */
export type RuntimeEnvelope = RuntimeEvent;
export interface ProviderModel {
  id: string;
  name: string;
  description?: string;
  isDefault?: boolean;
  isLegacy?: boolean;
  aliases?: string[];
}
export interface ProviderModelCatalog {
  provider: ProviderId;
  models: ProviderModel[];
  source: "cli" | "t3-manifest";
  error?: string;
}
export interface Environment {
  platform: string;
  appVersion: string;
  providers: {
    id: ProviderId;
    name: string;
    available: boolean;
    authenticated: boolean | null;
    version?: string;
    command?: string;
  }[];
}
export interface RunInput {
  providerSessions?: Record<string, string>;
  stepId?: string;
  step?: MissionStep;
  projectSnapshot?: ProjectSnapshot;
  workflowMode?: Task["workflowMode"];
  workflowOrigin?: Task["workflowOrigin"];
  initialStepProposal?: Task["initialStepProposal"];
  initialWorkflowProposal?: Task["initialWorkflowProposal"];
  workflowProposal?: Task["workflowProposal"];
  workflowAmendment?: Task["workflowAmendment"];
  previousSteps?: { id: string; title: string; summary: string }[];
  taskId: string;
  provider: ProviderId;
  cwd: string;
  prompt: string;
  model?: string;
  mode?: string;
  concurrency?: number;
  images?: { id: string; title: string; dataUrl: string }[];
  agents?: {
    id: string;
    name: string;
    role: string;
    prompt: string;
    writeScope?: string[];
    dependsOn?: string[];
    isolation?: Agent["isolation"];
    resources?: ResourceProfile;
    readOnly?: boolean;
  }[];
  guidance?: { id: string; text: string; agentId?: string }[];
  decisions?: string[];
  priorSummaries?: string[];
}
export interface DjinnBridge {
  getPendingPermissions?(): Promise<PermissionRequest[]>;
  respondPermission?(input: {
    taskId: string;
    requestId: string;
    decision: PermissionDecision;
    answers?: Record<string, string>;
  }): Promise<{ resolved: boolean }>;
  discoverProject?(input: {
    directory: string;
    provider: ProviderId;
    model?: string;
    includeHistory?: boolean;
    scanId: string;
  }): Promise<ProjectDiscoveryReport>;
  cancelProjectDiscovery?(scanId: string): Promise<{ cancelled: boolean }>;

  renderVisualization(source: string): Promise<{ url: string; token: string }>;
  validateProject(project: Project): Promise<Project>;
  getActions(taskId: string): Promise<TaskAction[]>;
  performAction(input: {
    taskId: string;
    cwd: string;
    action: TaskAction;
    operation: "run" | "stop" | "complete" | "open";
  }): Promise<TaskAction>;
  getEnvironment(): Promise<Environment>;
  getProviderModels?(
    provider: ProviderId,
    refresh?: boolean,
  ): Promise<ProviderModelCatalog>;
  getRuntimeSnapshot?(): Promise<{
    capturedAt: string;
    runs: NativeRuntimeRun[];
    notificationClicks?: RuntimeEnvelope[];
    permissions?: PermissionRequest[];
  }>;
  getMissionInteractions?(taskId: string): Promise<{ taskId: string; events: RuntimeEvent[] }>;
  getMissionJournalPage?(
    taskId: string,
    cursor?: number,
    limit?: number,
  ): Promise<{
    events: RuntimeEnvelope[];
    nextCursor: number | null;
    hasMore: boolean;
  }>;
  notifyQuestion(input: {
    taskId: string;
    questionId: string;
    title: string;
    body: string;
  }): Promise<{
    shown: boolean;
    error?: string;
    message?: string;
    reason?: string;
  }>;
  selectDirectory(): Promise<string | null>;
  loadState(): Promise<AppState | null>;
  saveState(state: AppState): Promise<unknown>;
  exportSession(session: unknown): Promise<unknown>;
  importSession(): Promise<unknown>;
  startRun(input: RunInput): Promise<{ runId: string }>;
  steerRun(input: {
    runId: string;
    id: string;
    text: string;
    agentId?: string;
  }): Promise<{
    id: string;
    status: string;
    reason?: string;
    delivery?: string;
  }>;
  cancelRun(runId: string): Promise<unknown>;
  loginProvider(provider: ProviderId): Promise<unknown>;
  onEvent(callback: (event: RuntimeEvent) => void): () => void;
  openExternal(url: string): Promise<unknown>;
  saveArtifact(input: { name: string; content: string }): Promise<unknown>;
}
declare global {
  interface Window {
    djinn?: DjinnBridge;
  }
}
