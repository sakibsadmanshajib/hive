"use client";

import { useMemo } from "react";
import { useRouter } from "next/navigation";

import { Avatar, AvatarFallback } from "../../../components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../../../components/ui/dropdown-menu";
import { clearAuthSession, useAuthSessionState } from "../../auth/auth-session";
import { createSupabaseBrowserClient, useSupabaseAuthSessionSync } from "../../../lib/supabase-client";

export function ProfileMenu() {
  const router = useRouter();
  useSupabaseAuthSessionSync();
  const { ready, session } = useAuthSessionState();

  const initials = useMemo(() => {
    const source = session?.name?.trim() || session?.email || "User";
    return source
      .split(/\s+/)
      .slice(0, 2)
      .map((part) => part[0]?.toUpperCase() ?? "")
      .join("");
  }, [session?.email, session?.name]);

  if (!ready || !session) {
    return null;
  }

  function openRoute(path: string) {
    router.push(path);
  }

  async function handleLogout() {
    const supabase = createSupabaseBrowserClient();
    try {
      await supabase.auth.signOut();
    } catch {
      // Local cleanup should still complete if remote sign-out fails.
    } finally {
      clearAuthSession();
      router.push("/auth");
    }
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger aria-label="Open profile menu" className="rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <Avatar className="h-9 w-9 border border-border/80 bg-muted/30">
          <AvatarFallback className="bg-slate-800 text-xs font-semibold text-slate-200">{initials || "U"}</AvatarFallback>
        </Avatar>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56 border-slate-700 bg-slate-900 text-slate-100">
        <DropdownMenuLabel className="space-y-1">
          <p className="text-xs font-medium uppercase tracking-wide text-slate-400">Signed in</p>
          <p className="truncate text-sm font-medium text-slate-200">{session?.email ?? "Unknown user"}</p>
        </DropdownMenuLabel>
        <DropdownMenuSeparator className="bg-slate-700" />
        <DropdownMenuItem className="focus:bg-slate-800" onSelect={() => openRoute("/settings")}>
          Settings
        </DropdownMenuItem>
        <DropdownMenuItem className="focus:bg-slate-800" onSelect={() => openRoute("/developer")}>
          Developer Panel
        </DropdownMenuItem>
        <DropdownMenuItem className="focus:bg-slate-800" onSelect={() => openRoute("/billing")}>
          Billing
        </DropdownMenuItem>
        <DropdownMenuSeparator className="bg-slate-700" />
        <DropdownMenuItem className="text-rose-300 focus:bg-slate-800 focus:text-rose-200" onSelect={() => void handleLogout()}>
          Log out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
