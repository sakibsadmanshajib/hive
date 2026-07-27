import Link from "next/link";
import { Building2 } from "lucide-react";

import { EmptyState } from "@/components/ui/empty-state";
import { Button, buttonVariants } from "@/components/ui/button";

interface NoWorkspaceStateProps {
  email: string;
}

// Rendered instead of the console when the signed-in user holds no tenant
// membership. That is a legitimate state now: the Supabase access-token hook
// issues a valid token with no tenant_id claim rather than failing sign-in, so
// the user reaches the console authenticated but with nothing to show them.
//
// The signed-in email is surfaced deliberately. The most common cause is
// signing up with a personal address instead of the organization one, and
// seeing which address they used is the fastest way for someone to spot that
// themselves. Sign-out reuses the existing POST /auth/sign-out route (the same
// mechanism as the console shell's sign-out button); that route is POST only so
// the auth cookies cannot be cleared by a cross-site navigation.
export function NoWorkspaceState({ email }: NoWorkspaceStateProps) {
  return (
    <div className="grid min-h-screen place-items-center px-6 py-12">
      <EmptyState
        className="w-full max-w-md"
        icon={<Building2 size={20} />}
        title="No workspace yet"
        description={
          <>
            You are signed in as{" "}
            <span className="font-medium text-[var(--color-ink-2)]">
              {email}
            </span>
            , but this account is not a member of a workspace yet. Ask an
            administrator to invite you. If your team uses a different address,
            sign in again with your organization email.
          </>
        }
        action={
          <div className="flex items-center justify-center gap-2">
            {/*
              Check again is the explicit retry, and the only thing that
              re-runs provisioning from this screen. It is a plain link to the
              provisioning handler rather than an automatic poll, so an
              administrator adding the invitation is followed by one deliberate
              attempt instead of a request on every render.
            */}
            <Link
              href="/console/provision"
              prefetch={false}
              className={buttonVariants({ variant: "primary", size: "md" })}
            >
              Check again
            </Link>
            <form action="/auth/sign-out" method="post">
              <Button type="submit" variant="secondary" size="md">
                Sign out
              </Button>
            </form>
          </div>
        }
      />
    </div>
  );
}
