import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../../components/ui/card";

type UserSnapshot = {
  user: { user_id: string; email: string; name?: string };
  credits: { availableCredits: number; purchasedCredits: number; promoCredits: number };
  api_keys: Array<{ key_id: string; revoked: boolean; scopes: string[]; createdAt: string }>;
};

type UsageCardsProps = {
  snapshot: UserSnapshot | null;
  usageCount: number;
};

export function UsageCards({ snapshot, usageCount }: UsageCardsProps) {
  const activeKeys = snapshot ? snapshot.api_keys.filter((key) => !key.revoked).length : 0;

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
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
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
          <CardDescription>Usage events</CardDescription>
          <CardTitle>{usageCount}</CardTitle>
        </CardHeader>
      </Card>
      <Card>
        <CardHeader>
          <CardDescription>Active API keys</CardDescription>
          <CardTitle>{activeKeys}</CardTitle>
        </CardHeader>
      </Card>
    </div>
  );
}
