import {
  AlertCircle,
  Check,
  ChevronDown,
  Loader2,
  RefreshCw,
  Search,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import type { ProviderId, ProviderModel, ProviderModelCatalog } from "./types";
import { t } from "./i18n";

type ModelPickerOption = ProviderModel & {
  unavailable?: boolean;
};

const defaultOption: ProviderModel = {
  id: "",
  name: t("models.provider_default"),
  description: t("models.provider_default_description"),
  isDefault: true,
};

function normalizeSearch(value: string): string {
  return value
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLocaleLowerCase()
    .trim();
}

function isSubsequence(query: string, value: string): boolean {
  let index = 0;
  for (const character of value) {
    if (character === query[index]) index += 1;
    if (index === query.length) return true;
  }
  return false;
}

function scoreModel(model: ModelPickerOption, query: string): number | null {
  const tokens = normalizeSearch(query).split(/\s+/u).filter(Boolean);
  if (!tokens.length) return 0;
  const fields = [
    model.name,
    model.id,
    ...(model.aliases || []),
    model.description || "",
  ].map(normalizeSearch);
  let score = 0;
  for (const token of tokens) {
    let best: number | null = null;
    fields.forEach((field, index) => {
      if (!field) return;
      let candidate: number | null = null;
      if (field === token) candidate = index * 10;
      else if (field.startsWith(token)) candidate = index * 10 + 10;
      else if (
        field
          .split(/[^\p{Letter}\p{Number}]+/u)
          .some((part) => part.startsWith(token))
      )
        candidate = index * 10 + 20;
      else if (field.includes(token)) candidate = index * 10 + 30;
      else if (token.length >= 3 && isSubsequence(token, field))
        candidate = index * 10 + 45;
      if (candidate !== null && (best === null || candidate < best))
        best = candidate;
    });
    if (best === null) return null;
    score += best;
  }
  return score;
}

function modelLabel(
  model: ModelPickerOption | undefined,
  value: string,
): string {
  return model?.name || value || defaultOption.name;
}

export function ModelPicker({
  provider,
  value,
  onChange,
}: {
  provider: ProviderId;
  value: string;
  onChange: (value: string) => void;
}) {
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const pickerId = useId().replace(/:/g, "");
  const requestRef = useRef(0);
  const observedProviderRef = useRef(provider);
  const loadedProviderRef = useRef<ProviderId | null>(null);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [catalog, setCatalog] = useState<ProviderModelCatalog | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [highlighted, setHighlighted] = useState(0);

  const load = useCallback(
    async (refresh = false) => {
      const request = ++requestRef.current;
      setLoading(true);
      setError("");
      try {
        const loader = window.djinn?.getProviderModels;
        if (!loader) {
          const fallback: ProviderModelCatalog = {
            provider,
            models: [],
            source: "t3-manifest",
            error: t("models.catalog_needs_app"),
          };
          if (request === requestRef.current) {
            setCatalog(fallback);
            setError(fallback.error || "");
          }
          return;
        }
        const result = await loader(provider, refresh);
        if (request !== requestRef.current) return;
        if (result.provider !== provider) {
          setCatalog({
            provider,
            models: [],
            source: result.source,
            error: t("models.catalog_wrong_provider"),
          });
          setError(t("models.catalog_wrong_provider"));
        } else {
          setCatalog(result);
          setError(result.error || "");
        }
      } catch (reason) {
        if (request !== requestRef.current) return;
        const message =
          reason instanceof Error ? reason.message : String(reason);
        setCatalog({
          provider,
          models: [],
          source: "t3-manifest",
          error: message,
        });
        setError(message || t("models.catalog_failed"));
      } finally {
        if (request === requestRef.current) {
          loadedProviderRef.current = provider;
          setLoading(false);
        }
      }
    },
    [provider],
  );

  useEffect(() => {
    const providerChanged = observedProviderRef.current !== provider;
    observedProviderRef.current = provider;
    if (!open && !providerChanged) return;
    if (!providerChanged && loadedProviderRef.current === provider) return;
    void load();
  }, [load, open, provider]);

  useEffect(() => {
    return () => {
      requestRef.current += 1;
    };
  }, []);

  useEffect(() => {
    if (!open) return;
    searchRef.current?.focus({ preventScroll: true });
    const closeOutside = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", closeOutside);
    return () => document.removeEventListener("pointerdown", closeOutside);
  }, [open]);

  const options = useMemo<ModelPickerOption[]>(() => {
    const models = (
      catalog?.provider === provider ? catalog.models : []
    ).filter((model) => model.id !== "");
    const known = new Set(models.map((model) => model.id));
    const selectedMissing =
      value && !known.has(value)
        ? [
            {
              id: value,
              name: value,
              description: t("models.selected_missing"),
              unavailable: true,
            },
          ]
        : [];
    return [defaultOption, ...selectedMissing, ...models];
  }, [catalog, provider, value]);

  const filteredOptions = useMemo(() => {
    return options
      .map((model) => ({ model, score: scoreModel(model, query) }))
      .filter(
        (entry): entry is { model: ModelPickerOption; score: number } =>
          entry.score !== null,
      )
      .sort(
        (a, b) =>
          a.score - b.score ||
          Number(Boolean(b.model.isDefault)) -
            Number(Boolean(a.model.isDefault)) ||
          a.model.name.localeCompare(b.model.name),
      )
      .map((entry) => entry.model);
  }, [options, query]);

  useEffect(() => {
    setHighlighted(0);
  }, [query, provider, open]);

  useEffect(() => {
    if (!open || !filteredOptions.length) return;
    document
      .getElementById(`${pickerId}-option-${highlighted}`)
      ?.scrollIntoView({ block: "nearest" });
  }, [filteredOptions.length, highlighted, open, pickerId]);

  const selected = options.find((model) => model.id === value);
  const select = (model: ModelPickerOption) => {
    onChange(model.id);
    setOpen(false);
    setQuery("");
    requestAnimationFrame(() =>
      triggerRef.current?.focus({ preventScroll: true }),
    );
  };
  const closeWithFocus = () => {
    setOpen(false);
    requestAnimationFrame(() =>
      triggerRef.current?.focus({ preventScroll: true }),
    );
  };
  const handleKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!open) return;
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      closeWithFocus();
      return;
    }
    const isSearchInput = event.target === searchRef.current;
    if (
      isSearchInput &&
      (event.key === "ArrowDown" || event.key === "ArrowUp")
    ) {
      event.preventDefault();
      if (!filteredOptions.length) return;
      setHighlighted((current) =>
        event.key === "ArrowDown"
          ? (current + 1) % filteredOptions.length
          : (current - 1 + filteredOptions.length) % filteredOptions.length,
      );
      return;
    }
    if (
      isSearchInput &&
      event.key === "Enter" &&
      filteredOptions[highlighted]
    ) {
      event.preventDefault();
      select(filteredOptions[highlighted]);
    }
  };

  return (
    <div className="model-picker" ref={rootRef} onKeyDown={handleKeyDown}>
      <button
        type="button"
        ref={triggerRef}
        className={`model-picker-trigger ${open ? "open" : ""}`}
        aria-label={t("models.choose")}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={value || defaultOption.name}
        onClick={() => setOpen((current) => !current)}
      >
        <span className="model-picker-current">
          <strong>{modelLabel(selected, value)}</strong>
          {selected?.id && <code>{selected.id}</code>}
        </span>
        <ChevronDown size={15} aria-hidden="true" />
      </button>
      {open && (
        <div className="model-picker-popover">
          <div className="model-picker-search-wrap">
            <Search size={14} aria-hidden="true" />
            <input
              ref={searchRef}
              role="combobox"
              aria-label={t("models.search")}
              aria-controls={`${pickerId}-options`}
              aria-activedescendant={
                !loading && filteredOptions[highlighted]
                  ? `${pickerId}-option-${highlighted}`
                  : undefined
              }
              aria-expanded="true"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t("models.search_placeholder")}
            />
          </div>
          {loading ? (
            <div className="model-picker-message" role="status">
              <Loader2 size={14} className="model-picker-spinner" />
              {t("models.loading")}
            </div>
          ) : (
            <>
              {(error ||
                (catalog?.provider === provider
                  ? catalog.error
                  : undefined)) && (
                <div className="model-picker-error" role="status">
                  <AlertCircle size={14} />
                  <span>
                    {error ||
                      (catalog?.provider === provider ? catalog.error : "")}
                  </span>
                  <button
                    type="button"
                    onClick={() => void load(true)}
                    aria-label={t("common.retry")}
                  >
                    <RefreshCw size={13} />
                  </button>
                </div>
              )}
              <div
                className="model-picker-list"
                id={`${pickerId}-options`}
                role="listbox"
                aria-label={t("models.available")}
              >
                {filteredOptions.length ? (
                  filteredOptions.map((model, index) => (
                    <button
                      type="button"
                      id={`${pickerId}-option-${index}`}
                      role="option"
                      aria-selected={model.id === value}
                      className={`model-picker-option ${index === highlighted ? "highlighted" : ""}`}
                      key={`${model.id || "default"}-${model.name}`}
                      onMouseEnter={() => setHighlighted(index)}
                      onClick={() => select(model)}
                    >
                      <span className="model-picker-option-copy">
                        <strong>{model.name}</strong>
                        {model.id && <code>{model.id}</code>}
                        {model.description && (
                          <small>{model.description}</small>
                        )}
                      </span>
                      <span className="model-picker-badges">
                        {model.isDefault && (
                          <em>{t("models.badge_default")}</em>
                        )}
                        {model.isLegacy && <em>{t("models.badge_legacy")}</em>}
                        {model.unavailable && (
                          <em>{t("models.badge_unavailable")}</em>
                        )}
                        {model.id === value && (
                          <Check size={14} aria-hidden="true" />
                        )}
                      </span>
                    </button>
                  ))
                ) : (
                  <div className="model-picker-message" role="status">
                    {t("models.no_match", { query })}
                  </div>
                )}
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}

export default ModelPicker;
