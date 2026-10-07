import {
  AlertCircle,
  Check,
  ChevronDown,
  Loader2,
  LockKeyhole,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { Environment, ProviderId, Task } from "./types";
import { ModelPicker } from "./model-picker";
import "./provider-switch.css";

export type ProviderSwitchProvider = Pick<
  Environment["providers"][number],
  "id" | "name" | "available" | "authenticated"
>;

export interface ProviderSwitchProps {
  task: Task;
  providers: ProviderSwitchProvider[];
  starting: boolean;
  onSwitch: (
    provider: ProviderId,
    model: string,
    resume: boolean,
  ) => Promise<boolean>;
}

const PROVIDER_IDS: ProviderId[] = ["codex", "claude"];

const providerFallbacks: Record<ProviderId, ProviderSwitchProvider> = {
  codex: {
    id: "codex",
    name: "Codex",
    available: false,
    authenticated: false,
  },
  claude: {
    id: "claude",
    name: "Claude Code",
    available: false,
    authenticated: false,
  },
};

function providerIsReady(provider: ProviderSwitchProvider | undefined) {
  return Boolean(provider?.available && provider.authenticated !== false);
}

function providerAvailability(provider: ProviderSwitchProvider) {
  if (!provider.available) return "Indisponible";
  if (provider.authenticated === false) return "Non authentifié";
  return "";
}

function defaultProviderName(id: ProviderId) {
  return id === "claude" ? "Claude Code" : "Codex";
}

function displayProviderName(provider: ProviderSwitchProvider) {
  return defaultProviderName(provider.id);
}

export function ProviderSwitch({
  task,
  providers,
  starting,
  onSwitch,
}: ProviderSwitchProps) {
  const switchId = useId().replace(/:/g, "");
  const rootRef = useRef<HTMLDetailsElement>(null);
  const summaryRef = useRef<HTMLElement>(null);
  const [open, setOpen] = useState(false);
  const [draftProvider, setDraftProvider] = useState<ProviderId>(task.provider);
  const [draftModel, setDraftModel] = useState(task.model || "");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [panelPosition, setPanelPosition] = useState<{
    top: number;
    right: number;
  } | null>(null);

  const providerOptions = useMemo(
    () =>
      PROVIDER_IDS.map(
        (id) =>
          providers.find((provider) => provider.id === id) ||
          providerFallbacks[id],
      ),
    [providers],
  );
  const selectedProvider = providerOptions.find(
    (provider) => provider.id === draftProvider,
  );
  const currentProvider =
    providerOptions.find((provider) => provider.id === task.provider) ||
    providerFallbacks[task.provider];
  const busy = Boolean(task.runId) || task.status === "running" || starting;
  const selectedProviderReady = providerIsReady(selectedProvider);
  const switchingProvider = draftProvider !== task.provider;
  const canApply = selectedProviderReady && !busy && !submitting;
  const canResume = canApply && !task.demo;

  useEffect(() => {
    if (!open && !submitting) {
      setDraftProvider(task.provider);
      setDraftModel(task.model || "");
      setError("");
    }
  }, [open, submitting, task.model, task.provider]);

  useEffect(() => {
    if (busy && open) setOpen(false);
  }, [busy, open]);

  const updatePanelPosition = useCallback(() => {
    const summary = summaryRef.current;
    if (!summary || typeof window === "undefined") return;
    const bounds = summary.getBoundingClientRect();
    const viewportPadding = 12;
    const panelWidth = Math.min(360, window.innerWidth - viewportPadding * 2);
    const idealRight = window.innerWidth - bounds.right;
    const right = Math.min(
      Math.max(viewportPadding, idealRight),
      Math.max(
        viewportPadding,
        window.innerWidth - viewportPadding - panelWidth,
      ),
    );
    const estimatedHeight = 370;
    const below = bounds.bottom + 8;
    const top =
      below + estimatedHeight <= window.innerHeight - viewportPadding
        ? below
        : Math.max(viewportPadding, bounds.top - estimatedHeight - 8);
    setPanelPosition({ top, right });
  }, []);

  useLayoutEffect(() => {
    if (!open) {
      setPanelPosition(null);
      return;
    }
    updatePanelPosition();
  }, [open, updatePanelPosition]);

  useEffect(() => {
    if (!open || typeof window === "undefined") return;
    const reposition = () => updatePanelPosition();
    window.addEventListener("resize", reposition);
    window.addEventListener("scroll", reposition, true);
    return () => {
      window.removeEventListener("resize", reposition);
      window.removeEventListener("scroll", reposition, true);
    };
  }, [open, updatePanelPosition]);

  useEffect(() => {
    if (!open || typeof document === "undefined") return;
    const closeOutside = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeWithEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
      requestAnimationFrame(() =>
        summaryRef.current?.focus({ preventScroll: true }),
      );
    };
    document.addEventListener("pointerdown", closeOutside);
    document.addEventListener("keydown", closeWithEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOutside);
      document.removeEventListener("keydown", closeWithEscape);
    };
  }, [open]);

  const changeProvider = (provider: ProviderId) => {
    setDraftProvider(provider);
    setDraftModel("");
    setError("");
  };

  const submit = async (resume: boolean) => {
    if (resume ? !canResume : !canApply) return;
    setSubmitting(true);
    setError("");
    try {
      const accepted = await onSwitch(draftProvider, draftModel, resume);
      if (accepted) {
        setOpen(false);
      } else {
        setError("Le changement de fournisseur n’a pas pu être appliqué.");
      }
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setSubmitting(false);
    }
  };

  const handleSummaryClick = (event: React.MouseEvent<HTMLElement>) => {
    if (!busy) return;
    event.preventDefault();
    event.stopPropagation();
  };

  return (
    <details
      ref={rootRef}
      className={`provider-switch ${open ? "is-open" : ""} ${busy ? "is-disabled" : ""}`}
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary
        ref={summaryRef}
        className="provider-switch-summary"
        aria-controls={`${switchId}-panel`}
        aria-disabled={busy}
        onClick={handleSummaryClick}
        title={
          busy
            ? "La mission est en cours d’exécution"
            : "Changer de fournisseur"
        }
      >
        <span className="badge muted provider-switch-badge">
          <span>{displayProviderName(currentProvider)}</span>
          <ChevronDown size={13} aria-hidden="true" />
        </span>
      </summary>
      <div
        id={`${switchId}-panel`}
        className="provider-switch-panel"
        style={
          panelPosition
            ? {
                top: `${panelPosition.top}px`,
                right: `${panelPosition.right}px`,
              }
            : undefined
        }
      >
        <div className="provider-switch-heading">
          <div>
            <span className="eyebrow">CONFIGURATION</span>
            <h3>Changer de fournisseur</h3>
          </div>
          <ChevronDown size={15} aria-hidden="true" />
        </div>
        <p className="provider-switch-copy">
          Les sorties, les décisions et l’historique de cette mission sont
          conservés.
        </p>

        <label
          className="provider-switch-field"
          htmlFor={`${switchId}-provider`}
        >
          <span>Fournisseur</span>
          <select
            id={`${switchId}-provider`}
            value={draftProvider}
            disabled={busy || submitting}
            onChange={(event) =>
              changeProvider(event.target.value as ProviderId)
            }
          >
            {providerOptions.map((provider) => {
              const availability = providerAvailability(provider);
              return (
                <option
                  key={provider.id}
                  value={provider.id}
                  disabled={!providerIsReady(provider)}
                >
                  {displayProviderName(provider)}
                  {availability ? ` · ${availability}` : ""}
                </option>
              );
            })}
          </select>
        </label>

        {selectedProvider && !selectedProviderReady && (
          <p className="provider-switch-status" role="status">
            <LockKeyhole size={13} aria-hidden="true" />
            {providerAvailability(selectedProvider) ||
              "Fournisseur indisponible"}
          </p>
        )}

        <fieldset
          className="provider-switch-model"
          disabled={busy || submitting || !selectedProviderReady}
        >
          <legend>Modèle</legend>
          <ModelPicker
            provider={draftProvider}
            value={draftModel}
            onChange={setDraftModel}
          />
        </fieldset>

        {task.demo && (
          <p className="provider-switch-demo" role="note">
            <AlertCircle size={13} aria-hidden="true" />
            La reprise est désactivée dans la mission d’exemple.
          </p>
        )}
        {error && (
          <p className="provider-switch-error" role="alert">
            <AlertCircle size={13} aria-hidden="true" />
            {error}
          </p>
        )}

        <div className="provider-switch-actions">
          <button
            type="button"
            className="button secondary small"
            disabled={!canApply}
            onClick={() => void submit(false)}
          >
            <Check size={14} aria-hidden="true" />
            Appliquer
          </button>
          <button
            type="button"
            className="button accent small"
            disabled={!canResume}
            onClick={() => void submit(true)}
          >
            {submitting ? (
              <Loader2
                size={14}
                className="provider-switch-spinner"
                aria-hidden="true"
              />
            ) : (
              <Check size={14} aria-hidden="true" />
            )}
            {switchingProvider
              ? "Changer et reprendre"
              : "Reprendre la mission"}
          </button>
        </div>
      </div>
    </details>
  );
}

export default ProviderSwitch;
