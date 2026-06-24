import { describe, expect, it, beforeEach, vi } from "vite-plus/test";

import {
  DEFAULT_LOCALE,
  LOCALE_STORAGE_KEY,
  SUPPORTED_LOCALES,
  chooseInitialLocale,
  normalizeLocale,
  setLocale,
} from "./index.js";
import { m } from "../paraglide/messages.js";
import { setLocale as setParaglideLocale } from "../paraglide/runtime.js";
import en from "../../../messages/en.json";
import zhCN from "../../../messages/zh-CN.json";

describe("i18n locale selection", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  it("normalizes supported locale variants", () => {
    expect(normalizeLocale("en-US")).toBe("en");
    expect(normalizeLocale("zh-Hans-CN")).toBe("zh-CN");
    expect(normalizeLocale("zh-cn")).toBe("zh-CN");
  });

  it("falls back to English for unsupported locales", () => {
    expect(normalizeLocale("fr-FR")).toBe(DEFAULT_LOCALE);
    expect(normalizeLocale("")).toBe(DEFAULT_LOCALE);
  });

  it("uses the stored locale before browser languages", () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, "zh-CN");
    vi.stubGlobal("navigator", {
      languages: ["en-US"],
      language: "en-US",
    });

    expect(chooseInitialLocale()).toBe("zh-CN");
  });

  it("uses the browser language when no stored locale exists", () => {
    vi.stubGlobal("navigator", {
      languages: ["zh-CN", "en-US"],
      language: "en-US",
    });

    expect(chooseInitialLocale()).toBe("zh-CN");
  });

  it("respects browser language priority", () => {
    vi.stubGlobal("navigator", {
      languages: ["en-US", "zh-CN"],
      language: "zh-CN",
    });

    expect(chooseInitialLocale()).toBe("en");
  });

  it("falls back to English when browser languages are unsupported", () => {
    vi.stubGlobal("navigator", {
      languages: ["fr-FR"],
      language: "fr-FR",
    });

    expect(chooseInitialLocale()).toBe("en");
  });

  it("keeps the supported locale list explicit", () => {
    expect(SUPPORTED_LOCALES).toEqual(["en", "zh-CN"]);
  });

  it("keeps Simplified Chinese locale keys aligned with English", () => {
    expect(Object.keys(zhCN).sort()).toEqual(Object.keys(en).sort());
  });

  it("renders generated Paraglide messages for each supported locale", () => {
    setParaglideLocale("en");
    expect(m.nav_sessions()).toBe("Sessions");
    expect(m.status_bar_sessions({ count: "12" })).toBe("12 sessions");

    setParaglideLocale("zh-CN");
    expect(m.nav_sessions()).toBe("会话");
    expect(m.status_bar_sessions({ count: "12" })).toBe("12 个会话");
  });

  it("sets and persists the Paraglide runtime locale", async () => {
    const runtime = await import("../paraglide/runtime.js");

    setLocale("zh-CN");

    expect(runtime.getLocale()).toBe("zh-CN");
    expect(localStorage.getItem(LOCALE_STORAGE_KEY)).toBe("zh-CN");
  });
});
