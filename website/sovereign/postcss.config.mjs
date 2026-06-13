// Tailwind 4 via PostCSS. Runs inside Astro's own vite 7 pipeline, avoiding the
// dual-vite/rolldown binding mismatch that the @tailwindcss/vite plugin trips
// in Astro 6.
const config = {
  plugins: {
    '@tailwindcss/postcss': {},
  },
};

export default config;
