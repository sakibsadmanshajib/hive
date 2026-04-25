import type { ReactNode } from "react";
import type { Metadata } from "next";
import { Geist, Geist_Mono, Fraunces } from "next/font/google";

import "./globals.css";

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

const fraunces = Fraunces({
  variable: "--font-fraunces",
  subsets: ["latin"],
  display: "swap",
  // `opsz` is the default axis Next bakes in when building Fraunces;
  // listing it explicitly here emitted a `Duplicate key "axisIndex"`
  // esbuild warning during the OpenNext production build, after which
  // the prerendered RSC payload dropped the root <html> className
  // entirely. Keep only the non-default `SOFT` axis.
  axes: ["SOFT"],
});

export const metadata: Metadata = {
  title: "Hive Console",
  description:
    "OpenAI-compatible inference, prepaid credits, and observability for builders in Bangladesh.",
  icons: { icon: "/favicon.ico" },
};

interface RootLayoutProps {
  children: ReactNode;
}

export default function RootLayout({ children }: RootLayoutProps) {
  return (
    <html
      lang="en"
      className={`${geistSans.variable} ${geistMono.variable} ${fraunces.variable}`}
    >
      {/*
        Browser extensions (Grammarly, etc.) mutate <body> attributes
        before React hydrates, which produces a hydration mismatch
        warning on every page load. Suppressing the warning on this
        single boundary node is the React-recommended workaround and
        does not silence mismatches inside the tree.
      */}
      <body
        className="min-h-screen bg-canvas text-ink antialiased"
        suppressHydrationWarning
      >
        {children}
      </body>
    </html>
  );
}
