import type { ReactNode } from "react";

import "./globals.css";
import { AppShell } from "../components/layout/app-shell";
import { ThemeProvider } from "../components/theme/theme-provider";
import { Toaster } from "../components/ui/toaster";

export const metadata = {
  title: "BD AI Gateway Web",
  description: "MVP web placeholders for chat and billing",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body>
        <ThemeProvider>
          <AppShell>{children}</AppShell>
          <Toaster />
        </ThemeProvider>
      </body>
    </html>
  );
}
