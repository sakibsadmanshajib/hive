import type { ReactNode } from "react";
import type { Metadata } from "next";
import { Geist, Geist_Mono, Noto_Sans_Bengali } from "next/font/google";
import { NextIntlClientProvider } from "next-intl";
import { getLocale } from "next-intl/server";

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

// Geist ships a latin subset only, so Bengali copy renders as tofu boxes
// without a family that carries the script. Noto Sans Bengali is appended to
// every font stack in globals.css rather than swapped in conditionally: CSS
// per-glyph fallback already picks it for Bengali codepoints and leaves Latin
// text on Geist, which keeps mixed strings ("API কী") correct in one pass.
const notoSansBengali = Noto_Sans_Bengali({
  variable: "--font-noto-bengali",
  subsets: ["bengali"],
  display: "swap",
});

// Fraunces (the former display serif) was dropped on 2026-07-26 along with
// the editorial type direction it carried; see the type-direction note in
// app/globals.css. Removing it also takes a variable webfont off the critical
// path — the console now ships one family plus its mono companion.

export const metadata: Metadata = {
  title: "Hive Console",
  description:
    "Hive inference API, prepaid credits, and observability for builders in Bangladesh.",
  icons: { icon: "/favicon.ico" },
};

interface RootLayoutProps {
  children: ReactNode;
}

export default async function RootLayout({ children }: RootLayoutProps) {
  const locale = await getLocale();

  return (
    <html
      lang={locale}
      className={`${geistSans.variable} ${geistMono.variable} ${notoSansBengali.variable}`}
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
        <NextIntlClientProvider>{children}</NextIntlClientProvider>
      </body>
    </html>
  );
}
