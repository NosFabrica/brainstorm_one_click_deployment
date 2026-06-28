"""Runtime configuration, all via environment variables.

The proxy intentionally keeps a tiny config surface (independent of
brainstorm-server's full settings block) so it can be deployed and restarted on
its own.
"""
import os


class Settings:
    def __init__(self) -> None:
        # Where this proxy listens. 0.0.0.0 so it's reachable inside the compose
        # network and via the host port mapping.
        self.host: str = os.environ.get("NIP50_HOST", "0.0.0.0")
        self.port: int = int(os.environ.get("NIP50_PORT", "7781"))

        # Backing Nostr relay. Used for (a) passthrough of all non-search
        # traffic, (b) fetching the original signed kind-0 events for search
        # results, and (c) sourcing the NIP-11 document we extend. neofry is the
        # relay that actually holds the kind-0 profiles in this deployment.
        self.backing_relay_ws: str = os.environ.get(
            "BACKING_RELAY_WS", "ws://neofry:7777"
        )
        self.backing_relay_http: str = os.environ.get(
            "BACKING_RELAY_HTTP", "http://neofry:7777"
        )

        # brainstorm-server base URL — its GET /search/byText is the ranked
        # search backend (Vespa + GrapeRank live behind it).
        self.search_api_url: str = os.environ.get(
            "SEARCH_API_URL", "http://brainstorm-server:8000"
        )

        # Result bounds. The backend itself clamps maxHits to 400.
        self.default_limit: int = int(os.environ.get("NIP50_DEFAULT_LIMIT", "100"))
        self.max_limit: int = int(os.environ.get("NIP50_MAX_LIMIT", "400"))

        # Per-connection resource caps (DoS guards).
        # max_subscriptions mirrors neofry's maxSubsPerConnection (20).
        self.max_subscriptions: int = int(
            os.environ.get("NIP50_MAX_SUBSCRIPTIONS", "20")
        )
        # Bounds concurrent expensive search fan-out (HTTP + relay fetch) per
        # connection so a client can't stampede brainstorm-server / Vespa.
        self.max_concurrent_searches: int = int(
            os.environ.get("NIP50_MAX_CONCURRENT_SEARCHES", "8")
        )
        # Authors per kind-0 fetch REQ, kept under the relay's per-filter cap so a
        # large result set is chunked rather than silently truncated.
        self.max_authors_per_fetch: int = int(
            os.environ.get("NIP50_MAX_AUTHORS_PER_FETCH", "500")
        )
        # Longest keyword query the backend accepts (brainstorm-server enforces
        # max_length=100); clamp here so long queries still return results.
        self.max_query_chars: int = int(os.environ.get("NIP50_MAX_QUERY_CHARS", "100"))

        # Timeouts (seconds).
        self.connect_timeout: float = float(
            os.environ.get("NIP50_CONNECT_TIMEOUT", "5")
        )
        self.fetch_timeout: float = float(os.environ.get("NIP50_FETCH_TIMEOUT", "8"))
        self.search_timeout: float = float(
            os.environ.get("NIP50_SEARCH_TIMEOUT", "15")
        )


settings = Settings()
