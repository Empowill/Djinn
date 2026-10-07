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
      .at(-1) || "Projet"
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
    summary:
      "Le repérage local n’a pas reçu de rapport agent. Vous pouvez continuer avec les réglages du projet.",
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
      aria-label="Fournisseur"
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
  return new Intl.NumberFormat("fr-FR").format(value);
}

function reportStatus(report: ProjectDiscoveryReport): string {
  if (report.analysis.status === "fallback") return "Repérage local";
  if (report.analysis.status === "local") return "Analyse locale";
  return `Analyse ${providerName(report.analysis.provider || "codex")}`;
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
          <span className="project-setup-kicker">Rapport de découverte</span>
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
        {report.summary || "Aucun résumé n’a été fourni."}
      </p>
      <div className="project-setup-metrics" aria-label="Résumé du scan">
        <span>
          <strong>{formatCount(report.filesScanned)}</strong> fichiers repérés
        </span>
        <span>
          <strong>{formatCount(report.historyCount)}</strong> éléments
          d’historique
        </span>
        <span>
          <strong>{report.sourcesOfTruth.length}</strong> références proposées
        </span>
      </div>
      {report.analysis.detail && (
        <p className="project-setup-analysis-detail">
          {report.analysis.detail}
        </p>
      )}
      <details className="project-setup-evidence-details">
        <summary>Voir les connaissances, scripts, skills et références</summary>
        <div className="project-setup-evidence-grid">
          {renderEvidence(
            "Connaissances",
            grouped.knowledge,
            "Aucune instruction repérée.",
          )}
          {renderEvidence("Scripts", grouped.scripts, "Aucun script repéré.")}
          {renderEvidence("Skills", grouped.skills, "Aucun skill repéré.")}
          {renderEvidence(
            "Références",
            grouped.references,
            "Aucune référence technique repérée.",
          )}
        </div>
      </details>
      {!!report.sourcesOfTruth.length && (
        <section
          className="project-setup-suggestions"
          aria-labelledby="project-setup-sources-title"
        >
          <div className="project-setup-subheading">
            <h4 id="project-setup-sources-title">Sources proposées</h4>
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
            <strong>Ajouter les conventions détectées</strong>
            <small>
              Cette action enrichit le projet localement. Enregistrez ensuite
              pour la confirmer.
            </small>
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
        <Check size={14} aria-hidden="true" /> Appliquer les suggestions
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
      onToast(
        "Le sélecteur de dossier est disponible dans l’application locale.",
      );
      return;
    }
    try {
      const picked = await window.djinn.selectDirectory();
      if (picked) setDirectory(picked);
    } catch (error) {
      onToast((error as Error).message || "Impossible de choisir ce dossier.");
    }
  };

  const analyze = async () => {
    const trimmedDirectory = directory.trim();
    if (!trimmedDirectory) {
      onToast("Choisissez le dossier du projet avant l’analyse.");
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
          "Le pont de découverte est indisponible dans cet aperçu : seul un repérage local vide est affiché.",
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
      setScanError(message || "Le projet n’a pas pu être analysé.");
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
      onToast((error as Error).message || "Impossible d’annuler l’analyse.");
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
      const marker = "Conventions détectées par le scan";
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
        ? "Suggestions appliquées dans le formulaire. Vérifiez puis enregistrez le projet."
        : "Aucune nouvelle suggestion à ajouter.",
    );
  };

  const parseLocations = (value: string): Record<string, string> => {
    if (!value.trim()) return {};
    const parsed: unknown = JSON.parse(value);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
      throw new Error("Les emplacements doivent être un objet JSON.");
    for (const [key, entry] of Object.entries(parsed)) {
      if (!key.trim() || typeof entry !== "string")
        throw new Error("Chaque emplacement doit associer un nom à un chemin.");
    }
    return parsed as Record<string, string>;
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (saving) return;
    const trimmedName = name.trim();
    const trimmedDirectory = directory.trim();
    if (!trimmedName) {
      onToast("Donnez un nom au projet.");
      return;
    }
    if (!trimmedDirectory) {
      onToast("Choisissez le dossier du projet.");
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
      onToast(
        (error as Error).message || "Impossible d’enregistrer le projet.",
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <form
      className="project-setup project-settings"
      aria-label={project ? "Paramètres du projet" : "Créer un projet"}
      onSubmit={handleSubmit}
    >
      <p className="modal-description">
        {project
          ? "Enregistrez le projet avec ses réglages. Un repérage du dépôt reste disponible à la demande dans la section optionnelle."
          : "Donnez un nom et un dossier. Vous pourrez lancer un repérage manuel des instructions et conventions quand vous le souhaiterez."}
      </p>
      <section className="project-setup-core" aria-label="Identité du projet">
        <label>
          Nom du projet
          <input
            autoFocus
            value={name}
            maxLength={1000}
            onChange={(event) => setName(event.target.value)}
            placeholder="Mon projet"
          />
        </label>
        <label>
          Dossier du projet
          <div className="directory-field project-setup-directory">
            <input
              value={directory}
              maxLength={4096}
              onChange={(event) => setDirectory(event.target.value)}
              placeholder="/chemin/vers/votre/projet"
            />
            <button
              type="button"
              aria-label="Choisir le dossier du projet"
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
          <summary>Repérage optionnel · lancement manuel</summary>
          <p className="project-setup-discovery-copy">
            Aucun repérage ne démarre à l’ouverture. Lancez-le uniquement pour
            obtenir des suggestions de sources et de conventions.
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
                ? "Analyse en cours…"
                : project
                  ? "Ré-analyser le projet"
                  : "Analyser le projet"}
            </button>
            {scanning && (
              <button
                type="button"
                className="button secondary"
                onClick={() => void cancelScan()}
              >
                <X size={14} aria-hidden="true" /> Annuler
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
                <span>Lecture du dépôt avec {providerName(provider)}</span>
              </div>
              <div className="project-setup-progress-track" aria-hidden="true">
                <span />
              </div>
              <small>Lecture seule · aucune modification du dépôt</small>
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
        <summary>Réglages avancés</summary>
        <div className="project-setup-advanced-grid">
          <label>
            Conventions du projet
            <textarea
              rows={4}
              value={conventions}
              onChange={(event) => setConventions(event.target.value)}
              placeholder="Règles, structure, commandes et décisions à respecter…"
            />
          </label>
          <label>
            Emplacements relatifs (optionnel)
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
            <small>Utilisez des chemins relatifs au dossier du projet.</small>
          </label>
          <section
            className="project-setup-sources"
            aria-labelledby="project-setup-sources-heading"
          >
            <div className="project-setup-subheading">
              <div>
                <h4 id="project-setup-sources-heading">Sources de vérité</h4>
                <p>
                  Références transmises au contexte des prochaines missions.
                </p>
              </div>
              <button
                type="button"
                className="button secondary small"
                onClick={() =>
                  setSources((current) => [...current, emptySource()])
                }
              >
                <Plus size={13} aria-hidden="true" /> Ajouter
              </button>
            </div>
            {!sources.length && (
              <p className="project-setup-empty-copy">
                Aucune source enregistrée.
              </p>
            )}
            {sources.map((source, index) => (
              <fieldset className="project-setup-source" key={source.id}>
                <legend>Référence {index + 1}</legend>
                <label>
                  Nom de la référence
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
                    placeholder="Décisions produit"
                  />
                </label>
                <label>
                  Chemin relatif
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
                  Rôle de la référence
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
                    placeholder="Décisions acceptées et vocabulaire du projet"
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
                  <Trash2 size={13} aria-hidden="true" /> Retirer cette
                  référence
                </button>
              </fieldset>
            ))}
          </section>
          <label>
            Fournisseur d’analyse et d’exécution
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
              Modèle <small>(facultatif)</small>
            </span>
            <ModelPicker
              provider={provider}
              value={model}
              onChange={setModel}
            />
          </div>
          <label>
            Workers simultanés <small>(facultatif)</small>
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
          Annuler
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
          {project ? "Enregistrer le projet" : "Créer le projet"}
        </button>
      </div>
    </form>
  );
}

export default ProjectSetup;
