# functions-kv

Minimal EdgeOne Pages Functions API backed by EdgeOne Blob storage.

## Blob Binding

Create or bind a Blob store named:

```text
kv
```

The function uses strong consistency:

```js
getStore({ name: "kv", consistency: "strong" })
```

## API

All data endpoints require:

```text
Cookie: __Host-Auth=<FUNCTIONS_KV_AUTH>
```

Set the auth cookie at build time. `npm run build` reads these environment variables and writes a generated, ignored module used by the EdgeOne function:

```text
FUNCTIONS_KV_AUTH=<cookie value or full __Host-Auth=... cookie>
FUNCTIONS_KV_AUTH_PATH=<optional path that issues the cookie>
```

`FUNCTIONS_KV_AUTH` is required during build.

Endpoints:

```text
GET    <FUNCTIONS_KV_AUTH_PATH>
GET    /:key
POST   /:key
DELETE /:key
LOCK   /:key?t=<version>
UNLOCK /:key
```

`GET`, `POST`, `LOCK` with changed data, and `UNLOCK` return `X-KV-Version`.

`LOCK?t=<version>` compares the supplied version with the current value's embedded version. If the version changed, it returns `200` with the current value and version. If the version matches, it tries to create a short-lived lock and returns `201` on success or `423` when another client holds the lock.

`UNLOCK` requires a non-empty body, writes it as the new value with a new embedded version, and removes the lock.

Values are stored internally as a JSON envelope containing the version and raw value. This is intentionally incompatible with the older plain-value storage format.

## Go Client

This repository is also a Go module:

```bash
go get github.com/LXY1226/functions-kv
```

```go
kv := functionskv.New[Token](
  "https://your-edgeone-domain",
  "__Host-Auth=...",
  "115-open",
)
```

Use `Init`, `BeforeRefresh`, and `AfterRefresh` to share refreshable tokens across multiple clients.

## Test

```bash
python test-edgeone-blob.py --base https://your-edgeone-domain --cookie "__Host-Auth=..."
```
