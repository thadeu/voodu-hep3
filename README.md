# voodu-hep3

Voodu plugin for the **SIP capture reader** — the read-only REST API over
the SIP messages [clowk-hep3](https://github.com/thadeu/clowk-hep3) writes
to a shared Postgres.

The architecture is two independent pieces sharing one external Postgres
(you create it and pass `DATABASE_URL` to both — they can run on different
servers, e.g. collector on one box, reader on another, both against one
RDS):

| Piece | What | How it's deployed |
| --- | --- | --- |
| **collector** | `clowk-hep3` — receives HEP3, writes SIP to Postgres | a plain `deployment` with the **public** `ghcr.io/thadeu/clowk-hep3` image (see clowk-hep3's `hep3-server.voodu`) |
| **reader** | this plugin — the REST API the webui consumes | the `hep3` kind → a `deployment` running a **local** image (this binary + a runtime Dockerfile, built by the install hook — no public registry) |

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

```bash
vd config voip/api set DATABASE_URL=postgres://user:pass@host/hep   # same DB the collector writes to
vd apply -f hep3-api.voodu
```

```hcl
# hep3-api.voodu
hep3 "voip" "api" {
  # api_port = 8080   # optional; internal (voodu0 only)
  resources {
    limits { cpu = "0.5", memory = "128Mi" }
  }
}
```

`expand` emits a `deployment` running `voodu-hep3-api:<version>` (local
image), with `env_from = ["voip/api"]` so `DATABASE_URL` comes from the
config bucket, and writes `HEP3_ENDPOINT` into that bucket for consumers.

## Manage the reader pod

```bash
vd hep3:api start   voip/api   # build local image (if needed) + start
vd hep3:api stop    voip/api
vd hep3:api restart voip/api   # rebuild local image + restart (new binary)
```

## REST API (versioned by media type)

The API is reached through the controller PAT proxy and versioned via the
`Accept` header — routes stay clean (`/calls`, `/calls/{id}`, `/stats`),
and the response shape can evolve to v2 without new paths.

```
Accept: application/vnd.clowk.hep+json;version=1
```

| Route | Description |
| --- | --- |
| `GET /calls` | list calls (grouped by correlation key), `from`/`until`/`q`/`page`/`per_page` |
| `GET /calls/{id}` | one call's messages, oldest-first (ladder diagram) |
| `GET /stats` | method/response counters + `active` (in-conversation) gauge, `interval` buckets |
| `GET /health` | liveness |

An explicit unsupported version → `406 Not Acceptable`; a generic Accept
(`*/*`, `application/json`, none) → the current version.

## Development

```bash
make test                                  # pure-logic tests
TEST_DATABASE_URL=postgres://… make test   # + Postgres-backed reader tests
make build
```

## License

AGPL-3.0-only © Thadeu Esteves Jr
