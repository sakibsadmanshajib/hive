/*
 * The Hive mark: an enclosure holding a single cell.
 *
 * Geometry is lifted verbatim from deploy/docker/owui-static/favicon.svg (the
 * asset the chat surface already ships as its favicon and splash), so all
 * three surfaces show one mark rather than one mark plus a placeholder. The
 * cream plate and forest-green stroke of that asset are dropped here on
 * purpose: rendering in `currentColor` keeps the mark monochrome, lets it sit
 * on either palette, and avoids introducing a second brand colour into a
 * console whose only accent is sienna.
 *
 * Decorative by default. Pass a `title` when the mark stands alone with no
 * adjacent "Hive" wordmark to name it.
 */
export function HiveMark({
  size = 28,
  className,
  title,
}: {
  size?: number;
  className?: string;
  title?: string;
}) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 64 64"
      className={className}
      role={title ? "img" : undefined}
      aria-hidden={title ? undefined : "true"}
      aria-label={title}
    >
      <rect
        x="11"
        y="11"
        width="42"
        height="42"
        rx="11"
        fill="none"
        stroke="currentColor"
        strokeWidth="6"
      />
      <rect x="25" y="25" width="14" height="14" rx="3.5" fill="currentColor" />
    </svg>
  );
}
