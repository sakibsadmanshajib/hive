"use client";

import { Button } from "../../components/ui/button";
import { cn } from "../../lib/utils";
import { createSupabaseBrowserClient } from "../../lib/supabase-client";

type GoogleLoginButtonProps = {
  className?: string;
};

export function GoogleLoginButton({ className }: GoogleLoginButtonProps) {
  async function handleGoogleLogin() {
    const supabase = createSupabaseBrowserClient();
    const { error } = await supabase.auth.signInWithOAuth({
      provider: "google",
      options: { redirectTo: `${window.location.origin}/auth/callback` },
    });
    if (error) {
      console.error("Google login failed:", error.message);
    }
  }

  return (
    <Button className={cn("w-full", className)} type="button" variant="outline" onClick={handleGoogleLogin}>
      Continue with Google
    </Button>
  );
}
