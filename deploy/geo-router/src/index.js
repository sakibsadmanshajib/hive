// Hive geo-router (Cloudflare Worker).
//
// Sits in front of both marketing sites and steers by geography:
//   Bangladesh traffic -> hive.scubed.com.bd
//   everywhere else    -> hive.scubed.co
//
// Design rules (see README):
//   1. Bots are never redirected, so each site stays independently indexable
//      (Googlebot crawls from US IPs and must be able to reach .com.bd directly).
//   2. Redirects are always 302, never 301 (the decision is per-visitor and
//      reversible; a 301 would de-index the source).
//   3. A manual override wins over geo-IP: the ?geo=bd|co query sets a sticky
//      hive_geo cookie (one year) so a visitor is never bounced back.
//   4. Routing is presentation only. It never mixes payment surfaces or copy
//      across hosts; .com.bd keeps BDT rails and zero FX language, .co keeps
//      USD/CAD. The shared API is reached by both via its own hostname.
//   5. On .com.bd responses the Worker injects hreflang Link headers so the BD
//      site stays byte for byte yet still cross-links for SEO.
//
// Origin is reached through service bindings (env.ORIGIN_SOV, env.ORIGIN_BD)
// so the Worker never fetches its own route and cannot loop. See wrangler.toml.

const SOV_HOST = 'hive.scubed.co';
const BD_HOST = 'hive.scubed.com.bd';
const COOKIE = 'hive_geo';

// Known crawlers. Matched loosely; the cost of a false positive is only that a
// human bot-like UA is not geo-steered, which is safe.
const BOT_RE =
  /(bot|crawler|spider|crawling|googlebot|bingbot|duckduckbot|slurp|baiduspider|yandex|facebookexternalhit|embedly|quora|slackbot|twitterbot|applebot|petalbot|ia_archiver|lighthouse|chrome-lighthouse)/i;

function parseCookies(header) {
  const out = {};
  if (!header) return out;
  for (const part of header.split(';')) {
    const i = part.indexOf('=');
    if (i === -1) continue;
    out[part.slice(0, i).trim()] = part.slice(i + 1).trim();
  }
  return out;
}

function originFor(host, env) {
  // Fall back to a normal fetch if no binding is configured (local dev).
  if (host === BD_HOST) return env.ORIGIN_BD || null;
  return env.ORIGIN_SOV || null;
}

async function serve(request, url, host, env) {
  const binding = originFor(host, env);
  const resp = binding ? await binding.fetch(request) : await fetch(request);

  // Inject hreflang on the BD site without editing its files.
  if (host === BD_HOST) {
    const headers = new Headers(resp.headers);
    const path = url.pathname;
    headers.append('Link', `<https://${SOV_HOST}${path}>; rel="alternate"; hreflang="en"`);
    headers.append('Link', `<https://${SOV_HOST}${path}>; rel="alternate"; hreflang="x-default"`);
    headers.append('Link', `<https://${BD_HOST}${path}>; rel="alternate"; hreflang="bn-BD"`);
    return new Response(resp.body, { status: resp.status, statusText: resp.statusText, headers });
  }
  return resp;
}

function redirectTo(url, host) {
  const dest = new URL(url.toString());
  dest.hostname = host;
  return new Response(null, { status: 302, headers: { Location: dest.toString() } });
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const host = url.hostname;
    const ua = request.headers.get('user-agent') || '';

    // 1. Bots: serve the requested host as is. Never redirect.
    if (BOT_RE.test(ua)) return serve(request, url, host, env);

    const cookies = parseCookies(request.headers.get('cookie'));

    // 2. ?geo override: set the sticky cookie, 302 to the chosen host, strip param.
    const geoParam = url.searchParams.get('geo');
    if (geoParam === 'bd' || geoParam === 'co') {
      const target = geoParam === 'bd' ? BD_HOST : SOV_HOST;
      const dest = new URL(url.toString());
      dest.searchParams.delete('geo');
      dest.hostname = target;
      const headers = new Headers({ Location: dest.toString() });
      headers.append(
        'Set-Cookie',
        `${COOKIE}=${geoParam}; Path=/; Max-Age=31536000; SameSite=Lax; Secure`,
      );
      return new Response(null, { status: 302, headers });
    }

    // 3. Sticky preference wins over geo-IP.
    const pref = cookies[COOKIE];
    if (pref === 'bd' || pref === 'co') {
      const target = pref === 'bd' ? BD_HOST : SOV_HOST;
      if (host !== target) return redirectTo(url, target);
      return serve(request, url, host, env);
    }

    // 4. Geo-IP default. BD -> .com.bd, everyone else -> .co.
    const country = request.cf && request.cf.country;
    const target = country === 'BD' ? BD_HOST : SOV_HOST;
    if (host !== target) return redirectTo(url, target);

    return serve(request, url, host, env);
  },
};
