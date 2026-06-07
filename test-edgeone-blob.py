import argparse
import concurrent.futures
import json
import os
import time
import urllib.error
import urllib.request
import uuid


def request(base, method, path, body=None, cookie=None, timeout=35):
    headers = {}
    if cookie:
        headers["Cookie"] = cookie
    if body is not None:
        headers["Content-Type"] = "text/plain; charset=utf-8"

    data = None if body is None else body.encode("utf-8")
    req = urllib.request.Request(base.rstrip("/") + path, data=data, headers=headers, method=method)
    start = time.time()

    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return {
                "method": method,
                "path": path,
                "status": resp.status,
                "body": resp.read().decode("utf-8", "replace"),
                "version": resp.headers.get("X-KV-Version", ""),
                "elapsed_ms": int((time.time() - start) * 1000),
            }
    except urllib.error.HTTPError as e:
        return {
            "method": method,
            "path": path,
            "status": e.code,
            "body": e.read().decode("utf-8", "replace"),
            "version": e.headers.get("X-KV-Version", ""),
            "elapsed_ms": int((time.time() - start) * 1000),
        }
    except Exception as e:
        return {
            "method": method,
            "path": path,
            "status": "ERR",
            "body": repr(e),
            "version": "",
            "elapsed_ms": int((time.time() - start) * 1000),
        }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", required=True)
    parser.add_argument("--cookie", default=os.environ.get("FUNCTIONS_KV_AUTH", ""))
    parser.add_argument("--auth-path", default=os.environ.get("FUNCTIONS_KV_AUTH_PATH", ""))
    parser.add_argument("--workers", type=int, default=8)
    args = parser.parse_args()
    if args.cookie and "=" not in args.cookie:
        args.cookie = "__Host-Auth=" + args.cookie

    key = "codex-test-" + uuid.uuid4().hex

    def req(method, path, body=None):
        return request(args.base, method, path, body=body, cookie=args.cookie)

    out = {"key": key, "basic": []}
    if args.auth_path:
        out["basic"].append(request(args.base, "GET", args.auth_path))
    out["basic"].append(request(args.base, "GET", "/" + key + "-noauth"))
    out["basic"].append(req("DELETE", "/" + key))
    out["basic"].append(req("GET", "/" + key))
    out["basic"].append(req("POST", "/" + key, "v1"))
    out["basic"].append(req("GET", "/" + key))

    current = req("GET", "/" + key)
    first_lock = req("LOCK", "/" + key + "?t=" + current["version"])
    out["basic"].append(first_lock)
    out["basic"].append(req("LOCK", "/" + key + "?t=" + current["version"]))
    out["basic"].append(req("UNLOCK", "/" + key, "v2"))
    updated = req("GET", "/" + key)
    out["basic"].append(updated)
    out["basic"].append(req("LOCK", "/" + key + "?t=" + current["version"]))
    out["basic"].append(req("DELETE", "/" + key))
    out["basic"].append(req("GET", "/" + key))

    race_key = key + "-race"
    req("DELETE", "/" + race_key)
    race_seed = req("POST", "/" + race_key, "race-seed")

    def lock_once(i):
        result = req("LOCK", "/" + race_key + "?t=" + race_seed["version"])
        result["worker"] = i
        return result

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as executor:
        out["race"] = list(executor.map(lock_once, range(args.workers)))

    winners = [item for item in out["race"] if item["status"] == 201]
    if winners:
        out["race_unlock"] = req("UNLOCK", "/" + race_key, "winner-value")
    out["race_final"] = req("GET", "/" + race_key)
    req("DELETE", "/" + race_key)

    print(json.dumps(out, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
