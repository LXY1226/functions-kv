import { getStore } from "@edgeone/pages-blob";
import { AUTH, AUTH_PATH } from "./_auth.generated.js";

const TTL_MS = 60_000;
const VERIFY_MS = 300;
const store = () => getStore("kv");
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const version = () => `${Date.now()}.${Math.random()}`;
const pack = (value, ver = version()) => JSON.stringify({ version: ver, value });

function res(body = "", status = 200, headers = {}) {
  return new Response(body, {
    status,
    headers: { "Cache-Control": "no-store", ...headers },
  });
}

function keyOf(request) {
  const path = decodeURIComponent(new URL(request.url).pathname);
  const key = path.slice(1);
  return key && new TextEncoder().encode(key).length <= 480 ? key : "";
}

async function getValue(db, key) {
  const raw = await db.get(`data/${key}`, { consistency: "strong" });
  if (raw == null) return null;
  const data = JSON.parse(raw);
  return data && data.version && typeof data.value === "string" ? data : null;
}

async function setValue(db, key, value) {
  const ver = version();
  await db.set(`data/${key}`, pack(value, ver));
  return ver;
}

async function lock(db, key, request) {
  const expect = new URLSearchParams(new URL(request.url).search).get("t") || "";
  if (!expect) return res("Missing version", 400);

  const currentValue = await getValue(db, key);
  if (currentValue == null) return res("", 404);
  if (currentValue.version !== expect) {
    return res(currentValue.value, 200, { "X-KV-Version": currentValue.version });
  }

  const lockKey = `locks/${key}`;
  const now = Date.now();
  const old = await db.get(lockKey, { consistency: "strong" });
  if (old && Number(old) > now) return res("Locked", 423);
  if (old) await db.delete(lockKey);

  const expiresAt = now + TTL_MS + Math.random();
  await db.set(lockKey, String(expiresAt));
  await sleep(VERIFY_MS);

  const current = await db.get(lockKey, { consistency: "strong" });
  return Number(current) === expiresAt ? res("", 201) : res("Locked", 423);
}

async function unlock(db, key, request) {
  const value = await request.text();
  if (!value) return res("Missing value", 400);
  const ver = await setValue(db, key, value);
  await db.delete(`locks/${key}`);
  return res("", 204, { "X-KV-Version": ver });
}

export async function onRequest({ request }) {
  try {
    const path = new URL(request.url).pathname;

    if (AUTH_PATH && path === AUTH_PATH) {
      if (!AUTH) return res("Missing auth config", 503);
      return res("Good!", 200, {
        "Set-Cookie": `${AUTH}; Secure; Path=/; HttpOnly; Max-Age=2592000; SameSite=Strict`,
      });
    }

    if (!AUTH) return res("Missing auth config", 503);
    if (!(request.headers.get("Cookie") || "").includes(AUTH)) {
      return res("Forbidden", 403);
    }

    const key = keyOf(request);
    if (!key) return res("Bad key", 400);

    const db = store();
    if (request.method === "GET") {
      const value = await getValue(db, key);
      return value == null ? res("", 404) : res(value.value, 200, { "X-KV-Version": value.version });
    }
    if (request.method === "POST") {
      const ver = await setValue(db, key, await request.text());
      return res(null, 204, { "X-KV-Version": ver });
    }
    if (request.method === "DELETE") {
      await db.delete(`data/${key}`);
      await db.delete(`locks/${key}`);
      return res(null, 204);
    }
    if (request.method === "LOCK") return await lock(db, key, request);
    if (request.method === "UNLOCK") return await unlock(db, key, request);

    return res("", 405, { Allow: "GET, POST, DELETE, LOCK, UNLOCK" });
  } catch (e) {
    return res(e && e.stack ? e.stack : String(e), 503);
  }
}
