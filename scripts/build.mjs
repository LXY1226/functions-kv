import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const out = join(root, "functions", "_auth.generated.js");

function normalizeAuthCookie(auth) {
  auth = (auth || "").trim();
  if (!auth) return "";
  return auth.includes("=") ? auth : `__Host-Auth=${auth}`;
}

const auth = normalizeAuthCookie(process.env.FUNCTIONS_KV_AUTH);
const authPath = (process.env.FUNCTIONS_KV_AUTH_PATH || "").trim();

if (!auth) {
  throw new Error("FUNCTIONS_KV_AUTH is required");
}

mkdirSync(dirname(out), { recursive: true });
writeFileSync(
  out,
  `export const AUTH = ${JSON.stringify(auth)};\nexport const AUTH_PATH = ${JSON.stringify(authPath)};\n`,
);
