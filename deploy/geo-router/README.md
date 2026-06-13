# Hive geo-router

A single Cloudflare Worker that steers visitors between the two marketing sites:

- Bangladesh traffic resolves to `hive.scubed.com.bd`.
- Everywhere else resolves to `hive.scubed.co`.

## Why a Worker and not a CNAME

A CNAME only maps a hostname to a target. It has no geographic logic. The geo
decision must run at the edge. This Worker reads `request.cf.country`, which
Cloudflare provides per request.

## Behaviour

1. Bots are never redirected. Each site stays independently indexable, which
   matters because Googlebot crawls from US IPs and must reach `.com.bd`
   directly. Combined with per-site self-canonical tags and the hreflang
   cross-links, both sites index correctly with no duplicate-content penalty.
2. All cross-host moves are `302`, never `301`. The decision is per-visitor and
   reversible, so it must not be cached as permanent.
3. A manual override wins over geo-IP. The footer country switcher links to
   `?geo=bd` and `?geo=co`. The Worker turns that into a sticky `hive_geo`
   cookie (one year, SameSite=Lax, Secure) and 302s to the chosen host, then
   strips the param. Once set, the visitor is never bounced back.
4. Routing is presentation only. It never moves payment surfaces or copy across
   hosts. `.com.bd` keeps BDT rails and carries no FX language; `.co` keeps
   USD/CAD. The shared API is reached by both via its own hostname (see the
   `api-hive.scubed.com.bd` CNAME to the shared API endpoint).
5. On `.com.bd` responses the Worker injects hreflang `Link` headers, so the BD
   site source stays byte for byte while still cross-linking to `.co` for SEO.

## Deploy

1. Create the two Cloudflare Pages projects (sovereign and BD) and note their
   project names.
2. In `wrangler.toml`, uncomment the two `[[services]]` bindings and set the
   service names to those Pages projects.
3. Bind the Worker to both apex routes (already in `wrangler.toml`).
4. `wrangler deploy`.

## Note on indexability

The `.co` site sets its own self-canonical and hreflang tags in
`website/sovereign/src/layouts/Base.astro`. The Worker adds the matching
hreflang headers on the `.com.bd` side. Do not switch any redirect to `301` or
the source site will be de-indexed.
