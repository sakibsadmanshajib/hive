import { getViewer } from "@/lib/control-plane/client";

export default async function ConsolePage() {
  const viewer = await getViewer();
  const isUnverified = viewer.user.email_verified === false;

  return (
    <div>
      <h1>Dashboard</h1>
      <p>
        Workspace: <strong>{viewer.current_account.display_name}</strong>
      </p>
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
