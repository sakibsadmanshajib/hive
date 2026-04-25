interface VerificationBannerProps {
  show: boolean;
}

export function VerificationBanner({ show }: VerificationBannerProps) {
  if (!show) return null;

  return (
    <div
      role="alert"
      className="border-b border-[var(--color-warning)]/40 bg-[var(--color-warning-soft)] px-6 py-2 text-center text-xs text-[var(--color-warning)]"
    >
      Please verify your email address to unlock all features.
    </div>
  );
}
