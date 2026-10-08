// Interface texts. One catalog per language in locales/, shared with the Go server: en.json is the
// source, every other file translates all of its keys. Whoever adds or changes a text writes the
// English source and its translations in the same change (see CONTRIBUTING.md).
import * as enModule from "../locales/en.json";
import * as frModule from "../locales/fr.json";

type Catalog = Record<string, string>;
// A namespace import reads the JSON whatever the bundler or test loader does with default imports.
const catalog = (module: object): Catalog =>
  ((module as { default?: Catalog }).default ?? module) as Catalog;
const en = catalog(enModule);

/** A key of the English source. The compiler refuses a key that en.json does not hold. */
export type TextKey = Exclude<keyof typeof enModule, "default">;
type PluralBase<K> = K extends `${infer Base}.other` ? Base : never;
/** A text with plural forms, written `key.one` and `key.other` (plus `key.many` where a language needs it). */
export type PluralKey = PluralBase<TextKey>;
export type TextParams = Record<string, string | number>;

const catalogs: Record<string, Catalog> = { en, fr: catalog(frModule) };

/** The languages the interface is translated into. */
export const languages = Object.keys(catalogs);

/** A language's name, written in that language ("Français"). */
export function languageName(code: string): string {
  return catalogs[code]?.["language.name"] ?? code;
}

const storageKey = "djinn.language";

/** The language chosen in the settings, or "" to follow the system. */
export function chosenLanguage(): string {
  try {
    return globalThis.localStorage?.getItem(storageKey) || "";
  } catch {
    return ""; // Storage can be blocked: follow the system.
  }
}

/** The best translated language for a list of BCP 47 tags, English when none matches. */
export function matchLanguage(tags: readonly (string | undefined)[]): string {
  for (const tag of tags) {
    const base = tag?.toLowerCase().split(/[-_]/)[0];
    if (base && base in catalogs) return base;
  }
  return "en";
}

/** The system's language, as the browser or the window reports it. */
export function systemLanguage(): string {
  const nav = globalThis.navigator;
  return matchLanguage([...(nav?.languages ?? []), nav?.language]);
}

/** The language of this page, resolved once at start: changing it reloads the page. */
export const language = matchLanguage([chosenLanguage(), systemLanguage()]);

/** Saves the language ("" follows the system) and reloads the page in it. */
export function setLanguage(next: string) {
  try {
    if (next) globalThis.localStorage?.setItem(storageKey, next);
    else globalThis.localStorage?.removeItem(storageKey);
  } catch {
    return;
  }
  globalThis.location?.reload();
}

const plurals = new Intl.PluralRules(language);

function lookup(key: string): string | undefined {
  return catalogs[language]?.[key] ?? catalogs.en[key];
}

/**
 * The text of a key in the page's language, falling back to English, then to the key itself.
 * `{name}` placeholders take their value from params; with a numeric `count`, the plural form
 * of the language is used (`key.one`, `key.other`).
 */
export function t(key: TextKey | PluralKey, params?: TextParams): string {
  let text: string | undefined;
  if (typeof params?.count === "number") {
    text =
      lookup(`${key}.${plurals.select(params.count)}`) ??
      lookup(`${key}.other`);
  }
  text ??= lookup(key) ?? key;
  if (!params) return text;
  return text.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in params ? String(params[name]) : match,
  );
}
