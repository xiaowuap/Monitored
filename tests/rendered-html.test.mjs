import assert from "node:assert/strict";
import test from "node:test";

test("production bundle contains Monitored product metadata", async () => {
  const worker = await import("../dist/server/index.js");
  const response = await worker.default.fetch(
    new Request("https://monitored.test/"),
    {},
    {},
  );
  const html = await response.text();

  assert.equal(response.status, 200);
  assert.match(html, /Monitored/);
  assert.match(html, /设备遥测优先/);
  assert.doesNotMatch(html, /Your site is taking shape/);
});
