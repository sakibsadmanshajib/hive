"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { readAuthSession } from "../../features/auth/auth-session";

export default function DeveloperPanelPage() {
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
    <section className="mx-auto max-w-4xl">
      <Card className="border-slate-800 bg-slate-900 text-slate-100">
        <CardHeader>
          <CardTitle>Developer Panel</CardTitle>
          <CardDescription className="text-slate-400">Internal diagnostics and advanced controls are available in this workspace.</CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-slate-300">Use this panel for power-user and integration debugging workflows.</CardContent>
      </Card>
    </section>
  );
}
