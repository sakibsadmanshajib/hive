import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { Mail } from "lucide-react";

import {
  getAccountProfile,
  getMembers,
  getViewer,
  type AccountMember,
} from "@/lib/control-plane/client";
import { can } from "@/lib/viewer-gates";
import {
  MEMBER_ROLES,
  MEMBER_ROLE_HINTS,
  MEMBER_ROLE_LABELS,
  inviteDisabledReason,
  memberIdentityLabel,
  roleChangeDisabledReason,
} from "@/lib/members/roles";
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
import { Field, Input, Label } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";

type ToneName = "success" | "warning" | "danger" | "neutral" | "accent";

// The invite and role-change proxy routes are plain HTML form targets, so they
// report their outcome by redirecting back here with a flag. Rendering those
// flags is the only way a customer learns whether granting access worked
// (issue #535).
interface MembersPageProps {
  searchParams: Promise<{
    invited?: string;
    joined?: string;
    role_updated?: string;
    error?: string;
  }>;
}

const SELECT_CLASSNAME = [
  "h-9 w-full rounded-md border border-[var(--color-border)]",
  "bg-[var(--color-surface)] px-2 text-sm text-[var(--color-ink)]",
  "transition-[border,box-shadow] duration-[var(--duration-fast)] ease-[var(--ease-out-expo)]",
  "focus-visible:outline-none focus-visible:border-[var(--color-accent)]",
  "focus-visible:ring-4 focus-visible:ring-[var(--color-accent-soft)]",
  "disabled:cursor-not-allowed disabled:opacity-50",
].join(" ");

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

// successMessage maps the redirect flags onto confirmation copy. Only one banner
// shows at a time, and a failure always wins so a partial success cannot hide it.
function successMessage(params: {
  invited?: string;
  joined?: string;
  role_updated?: string;
}): string | null {
  if (params.invited === "1") {
    return "Invitation sent. They join this workspace once they accept.";
  }
  if (params.joined === "1") {
    return "You joined this workspace. Switch to it from the workspace switcher.";
  }
  if (params.role_updated === "1") {
    return "Role updated.";
  }
  return null;
}

export default async function MembersPage({ searchParams }: MembersPageProps) {
  const params = await searchParams;
  const failureMessage =
    typeof params.error === "string" && params.error.trim() !== ""
      ? params.error
      : null;
  const confirmation = failureMessage === null ? successMessage(params) : null;

  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  // Validate the JWT server-side (getUser round-trips to Supabase and
  // rejects revoked tokens); getSession alone only reads the cookie.
  const {
    data: { user },
  } = await supabase.auth.getUser();
  if (!user) {
    redirect("/auth/sign-in");
  }

  const {
    data: { session },
  } = await supabase.auth.getSession();

  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }
  const inviteBlockedReason = inviteDisabledReason(viewer);
  const canInvite = inviteBlockedReason === null;
  const canManageRoles = can(viewer, "members.manage");

  const [members, profile] = await Promise.all([
    session ? getMembers(session.access_token) : Promise.resolve([]),
    getAccountProfile().catch(
      (): { owner_name: string } => ({ owner_name: "" }),
    ),
  ]);

  const activeOwners = members.filter(
    (member) => member.role === "owner" && member.status === "active",
  ).length;

  // The role editor mirrors the control-plane's invariants (no self change, the
  // workspace keeps an owner) so a control is never offered for a change the
  // server would refuse. Authorization itself is still decided upstream on every
  // request; this is presentation, not enforcement (issue #536).
  function roleCell(row: AccountMember) {
    const blockedReason = roleChangeDisabledReason({
      canManage: canManageRoles,
      isSelf: row.user_id === viewer.user.id,
      isLastOwner: row.role === "owner" && activeOwners <= 1,
    });

    if (blockedReason !== null) {
      return (
        <div className="flex flex-col gap-1">
          <Badge tone={roleTone(row.role)}>{row.role}</Badge>
          <span className="text-2xs text-[var(--color-ink-3)]">
            {blockedReason}
          </span>
        </div>
      );
    }

    const selectId = `role-${row.user_id}`;
    return (
      <form
        method="POST"
        action="/api/console/members/role"
        className="flex items-center gap-2"
      >
        <input type="hidden" name="user_id" value={row.user_id} />
        <Label htmlFor={selectId} className="sr-only">
          {`Role for ${memberIdentityLabel(row)}`}
        </Label>
        <select
          id={selectId}
          name="role"
          defaultValue={row.role}
          className={`${SELECT_CLASSNAME} max-w-[9rem]`}
        >
          {MEMBER_ROLES.map((role) => (
            <option key={role} value={role}>
              {MEMBER_ROLE_LABELS[role]}
            </option>
          ))}
        </select>
        <Button type="submit" variant="secondary" size="sm">
          Save
        </Button>
      </form>
    );
  }

  const columns: Column<AccountMember>[] = [
    {
      key: "user",
      header: "Member",
      cell: (row) => (
        <span
          className="text-[var(--color-ink)]"
          title={`User ID ${row.user_id}`}
        >
          {memberIdentityLabel(row)}
        </span>
      ),
    },
    {
      key: "role",
      header: "Role",
      cell: roleCell,
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
        {failureMessage !== null ? (
          <p
            role="alert"
            className="rounded-lg border border-[var(--color-danger)] bg-[var(--color-surface)] px-4 py-3 text-sm text-[var(--color-danger)]"
          >
            {failureMessage}
          </p>
        ) : null}
        {confirmation !== null ? (
          <p
            role="status"
            className="rounded-lg border border-[var(--color-success)] bg-[var(--color-surface)] px-4 py-3 text-sm text-[var(--color-success)]"
          >
            {confirmation}
          </p>
        ) : null}

        <Card>
          <CardHeader>
            <CardTitle>Invite a teammate</CardTitle>
            <CardDescription>
              An email invite is sent with a sign-in link. They&rsquo;ll join
              this workspace once they accept, with the role you pick here.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-5 py-5">
            {canInvite ? (
              <form
                method="POST"
                action="/api/console/members"
                className="grid gap-3 sm:grid-cols-[1fr_auto_auto] sm:items-end"
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
                <Field
                  label="Role"
                  htmlFor="invite-role"
                  hint={MEMBER_ROLE_HINTS.member}
                >
                  <select
                    id="invite-role"
                    name="role"
                    defaultValue="member"
                    className={`${SELECT_CLASSNAME} sm:w-36`}
                  >
                    {MEMBER_ROLES.map((role) => (
                      <option key={role} value={role}>
                        {MEMBER_ROLE_LABELS[role]}
                      </option>
                    ))}
                  </select>
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
                  {inviteBlockedReason}
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
