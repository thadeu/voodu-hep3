# Changelog

All notable changes to voodu-hep3 are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/) and the project adheres to
[Semantic Versioning](https://semver.org/). The first tagged release will
be `v0.1.0` (the tag must match `version:` in `plugin.yml`).

## [Unreleased]

## [0.4.0] - 2026-06-30

### Removed

- The `hep3:api start|stop|restart` command. The reader is a plain
  `deployment` once expanded, so its lifecycle is the generic `vd`
  (`get`/`logs`/`restart`/`stop`/`start`/`delete`); the local image is
  built by the install hook. The command only duplicated that — and its
  `docker build` broke under the controller's sandbox. Plugin commands are
  now just `expand` / `serve` / `help`.

## [0.3.0] - 2026-06-26

### Changed

- **Default read backend is now NDJSON** (`HEP_STORE=ndjson`). The reader
  tails the collector's shared NDJSON volume and serves
  `GET /export?since=<cursor>` (next cursor in the `X-Hep-Cursor` header),
  which the webui poller pulls into its own SQLite — that's where the
  calls/ladder/stats queries now run. The Postgres REST API becomes opt-in
  (`HEP_STORE=pg`). `expand` mounts `<data_volume>:/data:ro` and sets
  `HEP_STORE`/`HEP_DATA_DIR` on the ndjson path (DATABASE_URL via env_from
  on the pg path). Reader and collector share a named volume → same host,
  same uid; `Dockerfile.runtime` pins uid 10001 to match clowk-hep3.

### Removed

- API versioning by vendor media type (the `application/vnd.clowk.hep+json`
  Accept negotiation + `406`). Dead weight now that ndjson is the default
  read path and `/export` is an unversioned NDJSON tail; the pg `/calls`
  API just returns JSON.

### Added

- `hep3` resource kind (alias `hep`): `expand` emits a `deployment`
  running the read-only REST API over the Postgres clowk-hep3 writes to.
- Read REST API (`serve`, pg path): `/calls`, `/calls/{id}` (ladder),
  `/stats` (with the `active` in-conversation gauge), `/health` — JSON.
- Local-image deployment: the install hook builds `voodu-hep3-api:<version>`
  from the plugin binary + `Dockerfile.runtime` (no public registry, no
  build-mode); `expand` references that local tag, which the controller
  runs without a pull.
- `vd hep3:api start|stop|restart <scope/name>` — manage the reader pod;
  start/restart rebuild the local image (picking up a new binary).
- PAT-plane route (`routes` in `plugin.yml`): the controller reverse-
  proxies `/api/pat/v1/hep3/<scope>/<name>` to the reader on voodu0.
- Example `hep3-api.voodu`.

### Notes

- voodu-hep3 is READ-ONLY and ships no public image — only the release
  binary (GitHub Releases). The reader image is built locally at install.
