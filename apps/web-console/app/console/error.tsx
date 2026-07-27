"use client";

import { EmptyState } from "@/components/ui/empty-state";
import { Button } from "@/components/ui/button";

// Error boundary for every /console route. Without it, any thrown error in a
// Server Component rendered the bare Next.js digest page with no way back,
// which is how a 404 from one profile endpoint became an unrecoverable 500 on
// the billing settings page. The digest is the only server-side detail Next.js
// exposes to the browser in production, and it is safe to show: the message
// itself stays on the server.
export default function ConsoleError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <div className="p-6">
      <EmptyState
        title="Something went wrong on this page"
        description={
          <>
            The rest of the console is unaffected. Try again, and if it keeps
            failing, contact support
            {error.digest ? ` and quote reference ${error.digest}` : ""}.
          </>
        }
        action={<Button onClick={reset}>Try again</Button>}
      />
    </div>
  );
}
