"use client";

import { AuthExperience } from "../../features/auth/components/auth-experience";
import { useSupabaseAuthSessionSync } from "../../lib/supabase-client";

export default function AuthPage() {
  useSupabaseAuthSessionSync();
  return <AuthExperience variant="page" />;
}
