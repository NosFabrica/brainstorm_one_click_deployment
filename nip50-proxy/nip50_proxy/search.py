"""NIP-50 search-string parsing and the ranked-search backend call.

The search string grammar mirrors brainstorm.world (tapestry) so the same
clients work against both relays:

    "vitor observer:<hex-or-npub> sort:followers:desc filter:rank:gte:2"

v1 honors only the `observer:` extension. `sort:`/`filter:` tokens are
recognized and stripped from the keyword query (for client portability) but are
not yet applied.
"""
from typing import TYPE_CHECKING

from nip50_proxy.config import settings
from nip50_proxy.log import log

if TYPE_CHECKING:
    import httpx

_OBSERVER_PREFIX = "observer:"
_IGNORED_PREFIXES = ("sort:", "filter:")


def parse_search_string(search: str) -> tuple[str, str | None]:
    """Split a NIP-50 search string into (clean_query, observer_or_None)."""
    if not search or not isinstance(search, str):
        return "", None

    observer: str | None = None
    query_parts: list[str] = []

    for token in search.strip().split():
        if token.startswith(_OBSERVER_PREFIX):
            value = token[len(_OBSERVER_PREFIX):]
            observer = value or None
        elif token.startswith(_IGNORED_PREFIXES):
            # Recognized extension grammar, not yet honored in v1. Stripped so it
            # doesn't pollute the keyword query.
            continue
        else:
            query_parts.append(token)

    return " ".join(query_parts), observer


# Shared async HTTP client to brainstorm-server (pooled for the process lifetime).
_client: "httpx.AsyncClient | None" = None


def _http() -> "httpx.AsyncClient":
    global _client
    if _client is None:
        import httpx

        _client = httpx.AsyncClient(
            timeout=httpx.Timeout(settings.search_timeout, connect=5.0)
        )
    return _client


async def aclose() -> None:
    global _client
    if _client is not None:
        await _client.aclose()
        _client = None


async def _call_search(query: str, observer: str | None, limit: int) -> list[dict]:
    """Call brainstorm-server GET /search/byText. Returns the raw results list
    (each a dict of Vespa fields incl. `pubkey`), or [] on any error."""
    # Clamp to the backend's max_length (it rejects longer queries with 422);
    # the backend then applies its own per-word truncation.
    params: dict[str, str | int] = {
        "text": query[: settings.max_query_chars],
        "onlyRanked": "true",
        "maxHits": min(limit, settings.max_limit),
    }
    if observer:
        params["observerPubkey"] = observer

    url = settings.search_api_url.rstrip("/") + "/search/byText"
    try:
        r = await _http().get(url, params=params)
        r.raise_for_status()
        data = r.json()
        return (data.get("data") or {}).get("results") or []
    except Exception as exc:  # noqa: BLE001 - search is best-effort
        log.warning("search backend error: %r", exc)
        return []


def _ordered_pubkeys(results: list[dict], limit: int) -> list[str]:
    """Pubkeys in the backend's rank order, de-duplicated, capped at `limit`."""
    out: list[str] = []
    seen: set[str] = set()
    for r in results:
        pk = r.get("pubkey")
        if pk and pk not in seen:
            seen.add(pk)
            out.append(pk)
            if len(out) >= limit:
                break
    return out


async def run_search(
    query: str, observer: str | None, limit: int
) -> tuple[list[str], str | None]:
    """Run a ranked profile search and return (ranked_pubkeys, effective_observer).

    Cold-observer fallback: if an observer perspective was requested but yields no
    ranked results (their scores aren't loaded in Vespa), silently retry from the
    house perspective. No score calculation is triggered (by design, v1).
    """
    results = await _call_search(query, observer, limit)
    pubkeys = _ordered_pubkeys(results, limit)

    if not pubkeys and observer:
        results = await _call_search(query, None, limit)
        return _ordered_pubkeys(results, limit), None

    return pubkeys, observer
