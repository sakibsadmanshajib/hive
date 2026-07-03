// Thin wrapper around window.location.assign so client components can be
// unit tested without fighting jsdom's non-configurable Location.assign
// (see components/oauth/consent-panel.test.tsx and
// __tests__/sign-in-next-redirect.test.tsx, which mock this module instead
// of the DOM API).
export function navigate(url: string): void {
  window.location.assign(url);
}
