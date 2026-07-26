// Open WebUI's prebuilt index.html unconditionally loads this script
// (<script src="/static/loader.js" defer>) from STATIC_DIR before the SPA
// hydrates. Upstream intends it as an optional hook for a custom
// pre-hydration splash; Hive uses it as the one supported place to add chrome
// to a pinned, unforked Open WebUI image.
//
// What it adds: an entry point to the Hive agent workspace, which Caddy serves
// at /agent-workspace on this same origin (deploy/docker/Caddyfile.owui). Until
// now that surface was reachable only by typing the URL or by clicking the
// per-message "Open Agent Workspace" Action
// (deploy/docker/pipelines/hive_agent_console_action.py), which requires
// already having sent a message -- so in practice nobody found it.
//
// WHY NOT A REAL SIDEBAR NAV ITEM (asked during the 2026-07-26 UI/UX pass):
// Open WebUI v0.10.2 exposes no plugin slot for navigation. Its Functions API
// (Filter/Pipe/Action/Event) can add message-level actions and pipe models, not
// nav entries, and the sidebar is compiled Svelte with no extension point. The
// only way in would be to query OWUI's internal DOM by Tailwind utility class
// and re-append the node on every client-side render via a MutationObserver --
// a treadmill that breaks on any upstream markup change and can double-inject.
// A body-level fixed element touches none of OWUI's tree, survives SPA
// navigation because SvelteKit hydrates its own container (the
// `<div style="display: contents">` in index.html) and never this sibling, and
// degrades to nothing if upstream changes. A genuine nav item needs a fork of
// Open WebUI's frontend, or an upstream nav-slot API; neither is in scope here.
//
// Styling lives in custom.css (#hive-agent-launcher), including the viewport
// gate: the launcher is deliberately hidden on narrow viewports because a
// fixed overlay cannot reflow OWUI's layout and must not cover its composer.
(function () {
  "use strict";

  var ID = "hive-agent-launcher";
  var HREF = "/agent-workspace";

  function signedIn() {
    // localStorage.token is Open WebUI's own session token (its frontend reads
    // it on essentially every authenticated call). Absent means signed out, in
    // which case the launcher would only lead to a second sign-in prompt.
    // Wrapped because localStorage access throws outright in some privacy
    // modes, and a throwing branding script must not break the app.
    try {
      return Boolean(localStorage.getItem("token"));
    } catch (err) {
      return false;
    }
  }

  function inject() {
    if (!document.body || document.getElementById(ID) || !signedIn()) return;

    var link = document.createElement("a");
    link.id = ID;
    link.href = HREF;
    // Same-origin, full page load on purpose: it is a separate Next.js app,
    // not a route inside this SPA.
    link.setAttribute("aria-label", "Open the Hive agent workspace");

    // The Hive mark, same geometry as favicon.svg in this directory. Inline so
    // the launcher needs no extra network request, and stroked in currentColor
    // so it follows the pill's text colour in both OWUI themes.
    link.innerHTML =
      '<svg viewBox="0 0 64 64" aria-hidden="true" focusable="false">' +
      '<rect x="11" y="11" width="42" height="42" rx="11" fill="none"' +
      ' stroke="currentColor" stroke-width="6"/>' +
      '<rect x="25" y="25" width="14" height="14" rx="3.5" fill="currentColor"/>' +
      "</svg>" +
      '<span id="' + ID + '-label">Agent workspace</span>';

    document.body.appendChild(link);
  }

  // `defer` means the document is already parsed, so document.body exists and
  // the first attempt runs immediately.
  //
  // The retries exist because this script runs BEFORE the SPA boots, and on the
  // first page load of a session OWUI has not written localStorage.token yet --
  // verified by screenshot: the launcher was missing on the landing screen and
  // appeared only after the next navigation, which is the worst possible place
  // to lose it. A short bounded poll covers hydration and the moment just after
  // sign-in. Bounded on purpose: it gives up for good rather than becoming a
  // permanent interval, and it never reads OWUI's DOM.
  var ATTEMPT_INTERVAL_MS = 400;
  var MAX_ATTEMPTS = 15; // ~6s
  var attempts = 0;

  function attempt() {
    inject();
    attempts += 1;
    if (document.getElementById(ID) || attempts >= MAX_ATTEMPTS) return;
    setTimeout(attempt, ATTEMPT_INTERVAL_MS);
  }

  attempt();
})();
