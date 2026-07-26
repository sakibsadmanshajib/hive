// Open WebUI's prebuilt index.html unconditionally loads this script
// (<script src="/static/loader.js" defer>) from STATIC_DIR before the SPA
// hydrates. It is an optional hook for a custom pre-hydration splash
// animation; this deployment does not use one. The file existing (even
// empty) is what stops the request 404ing on every page load.
//
// ponytail: intentionally a no-op. Add real splash logic only if product
// ever wants a custom loading animation before the SPA takes over.
