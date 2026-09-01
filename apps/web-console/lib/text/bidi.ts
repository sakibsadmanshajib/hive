/**
 * Unicode bidirectional control characters, removed from every string the
 * console decodes out of the control plane (issue #1653).
 *
 * This is not HTML injection: React escapes text nodes, and none of these
 * codepoints closes a tag. It is a rendering-order defect. Browsers honour
 * these characters inside a plain text node, so a single U+202E stored in an
 * API key nickname reverses the characters after it and visually reorders the
 * labels around it wherever that nickname is rendered: the /console/logs
 * filter select, the usage table, the CSV export, and the same code path that
 * carries member names.
 *
 * Codepoints, per UAX 9:
 *   U+061C          arabic letter mark
 *   U+200E, U+200F  left-to-right mark, right-to-left mark
 *   U+202A..U+202E  the two embeddings, the pop, and the two overrides
 *   U+2066..U+2069  the three isolates and the pop
 *
 * Removed rather than replaced with a visible marker: these characters carry
 * no meaning of their own, so a name that contained one reads exactly as its
 * letters already read without it. Genuine right-to-left text is untouched,
 * because Arabic and Hebrew letters have their own strong direction and need
 * none of these to lay out correctly.
 */
export function stripBidiControls(value: string): string {
  return value.replace(/[\u061C\u200E\u200F\u202A-\u202E\u2066-\u2069]/gu, "");
}
