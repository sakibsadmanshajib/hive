import Link from "next/link";

import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";

export default function HomePage() {
  return (
    <section className="space-y-8">
      <Card className="border-0 bg-gradient-to-br from-sky-50 via-amber-50 to-teal-100 shadow-md">
        <CardHeader className="space-y-3">
          <Badge className="w-fit" variant="secondary">
            OpenAI-compatible gateway
          </Badge>
          <CardTitle className="text-3xl leading-tight md:text-4xl">Run chat and billing flows from one place</CardTitle>
          <CardDescription className="max-w-2xl text-base text-slate-700">
            This workspace pairs provider routing with prepaid credits so you can validate user auth, usage, and top-up behavior from a single UI.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-3">
          <Button asChild>
            <Link href="/chat">Open chat workspace</Link>
          </Button>
          <Button asChild variant="outline">
            <Link href="/billing">Open billing dashboard</Link>
          </Button>
        </CardContent>
      </Card>

      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-xl">Chat workspace</CardTitle>
            <CardDescription>Test model routing, message exchange, and account auth in one flow.</CardDescription>
          </CardHeader>
          <CardContent>
            <Button asChild variant="secondary">
              <Link href="/chat">Go to Chat</Link>
            </Button>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-xl">Billing and usage</CardTitle>
            <CardDescription>Load account snapshots, top up demo credits, and inspect usage events.</CardDescription>
          </CardHeader>
          <CardContent>
            <Button asChild variant="secondary">
              <Link href="/billing">Go to Billing</Link>
            </Button>
          </CardContent>
        </Card>
      </div>
    </section>
  );
}
