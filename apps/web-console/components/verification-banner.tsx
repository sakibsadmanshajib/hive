import { useTranslations } from "next-intl";

interface VerificationBannerProps {
  show: boolean;
}

export function VerificationBanner({ show }: VerificationBannerProps) {
  const t = useTranslations("Verification");

  if (!show) return null;

  return (
    <div
      role="alert"
      className="border-b border-[var(--color-warning)]/40 bg-[var(--color-warning-soft)] px-6 py-2 text-center text-xs text-[var(--color-warning)]"
    >
      {t("banner")}
    </div>
  );
}
