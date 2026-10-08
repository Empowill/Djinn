import {
  AlertCircle,
  Check,
  FileUp,
  FolderOpen,
  Loader2,
  Plus,
  Sparkles,
  Trash2,
  X,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import type {
  Project,
  ProjectDiscoveryReport,
  ProviderId,
  SourceOfTruth,
} from "./types";
import { now, uid } from "./data";
import { validateProject } from "./workflow";
import { ModelPicker } from "./model-picker";
import { language, t } from "./i18n";
import "./project-setup.css";

export interface ProjectSetupProps {
  /** Undefined opens the setup in create mode. */
  project?: Project;
  onClose: () => void;
  onSave: (project: Project) => void | Promise<void>;
  onToast: (message: string) => void;
  defaultProvider: ProviderId;
  defaultModel: string;
}

type DiscoveryKind = ProjectDiscoveryReport["evidence"][number]["kind"];

const MAX_SUB_AGENTS = 16;

const kindLabels: Record<DiscoveryKind, string> = {
  instructions: "Instructions",
  skill: "Skills",
  automation: "Scripts",
  prototype: "Prototypes",
  documentation: "Documentation",
};

const cleanName = (value: string) => value.trim();

function providerName(provider: ProviderId): string {
  return provider === "codex" ? "Codex" : "Claude Code";
}

function pathName(directory: string): string {
  return (
    directory
      .replace(/[\\/]+$/u, "")
      .split(/[\\/]/u)
      .filter(Boolean)
      .at(-1) || t("setup.default_project_name")
  );
}

function scanId(): string {
  return typeof crypto !== "undefined" &&
    typeof crypto.randomUUID === "function"
    ? crypto.randomUUID()
    : `scan-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function emptyReport(
  directory: string,
  provider: ProviderId,
  detail: string,
): ProjectDiscoveryReport {
  return {
    directory,
    name: pathName(directory),
    scannedAt: now(),
    filesScanned: 0,
    historyCount: 0,
    summary: t("setup.fallback_summary"),
    evidence: [],
    workflows: [],
    sourcesOfTruth: [],
    locations: {},
    conventions: "",
    notes: [detail],
    analysis: { provider, status: "fallback", detail },
  };
}

function normalizeReport(
  value: ProjectDiscoveryReport,
  directory: string,
  provider: ProviderId,
): ProjectDiscoveryReport {
  const evidence = Array.isArray(value.evidence)
    ? value.evidence.filter(
        (entry) =>
          entry &&
          typeof entry.path === "string" &&
          typeof entry.excerpt === "string" &&
          entry.kind in kindLabels,
      )
    : [];
  const workflows = Array.isArray(value.workflows)
    ? value.workflows.filter(
        (workflow) =>
          workflow &&
          typeof workflow.id === "string" &&
          typeof workflow.title === "string" &&
          Array.isArray(workflow.steps) &&
          workflow.steps.length > 0,
      )
    : [];
  const sourcesOfTruth = Array.isArray(value.sourcesOfTruth)
    ? value.sourcesOfTruth.filter(
        (source) =>
          source &&
          typeof source.id === "string" &&
          typeof source.title === "string" &&
          typeof source.path === "string",
      )
    : [];
  return {
    // The main process may canonicalize aliases such as /var and /private.
    // Preserve that report value for display instead of treating it as stale.
    directory:
      typeof value.directory === "string" && value.directory
        ? value.directory
        : directory,
    name:
      typeof value.name === "string" && value.name
        ? value.name
        : pathName(directory),
    scannedAt:
      typeof value.scannedAt === "string" && value.scannedAt
        ? value.scannedAt
        : now(),
    filesScanned:
      typeof value.filesScanned === "number" && value.filesScanned >= 0
        ? value.filesScanned
        : 0,
    historyCount:
      typeof value.historyCount === "number" && value.historyCount >= 0
        ? value.historyCount
        : 0,
    summary: typeof value.summary === "string" ? value.summary : "",
    evidence,
    workflows: workflows.slice(0, 6),
    sourcesOfTruth,
    locations:
      value.locations && typeof value.locations === "object"
        ? value.locations
        : {},
    conventions: typeof value.conventions === "string" ? value.conventions : "",
    notes: Array.isArray(value.notes)
      ? value.notes.filter((note): note is string => typeof note === "string")
      : [],
    analysis: {
      provider: value.analysis?.provider || provider,
      status: value.analysis?.status || "agent",
      detail: value.analysis?.detail,
    },
  };
}

function sourceSignature(source: SourceOfTruth): string {
  const path = source.path.trim().replaceAll("\\", "/").toLocaleLowerCase();
  return path || source.title.trim().toLocaleLowerCase();
}

const emptySource = (): SourceOfTruth => ({
  id: uid(),
  title: "",
  path: "",
  description: "",
});

function ProviderChoice({
  provider,
  onChange,
}: {
  provider: ProviderId;
  onChange: (provider: ProviderId) => void;
}) {
  return (
    <div
      className="project-setup-provider"
      role="group"
      aria-label={t("setup.provider")}
    >
      {(["codex", "claude"] as const).map((candidate) => (
        <button
          key={candidate}
          type="button"
          aria-pressed={provider === candidate}
          className={provider === candidate ? "is-selected" : ""}
          onClick={() => onChange(candidate)}
        >
          {providerName(candidate)}
        </button>
      ))}
    </div>
  );
}

function formatCount(value: number): string {
  return new Intl.NumberFormat(language).format(value);
}

function reportStatus(report: ProjectDiscoveryReport): string {
  if (report.analysis.status === "fallback") return t("setup.status_fallback");
  if (report.analysis.status === "local") return t("setup.status_local");
  return t("setup.status_agent", {
    provider: providerName(report.analysis.provider || "codex"),
  });
}

export function ProjectSetupReport({
  report,
  selectedSources,
  includeConventions,
  onSourceSelection,
  onIncludeConventions,
  onApply,
}: {
  report: ProjectDiscoveryReport;
  selectedSources: Set<string>;
  includeConventions: boolean;
  onSourceSelection: (id: string, selected: boolean) => void;
  onIncludeConventions: (selected: boolean) => void;
  onApply: () => void;
}) {
  const grouped = useMemo(() => {
    const groups: Record<
      "knowledge" | "scripts" | "skills" | "references",
      typeof report.evidence
    > = {
      knowledge: [],
      scripts: [],
      skills: [],
      references: [],
    };
    report.evidence.forEach((entry) => {
      if (entry.kind === "skill") groups.skills.push(entry);
      else if (entry.kind === "automation") groups.scripts.push(entry);
      else if (entry.kind === "prototype") groups.references.push(entry);
      else groups.knowledge.push(entry);
    });
    return groups;
  }, [report.evidence]);

  const renderEvidence = (
    title: string,
    entries: typeof report.evidence,
    empty: string,
  ) => (
    <section className="project-setup-evidence-group" aria-label={title}>
      <div className="project-setup-subheading">
        <h4>{title}</h4>
        <span>{entries.length}</span>
      </div>
      {entries.length ? (
        <ul>
          {entries.slice(0, 5).map((entry) => (
            <li key={`${entry.kind}:${entry.path}`}>
              <code>{entry.path}</code>
              {entry.excerpt && <p>{entry.excerpt}</p>}
            </li>
          ))}
        </ul>
      ) : (
        <p className="project-setup-empty-copy">{empty}</p>
      )}
    </section>
  );

  return (
    <section
      className="project-setup-report"
      aria-labelledby="project-setup-report-title"
    >
      <div className="project-setup-report-heading">
        <div>
          <span className="project-setup-kicker">
            {t("setup.report_kicker")}
          </span>
          <h3 id="project-setup-report-title">{report.name}</h3>
          <code className="project-setup-report-directory">
            {report.directory}
          </code>
        </div>
        <span
          className={`project-setup-analysis-status is-${report.analysis.status}`}
        >
          {reportStatus(report)}
        </span>
      </div>
      <p className="project-setup-summary">
        {report.summary || t("setup.no_summary")}
      </p>
      <div
        className="project-setup-metrics"
        aria-label={t("setup.scan_summary")}
      >
        <span>
          <strong>{formatCount(report.filesScanned)}</strong>{" "}
          {t("setup.files_found", { count: report.filesScanned })}
        </span>
        <span>
          <strong>{formatCount(report.historyCount)}</strong>{" "}
          {t("setup.history_items", { count: report.historyCount })}
        </span>
        <span>
          <strong>{report.sourcesOfTruth.length}</strong>{" "}
          {t("setup.suggested_references", {
            count: report.sourcesOfTruth.length,
          })}
        </span>
      </div>
      {report.analysis.detail && (
        <p className="project-setup-analysis-detail">
          {report.analysis.detail}
        </p>
      )}
      <details className="project-setup-evidence-details">
        <summary>{t("setup.evidence_toggle")}</summary>
        <div className="project-setup-evidence-grid">
          {renderEvidence(
            t("setup.group_knowledge"),
            grouped.knowledge,
            t("setup.no_instructions"),
          )}
          {renderEvidence("Scripts", grouped.scripts, t("setup.no_scripts"))}
          {renderEvidence("Skills", grouped.skills, t("setup.no_skills"))}
          {renderEvidence(
            t("setup.group_references"),
            grouped.references,
            t("setup.no_references"),
          )}
        </div>
      </details>
      {!!report.sourcesOfTruth.length && (
        <section
          className="project-setup-suggestions"
          aria-labelledby="project-setup-sources-title"
        >
          <div className="project-setup-subheading">
            <h4 id="project-setup-sources-title">
              {t("setup.suggested_sources")}
            </h4>
            <span>{report.sourcesOfTruth.length}</span>
          </div>
          <div className="project-setup-choice-list">
            {report.sourcesOfTruth.map((source) => (
              <label key={source.id} className="project-setup-choice">
                <input
                  type="checkbox"
                  checked={selectedSources.has(source.id)}
                  onChange={(event) =>
                    onSourceSelection(source.id, event.target.checked)
                  }
                />
                <span>
                  <strong>{source.title}</strong>
                  <small>{source.path}</small>
                </span>
              </label>
            ))}
          </div>
        </section>
      )}
      {!!report.conventions.trim() && (
        <label className="project-setup-conventions-choice">
          <input
            type="checkbox"
            checked={includeConventions}
            onChange={(event) => onIncludeConventions(event.target.checked)}
          />
          <span>
            <strong>{t("setup.add_conventions")}</strong>
            <small>{t("setup.add_conventions_detail")}</small>
          </span>
        </label>
      )}
      {!!report.notes.length && (
        <ul className="project-setup-notes">
          {report.notes.slice(0, 4).map((note) => (
            <li key={note}>{note}</li>
          ))}
        </ul>
      )}
      <button
        type="button"
        className="button accent project-setup-apply"
        onClick={onApply}
      >
        <Check size={14} aria-hidden="true" /> {t("setup.apply_suggestions")}
      </button>
    </section>
  );
}

function buildProject(
  project: Project | undefined,
  fields: {
    name: string;
    directory: string;
    conventions: string;
    locations: Record<string, string>;
    workflows: Project["workflows"];
    sourcesOfTruth: SourceOfTruth[];
    provider: ProviderId;
    model: string;
    concurrency?: number;
    policy: "flexible" | "enforced";
  },
): Project {
  const base: Project = {
    id: project?.id || uid(),
    name: cleanName(fields.name),
    directory: fields.directory.trim(),
    conventions: fields.conventions,
    locations: fields.locations,
    workflows: fields.workflows,
    workflowPolicy: fields.policy,
    sourcesOfTruth: fields.sourcesOfTruth,
    updatedAt: now(),
  };
  const validated = validateProject(base);
  return {
    ...validated,
    preferences: {
      provider: fields.provider,
      model: fields.model.trim(),
      ...(fields.concurrency === undefined
        ? {}
        : { concurrency: fields.concurrency }),
    },
  };
}

/**
 * Small project onboarding surface. Discovery is intentionally read-only and
 * report application only changes this form; persistence remains an explicit
 * human action through the footer.
 */
export function ProjectSetup({
  project,
  onClose,
  onSave,
  onToast,
  defaultProvider,
  defaultModel,
}: ProjectSetupProps) {
  const existingPreferences = project?.preferences || {};
  const [name, setName] = useState(project?.name || "");
  const [directory, setDirectory] = useState(project?.directory || "");
  const [conventions, setConventions] = useState(project?.conventions || "");
  const [locations, setLocations] = useState<Record<string, string>>(
    structuredClone(project?.locations || {}),
  );
  const [locationsText, setLocationsText] = useState(
    JSON.stringify(project?.locations || {}, null, 2),
  );
  const [sources, setSources] = useState<SourceOfTruth[]>(
    structuredClone(project?.sourcesOfTruth || []),
  );
  // Keep existing workflow data intact while the project onboarding surface
  // focuses on repository knowledge. The field remains persisted for older
  // projects and can be migrated by the surrounding product later.
  const workflows = structuredClone(project?.workflows || []);
  const policy: "flexible" | "enforced" = project?.workflowPolicy || "flexible";
  const [provider, setProvider] = useState<ProviderId>(
    existingPreferences.provider || defaultProvider,
  );
  const [model, setModel] = useState(
    existingPreferences.model ??
      (existingPreferences.provider &&
      existingPreferences.provider !== defaultProvider
        ? ""
        : defaultModel),
  );
  const [concurrency, setConcurrency] = useState<number | undefined>(
    existingPreferences.concurrency,
  );
  const [report, setReport] = useState<ProjectDiscoveryReport | null>(null);
  const [selectedSourceSuggestions, setSelectedSourceSuggestions] = useState<
    Set<string>
  >(new Set());
  const [includeConventions, setIncludeConventions] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [scanError, setScanError] = useState("");
  const [discoveryOpen, setDiscoveryOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const scanRef = useRef<string | null>(null);
  const mountedRef = useRef(true);
  const scanInputsRef = useRef({
    directory,
    provider,
    model,
  });

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      const activeScan = scanRef.current;
      if (activeScan) {
        scanRef.current = null;
        const cancel = window.djinn?.cancelProjectDiscovery;
        if (cancel) void cancel(activeScan).catch(() => undefined);
      }
    };
  }, []);

  useEffect(() => {
    const previous = scanInputsRef.current;
    const changed =
      previous.directory !== directory ||
      previous.provider !== provider ||
      previous.model !== model;
    scanInputsRef.current = { directory, provider, model };
    if (!changed) return;
    const activeScan = scanRef.current;
    if (activeScan) {
      scanRef.current = null;
      setScanning(false);
      const cancel = window.djinn?.cancelProjectDiscovery;
      if (cancel) void cancel(activeScan).catch(() => undefined);
    }
    // A report belongs to the exact input that produced it. Changing a scan
    // input requires an explicit new analysis before suggestions can apply.
    setReport(null);
    setScanError("");
    setSelectedSourceSuggestions(new Set());
    setIncludeConventions(false);
  }, [directory, model, provider]);

  const pickDirectory = async () => {
    if (!window.djinn?.selectDirectory) {
      onToast(t("setup.picker_unavailable"));
      return;
    }
    try {
      const picked = await window.djinn.selectDirectory();
      if (picked) setDirectory(picked);
    } catch (error) {
      onToast((error as Error).message || t("setup.pick_failed"));
    }
  };

  const analyze = async () => {
    const trimmedDirectory = directory.trim();
    if (!trimmedDirectory) {
      onToast(t("setup.directory_before_scan"));
      return;
    }
    if (scanning) return;
    setDiscoveryOpen(true);
    const id = scanId();
    scanRef.current = id;
    setScanning(true);
    setScanError("");
    setReport(null);
    try {
      const discover = window.djinn?.discoverProject;
      if (!discover) {
        const fallback = emptyReport(
          trimmedDirectory,
          provider,
          t("setup.discovery_unavailable"),
        );
        if (mountedRef.current && scanRef.current === id) setReport(fallback);
        return;
      }
      const discovered = await discover({
        directory: trimmedDirectory,
        provider,
        model: model.trim(),
        includeHistory: true,
        scanId: id,
      });
      if (!mountedRef.current || scanRef.current !== id) return;
      const normalized = normalizeReport(
        discovered,
        trimmedDirectory,
        provider,
      );
      setReport(normalized);
      setSelectedSourceSuggestions(
        new Set(normalized.sourcesOfTruth.map((source) => source.id)),
      );
      setIncludeConventions(false);
    } catch (error) {
      if (!mountedRef.current || scanRef.current !== id) return;
      const message = error instanceof Error ? error.message : String(error);
      setScanError(message || t("setup.scan_failed"));
    } finally {
      if (mountedRef.current && scanRef.current === id) {
        scanRef.current = null;
        setScanning(false);
      }
    }
  };

  const cancelScan = async () => {
    const activeScan = scanRef.current;
    if (!activeScan) return;
    scanRef.current = null;
    setScanning(false);
    try {
      await window.djinn?.cancelProjectDiscovery?.(activeScan);
    } catch (error) {
      onToast((error as Error).message || t("setup.cancel_failed"));
    }
  };

  const applySuggestions = () => {
    if (!report) return;
    const sourceSignatures = new Set(sources.map(sourceSignature));
    const addedSources = report.sourcesOfTruth
      .filter((source) => selectedSourceSuggestions.has(source.id))
      .filter((source) => !sourceSignatures.has(sourceSignature(source)))
      .map((source) => ({ ...source, id: uid() }))
      .filter((source) => {
        const signature = sourceSignature(source);
        if (sourceSignatures.has(signature)) return false;
        sourceSignatures.add(signature);
        return true;
      });
    const nextSources = [...sources, ...addedSources];
    setSources(nextSources);
    if (includeConventions && report.conventions.trim()) {
      const marker = t("setup.conventions_marker");
      if (!conventions.includes(marker)) {
        setConventions(
          conventions.trim()
            ? `${conventions.trim()}\n\n${marker}:\n${report.conventions.trim()}`
            : `${marker}:\n${report.conventions.trim()}`,
        );
      }
    }
    setLocations((current) => ({ ...report.locations, ...current }));
    setLocationsText((current) => {
      let existing: Record<string, string> = {};
      try {
        existing = parseLocations(current);
      } catch {
        existing = locations;
      }
      return JSON.stringify({ ...report.locations, ...existing }, null, 2);
    });
    onToast(
      addedSources.length || includeConventions
        ? t("setup.suggestions_applied")
        : t("setup.no_new_suggestion"),
    );
  };

  const parseLocations = (value: string): Record<string, string> => {
    if (!value.trim()) return {};
    const parsed: unknown = JSON.parse(value);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
      throw new Error(t("setup.locations_not_object"));
    for (const [key, entry] of Object.entries(parsed)) {
      if (!key.trim() || typeof entry !== "string")
        throw new Error(t("setup.location_needs_path"));
    }
    return parsed as Record<string, string>;
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (saving) return;
    const trimmedName = name.trim();
    const trimmedDirectory = directory.trim();
    if (!trimmedName) {
      onToast(t("setup.name_required"));
      return;
    }
    if (!trimmedDirectory) {
      onToast(t("setup.directory_required"));
      return;
    }
    setSaving(true);
    try {
      const parsedLocations = parseLocations(locationsText);
      const next = buildProject(project, {
        name: trimmedName,
        directory: trimmedDirectory,
        conventions,
        locations: parsedLocations,
        workflows: structuredClone(workflows),
        sourcesOfTruth: sources,
        provider,
        model,
        concurrency:
          concurrency === undefined
            ? undefined
            : Math.max(1, Math.min(MAX_SUB_AGENTS, Math.trunc(concurrency))),
        policy,
      });
      let result = next;
      if (window.djinn?.validateProject)
        result = await window.djinn.validateProject(next);
      await onSave(result);
    } catch (error) {
      onToast((error as Error).message || t("setup.save_failed"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form
      className="project-setup project-settings"
      aria-label={project ? t("setup.settings_label") : t("setup.create_label")}
      onSubmit={handleSubmit}
    >
      <p className="modal-description">
        {project ? t("setup.edit_description") : t("setup.create_description")}
      </p>
      <section className="project-setup-core" aria-label={t("setup.identity")}>
        <label>
          {t("setup.name")}
          <input
            autoFocus
            value={name}
            maxLength={1000}
            onChange={(event) => setName(event.target.value)}
            placeholder={t("setup.name_placeholder")}
          />
        </label>
        <label>
          {t("setup.directory")}
          <div className="directory-field project-setup-directory">
            <input
              value={directory}
              maxLength={4096}
              onChange={(event) => setDirectory(event.target.value)}
              placeholder={t("setup.directory_placeholder")}
            />
            <button
              type="button"
              aria-label={t("setup.pick_directory")}
              onClick={pickDirectory}
            >
              <FolderOpen size={17} aria-hidden="true" />
            </button>
          </div>
        </label>
        <details
          className="project-setup-discovery"
          open={discoveryOpen}
          onToggle={(event) => setDiscoveryOpen(event.currentTarget.open)}
        >
          <summary>{t("setup.discovery_summary")}</summary>
          <p className="project-setup-discovery-copy">
            {t("setup.discovery_detail")}
          </p>
          <div className="project-setup-scan-actions">
            <button
              type="button"
              className="button secondary"
              disabled={scanning}
              onClick={() => void analyze()}
            >
              {scanning ? (
                <Loader2
                  className="project-setup-spin"
                  size={15}
                  aria-hidden="true"
                />
              ) : (
                <Sparkles size={15} aria-hidden="true" />
              )}
              {scanning
                ? t("setup.scanning")
                : project
                  ? t("setup.rescan")
                  : t("setup.scan")}
            </button>
            {scanning && (
              <button
                type="button"
                className="button secondary"
                onClick={() => void cancelScan()}
              >
                <X size={14} aria-hidden="true" /> {t("common.cancel")}
              </button>
            )}
          </div>
          {scanning && (
            <div
              className="project-setup-progress"
              role="status"
              aria-live="polite"
            >
              <div className="project-setup-progress-label">
                <Loader2
                  className="project-setup-spin"
                  size={14}
                  aria-hidden="true"
                />
                <span>
                  {t("setup.reading_repository", {
                    provider: providerName(provider),
                  })}
                </span>
              </div>
              <div className="project-setup-progress-track" aria-hidden="true">
                <span />
              </div>
              <small>{t("setup.read_only")}</small>
            </div>
          )}
          {scanError && (
            <p className="project-setup-error" role="alert">
              <AlertCircle size={14} aria-hidden="true" /> {scanError}
            </p>
          )}
        </details>
      </section>
      {report && (
        <ProjectSetupReport
          report={report}
          selectedSources={selectedSourceSuggestions}
          includeConventions={includeConventions}
          onSourceSelection={(id, selected) =>
            setSelectedSourceSuggestions((current) => {
              const next = new Set(current);
              if (selected) next.add(id);
              else next.delete(id);
              return next;
            })
          }
          onIncludeConventions={setIncludeConventions}
          onApply={applySuggestions}
        />
      )}
      <details className="project-setup-advanced">
        <summary>{t("setup.advanced")}</summary>
        <div className="project-setup-advanced-grid">
          <label>
            {t("setup.conventions")}
            <textarea
              rows={4}
              value={conventions}
              onChange={(event) => setConventions(event.target.value)}
              placeholder={t("setup.conventions_placeholder")}
            />
          </label>
          <label>
            {t("setup.locations")}
            <textarea
              rows={3}
              value={locationsText}
              onChange={(event) => {
                const value = event.target.value;
                setLocationsText(value);
                try {
                  setLocations(parseLocations(value));
                } catch {
                  /* Save reports the parse error while preserving the edit. */
                }
              }}
              placeholder={'{"prototype":"app/prototypes"}'}
              spellCheck={false}
            />
            <small>{t("setup.locations_hint")}</small>
          </label>
          <section
            className="project-setup-sources"
            aria-labelledby="project-setup-sources-heading"
          >
            <div className="project-setup-subheading">
              <div>
                <h4 id="project-setup-sources-heading">{t("setup.sources")}</h4>
                <p>{t("setup.sources_detail")}</p>
              </div>
              <button
                type="button"
                className="button secondary small"
                onClick={() =>
                  setSources((current) => [...current, emptySource()])
                }
              >
                <Plus size={13} aria-hidden="true" /> {t("setup.add")}
              </button>
            </div>
            {!sources.length && (
              <p className="project-setup-empty-copy">
                {t("setup.no_sources")}
              </p>
            )}
            {sources.map((source, index) => (
              <fieldset className="project-setup-source" key={source.id}>
                <legend>
                  {t("setup.reference_number", { number: index + 1 })}
                </legend>
                <label>
                  {t("setup.reference_name")}
                  <input
                    value={source.title}
                    onChange={(event) =>
                      setSources((current) =>
                        current.map((item) =>
                          item.id === source.id
                            ? { ...item, title: event.target.value }
                            : item,
                        ),
                      )
                    }
                    placeholder={t("setup.reference_name_placeholder")}
                  />
                </label>
                <label>
                  {t("setup.reference_path")}
                  <input
                    value={source.path}
                    onChange={(event) =>
                      setSources((current) =>
                        current.map((item) =>
                          item.id === source.id
                            ? { ...item, path: event.target.value }
                            : item,
                        ),
                      )
                    }
                    placeholder="docs/CONTEXT.md"
                  />
                </label>
                <label>
                  {t("setup.reference_role")}
                  <input
                    value={source.description}
                    onChange={(event) =>
                      setSources((current) =>
                        current.map((item) =>
                          item.id === source.id
                            ? { ...item, description: event.target.value }
                            : item,
                        ),
                      )
                    }
                    placeholder={t("setup.reference_role_placeholder")}
                  />
                </label>
                <button
                  type="button"
                  className="text-button project-setup-danger"
                  onClick={() =>
                    setSources((current) =>
                      current.filter((item) => item.id !== source.id),
                    )
                  }
                >
                  <Trash2 size={13} aria-hidden="true" />{" "}
                  {t("setup.remove_reference")}
                </button>
              </fieldset>
            ))}
          </section>
          <label>
            {t("setup.provider_label")}
            <ProviderChoice
              provider={provider}
              onChange={(next) => {
                if (next !== provider) setModel("");
                setProvider(next);
              }}
            />
          </label>
          <div className="project-setup-model-field">
            <span>
              {t("setup.model")} <small>{t("setup.optional")}</small>
            </span>
            <ModelPicker
              provider={provider}
              value={model}
              onChange={setModel}
            />
          </div>
          <label>
            {t("setup.concurrency")} <small>{t("setup.optional")}</small>
            <input
              type="number"
              min={1}
              max={MAX_SUB_AGENTS}
              value={concurrency ?? ""}
              onChange={(event) =>
                setConcurrency(
                  event.target.value.trim()
                    ? Number(event.target.value)
                    : undefined,
                )
              }
              placeholder="3"
            />
          </label>
        </div>
      </details>
      <div className="modal-footer project-setup-footer">
        <button type="button" className="button secondary" onClick={onClose}>
          {t("common.cancel")}
        </button>
        <button type="submit" className="button accent" disabled={saving}>
          {saving ? (
            <Loader2
              className="project-setup-spin"
              size={15}
              aria-hidden="true"
            />
          ) : (
            <FileUp size={15} aria-hidden="true" />
          )}
          {project ? t("setup.save") : t("setup.create")}
        </button>
      </div>
    </form>
  );
}

export default ProjectSetup;
