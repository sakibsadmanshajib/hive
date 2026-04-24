import type { ReactNode } from "react";

// All dynamic routes run on the Cloudflare Pages Edge runtime.
// Inherited by nested segments (console, auth, invitations, etc.).
export const runtime = "edge";

interface RootLayoutProps {
  children: ReactNode;
}

export default function RootLayout({ children }: RootLayoutProps) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
