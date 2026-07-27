"use client";

import { EmptyState } from "@/components/ui/empty-state";
import { Button } from "@/components/ui/button";

// Parent-segment error boundary. error.js never catches a throw in its own
// segment's layout.js, only a throw in a CHILD segment (Next.js App Router
// semantics), so apps/web-console/app/console/error.tsx cannot catch a throw
// inside apps/web-console/app/console/layout.tsx itself, e.g. its
// `await getViewer()` call. This root-segment boundary is the parent of
// every /console route and does catch that case.
export default function RootError({
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
            Try again, and if it keeps failing, contact support
            {error.digest ? ` and quote reference ${error.digest}` : ""}.
          </>
        }
        action={<Button onClick={reset}>Try again</Button>}
      />
    </div>
  );
}
