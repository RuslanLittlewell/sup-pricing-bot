import { translations } from "./translations";

export const languages = ["en", "ru", "pl"] as const;
export type Lang = (typeof languages)[number];
export const defaultLang: Lang = "en";

/** BCP 47 locale for og:locale and similar. */
export const ogLocales: Record<Lang, string> = { en: "en_US", ru: "ru_RU", pl: "pl_PL" };

const isLang = (value: string | undefined): value is Lang =>
  (languages as readonly string[]).includes(value ?? "");

/** English lives at the root ("/"), other languages under a prefix ("/ru/", "/pl/"). */
export function getLangFromUrl(url: URL): Lang {
  const [, first] = url.pathname.split("/");
  return isLang(first) ? first : defaultLang;
}

/** Path without the language prefix: "/ru/" -> "/", "/pl/foo" -> "/foo". */
export function stripLang(pathname: string): string {
  const [, first, ...rest] = pathname.split("/");
  return isLang(first) ? `/${rest.join("/")}` : pathname;
}

export function localizePath(lang: Lang, path = "/"): string {
  const clean = path.startsWith("/") ? path : `/${path}`;
  return lang === defaultLang ? clean : `/${lang}${clean}`;
}

export function useTranslations(lang: Lang) {
  const dict: Record<string, string> = translations[lang];
  const fallback: Record<string, string> = translations[defaultLang];
  return (key: string, fallbackText?: string): string =>
    dict[key] ?? fallback[key] ?? fallbackText ?? key;
}

/** Shorthand for Astro components: `const t = useAstroTranslations(Astro.url)`. */
export function useAstroTranslations(url: URL) {
  const lang = getLangFromUrl(url);
  return { lang, t: useTranslations(lang) };
}
