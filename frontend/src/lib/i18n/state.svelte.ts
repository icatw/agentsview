import type { SupportedLocale } from "./index.js";

class I18nState {
  locale: SupportedLocale = $state("en");
}

export const i18nState = new I18nState();
