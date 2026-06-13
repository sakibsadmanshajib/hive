import type { Config } from 'tailwindcss';

// Tailwind 4 is configured CSS-first through the @theme block in
// src/styles/tokens.css, which is the canonical, build-time source of design
// tokens. This file mirrors those tokens for tooling and editor integrations
// that still read a JS/TS config, and to keep theme.extend explicit and
// reviewable in one place. It is intentionally NOT wired via the @config
// directive (that path trips a rolldown-vite resolver bug in Astro 6). Both
// must stay in sync; tokens.css wins at build time.
const config: Config = {
  content: ['./src/**/*.{astro,html,js,jsx,ts,tsx,md,mdx}'],
  theme: {
    extend: {
      colors: {
        bg: {
          DEFAULT: '#0a0e16',
          raised: '#11161f',
          card: '#141b26',
        },
        border: {
          DEFAULT: '#1e2733',
          strong: '#2a3543',
        },
        text: {
          DEFAULT: '#f5f7fa',
          muted: '#aeb6c2',
          faint: '#828b99',
        },
        gold: '#f0b429',
        teal: '#2dd4bf',
        // Reserved exclusively for RISK framing.
        risk: '#f04438',
        // Competitor baseline. AA verified at 3.71:1 on the dark card surface.
        // Do not substitute #5b6472; it fails contrast.
        grey: '#6b7585',
      },
      borderRadius: {
        DEFAULT: '14px',
        card: '14px',
      },
      maxWidth: {
        shell: '1140px',
      },
      spacing: {
        section: '120px',
      },
      fontFamily: {
        // Fraunces is H1 only. Inter is every other UI surface.
        display: ['Fraunces', 'Inter', 'ui-serif', 'serif'],
        sans: ['Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        // Geist Mono carries all numerals and code, tabular figures.
        mono: ['Geist Mono', 'ui-monospace', 'SFMono-Regular', 'monospace'],
      },
    },
  },
  plugins: [],
};

export default config;
