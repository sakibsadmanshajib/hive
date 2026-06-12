# Hive marketing website

Static marketing site for Hive, Bangladesh's AI platform. Hand-rolled semantic
HTML with a single handcrafted stylesheet. No framework, no build step, no
external font or CSS CDN. Everything ships exactly as committed, which keeps it
trivial to host on a static platform and trivial to verify.

## Structure

```
website/
  index.html        Landing page (hero, editions, developers, EnterpriseEdge,
                    chat workstation, pricing teaser, footer)
  docs.html         Minimal quickstart docs page
  assets/
    styles.css      The entire stylesheet (design tokens + all components)
  _headers          Cloudflare Pages security headers
  README.md         This file
```

There is no `package.json` and nothing to compile. The committed files are the
deployable output.

## Local preview

Open `index.html` directly in a browser, or serve the folder with any static
server, for example:

```bash
cd website
python3 -m http.server 8000
# then open http://localhost:8000
```

## Verification

Because there is no build step, verification is:

1. The site renders with files opened directly (no bundler, no transform).
2. HTML validates as well-formed (single root, balanced tags, declared `lang`).
3. Accessibility basics: skip link, semantic landmarks, `aria-label`s on icon-only
   controls, decorative graphics marked `aria-hidden`, AA contrast on the dark base.
4. Responsive at mobile, tablet and desktop breakpoints via the media queries in
   `assets/styles.css`.

## Deploy to Cloudflare Pages (free tier)

This site is plain static output, so Cloudflare Pages serves it with no build
configuration.

### Option A, dashboard (Git integration)

1. Push this repository to GitHub (already the case for `hive`).
2. In the Cloudflare dashboard go to **Workers & Pages > Create > Pages >
   Connect to Git** and select the `hive` repository.
3. Build settings:
   - **Framework preset:** None
   - **Build command:** leave empty (no build step)
   - **Build output directory:** `website`
4. Deploy. Cloudflare publishes the contents of `website/` to a
   `*.pages.dev` URL.

### Option B, Wrangler CLI (direct upload)

```bash
npm install -g wrangler
wrangler pages deploy website --project-name hive-marketing
```

### Custom domain

Once a domain is chosen, add it under **Pages project > Custom domains** and
point a CNAME at the `*.pages.dev` target. Cloudflare provisions TLS
automatically.

## Domain note (decision pending)

The owner mentioned **hive.sqv.co** as the likely target domain. **scubed.co**
is also owned, and the API endpoint referenced in the copy currently uses
`api-hive.scubed.co`. The final marketing domain is **not yet decided**. The
site itself is domain-agnostic: all internal links are relative and external
links use absolute public URLs, so it will work under whichever hostname is
chosen without code changes. When the decision is made, only DNS and the
Cloudflare custom-domain binding need updating.
