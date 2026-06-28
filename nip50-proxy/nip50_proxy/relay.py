"""Backing-relay interactions: NIP-11 document + signed kind-0 retrieval.

The Vespa index stores denormalized profile fields, not signed events. To return
spec-valid EVENTs, we fetch the original signed kind-0 events from the backing
relay by author (kind 0 is replaceable, so the relay holds the latest per author).
"""
import asyncio
import json
import time

import httpx
import websockets

from nip50_proxy.config import settings
from nip50_proxy.log import log

_NIP11_TTL_SECONDS = 300.0
_nip11_cache: dict = {"doc": None, "ts": 0.0}


async def _fetch_backing_nip11() -> dict:
    try:
        async with httpx.AsyncClient(timeout=10.0) as client:
            r = await client.get(
                settings.backing_relay_http,
                headers={"Accept": "application/nostr+json"},
            )
            if r.status_code == 200:
                return r.json()
            log.warning("backing NIP-11 returned status %d", r.status_code)
    except Exception as exc:  # noqa: BLE001
        log.warning("backing NIP-11 fetch failed: %r", exc)
    return {}


async def build_nip11_doc() -> dict:
    """Backing relay's NIP-11 with NIP-50 (50) merged in + search_capabilities."""
    now = time.monotonic()
    if (
        _nip11_cache["doc"] is not None
        and now - _nip11_cache["ts"] < _NIP11_TTL_SECONDS
    ):
        base = _nip11_cache["doc"]
    else:
        base = await _fetch_backing_nip11()
        _nip11_cache["doc"] = base
        _nip11_cache["ts"] = now

    doc = dict(base)
    # Coerce to ints + add 50; tolerate a malformed upstream list rather than
    # 500-ing the NIP-11 endpoint on a mixed-type sort.
    nips = {50}
    for n in base.get("supported_nips") or []:
        if isinstance(n, int):
            nips.add(n)
        elif isinstance(n, str) and n.isdigit():
            nips.add(int(n))
    doc["supported_nips"] = sorted(nips)
    doc["software"] = "neofry+brainstorm-nip50-proxy"

    limitation = dict(base.get("limitation") or {})
    limitation["search_extensions"] = ["observer"]
    doc["limitation"] = limitation

    doc["search_capabilities"] = {
        "description": (
            "Full-text Nostr profile (kind 0) search ranked by GrapeRank "
            "Web-of-Trust, served from Vespa."
        ),
        "supported_kinds": [0],
        "extensions": {
            "observer": {
                "description": (
                    "Hex or npub pubkey for the Web-of-Trust point of view. "
                    "Unauthenticated; falls back silently to the relay house "
                    "perspective if omitted or unavailable."
                ),
                "format": "observer:<hex-or-npub>",
            }
        },
    }
    return doc


async def _drain_sub(ws, sub_id: str, events: dict[str, dict]) -> None:
    """Read EVENTs for `sub_id` into `events` (newest per author) until EOSE/CLOSED."""
    async for raw in ws:
        try:
            msg = json.loads(raw if isinstance(raw, str) else raw.decode())
        except Exception:  # noqa: BLE001
            continue
        if not isinstance(msg, list) or len(msg) < 2 or msg[1] != sub_id:
            continue
        tag = msg[0]
        if tag == "EVENT" and len(msg) >= 3:
            ev = msg[2]
            if not isinstance(ev, dict):
                continue
            pk = ev.get("pubkey")
            if not pk:
                continue
            prev = events.get(pk)
            if prev is None or ev.get("created_at", 0) > prev.get("created_at", 0):
                events[pk] = ev
        elif tag in ("EOSE", "CLOSED"):
            return


async def fetch_kind0_by_authors(authors: list[str]) -> dict[str, dict]:
    """Fetch the latest signed kind-0 event per author from the backing relay.

    Authors are chunked under the relay's per-filter cap so a large result set is
    fetched across multiple REQs rather than silently truncated. Best-effort:
    returns whatever arrived before EOSE or the per-chunk fetch timeout.
    """
    if not authors:
        return {}

    events: dict[str, dict] = {}
    chunk_size = max(settings.max_authors_per_fetch, 1)
    chunks = [authors[i : i + chunk_size] for i in range(0, len(authors), chunk_size)]

    try:
        async with websockets.connect(
            settings.backing_relay_ws,
            max_size=2**20,
            open_timeout=settings.connect_timeout,
        ) as ws:
            for idx, chunk in enumerate(chunks):
                sub_id = f"_bs_fetch_{idx}"
                await ws.send(
                    json.dumps(
                        ["REQ", sub_id, {"authors": chunk, "kinds": [0], "limit": len(chunk)}]
                    )
                )
                try:
                    await asyncio.wait_for(
                        _drain_sub(ws, sub_id, events), timeout=settings.fetch_timeout
                    )
                except asyncio.TimeoutError:
                    log.warning(
                        "kind-0 fetch timed out (chunk %d/%d)", idx + 1, len(chunks)
                    )
                try:
                    await ws.send(json.dumps(["CLOSE", sub_id]))
                except Exception:  # noqa: BLE001
                    pass
    except Exception as exc:  # noqa: BLE001
        log.warning("kind-0 fetch failed: %r", exc)

    missing = len(authors) - len(events)
    if missing > 0:
        log.info(
            "kind-0 fetch resolved %d/%d authors (%d missing)",
            len(events),
            len(authors),
            missing,
        )
    return events
