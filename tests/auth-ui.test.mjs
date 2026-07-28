import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

test("live console gates the shell behind the Master session endpoint", async () => {
  const [clientSource, pageSource] = await Promise.all([
    readFile("app/MonitoredApp.tsx", "utf8"),
    readFile("app/page.tsx", "utf8"),
  ]);

  assert.match(clientSource, /apiRequest\("\/auth\/me"\)/);
  assert.match(clientSource, /response\.status === 401/);
  assert.match(clientSource, /window\.location\.replace\(`\/login\?next=/);
  assert.match(clientSource, /configuredMode === "live"/);
  assert.match(pageSource, /MONITORED_WEB_REQUIRE_AUTH/);
  assert.match(pageSource, /\/api\/v1\/auth\/me/);
  assert.match(pageSource, /redirect\("\/login\?next=%2F"\)/);
});

test("login route posts credentials and supports optional TOTP", async () => {
  const source = await readFile("app/login/LoginForm.tsx", "utf8");

  assert.match(source, /fetch\(`\$\{apiBase\}\/auth\/login`/);
  assert.match(source, /credentials: "include"/);
  assert.match(source, /totpCode/);
  assert.match(source, /code === "totp_required"/);
  assert.match(source, /safeReturnPath/);
});

test("topbar notification bell opens a navigable notification center", async () => {
  const source = await readFile("app/MonitoredApp.tsx", "utf8");

  assert.match(source, /onClick=\{onNotifications\}/);
  assert.match(source, /type: "notifications"/);
  assert.match(source, /function NotificationCenter/);
  assert.match(source, /apiRequest\("\/alerts\?active=true"\)/);
  assert.match(source, /onNavigate\("actions"\)/);
});

test("live Agent settings expose cached binary and Docker update rollout", async () => {
  const source = await readFile("app/MonitoredApp.tsx", "utf8");

  assert.match(source, /apiRequest\("\/agent-update-packages"/);
  assert.match(source, /\/agents\/\$\{encodeURIComponent\(target\)\}\/updates/);
  assert.match(source, /Server 二进制包/);
  assert.match(source, /Docker Pull/);
});
