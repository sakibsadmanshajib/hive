import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../../components/ui/card";

export type UsageSummary = {
  windowDays: number;
  totalRequests: number;
  totalCredits: number;
  daily: Array<{
    date: string;
    requests: number;
    credits: number;
  }>;
  byModel: Array<{
    key: string;
    requests: number;
    credits: number;
  }>;
  byEndpoint: Array<{
    key: string;
    requests: number;
    credits: number;
  }>;
  byChannel?: Array<{
    key: string;
    requests: number;
    credits: number;
  }>;
  byApiKey?: Array<{
    key: string;
    requests: number;
    credits: number;
  }>;
};

export type UserSnapshot = {
  user: { user_id: string; email: string; name?: string };
  credits: { availableCredits: number; purchasedCredits: number; promoCredits: number };
  api_keys: Array<{
    id: string;
    key_id: string;
    nickname: string;
    status: "active" | "revoked" | "expired";
    revoked: boolean;
    scopes: string[];
    createdAt: string;
    expiresAt?: string;
    revokedAt?: string;
  }>;
  api_key_events?: Array<{
    id: string;
    apiKeyId: string;
    userId: string;
    eventType: "created" | "revoked" | "expired_observed";
    eventAt: string;
    metadata: Record<string, unknown>;
  }>;
};

type UsageCardsProps = {
  snapshot: UserSnapshot | null;
  usageSummary: UsageSummary | null;
  usageCount: number;
};

function formatSplitLabel(entry: { key: string; requests: number; credits: number } | undefined) {
  if (!entry) {
    return "No data yet";
  }
  return `${entry.key} · ${entry.requests} req · ${entry.credits} cr`;
}

export function UsageCards({ snapshot, usageSummary, usageCount }: UsageCardsProps) {
  const activeKeys = snapshot ? snapshot.api_keys.filter((key) => key.status === "active").length : 0;
  const topModel = usageSummary?.byModel[0];
  const topEndpoint = usageSummary?.byEndpoint[0];
  const topChannel = usageSummary?.byChannel?.[0];
  const topApiKey = usageSummary?.byApiKey?.[0];

  if (!snapshot) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Account snapshot</CardTitle>
          <CardDescription>Load account to view balances and usage.</CardDescription>
        </CardHeader>
      </Card>
    );
  }

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
      <Card>
        <CardHeader>
          <CardDescription>User</CardDescription>
          <CardTitle className="text-base">{snapshot.user.email}</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-xs text-muted-foreground">{snapshot.user.user_id}</p>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardDescription>Available credits</CardDescription>
          <CardTitle>{snapshot.credits.availableCredits}</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-xs text-muted-foreground">
            Purchased {snapshot.credits.purchasedCredits} / Promo {snapshot.credits.promoCredits}
          </p>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardDescription>Usage requests</CardDescription>
          <CardTitle>{usageSummary ? usageSummary.totalRequests : "—"}</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-xs text-muted-foreground">
            {usageSummary ? `${usageSummary.windowDays}-day window` : "Load usage to view summary"}
          </p>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardDescription>Credits spent</CardDescription>
          <CardTitle>{usageSummary ? usageSummary.totalCredits : "—"}</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-xs text-muted-foreground">
            {usageSummary ? `${usageCount} raw events loaded` : "Usage summary unavailable"}
          </p>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardDescription>Active API keys</CardDescription>
          <CardTitle>{activeKeys}</CardTitle>
        </CardHeader>
      </Card>
      <Card className="sm:col-span-2 xl:col-span-3">
        <CardHeader>
          <CardTitle>Usage breakdown</CardTitle>
          <CardDescription>Top model, endpoint, channel, API key, and daily trend for the current analytics window.</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 lg:grid-cols-5">
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Top model</p>
            <p className="text-sm font-medium">{formatSplitLabel(topModel)}</p>
          </div>
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Top endpoint</p>
            <p className="text-sm font-medium">{formatSplitLabel(topEndpoint)}</p>
          </div>
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Top channel</p>
            <p className="text-sm font-medium">{formatSplitLabel(topChannel)}</p>
          </div>
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Top API key</p>
            <p className="text-sm font-medium">{formatSplitLabel(topApiKey)}</p>
          </div>
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Daily credits</p>
            <div className="space-y-1">
              {usageSummary?.daily.length ? (
                usageSummary.daily.map((point) => (
                  <div key={point.date} className="flex items-center justify-between text-xs text-muted-foreground">
                    <span>{point.date}</span>
                    <span>{point.credits} cr / {point.requests} req</span>
                  </div>
                ))
              ) : (
                <p className="text-sm text-muted-foreground">Load usage to inspect the current trend.</p>
              )}
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
