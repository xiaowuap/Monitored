import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  turbopack: {
    root: process.cwd(),
  },
  async rewrites() {
    const apiOrigin =
      process.env.MONITORED_API_INTERNAL_URL?.replace(/\/$/, "") ??
      "http://server:8080";
    return [
      {
        source: "/api/v1/:path*",
        destination: `${apiOrigin}/api/v1/:path*`,
      },
      {
        source: "/healthz",
        destination: `${apiOrigin}/healthz`,
      },
    ];
  },
};

export default nextConfig;
