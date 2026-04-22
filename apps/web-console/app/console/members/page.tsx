import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { createClient } from "@/lib/supabase/server";
import { getMembers, getViewer } from "@/lib/control-plane/client";
import { canInviteMembers } from "@/lib/viewer-gates";

export default async function MembersPage() {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  const {
    data: { session },
  } = await supabase.auth.getSession();

  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }
  const canInvite = canInviteMembers(viewer);

  const members = session ? await getMembers(session.access_token) : [];

  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "1.5rem" }}>
        <h1>Members</h1>
        {canInvite ? (
          <form
            method="POST"
            action={`${process.env.CONTROL_PLANE_BASE_URL}/api/v1/accounts/current/invitations`}
            style={{ display: "flex", gap: "0.5rem" }}
          >
            <input
              type="email"
              name="email"
              placeholder="teammate@example.com"
              required
              style={{
                padding: "0.5rem 0.75rem",
                border: "1px solid #d1d5db",
                borderRadius: "0.375rem",
                fontSize: "0.875rem",
              }}
            />
            <button
              type="submit"
              style={{
                padding: "0.5rem 1rem",
                backgroundColor: "#111827",
                color: "#fff",
                border: "none",
                borderRadius: "0.375rem",
                fontSize: "0.875rem",
                cursor: "pointer",
              }}
            >
              Invite
            </button>
          </form>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", alignItems: "flex-end", gap: "0.25rem" }}>
            <button
              type="button"
              disabled
              style={{
                padding: "0.5rem 1rem",
                backgroundColor: "#e5e7eb",
                color: "#9ca3af",
                border: "none",
                borderRadius: "0.375rem",
                fontSize: "0.875rem",
                cursor: "not-allowed",
              }}
            >
              Invite
            </button>
            <span style={{ fontSize: "0.75rem", color: "#6b7280" }}>
              Email verification is required before you can invite teammates.
            </span>
          </div>
        )}
      </div>

      {members.length === 0 ? (
        <p style={{ color: "#6b7280" }}>No members found.</p>
      ) : (
        <table style={{ width: "100%", borderCollapse: "collapse" }}>
          <thead>
            <tr style={{ borderBottom: "2px solid #e5e7eb" }}>
              <th style={{ textAlign: "left", padding: "0.5rem", fontWeight: 600 }}>User ID</th>
              <th style={{ textAlign: "left", padding: "0.5rem", fontWeight: 600 }}>Role</th>
              <th style={{ textAlign: "left", padding: "0.5rem", fontWeight: 600 }}>Status</th>
            </tr>
          </thead>
          <tbody>
            {members.map((member) => (
              <tr key={member.user_id} style={{ borderBottom: "1px solid #f3f4f6" }}>
                <td style={{ padding: "0.5rem" }}>{member.user_id}</td>
                <td style={{ padding: "0.5rem" }}>{member.role}</td>
                <td style={{ padding: "0.5rem" }}>{member.status}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
