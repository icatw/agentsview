# Paraglide I18n Migration Design

## Context

The frontend currently uses `svelte-i18n` as a runtime dictionary store. The app
is a plain Svelte 5 SPA built by Vite+, embedded behind the Go server, not a
SvelteKit app. The Paraglide SvelteKit example is still the right source for the
modern Paraglide setup, but only the compiler, Vite plugin, generated message
functions, and runtime locale switching pieces apply here.

## Decision

Use Paraglide JS as the i18n compiler and runtime. Configure
`paraglideVitePlugin` in `frontend/vite.config.ts`, generate files under
`frontend/src/lib/paraglide`, and store source messages in `frontend/messages`
using `frontend/project.inlang/settings.json`.

The generated `frontend/src/lib/paraglide` output remains ignored. Vite
regenerates it for dev, build, and test runs, and `npm run check` regenerates it
through the `i18n:compile` script before running `svelte-check`.

Message ids will be flattened from the current dotted JSON paths into generated
function names, for example `nav.sessions` becomes `m.nav_sessions()`. Svelte
components call these through the local `t(m.nav_sessions)` facade so locale
changes re-render in place without adding a string-key compatibility layer.

## Locale Behavior

Keep the app's current locale selection behavior:

- Supported locales are `en` and `zh-CN`.
- Paraglide uses `["localStorage", "preferredLanguage", "baseLocale"]` rather
  than URL-as-source routing because the frontend is an embedded SPA without
  localized routes.
- Stored locale in `localStorage["agentsview-locale"]` wins when valid.
- Browser language preference is used when no valid stored locale exists.
- Unsupported locales fall back to English.
- Calling `setLocale()` updates Paraglide's runtime locale and persists the
  selection when storage is available.

## Out of Scope

Do not add SvelteKit hooks, localized URLs, middleware, or cookie routing. Those
pieces solve SSR and URL localization, while this app is an embedded SPA served
from the Go binary.

## Testing

Keep the existing locale-selection unit tests and add Paraglide-specific tests
that verify:

- Flattened source messages preserve locale key parity.
- Generated message functions return English and Simplified Chinese text after
  runtime locale changes.
- Parameterized messages still interpolate values.

Run focused frontend tests while migrating, then run the frontend validation
suite before committing.
