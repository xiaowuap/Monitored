import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import Ajv2020 from "ajv/dist/2020.js";
import { parse } from "yaml";

test("OpenAPI contract exposes the implemented control-plane surface", async () => {
  const source = await readFile("openapi/monitored.v1.yaml", "utf8");
  const document = parse(source);

  assert.equal(document.openapi, "3.1.0");
  for (const route of [
    "/healthz",
    "/api/v1/overview",
    "/api/v1/assets",
    "/api/v1/sensors",
    "/api/v1/discovery/runs",
    "/api/v1/discovery/scans",
    "/api/v1/llm/suggestions/generate",
    "/api/v1/llm/suggestion-jobs",
    "/api/v1/llm/suggestions/{id}/decision",
    "/api/v1/diagnoses",
    "/api/v1/actions/{id}/decision",
    "/api/v1/auth/totp/enroll",
    "/api/v1/auth/totp/confirm",
    "/api/v1/audit",
    "/api/v1/ingest",
  ]) {
    assert.ok(document.paths[route], `missing OpenAPI path ${route}`);
  }
});

test("bundled SNMP profile satisfies the no-code extension schema", async () => {
  const [schemaSource, profileSource] = await Promise.all([
    readFile("profiles/snmp/schema.json", "utf8"),
    readFile("profiles/snmp/example-ups.yaml", "utf8"),
  ]);
  const schema = JSON.parse(schemaSource);
  const profile = parse(profileSource);
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  const validate = ajv.compile(schema);

  assert.equal(validate(profile), true, JSON.stringify(validate.errors, null, 2));
});
