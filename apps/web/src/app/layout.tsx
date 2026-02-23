import type { CSSProperties, ReactNode } from "react";

export const metadata = {
  title: "BD AI Gateway Web",
  description: "MVP web placeholders for chat and billing",
};

const shellStyle: CSSProperties = {
  maxWidth: "960px",
  margin: "0 auto",
  padding: "24px 16px",
  fontFamily: "ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body style={{ margin: 0, background: "#f8fafc", color: "#0f172a" }}>
        <main style={shellStyle}>{children}</main>
      </body>
    </html>
  );
}
