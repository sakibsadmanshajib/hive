# Hive Console — Style Guide

> Aesthetic anchor: **Claude.com** — restrained, dense, calm.
> One accent. Monochrome data. Generous letter-spacing on display type.
> Tabular numerals on every numeric surface.

This document is the visual contract for the Hive web console. It pairs
with `app/globals.css` (the runtime tokens) and the primitives in
`components/ui/`.

## Operating principles

- **Restraint over flair.** One accent (sienna). No gradients, no
  glassmorphism, no decorative illustrations on internal pages.
- **Density without clutter.** 14px body, 13px small. Spacing scale steps
  at 4 / 6 / 8 / 12 / 16 / 24 / 32 / 48. Internal pages rarely cross 32px.
- **Tabular numerics everywhere** usage, billing, latency, IDs render —
  add `tabular-nums` class or the `data-numeric` attribute.
- **Single-column thinking.** No 3-column splits except true settings
  pages where a left index aids navigation.
- **Motion is functional.** Hover, focus, page transitions — 120-180ms
  ease-out (`cubic-bezier(0.16, 1, 0.3, 1)`). No decorative loops.

## Typography

| Role | Family | CSS variable | Usage |
|------|--------|--------------|-------|
| Body | Geist Sans | `--font-sans` | Default body, forms, tables |
| Display | Fraunces (variable serif) | `--font-display` | h1/h2 only — page titles, hero copy |
| Mono | Geist Mono | `--font-mono` | Code, API keys, IDs, tabular columns |

Loaded via `next/font/google` in `app/layout.tsx`. Display headings get
`letter-spacing: -0.02em` and `font-weight: 500`; body headings (h3+) get
`-0.01em` and `font-weight: 600`.

### Type scale

| Token | px | Use |
|-------|----|-----|
| `text-2xs` | 11 | Badges, eyebrow labels |
| `text-xs` | 12 | Helper text, table headers, footnotes |
| `text-sm` | 13 | Default secondary, sidebar items |
| `text-base` | 14 | Body, form fields |
| `text-md` | 15 | Card titles |
| `text-lg` | 16 | Large body, dialog titles |
| `text-xl` | 18 | Section headers |
| `text-2xl` | 22 | Auth heading |
| `text-3xl` | 28 | Page heading (`PageHeader`) |
| `text-4xl` | 36 | Hero/marketing only — never internal |

## Color tokens

All colors are defined in oklch in `app/globals.css` under `@theme`. Dark
mode uses `prefers-color-scheme` with an inline `@theme` override.

| Token | Light intent | Where |
|-------|--------------|-------|
| `--color-canvas` | Page background | `body`, app shell |
| `--color-surface` | Card / panel surface | `Card`, sidebar, tables |
| `--color-surface-2` | Hover surface | Button hover, list hover |
| `--color-surface-inset` | Subtle inset / table stripes | Code blocks, table headers |
| `--color-border` | Default divider | All borders |
| `--color-border-strong` | Hover border | Active controls |
| `--color-ink` | Primary text | Body, headings |
| `--color-ink-2` | Secondary text | Sidebar, labels |
| `--color-ink-3` | Muted | Helper text, eyebrows |
| `--color-ink-4` | Disabled / placeholder | Empty inputs |
| `--color-accent` | Sienna accent | Primary buttons, links, focus |
| `--color-accent-soft` | Tinted bg for accent badges | `Badge tone="accent"` |
| `--color-success/warning/danger` | Status pairs | `Badge`, banners |

### Accent discipline

- Use accent **only** for: primary CTA on a page, active route in sidebar,
  focus ring, the single most important inline link.
- **Never** use accent for: card chrome, dividers, body links inside
  paragraphs, table rows.

## Spacing & radius

- Spacing follows Tailwind defaults; pages typically compose in
  multiples of 2 (gap-2, gap-3, gap-4, gap-6).
- Radii: `rounded-md` (8) for inputs + buttons, `rounded-lg` (12) for
  cards + tables, `rounded-full` for badges + avatars only.

## Motion

- Duration: `--duration-fast` (120ms) for hover/focus, `--duration-base`
  (180ms) for page-level transitions, `--duration-slow` (280ms) only for
  panel/sheet enter.
- Easing: `--ease-out-expo` (`cubic-bezier(0.16, 1, 0.3, 1)`) — feels
  fast at the front, settles softly. Linear-style.

## Components (in `components/ui/`)

- `Button` — variants `primary`, `accent`, `secondary`, `ghost`, `danger`,
  `link`. Sizes `sm`, `md`, `lg`, `icon`.
- `Input`, `Label`, `Field` — form primitives. `Field` composes the
  three with `hint` / `error` slots.
- `Card`, `CardHeader`, `CardTitle`, `CardDescription`, `CardContent`,
  `CardFooter` — content surfaces.
- `Badge` — six tones (`neutral`, `accent`, `success`, `warning`,
  `danger`, `outline`). Use for status, capability tags, role chips.
- `DataTable` — typed columns API. `numeric: true` flips tabular nums
  + right-aligns automatically.
- `EmptyState` — illustrated zero-state with optional CTA.
- `PageHeader` — page title block with eyebrow, description, and right-
  aligned actions.

## Shells (in `components/app-shell/`)

- `AuthShell` — single-column centered card layout for unauth pages
  (sign-in, sign-up, forgot-password, reset-password, accept-invite).
  Wordmark top-left, copyright + version footer.
- `ConsoleShell` — sidebar (240px) + topbar + scrollable content
  (max-width 64rem). Workspace switcher in the sidebar header, account
  avatar at the sidebar foot, route-based active state on links.

## Don'ts

- No emoji, no decorative SVG illustrations on internal pages.
- No purple gradients. No glassmorphism. No drop shadows above
  `--shadow-md` on internal surfaces.
- No three-column grids; favor two columns max with generous whitespace.
- Never display FX rates or "≈ USD" hints to BD users (regulatory rule).
- Never use `as`, `any`, `unknown` in component props (`feedback_strict_typescript`).
