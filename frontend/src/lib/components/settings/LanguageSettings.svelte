<script lang="ts">
  import { _ } from "svelte-i18n";
  import {
    LOCALE_STORAGE_KEY,
    SUPPORTED_LOCALES,
    chooseInitialLocale,
    setLocale,
    type SupportedLocale,
  } from "../../i18n/index.js";
  import SettingsSection from "./SettingsSection.svelte";

  function currentLocale(): SupportedLocale {
    try {
      const raw = localStorage.getItem(LOCALE_STORAGE_KEY);
      return SUPPORTED_LOCALES.includes(raw as SupportedLocale)
        ? raw as SupportedLocale
        : chooseInitialLocale();
    } catch {
      return chooseInitialLocale();
    }
  }

  let selectedLocale = $state<SupportedLocale>(currentLocale());

  function handleLocaleChange(event: Event) {
    const value = (event.currentTarget as HTMLSelectElement)
      .value as SupportedLocale;
    if (!SUPPORTED_LOCALES.includes(value)) return;
    selectedLocale = value;
    setLocale(value);
  }
</script>

<SettingsSection
  title={$_("settings.language.title")}
  description={$_("settings.language.description")}
>
  <div class="setting-row">
    <span class="setting-label">{$_("settings.language.label")}</span>
    <div class="select-wrap">
      <select
        class="language-select"
        aria-label={$_("settings.language.label")}
        value={selectedLocale}
        onchange={handleLocaleChange}
      >
        <option value="en">{$_("settings.language.english")}</option>
        <option value="zh-CN">
          {$_("settings.language.simplifiedChinese")}
        </option>
      </select>
    </div>
  </div>
</SettingsSection>

<style>
  .setting-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }

  .setting-label {
    font-size: 12px;
    font-weight: 500;
    color: var(--text-secondary);
    white-space: nowrap;
  }

  .select-wrap {
    position: relative;
  }

  .language-select {
    height: 28px;
    padding: 0 28px 0 10px;
    border-radius: var(--radius-sm);
    font-size: 12px;
    font-weight: 500;
    color: var(--text-secondary);
    background: var(--bg-inset);
    border: 1px solid var(--border-muted);
    cursor: pointer;
  }

  .language-select:focus {
    outline: none;
    border-color: var(--accent-blue);
  }
</style>
