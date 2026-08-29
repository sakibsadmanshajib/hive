import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

// Guard for issue #491. The dark-mode CSS block redefined only the -soft
// semantic tokens, never the solid ones (--color-success, --color-warning,
// --color-danger), so dark mode fell through the cascade to the light-mode
// values -- which fail WCAG AA against dark backgrounds. This test parses
// the real token values out of globals.css (not a hardcoded copy of them)
// and computes actual contrast ratios, so it goes red again if the dark
// redefinition is ever reverted or a future value regresses.

const CSS_PATH = resolve(__dirname, "../../app/globals.css");

type Oklch = { L: number; C: number; H: number };

function parseTokens(block: string): Record<string, Oklch> {
  const tokens: Record<string, Oklch> = {};
  const re = /--(color-[a-z0-9-]+):\s*oklch\(([\d.]+)\s+([\d.]+)\s+([\d.]+)\)/g;
  let match = re.exec(block);
  while (match !== null) {
    const [, name, l, c, h] = match;
    tokens[name] = { L: Number(l), C: Number(c), H: Number(h) };
    match = re.exec(block);
  }
  return tokens;
}

function loadTokens(): { light: Record<string, Oklch>; dark: Record<string, Oklch> } {
  const css = readFileSync(CSS_PATH, "utf8");
  const themeMatch = css.match(/@theme\s*\{([\s\S]*?)\n\}/);
  const darkMatch = css.match(/prefers-color-scheme:\s*dark\)\s*\{\s*:root\s*\{([\s\S]*?)\n\s*\}\n\}/);
  if (!themeMatch || !darkMatch) {
    throw new Error("could not locate @theme or dark :root block in globals.css");
  }
  const light = parseTokens(themeMatch[1]);
  const darkOverrides = parseTokens(darkMatch[1]);
  const dark: Record<string, Oklch> = Object.assign({}, light, darkOverrides);
  return { light: light, dark: dark };
}

// OKLCH -> linear sRGB per the CSS Color 4 conversion formulas, clamped to
// the visible gamut for luminance purposes (matches how a browser displays
// an out-of-gamut token). Validated by reproducing this issue's own cited
// figures exactly (danger badge 2.65:1, success badge 3.12:1, etc.) before
// any fix was applied.
function oklchToLinearSrgb(token: Oklch): [number, number, number] {
  const hrad = (token.H * Math.PI) / 180;
  const a = token.C * Math.cos(hrad);
  const b = token.C * Math.sin(hrad);
  const l_ = token.L + 0.3963377774 * a + 0.2158037573 * b;
  const m_ = token.L - 0.1055613458 * a - 0.0638541728 * b;
  const s_ = token.L - 0.0894841775 * a - 1.291485548 * b;
  const l = l_ ** 3;
  const m = m_ ** 3;
  const s = s_ ** 3;
  const X = 1.2268798758459243 * l - 0.5578149944602171 * m + 0.2813910456659647 * s;
  const Y = -0.0405757452148008 * l + 1.112286803280317 * m - 0.0717110580655164 * s;
  const Z = -0.0763729366746601 * l - 0.4214933324022432 * m + 1.5869240198367816 * s;
  const R = 3.2409699419045226 * X - 1.537383177570094 * Y - 0.4986107602930034 * Z;
  const G = -0.9692436362808796 * X + 1.8759675015077202 * Y + 0.04155505740717559 * Z;
  const B = 0.05563007969699366 * X - 0.20397695888897652 * Y + 1.0569715142428786 * Z;
  const clamp = (c: number): number => Math.min(1, Math.max(0, c));
  return [clamp(R), clamp(G), clamp(B)];
}

function relativeLuminance(rgb: [number, number, number]): number {
  return 0.2126 * rgb[0] + 0.7152 * rgb[1] + 0.0722 * rgb[2];
}

// WCAG 2.x contrast ratio, per https://www.w3.org/TR/WCAG21/#contrast-minimum.
function contrastRatio(fg: Oklch, bg: Oklch): number {
  const L1 = relativeLuminance(oklchToLinearSrgb(fg));
  const L2 = relativeLuminance(oklchToLinearSrgb(bg));
  const lighter = Math.max(L1, L2);
  const darker = Math.min(L1, L2);
  return (lighter + 0.05) / (darker + 0.05);
}

const AA_NORMAL_TEXT = 4.5;
const AA_NON_TEXT = 3;
const SOLID_TOKENS = ["color-success", "color-warning", "color-danger"];

describe("dark mode solid semantic tokens clear WCAG AA (issue #491)", () => {
  const { light, dark } = loadTokens();
  it.each(SOLID_TOKENS)("%s text clears 4.5:1 against dark canvas", (name) => {
    expect(contrastRatio(dark[name], dark["color-canvas"])).toBeGreaterThanOrEqual(AA_NORMAL_TEXT);
  });

  it.each(SOLID_TOKENS)("%s text clears 4.5:1 against dark surface (card)", (name) => {
    expect(contrastRatio(dark[name], dark["color-surface"])).toBeGreaterThanOrEqual(AA_NORMAL_TEXT);
  });

  it.each(SOLID_TOKENS)("%s badge foreground clears 4.5:1 against its own soft background in dark mode", (name) => {
    const softName = name + "-soft";
    expect(contrastRatio(dark[name], dark[softName])).toBeGreaterThanOrEqual(AA_NORMAL_TEXT);
  });

  it("canvas-on-danger pairing clears 4.5:1 in both themes (paired with the button.tsx literal check below, which confirms the component actually uses this pairing)", () => {
    expect(contrastRatio(dark["color-canvas"], dark["color-danger"])).toBeGreaterThanOrEqual(AA_NORMAL_TEXT);
    expect(contrastRatio(light["color-canvas"], light["color-danger"])).toBeGreaterThanOrEqual(AA_NORMAL_TEXT);
  });

  it("success cache-split bar fill clears the 3:1 non-text threshold against surface-2 in both themes", () => {
    expect(contrastRatio(dark["color-success"], dark["color-surface-2"])).toBeGreaterThanOrEqual(AA_NON_TEXT);
    expect(contrastRatio(light["color-success"], light["color-surface-2"])).toBeGreaterThanOrEqual(AA_NON_TEXT);
  });

  it.each(SOLID_TOKENS)("dark :root block explicitly redefines --%s (not just its -soft variant)", (name) => {
    const css = readFileSync(CSS_PATH, "utf8");
    const darkMatch = css.match(/prefers-color-scheme:\s*dark\)\s*\{\s*:root\s*\{([\s\S]*?)\n\s*\}\n\}/);
    const darkBlock = darkMatch ? darkMatch[1] : "";
    expect(darkBlock).toContain("--" + name + ": oklch(");
  });

  it("button.tsx danger variant does not hardcode text-white (fails AA once dark danger is lightened)", () => {
    const buttonPath = resolve(__dirname, "../../components/ui/button.tsx");
    const buttonSrc = readFileSync(buttonPath, "utf8");
    expect(buttonSrc).not.toContain("bg-[var(--color-danger)] text-white");
    expect(buttonSrc).toContain("bg-[var(--color-danger)] text-[var(--color-canvas)]");
  });
});
