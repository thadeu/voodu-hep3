# Changelog

All notable changes to voodu-hep3 are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/) and the project adheres to
[Semantic Versioning](https://semver.org/). The first tagged release will
be `v0.1.0` (the tag must match `version:` in `plugin.yml`).

## [Unreleased]

### Added

- `hep3` resource kind (alias `hep`): `expand` emits a `deployment`
  running the read-only REST API over the Postgres clowk-hep3 writes to.
- Read REST API (`serve`): `/calls`, `/calls/{id}` (ladder), `/stats`
  (with the `active` in-conversation gauge), `/health` — versioned by
  vendor media type (`Accept: application/vnd.clowk.hep+json;version=1`),
  not by URL path; unsupported version → 406.
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
