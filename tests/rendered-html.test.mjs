import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import net from "node:net";
import test from "node:test";

async function availablePort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert.ok(address && typeof address === "object");
  const port = address.port;
  await new Promise((resolve) => server.close(resolve));
  return port;
}

test("standalone production bundle contains Monitored product metadata", async (context) => {
  const port = await availablePort();
  const child = spawn(process.execPath, [".next/standalone/server.js"], {
    cwd: process.cwd(),
    env: {
      ...process.env,
      HOSTNAME: "127.0.0.1",
      PORT: String(port),
      NEXT_TELEMETRY_DISABLED: "1",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  context.after(() => {
    child.kill("SIGTERM");
  });
  let response;
  for (let attempt = 0; attempt < 50; attempt += 1) {
    try {
      response = await fetch(`http://127.0.0.1:${port}/`);
      break;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
  }
  assert.ok(response, "standalone Next.js server did not become ready");
  const html = await response.text();

  assert.equal(response.status, 200);
  assert.match(html, /Monitored/);
  if (process.env.MONITORED_WEB_REQUIRE_AUTH === "true") {
    assert.match(response.url, /\/login\?next=%2F$/);
    assert.match(html, /登录控制台/);
  } else {
    assert.match(html, /设备遥测优先/);
    assert.match(html, /DEMO MODE/);
  }
  assert.doesNotMatch(html, /Your site is taking shape/);
});
