import "@testing-library/jest-dom/vitest";

process.env.NEXT_PUBLIC_API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://127.0.0.1:8080";
process.env.NEXT_PUBLIC_SUPABASE_URL = process.env.NEXT_PUBLIC_SUPABASE_URL ?? "http://127.0.0.1:54321";
process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY =
  process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY ?? "test-supabase-anon-key";

// Radix UI Select requires ResizeObserver for portal content rendering in jsdom
if (typeof window.ResizeObserver === "undefined") {
  window.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof globalThis.ResizeObserver;
}

// Radix UI needs non-zero bounding rects for pointer/position logic in jsdom
const originalGetBoundingClientRect = Element.prototype.getBoundingClientRect;
Element.prototype.getBoundingClientRect = function () {
  const rect = originalGetBoundingClientRect.call(this);
  if (rect.width === 0 && rect.height === 0) {
    return { x: 0, y: 0, width: 100, height: 20, top: 0, right: 100, bottom: 20, left: 0, toJSON: () => ({}) } as DOMRect;
  }
  return rect;
};

// Radix UI Presence relies on CSS animations completing; jsdom doesn't fire
// animationend/transitionend, so content stays hidden. Mock Element.animate
// so Radix immediately resolves its presence state.
if (typeof Element.prototype.animate === "undefined") {
  Element.prototype.animate = function () {
    return {
      finished: Promise.resolve(),
      cancel: () => {},
      onfinish: null,
      persist: () => {},
      commitStyles: () => {},
    } as unknown as Animation;
  };
}
if (typeof Element.prototype.getAnimations === "undefined") {
  Element.prototype.getAnimations = function () {
    return [];
  };
}

if (typeof window.scrollTo === "undefined") {
  window.scrollTo = () => undefined;
}

Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string) => ({
    matches: query.includes("dark"),
    media: query,
    onchange: null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    addListener: () => undefined,
    removeListener: () => undefined,
    dispatchEvent: () => false,
  }),
});
