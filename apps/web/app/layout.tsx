import type { Metadata } from "next";
import "./globals.css";
export const metadata: Metadata = {
  title: "Hookgarde · Delivery console",
  description: "Publish, inspect and recover your webhook deliveries.",
};
export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
