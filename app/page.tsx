import MonitoredApp from "./MonitoredApp";
import { headers } from "next/headers";
import { redirect } from "next/navigation";

export const dynamic = "force-dynamic";

export default async function Home() {
  if (process.env.MONITORED_WEB_REQUIRE_AUTH === "true") {
    const requestHeaders = await headers();
    const cookie = requestHeaders.get("cookie") ?? "";
    const apiOrigin =
      process.env.MONITORED_API_INTERNAL_URL?.replace(/\/$/, "") ??
      "http://server:8080";
    let authStatus = 0;
    try {
      const response = await fetch(`${apiOrigin}/api/v1/auth/me`, {
        headers: { cookie },
        cache: "no-store",
      });
      authStatus = response.status;
    } catch {
      // The client AuthenticatedShell renders the recoverable Master error.
    }
    if (authStatus === 401) {
      redirect("/login?next=%2F");
    }
  }
  return <MonitoredApp />;
}
