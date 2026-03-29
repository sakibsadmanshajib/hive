interface VerificationBannerProps {
  show: boolean;
}

export function VerificationBanner({ show }: VerificationBannerProps) {
  if (!show) return null;

  return (
    <div
      role="alert"
      style={{
        backgroundColor: "#fef3c7",
        borderBottom: "1px solid #fbbf24",
        padding: "0.75rem 1.5rem",
        textAlign: "center",
        fontSize: "0.875rem",
      }}
    >
      Please verify your email address to unlock all features.
    </div>
  );
}
