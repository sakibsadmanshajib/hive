import type { ReactNode } from "react";

/*
 * Hive brand chrome for the agent workspace.
 *
 * The mark's geometry is the same as deploy/docker/owui-static/favicon.svg and
 * apps/web-console/components/brand/hive-mark.tsx. It is duplicated rather
 * than imported because deploy/docker/Dockerfile.agent-console copies only
 * apps/agent-console into the image, so a cross-app import would break that
 * build for the sake of forty lines of SVG. Keep the three in step: same
 * enclosure, same inner cell, rendered in currentColor so it works on either
 * palette without introducing a second brand colour.
 */
export function HiveMark({
  size = 26,
  className,
}: {
  size?: number;
  className?: string;
}) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 64 64"
      className={className}
      aria-hidden="true"
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

export function Wordmark({ size = 26 }: { size?: number }) {
  return (
    <span className="flex items-center gap-2.5 text-[var(--color-ink)]">
      <HiveMark size={size} />
      <span className="font-display text-lg font-semibold leading-none tracking-[-0.03em]">
        Hive
      </span>
    </span>
  );
}

/**
 * Top bar for the signed-in workspace. Names the product, names this surface,
 * and gives the user a way back to chat -- the sidecar previously had no
 * chrome at all, so it read as a stray page rather than part of Hive.
 */
export function AppHeader() {
  return (
    <header className="sticky top-0 z-10 border-b border-[var(--color-border)] bg-[var(--color-surface)]/95 backdrop-blur-sm">
      <div className="mx-auto flex h-14 w-full max-w-3xl items-center justify-between gap-4 px-6">
        <div className="flex min-w-0 items-center gap-3">
          <Wordmark size={22} />
          <span
            aria-hidden="true"
            className="h-4 w-px shrink-0 bg-[var(--color-border-strong)]"
          />
          <span className="truncate text-sm text-[var(--color-ink-2)]">
            Agent workspace
          </span>
        </div>
        {/*
          Raw anchor, not next/link: this app runs under basePath
          "/agent-workspace", and next/link would rewrite "/" to
          "/agent-workspace/". The chat SPA is served from the origin root by
          the same Caddy listener (deploy/docker/Caddyfile.owui), so an
          unprefixed href is exactly what gets the user back to chat.
        */}
        <a
          href="/"
          className="shrink-0 text-xs text-[var(--color-ink-3)] transition-colors duration-[var(--duration-fast)] hover:text-[var(--color-ink)]"
        >
          &larr; Back to chat
        </a>
      </div>
    </header>
  );
}

/**
 * Centered single-column frame for the sign-in screen. Mirrors
 * apps/web-console/components/app-shell/auth-shell.tsx so the two sign-in
 * pages read as the same product: wordmark, eyebrow, heading, subtitle, form,
 * and a hairline footer.
 */
export function AuthShell({
  eyebrow,
  title,
  subtitle,
  children,
}: {
  eyebrow?: string;
  title: string;
  subtitle?: string;
  children: ReactNode;
}) {
  return (
    <main className="flex min-h-screen w-full flex-col bg-[var(--color-canvas)]">
      <div className="flex flex-1 flex-col items-center justify-center px-6 py-16">
        <div className="flex w-full max-w-[380px] flex-col gap-7">
          <div className="flex flex-col gap-3">
            <Wordmark />
            {eyebrow ? (
              <span className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
                {eyebrow}
              </span>
            ) : null}
            <h1 className="text-2xl leading-tight text-[var(--color-ink)]">
              {title}
            </h1>
            {subtitle ? (
              <p className="text-sm leading-relaxed text-[var(--color-ink-3)]">
                {subtitle}
              </p>
            ) : null}
          </div>
          <div className="flex flex-col gap-5">{children}</div>
        </div>
      </div>
      <footer className="flex items-center justify-between border-t border-[var(--color-border)] px-6 py-4 text-2xs text-[var(--color-ink-3)]">
        <span>&copy; {new Date().getFullYear()} Hive</span>
        <span className="font-mono">agent workspace &middot; v1</span>
      </footer>
    </main>
  );
}
