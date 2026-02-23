import { Button } from "../../../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../../components/ui/card";
import { Input } from "../../../components/ui/input";

type TopUpPanelProps = {
  latestIntent: string;
  loading: boolean;
  onTopUp: () => Promise<void>;
  setTopUpAmount: (value: number) => void;
  topUpAmount: number;
};

export function TopUpPanel({ latestIntent, loading, onTopUp, setTopUpAmount, topUpAmount }: TopUpPanelProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Prepaid top-up</CardTitle>
        <CardDescription>Create a demo payment intent and confirm it to mint credits.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <label className="grid gap-2" htmlFor="top-up-amount">
          <span className="text-sm font-medium">Top-up amount (BDT)</span>
          <Input
            id="top-up-amount"
            min={1}
            onChange={(event) => setTopUpAmount(Number(event.target.value) || 1)}
            type="number"
            value={topUpAmount}
          />
        </label>
        <div className="flex flex-wrap items-center gap-3">
          <Button disabled={loading} onClick={() => void onTopUp()} type="button">
            Top up now
          </Button>
          <p className="text-sm text-muted-foreground">
            Latest intent: <strong>{latestIntent || "N/A"}</strong>
          </p>
        </div>
      </CardContent>
    </Card>
  );
}
