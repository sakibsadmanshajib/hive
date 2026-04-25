import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { Mail } from "lucide-react";

import {
  getAccountProfile,
  getMembers,
  getViewer,
  type AccountMember,
} from "@/lib/control-plane/client";
import { canInviteMembers } from "@/lib/viewer-gates";
import { createClient } from "@/lib/supabase/server";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";

type ToneName = "success" | "warning" | "danger" | "neutral" | "accent";

function roleTone(role: string): ToneName {
  const lowered = role.toLowerCase();
  if (lowered === "owner") return "accent";
  if (lowered === "admin") return "warning";
  return "neutral";
}

function statusTone(status: string): { label: string; tone: ToneName } {
  switch (status.toLowerCase()) {
    case "active":
      return { label: "Active", tone: "success" };
    case "pending":
      return { label: "Pending", tone: "warning" };
    case "revoked":
      return { label: "Revoked", tone: "danger" };
    default:
      return { label: status, tone: "neutral" };
  }
}

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

  const [members, profile] = await Promise.all([
    session ? getMembers(session.access_token) : Promise.resolve([]),
    getAccountProfile().catch(
      (): { owner_name: string } => ({ owner_name: "" }),
    ),
  ]);

  const columns: Column<AccountMember>[] = [
    {
      key: "user",
      header: "Member",
      cell: (row) => (
        <code className="font-mono text-xs text-[var(--color-ink-2)]">
          {row.user_id}
        </code>
      ),
    },
    {
      key: "role",
      header: "Role",
      cell: (row) => <Badge tone={roleTone(row.role)}>{row.role}</Badge>,
    },
    {
      key: "status",
      header: "Status",
      cell: (row) => {
        const { label, tone } = statusTone(row.status);
        return <Badge tone={tone}>{label}</Badge>;
      },
    },
  ];

  return (
    <ConsoleShell
      workspace={{
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console/members"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">Members</span>
      }
    >
      <PageHeader
        eyebrow="Workspace"
        title="Members"
        description="Invite teammates to share API keys, billing visibility, and analytics. Roles control what each member can change."
      />

      <div className="flex flex-col gap-6">
        <Card>
          <CardHeader>
            <CardTitle>Invite a teammate</CardTitle>
            <CardDescription>
              An email invite is sent with a sign-in link. They&rsquo;ll join
              this workspace once they accept.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-5 py-5">
            {canInvite ? (
              <form
                method="POST"
                action={`${process.env.CONTROL_PLANE_BASE_URL}/api/v1/accounts/current/invitations`}
                className="grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end"
              >
                <Field label="Email" htmlFor="invite-email" required>
                  <Input
                    id="invite-email"
                    type="email"
                    name="email"
                    placeholder="teammate@example.com"
                    required
                  />
                </Field>
                <Button type="submit" variant="primary" size="md">
                  <Mail size={14} aria-hidden="true" />
                  Send invite
                </Button>
              </form>
            ) : (
              <div className="flex flex-col gap-2">
                <Button
                  type="button"
                  variant="secondary"
                  size="md"
                  disabled
                  className="self-start"
                >
                  Send invite
                </Button>
                <p className="text-xs text-[var(--color-ink-3)]">
                  Email verification is required before you can invite
                  teammates.
                </p>
              </div>
            )}
          </CardContent>
        </Card>

        {members.length === 0 ? (
          <EmptyState
            title="No members yet"
            description="Once teammates accept their invites they&rsquo;ll appear here with their role and status."
          />
        ) : (
          <DataTable<AccountMember>
            rows={members}
            columns={columns}
            rowKey={(row) => row.user_id}
          />
        )}
      </div>
    </ConsoleShell>
  );
}
