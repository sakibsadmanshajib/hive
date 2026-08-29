import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import {
  ControlPlaneError,
  getAccountProfile,
  getMembers,
  getViewer,
  type AccountMember,
  type PendingInvitation,
} from "@/lib/control-plane/client";
import {
  invitationOutcome,
  parseDeliveryFlag,
} from "@/lib/members/invite-outcome";
import {
  InviteTeammateForm,
  ResendInvitationButton,
} from "@/components/members/invite-panel";
import { can } from "@/lib/viewer-gates";
import {
  MEMBER_ROLES,
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
import { Label } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";

type ToneName = "success" | "warning" | "danger" | "neutral" | "accent";

// The invite and role-change proxy routes are plain HTML form targets, so they
// report their outcome by redirecting back here with a flag. Rendering those
// flags is the only way a customer learns whether granting access worked
// (issue #535).
interface MembersPageProps {
  searchParams: Promise<{
    // The delivery outcome of an invitation issued through the no-JavaScript
    // form post: "sent", "not_configured" or "failed". It is never a bare
    // success flag any more, because the flag it replaced ("1") was rendered as
    // "Invitation sent" while nothing in the product could send anything
    // (issue #1440).
    invited?: string;
    joined?: string;
    role_updated?: string;
    revoked?: string;
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

// outcomeBanner maps the redirect flags onto confirmation copy. Only one banner
// shows at a time, and a failure always wins so a partial success cannot hide it.
//
// The invitation branch delegates to invitationOutcome, so this page has no
// wording of its own for a delivery and cannot claim one. It arrives here only
// on the no-JavaScript path; with JavaScript the invite panel renders the same
// outcome in place, along with the link.
function outcomeBanner(params: {
  invited?: string;
  joined?: string;
  role_updated?: string;
  revoked?: string;
}): { tone: "success" | "warning"; message: string } | null {
  const delivery = parseDeliveryFlag(params.invited);
  if (delivery !== null) {
    const outcome = invitationOutcome(delivery, null);
    return {
      tone: outcome.tone,
      // The acceptance token cannot ride a redirect, so this path can only say
      // where to get a link, not show one.
      message:
        outcome.action === null
          ? outcome.message
          : `${outcome.message} Use New link on the invitation below to get a link you can pass on.`,
    };
  }
  if (params.joined === "1") {
    return {
      tone: "success",
      message:
        "You joined this workspace. Switch to it from the workspace switcher.",
    };
  }
  if (params.role_updated === "1") {
    return { tone: "success", message: "Role updated." };
  }
  if (params.revoked === "1") {
    return {
      tone: "success",
      message: "Invitation withdrawn. Its link no longer works.",
    };
  }
  return null;
}

// A roster row is either somebody who is already in the workspace or somebody
// who has been invited into it. They share a table because they answer one
// question, and because an invited address appearing nowhere at all is half of
// why a broken invitation went unnoticed (issue #1440).
type RosterRow =
  | ({ kind: "member" } & AccountMember)
  | ({ kind: "invitation" } & PendingInvitation);

function invitationStatusTone(status: string): { label: string; tone: ToneName } {
  return status === "expired"
    ? { label: "Expired", tone: "danger" }
    : { label: "Invited", tone: "warning" };
}

function formatExpiry(raw: string): string | null {
  if (!raw) return null;
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return null;
  return parsed.toISOString().slice(0, 10);
}

export default async function MembersPage({ searchParams }: MembersPageProps) {
  const params = await searchParams;
  const failureMessage =
    typeof params.error === "string" && params.error.trim() !== ""
      ? params.error
      : null;
  const confirmation = failureMessage === null ? outcomeBanner(params) : null;

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

  // The control-plane restricts the member list to owners. A plain member is a
  // legitimate visitor here (they can see who they work with is a workspace
  // question, and the page still explains their own standing), so a refusal
  // becomes an explanation rather than an error boundary. Anything that is not a
  // refusal still throws, so a real outage stays visible.
  async function loadMembers(): Promise<{
    members: AccountMember[];
    invitations: PendingInvitation[];
    restricted: boolean;
  }> {
    if (!session) return { members: [], invitations: [], restricted: false };
    try {
      const roster = await getMembers(session.access_token);
      return { ...roster, restricted: false };
    } catch (err: unknown) {
      if (err instanceof ControlPlaneError && err.status === 403) {
        return { members: [], invitations: [], restricted: true };
      }
      throw err;
    }
  }

  const [memberList, profile] = await Promise.all([
    loadMembers(),
    getAccountProfile().catch(
      (): { owner_name: string } => ({ owner_name: "" }),
    ),
  ]);
  const members = memberList.members;
  const invitations = memberList.invitations;

  // Invitations first: they are the rows that need an action taken on them.
  const rosterRows: RosterRow[] = [
    ...invitations.map((invitation): RosterRow => ({
      kind: "invitation",
      ...invitation,
    })),
    ...members.map((member): RosterRow => ({ kind: "member", ...member })),
  ];

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

  const columns: Column<RosterRow>[] = [
    {
      key: "user",
      header: "Member",
      cell: (row) =>
        row.kind === "member" ? (
          <span
            className="text-[var(--color-ink)]"
            title={`User ID ${row.user_id}`}
          >
            {memberIdentityLabel(row)}
          </span>
        ) : (
          <span className="text-[var(--color-ink)]">{row.email}</span>
        ),
    },
    {
      key: "role",
      header: "Role",
      cell: (row) =>
        row.kind === "member" ? (
          roleCell(row)
        ) : (
          <Badge tone={roleTone(row.role)}>{row.role}</Badge>
        ),
    },
    {
      key: "status",
      header: "Status",
      cell: (row) => {
        if (row.kind === "member") {
          const { label, tone } = statusTone(row.status);
          return <Badge tone={tone}>{label}</Badge>;
        }
        const { label, tone } = invitationStatusTone(row.status);
        const expiry = formatExpiry(row.expires_at);
        return (
          <div className="flex flex-col gap-1">
            <Badge tone={tone}>{label}</Badge>
            {expiry !== null ? (
              <span className="text-2xs text-[var(--color-ink-3)]">
                {row.status === "expired"
                  ? `Expired ${expiry}`
                  : `Expires ${expiry}`}
              </span>
            ) : null}
          </div>
        );
      },
    },
    {
      key: "actions",
      header: "Actions",
      cell: (row) => {
        // Members are managed through the Role column. Only an outstanding
        // invitation has anything to act on here.
        if (row.kind === "member") return null;
        // The card above already explains why the controls are unavailable.
        // Repeating it on every row would say the same sentence four times.
        if (!canInvite) return null;
        return (
          <div className="flex flex-col gap-2 sm:flex-row sm:items-start">
            <ResendInvitationButton email={row.email} role={row.role} />
            <form method="POST" action="/api/console/members/invitations/revoke">
              <input type="hidden" name="id" value={row.id} />
              <Button type="submit" variant="secondary" size="sm">
                Withdraw
              </Button>
            </form>
          </div>
        );
      },
    },
  ];

  return (
    <ConsoleShell
      workspace={{
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      viewer={viewer}
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
            className={
              confirmation.tone === "success"
                ? "rounded-lg border border-[var(--color-success)] bg-[var(--color-surface)] px-4 py-3 text-sm text-[var(--color-success)]"
                : "rounded-lg border border-[var(--color-warning)] bg-[var(--color-surface)] px-4 py-3 text-sm text-[var(--color-warning)]"
            }
          >
            {confirmation.message}
          </p>
        ) : null}

        <Card>
          <CardHeader>
            <CardTitle>Invite a teammate</CardTitle>
            <CardDescription>
              Creating an invitation produces a private link that joins this
              workspace with the role you pick. Where mail delivery is
              configured we email that link as well, and either way the link is
              shown to you here so you can pass it on yourself.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-5 py-5">
            {canInvite ? (
              <InviteTeammateForm />
            ) : (
              <div className="flex flex-col gap-2">
                <Button
                  type="button"
                  variant="secondary"
                  size="md"
                  disabled
                  className="self-start"
                >
                  Create invitation
                </Button>
                <p className="text-xs text-[var(--color-ink-3)]">
                  {inviteBlockedReason}
                </p>
              </div>
            )}
          </CardContent>
        </Card>

        {memberList.restricted ? (
          <EmptyState
            title="Member list is owner-only"
            description="Only workspace owners can see who else belongs to this workspace. Ask an owner if you need the list."
          />
        ) : rosterRows.length === 0 ? (
          <EmptyState
            title="No members yet"
            description="Invitations you create appear here straight away, and become members once they are accepted."
          />
        ) : (
          <DataTable<RosterRow>
            rows={rosterRows}
            columns={columns}
            rowKey={(row) =>
              row.kind === "member" ? `member-${row.user_id}` : `invitation-${row.id}`
            }
          />
        )}
      </div>
    </ConsoleShell>
  );
}
