"""Entry point: one HTTP/WebSocket server.

The same port answers:
  - NIP-11 (HTTP GET with Accept: application/nostr+json)
  - /health, /status (HTTP GET)
  - WebSocket upgrades -> a per-connection Session (NIP-01 + NIP-50)
"""
import asyncio
import http
import json
import signal

import websockets

from nip50_proxy.config import settings
from nip50_proxy.log import log
from nip50_proxy.relay import build_nip11_doc
from nip50_proxy.search import aclose as search_aclose
from nip50_proxy.session import Session

_CORS = [
    ("Access-Control-Allow-Origin", "*"),
    ("Access-Control-Allow-Methods", "GET, OPTIONS"),
    ("Access-Control-Allow-Headers", "Accept"),
]


async def process_request(path, request_headers):
    """Handle plain HTTP before the WebSocket handshake.

    Returning None lets the WS handshake proceed; returning a (status, headers,
    body) tuple short-circuits with an HTTP response.
    """
    # WebSocket upgrade requests carry `Upgrade: websocket` — let them through.
    if request_headers.get("Upgrade", "").lower() == "websocket":
        return None

    accept = request_headers.get("Accept", "") or ""
    if "application/nostr+json" in accept:
        try:
            body = json.dumps(await build_nip11_doc()).encode()
            return (
                http.HTTPStatus.OK,
                _CORS + [("Content-Type", "application/nostr+json")],
                body,
            )
        except Exception as exc:  # noqa: BLE001
            log.warning("failed to build NIP-11 doc: %r", exc)
            return (
                http.HTTPStatus.INTERNAL_SERVER_ERROR,
                [("Content-Type", "application/json")],
                b'{"error":"internal"}',
            )

    if path.rstrip("/") in ("/health", "/status"):
        body = json.dumps({"service": "nip50-proxy", "status": "ok"}).encode()
        return (http.HTTPStatus.OK, _CORS + [("Content-Type", "application/json")], body)

    return (
        http.HTTPStatus.OK,
        _CORS + [("Content-Type", "text/plain; charset=utf-8")],
        "brainstorm NIP-50 proxy — connect via WebSocket\n".encode(),
    )


async def ws_handler(websocket) -> None:
    await Session(websocket).run()


async def main() -> None:
    log.info("listening on %s:%d", settings.host, settings.port)
    log.info("backing relay: %s", settings.backing_relay_ws)
    log.info("search backend: %s", settings.search_api_url)

    loop = asyncio.get_running_loop()
    stop = loop.create_future()
    for sig in (signal.SIGTERM, signal.SIGINT):
        try:
            loop.add_signal_handler(
                sig, lambda: stop.done() or stop.set_result(None)
            )
        except NotImplementedError:  # e.g. Windows
            pass

    try:
        async with websockets.serve(
            ws_handler,
            settings.host,
            settings.port,
            process_request=process_request,
            ping_interval=30,
            ping_timeout=30,
            max_size=2**20,
        ):
            await stop  # run until SIGTERM/SIGINT
            log.info("shutting down")
    finally:
        await search_aclose()


def run() -> None:
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        log.info("shutting down")


if __name__ == "__main__":
    run()
