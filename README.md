# voodu-hep3

Voodu plugin for the **SIP capture reader** — it serves the SIP data the
[clowk-hep3](https://github.com/thadeu/clowk-hep3) collector captured.
`HEP_STORE` picks how:

- **`ndjson`** (default) — tails the collector's shared NDJSON volume and
  serves `GET /export?since=<cursor>`, which the webui poller pulls into
  its own SQLite. No database.
- **`pg`** — a REST query API over the shared Postgres.

| Piece | What | How it's deployed |
| --- | --- | --- |
| **collector** | `clowk-hep3` — receives HEP3, writes SIP | a plain `deployment` with the **public** `ghcr.io/thadeu/clowk-hep3` image (see clowk-hep3's `hep3-server.voodu`) |
| **reader** | this plugin — what the webui consumes | the `hep3` kind → a `deployment` running a **local** image (this binary + `Dockerfile.runtime`, built by the install hook — no public registry) |

> **Shared volume (ndjson) — required.** Collector and reader share one
> named docker volume (e.g. `hep3-data`), so they must run on the **same
> host** as the **same uid (10001)** — the NDJSON files are `0600`. You wire
> it on the reader with a standard `volumes = ["hep3-data:/data:ro"]` (the
> block is a deployment spec — see below). Full requirements:
> [clowk-hep3 docs/transport.md](https://github.com/thadeu/clowk-hep3/blob/main/docs/transport.md#shared-volume-ndjson--required).
> On the **pg** path there's no shared volume — only `DATABASE_URL`, and
> the two may run on different servers.

The reader is internal-only; the webui reaches it through the controller's
authenticated **PAT proxy** at `/api/pat/v1/hep3/<scope>/<name>`.

`vd hep:<cmd>` works alongside `vd hep3:<cmd>`.

## Install

```bash
vd plugins:install hep3
```

The install hook downloads the plugin binary **and** builds the local
reader image (`voodu-hep3-api:<version>`) from `Dockerfile.runtime` + the
binary. No public image is ever pushed.

## Deploy the reader

The `hep3` block **is a deployment spec**: standard fields (`volumes`,
`env`, `ports`, `resources`, …) pass through; `store` is the only
plugin-specific field. Default (ndjson) — wire the collector's volume:

```hcl
# hep3-api.voodu
hep3 "voip" "api" {
  store   = "ndjson"                  # only plugin field; "pg" reads Postgres
  volumes = ["hep3-data:/data:ro"]    # standard field — the shared volume
  resources {
    limits { cpu = "0.5", memory = "128Mi" }
  }
}
```

```bash
vd apply -f hep3-api.voodu
```

`expand` keeps the operator's fields and overlays only its own: the local
image `voodu-hep3-api:<version>`, `HEP_STORE` (from `store`),
`HEP_DATA_DIR=/data`, `HEP3_API_ADDR`, a `/health` check, and `env_from`
(the resource's bucket) — none of which clobber what you set. On the **pg**
path drop the `volumes` and put `DATABASE_URL` in the bucket
(`vd config voip/api set DATABASE_URL=...`). Either way it writes
`HEP3_ENDPOINT` into the bucket for consumers.

## Manage the reader

Once applied, the reader is a plain `deployment` the controller manages —
use the generic `vd` commands, no plugin command:

```bash
vd get                      # is the reader Running?
vd logs voip/api            # reader logs
vd restart voip/api         # bounce the pod
vd stop|start voip/api
vd delete voip/api
```

The local image is (re)built by the install hook on `vd plugins:install` /
`vd plugins:update`; a `vd apply` after an update rolls the new image.

## Read paths

All routes are reached through the controller PAT proxy.

### `ndjson` (default) — export tail

| Route | Description |
| --- | --- |
| `GET /export?since=<cursor>` | NDJSON lines newer than the cursor (file:offset). Returns the next cursor in the `X-Hep-Cursor` header; a partial trailing line is withheld until complete. Soft-capped per call so a cold poller pages forward. |
| `GET /health` | liveness |

The webui poller loops `/export` with the returned cursor, dedups by line,
and ingests into its local SQLite — where the calls/ladder/stats queries
actually run.

### `pg` — REST query API

When `store = "pg"`, the reader serves JSON over the shared Postgres.

| Route | Description |
| --- | --- |
| `GET /calls` | list calls (grouped by correlation key), `from`/`until`/`q`/`page`/`per_page` |
| `GET /calls/{id}` | one call's messages, oldest-first (ladder diagram) |
| `GET /stats` | method/response counters + `active` (in-conversation) gauge, `interval` buckets |
| `GET /health` | liveness |

## Development

```bash
make test                                  # pure-logic tests
TEST_DATABASE_URL=postgres://… make test   # + Postgres-backed reader tests
make build
```

## License

AGPL-3.0-only © Thadeu Esteves Jr
