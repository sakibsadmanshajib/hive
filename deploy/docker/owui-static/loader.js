// Open WebUI's prebuilt index.html unconditionally loads this script
// (<script src="/static/loader.js" defer>) from STATIC_DIR before the SPA
// hydrates. Upstream intends it as an optional hook for a custom
// pre-hydration splash; Hive uses it to add chrome to the pinned upstream
// image without rebuilding the bundle.
//
// NOT A RULE ANY MORE (owner decision 2026-08-11, .wolf/decisions.md D-036):
// this file used to say it was "the one supported place" because forking Open
// WebUI was forbidden under D-013. D-013 is revoked. Open WebUI is forked and
// heavily modified, so this hook is the old ceiling, not a constraint, and
// nothing here may be cited to refuse fork work.
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
// Open WebUI's frontend, or an upstream nav-slot API. That fork was out of
// scope when this was written and is now the chosen direction (D-036), so the
// real nav item is buildable; this launcher stands until the fork lands it.
//
// Styling lives in custom.css (#hive-agent-launcher), which matches OWUI's own
// header icon buttons value for value and carries the measurements behind the
// position and the viewport gate. Short version: shown from 768px up (not
// 1024px as originally shipped), icon-only below 1024px, and the floor is set
// by OWUI's own unclamped model selector rather than by this element.
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
    // The getElementById check is the idempotency guard: inject() is called
    // repeatedly by the poll below, and a second node would stack a duplicate
    // control on top of the first. Verified live against SvelteKit's
    // client-side re-renders -- a New Chat navigation and a full send/stream
    // round trip both leave exactly one node -- because this element is a
    // sibling of the container SvelteKit hydrates, never a child of it.
    if (!document.body || document.getElementById(ID) || !signedIn()) return;

    var link = document.createElement("a");
    link.id = ID;
    link.href = HREF;
    // Same-origin, full page load on purpose: it is a separate Next.js app,
    // not a route inside this SPA.
    link.setAttribute("aria-label", "Open the Hive agent workspace");
    // Below 1024px custom.css hides the text label, matching the icon-only
    // shape of OWUI's own header buttons. The native tooltip is what carries
    // the name at those widths.
    link.title = "Agent workspace";

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
  // to lose it.
  //
  // Two intervals, because one cannot serve both cases this has to cover:
  //
  //   1. Hydration, which resolves in well under a second. FAST_INTERVAL_MS.
  //   2. Sign-in, which is however long a person takes to type a password.
  //      OWUI signs in WITHOUT a document load -- confirmed live: zero `load`
  //      events fire, the SPA just routes from /auth to / -- so this script
  //      never runs again and the poll is the only thing that can notice.
  //
  // The stop condition is the tab being hidden, NOT an elapsed-time ceiling.
  // Every deadline measured from page load has the same defect, only further
  // out: whatever the number, a person who sits on the sign-in screen longer
  // than it loses the launcher for the rest of the session. A ~6s ceiling
  // shipped first and broke on password managers; a 10 minute one would still
  // break on someone reading the page. Elapsed time since load is simply not
  // the signal -- the timer only needs to stop when nobody is looking, which
  // `document.hidden` answers directly. So it polls while the tab is
  // foreground, stops when it is backgrounded, and resumes on
  // visibilitychange. An abandoned tab keeps no timer alive; a tab someone is
  // actually looking at keeps waiting, which is exactly when it should.
  //
  // Each attempt is two synchronous property reads and never touches OWUI's
  // DOM, so the slow phase costs nothing worth measuring.
  //
  // Sign-OUT needs no equivalent handling: it does trigger a full document
  // load, so the launcher disappears with the rest of the page. Also verified.
  var FAST_INTERVAL_MS = 400;
  var SLOW_INTERVAL_MS = 1000;
  var FAST_ATTEMPTS = 15; // ~6s of hydration cover
  var attempts = 0;
  var pending = null;

  function attempt() {
    pending = null;
    inject();
    attempts += 1;
    if (document.getElementById(ID) || document.hidden) return;
    pending = setTimeout(
      attempt,
      attempts < FAST_ATTEMPTS ? FAST_INTERVAL_MS : SLOW_INTERVAL_MS,
    );
  }

  // `pending` keeps a resume from stacking a second timer on top of a live one.
  document.addEventListener("visibilitychange", function () {
    if (!document.hidden && !pending && !document.getElementById(ID)) attempt();
  });

  attempt();
})();
