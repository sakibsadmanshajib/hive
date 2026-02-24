"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { readAuthSession } from "../../features/auth/auth-session";

export default function SettingsPage() {
  const router = useRouter();
  const authSession = readAuthSession();

  useEffect(() => {
    if (!authSession?.apiKey) {
      router.push("/auth");
    }
  }, [authSession?.apiKey, router]);

  if (!authSession?.apiKey) {
    return null;
  }

  return (
    <section className="mx-auto max-w-3xl">
      <Card className="border-slate-800 bg-slate-900 text-slate-100">
        <CardHeader>
          <CardTitle>Settings</CardTitle>
          <CardDescription className="text-slate-400">Account preferences and app controls will live here.</CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-slate-300">Use Billing for credit and usage management.</CardContent>
      </Card>
    </section>
  );
}
