import type { ReactNode } from "react";
import type { Metadata } from "next";
import { headers } from "next/headers";
import { Geist, Geist_Mono, Noto_Sans_Bengali } from "next/font/google";

import { HIVE_EMBED_HEADER, HIVE_THEME_HEADER, readEmbedTheme } from "@/lib/embed";

import "./globals.css";

// Same two families, same CSS variable names, as apps/web-console -- the two
// apps are one product and share a type system (see app/globals.css).
const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
  display: "swap",
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
  display: "swap",
});

// Backs the Bengali fallback the shared font tokens declare. Loaded here (not
// only in web-console) because those tokens are asserted identical across the
// two apps, and an undefined --font-noto-bengali would invalidate every
// font-family declaration that references it.
const notoSansBengali = Noto_Sans_Bengali({
  variable: "--font-noto-bengali",
  subsets: ["bengali"],
  display: "swap",
});

export const metadata: Metadata = {
  title: "Hive Agent Workspace",
  description: "Start and monitor coding-pack and knowledge-work-pack agent tasks.",
};

interface RootLayoutProps {
  children: ReactNode;
}

export default async function RootLayout({ children }: RootLayoutProps) {
  /*
   * Set on <html> from the request, so an embedded panel is in the shell's
   * theme on its very first paint rather than after a flash. Middleware puts
   * these on the request and preserves them across the sign-in redirect, which
   * is why the sign-in screen matches too.
   */
  const requestHeaders = await headers();
  const embedded = requestHeaders.get(HIVE_EMBED_HEADER) === "1";
  const theme = readEmbedTheme(requestHeaders.get(HIVE_THEME_HEADER));

  return (
    <html
      lang="en"
      data-hv-embedded={embedded ? "1" : undefined}
      data-hv-theme={theme}
      className={`${geistSans.variable} ${geistMono.variable} ${notoSansBengali.variable}`}
    >
      <body className="min-h-screen bg-canvas text-ink antialiased">
        {children}
      </body>
    </html>
  );
}
