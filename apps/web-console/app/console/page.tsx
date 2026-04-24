import {
  getAccountProfile,
  getViewer,
} from "@/lib/control-plane/client";

export default async function ConsolePage() {
  const viewer = await getViewer();
  const profile = await getAccountProfile();
  const isUnverified = viewer.user.email_verified === false;
  const needsSetup = profile.profile_setup_complete === false;

  return (
    <div>
      <h1>Dashboard</h1>
      <p>
        Workspace: <strong>{viewer.current_account.display_name}</strong>
      </p>
      {needsSetup && (
        <div
          role="status"
          style={{
            marginTop: "1rem",
            padding: "1rem",
            backgroundColor: "#eff6ff",
            border: "1px solid #93c5fd",
            borderRadius: "0.75rem",
            display: "grid",
            gap: "0.5rem",
            maxWidth: "36rem",
          }}
        >
          <strong>Complete your minimal workspace profile</strong>
          <span style={{ color: "#1f2937" }}>
            Finish the owner, account, and location basics to remove this
            reminder. Billing details stay optional until later.
          </span>
          <a
            href="/console/setup"
            style={{ width: "fit-content", color: "#1d4ed8", fontWeight: 600 }}
          >
            Complete setup
          </a>
        </div>
      )}
      {isUnverified && (
        <div
          role="status"
          style={{
            marginTop: "1rem",
            padding: "1rem",
            backgroundColor: "#fef9c3",
            border: "1px solid #fde047",
            borderRadius: "0.5rem",
          }}
        >
          Your email address has not been verified. Some features are restricted
          until you confirm your email.
        </div>
      )}
    </div>
  );
}
