# brainstorm NIP-50 proxy

A small, stateless WebSocket relay shim that adds **NIP-50** (search) to the
brainstorm relay stack — without touching the relay or the FastAPI server.

It sits in front of the backing relay (`neofry`) and:

- intercepts `REQ`s whose filter has a string `search` field, runs a
  **GrapeRank Web-of-Trust–ranked profile search** via `brainstorm-server`'s
  `GET /search/byText`, and returns the matching **original signed kind-0
  events** (fetched from the relay by author);
- forwards every other relay message (non-search `REQ`, `EVENT`, `CLOSE`, …)
  to the backing relay untouched;
- serves a **NIP-11** document advertising `50`.

It mirrors the design of brainstorm.world (tapestry's `nip50-proxy`), adapted to
brainstorm's Python + Vespa stack.

## Scope (v1)

- **Profiles only** (kind 0). A `REQ` whose `kinds` excludes 0 gets an immediate
  `EOSE`.
- **No AUTH.** NIP-42 is not required or supported yet.
- **Optional `observer:` token** in the search string for the ranking point of
  view (hex or npub). If omitted, or if the observer's scores aren't loaded in
  Vespa, results **silently fall back to the house perspective**.
- **No score warming.** A cold observer never triggers a GrapeRank run — it just
  falls back. (Deferred deliberately; see the team notes.)
- `sort:` / `filter:` tokens are parsed (for client portability) but **ignored**.

## Search string grammar

```
<keywords> [observer:<hex-or-npub>] [sort:<metric>:<dir>] [filter:<metric>:<op>:<val>]
```

Example: `vitor observer:3bf0c63f... ` → keyword search "vitor" from that
pubkey's WoT perspective. Only `observer:` is honored in v1.

## How a search is served

1. Parse the `search` string → clean query + optional observer.
2. `GET /search/byText?text=<query>&observerPubkey=<observer>&onlyRanked=true` on
   `brainstorm-server` → ranked list of pubkeys. If empty and an observer was
   given, retry without `observerPubkey` (house fallback).
3. `REQ {authors:[...], kinds:[0]}` to the backing relay to fetch the original
   signed kind-0 events.
4. Emit `EVENT`s **in the backend's rank order**, then `EOSE`.

## Known limitations (v1)

- **Only `kinds` and `limit` are read from the search filter.** Other NIP-01
  attributes inside the search filter (`since`, `until`, `authors`, `ids`, tag
  filters) are ignored — this is profile keyword search, not general querying.
- **Mixed REQs may return a kind-0 event twice.** If a single REQ combines a
  `search` filter with a separate non-search filter that *also* matches kind 0,
  the event can arrive once from search and once from the backing relay. Plain
  search REQs (the normal case) are unaffected.
- **`Accept` matching is a substring check** (`application/nostr+json`), which is
  what real Nostr clients send; it does not parse media-range `q=0` weights.

## Configuration (env)

| Var | Default | Purpose |
|---|---|---|
| `NIP50_HOST` | `0.0.0.0` | listen host |
| `NIP50_PORT` | `7781` | listen port |
| `BACKING_RELAY_WS` | `ws://neofry:7777` | passthrough + signed-event source |
| `BACKING_RELAY_HTTP` | `http://neofry:7777` | NIP-11 source |
| `SEARCH_API_URL` | `http://brainstorm-server:8000` | ranked-search backend |
| `NIP50_DEFAULT_LIMIT` | `100` | default result limit |
| `NIP50_MAX_LIMIT` | `400` | hard cap (backend clamps to 400 too) |
| `NIP50_CONNECT_TIMEOUT` | `5` | relay connect timeout (s) |
| `NIP50_FETCH_TIMEOUT` | `8` | kind-0 fetch timeout (s) |
| `NIP50_SEARCH_TIMEOUT` | `15` | search backend timeout (s) |

## Run

In the compose stack (added as the `nip50-proxy` service):

```bash
docker compose up -d nip50-proxy
```

Standalone (dev):

```bash
cd nip50-proxy
pip install -r requirements.txt
SEARCH_API_URL=http://localhost:8000 BACKING_RELAY_WS=ws://localhost:7777 \
  BACKING_RELAY_HTTP=http://localhost:7777 python -m nip50_proxy
```

A public TLS endpoint (e.g. `wss://search.nosfabrica.com`) should be routed to
this service's port by the fronting reverse proxy. The same URL answers NIP-11
when requested with `Accept: application/nostr+json`.

## Test

Parser unit tests (pure, no stack needed):

```bash
cd nip50-proxy && python -m tests.test_search_parse   # or: pytest
```

Manual end-to-end with [`nak`](https://github.com/fiatjaf/nak) against a running stack:

```bash
# NIP-11 advertises 50
curl -H 'Accept: application/nostr+json' http://localhost:7781 | jq .supported_nips

# search (house perspective)
nak req -k 0 --search "vitor" ws://localhost:7781

# search from an observer's perspective
nak req -k 0 --search "vitor observer:<hex>" ws://localhost:7781
```
