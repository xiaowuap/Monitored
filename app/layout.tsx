import type { Metadata } from "next";
import { headers } from "next/headers";
import "./globals.css";

const title = "Monitored · 设备遥测优先的智能可观测平台";
const description =
  "跨主机、存储、GPU、网络、BMC、UPS 与 SNMP 设备的发现、遥测、告警和安全诊断控制台。";

export async function generateMetadata(): Promise<Metadata> {
  const requestHeaders = await headers();
  const forwardedHost = requestHeaders
    .get("x-forwarded-host")
    ?.split(",")[0]
    .trim();
  const candidateHost = forwardedHost ?? requestHeaders.get("host") ?? "localhost:3000";
  const host = /^[a-z0-9.-]+(?::\d+)?$/i.test(candidateHost)
    ? candidateHost
    : "localhost:3000";
  const forwardedProtocol = requestHeaders
    .get("x-forwarded-proto")
    ?.split(",")[0]
    .trim();
  const protocol =
    forwardedProtocol === "http" || forwardedProtocol === "https"
      ? forwardedProtocol
      : host.startsWith("localhost")
        ? "http"
        : "https";
  const base = new URL(`${protocol}://${host}`);
  const socialImage = new URL("/og.png", base).toString();

  return {
    metadataBase: base,
    title,
    description,
    applicationName: "Monitored",
    keywords: [
      "device telemetry",
      "hardware monitoring",
      "observability",
      "TrueNAS",
      "GPU",
      "RDMA",
    ],
    openGraph: {
      type: "website",
      title,
      description,
      siteName: "Monitored",
      images: [{ url: socialImage, width: 1731, height: 909, alt: title }],
    },
    twitter: {
      card: "summary_large_image",
      title,
      description,
      images: [socialImage],
    },
  };
}

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <body>{children}</body>
    </html>
  );
}
