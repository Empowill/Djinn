// Generated from src/i18n.ts. Keep both in sync.
"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.language = exports.languages = void 0;
exports.languageName = languageName;
exports.chosenLanguage = chosenLanguage;
exports.matchLanguage = matchLanguage;
exports.systemLanguage = systemLanguage;
exports.setLanguage = setLanguage;
exports.t = t;
// Interface texts. One catalog per language in locales/, shared with the Go server: en.json is the
// source, every other file translates all of its keys. Whoever adds or changes a text writes the
// English source and its translations in the same change (see CONTRIBUTING.md).
const enModule = require("../locales/en.json");
const frModule = require("../locales/fr.json");
// A namespace import reads the JSON whatever the bundler or test loader does with default imports.
const catalog = (module) => (module.default ?? module);
const en = catalog(enModule);
const catalogs = { en, fr: catalog(frModule) };
/** The languages the interface is translated into. */
exports.languages = Object.keys(catalogs);
/** A language's name, written in that language ("Français"). */
function languageName(code) {
    return catalogs[code]?.["language.name"] ?? code;
}
const storageKey = "djinn.language";
/** The language chosen in the settings, or "" to follow the system. */
function chosenLanguage() {
    try {
        return globalThis.localStorage?.getItem(storageKey) || "";
    }
    catch {
        return ""; // Storage can be blocked: follow the system.
    }
}
/** The best translated language for a list of BCP 47 tags, English when none matches. */
function matchLanguage(tags) {
    for (const tag of tags) {
        const base = tag?.toLowerCase().split(/[-_]/)[0];
        if (base && base in catalogs)
            return base;
    }
    return "en";
}
/** The system's language, as the browser or the window reports it. */
function systemLanguage() {
    const nav = globalThis.navigator;
    return matchLanguage([...(nav?.languages ?? []), nav?.language]);
}
/** The language of this page, resolved once at start: changing it reloads the page. */
exports.language = matchLanguage([chosenLanguage(), systemLanguage()]);
/** Saves the language ("" follows the system) and reloads the page in it. */
function setLanguage(next) {
    try {
        if (next)
            globalThis.localStorage?.setItem(storageKey, next);
        else
            globalThis.localStorage?.removeItem(storageKey);
    }
    catch {
        return;
    }
    globalThis.location?.reload();
}
const plurals = new Intl.PluralRules(exports.language);
function lookup(key) {
    return catalogs[exports.language]?.[key] ?? catalogs.en[key];
}
/**
 * The text of a key in the page's language, falling back to English, then to the key itself.
 * `{name}` placeholders take their value from params; with a numeric `count`, the plural form
 * of the language is used (`key.one`, `key.other`).
 */
function t(key, params) {
    let text;
    if (typeof params?.count === "number") {
        text =
            lookup(`${key}.${plurals.select(params.count)}`) ??
                lookup(`${key}.other`);
    }
    text ??= lookup(key) ?? key;
    if (!params)
        return text;
    return text.replace(/\{(\w+)\}/g, (match, name) => name in params ? String(params[name]) : match);
}
