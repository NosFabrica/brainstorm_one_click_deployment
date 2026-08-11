"""Per-connection proxy logic.

Each client connection gets a Session that opens a parallel connection to the
backing relay for passthrough, and routes messages:

    REQ with a string `search` field  -> ranked search -> EVENT* + EOSE
    REQ without `search`              -> forward to backing relay
    EVENT (publish)                  -> forward to backing relay
    CLOSE                            -> forward if passthrough/mixed; local-only for pure search
    anything else                    -> forward to backing relay

If the backing relay is unreachable, search still works (it uses its own
short-lived fetch connection); passthrough REQs are answered with CLOSED and
publishes with OK=false so clients are never left hanging.

v1 reads only `kinds` and `limit` from the search filter. Other filter
attributes (since/until/authors/ids/tags) are intentionally ignored — profile
keyword search is the only supported mode.
"""
import asyncio
import json

import websockets

from nip50_proxy.config import settings
from nip50_proxy.log import log
from nip50_proxy.relay import fetch_kind0_by_authors
from nip50_proxy.search import parse_search_string, run_search


def _is_search_filter(f) -> bool:
    return isinstance(f, dict) and isinstance(f.get("search"), str)


class Session:
    def __init__(self, client_ws) -> None:
        self.client = client_ws
        self.backing = None
        # sub_id -> {"search": bool, "mixed": bool}
        self.subs: dict[str, dict] = {}
        # sub_id -> in-flight search Task (so a CLOSE can cancel it)
        self._search_tasks: dict[str, asyncio.Task] = {}
        self._pumps: list[asyncio.Task] = []
        self._search_sem = asyncio.Semaphore(settings.max_concurrent_searches)
        self._closed = False

    async def run(self) -> None:
        await self._connect_backing()
        self._pumps = [asyncio.create_task(self._pump_client())]
        if self.backing is not None:
            self._pumps.append(asyncio.create_task(self._pump_backing()))
        try:
            await asyncio.wait(self._pumps, return_when=asyncio.FIRST_COMPLETED)
        finally:
            await self._teardown()

    # -- connections --------------------------------------------------------
    async def _connect_backing(self) -> None:
        try:
            self.backing = await websockets.connect(
                settings.backing_relay_ws,
                max_size=2**20,
                open_timeout=settings.connect_timeout,
                ping_interval=30,
            )
        except Exception as exc:  # noqa: BLE001
            log.warning(
                "backing relay unavailable (%s): %r; passthrough disabled",
                settings.backing_relay_ws,
                exc,
            )
            self.backing = None

    async def _pump_backing(self) -> None:
        try:
            async for raw in self.backing:
                await self._send_client(raw if isinstance(raw, str) else raw.decode())
        except Exception:  # noqa: BLE001
            pass

    async def _pump_client(self) -> None:
        try:
            async for raw in self.client:
                await self._handle_client(raw if isinstance(raw, str) else raw.decode())
        except Exception:  # noqa: BLE001
            pass

    # -- routing ------------------------------------------------------------
    async def _handle_client(self, raw: str) -> None:
        try:
            msg = json.loads(raw)
        except Exception:  # noqa: BLE001
            await self._forward_backing(raw)
            return
        if not isinstance(msg, list) or not msg:
            await self._forward_backing(raw)
            return

        kind = msg[0]
        if kind == "REQ":
            await self._handle_req(msg, raw)
        elif kind == "CLOSE":
            await self._handle_close(msg, raw)
        elif kind == "EVENT":
            await self._handle_publish(msg, raw)
        else:
            await self._forward_backing(raw)

    async def _handle_req(self, msg: list, raw: str) -> None:
        if len(msg) < 3:
            await self._forward_backing(raw)
            return
        sub_id = msg[1]
        filters = msg[2:]

        # Per-connection subscription cap (DoS guard).
        if sub_id not in self.subs and len(self.subs) >= settings.max_subscriptions:
            await self._send_client(
                json.dumps(["CLOSED", sub_id, "rate-limited: too many subscriptions"])
            )
            return

        search_filter = next((f for f in filters if _is_search_filter(f)), None)

        if search_filter is None:
            self.subs[sub_id] = {"search": False, "mixed": False}
            if self.backing is not None:
                await self._send_backing(raw)
            else:
                await self._send_client(
                    json.dumps(["CLOSED", sub_id, "unavailable: relay not reachable"])
                )
            return

        self.subs[sub_id] = {"search": True, "mixed": False}
        task = asyncio.create_task(self._handle_search(sub_id, search_filter, filters))
        self._search_tasks[sub_id] = task
        task.add_done_callback(
            lambda t, sid=sub_id: self._search_tasks.pop(sid, None)
        )

    async def _handle_search(
        self, sub_id: str, search_filter: dict, all_filters: list
    ) -> None:
        try:
            async with self._search_sem:
                query, observer = parse_search_string(search_filter.get("search", ""))

                # Profiles only: a kinds filter that explicitly excludes 0 has
                # nothing for us. A missing/non-list kinds means "no restriction".
                kinds = search_filter.get("kinds")
                if isinstance(kinds, list) and 0 not in kinds:
                    await self._send_client(json.dumps(["EOSE", sub_id]))
                    self.subs.pop(sub_id, None)
                    return

                limit = settings.default_limit
                raw_limit = search_filter.get("limit")
                if raw_limit is not None:
                    try:
                        limit = min(max(int(raw_limit), 1), settings.max_limit)
                    except (TypeError, ValueError):
                        limit = settings.default_limit

                pubkeys: list[str] = []
                if query.strip():
                    pubkeys, _ = await run_search(query, observer, limit)

                events = await fetch_kind0_by_authors(pubkeys) if pubkeys else {}

                # If a CLOSE arrived during the awaits, stop without emitting.
                if sub_id not in self.subs:
                    return

                # Emit in the backend's rank order; drop any author whose signed
                # event the relay didn't return.
                for pk in pubkeys:
                    ev = events.get(pk)
                    if ev is not None:
                        await self._send_client(json.dumps(["EVENT", sub_id, ev]))

                # Mixed REQ: forward the non-search filter objects to the backing
                # relay under the same sub_id and let its EOSE close the sub.
                non_search = [f for f in all_filters if not _is_search_filter(f)]
                if non_search and self.backing is not None and sub_id in self.subs:
                    self.subs[sub_id]["mixed"] = True
                    await self._send_backing(json.dumps(["REQ", sub_id, *non_search]))
                    # If a CLOSE landed while we were forwarding, clean up the
                    # backing sub we just opened.
                    if sub_id not in self.subs:
                        await self._send_backing(json.dumps(["CLOSE", sub_id]))
                else:
                    await self._send_client(json.dumps(["EOSE", sub_id]))
                    self.subs.pop(sub_id, None)
        except asyncio.CancelledError:
            raise
        except Exception as exc:  # noqa: BLE001
            log.warning("search failed sub=%s: %r", sub_id, exc)
            # Always close the subscription so the client doesn't hang.
            if not self.subs.get(sub_id, {}).get("mixed"):
                self.subs.pop(sub_id, None)
            await self._send_client(json.dumps(["EOSE", sub_id]))

    async def _handle_close(self, msg: list, raw: str) -> None:
        sub_id = msg[1] if len(msg) > 1 else None
        sub = self.subs.pop(sub_id, None)
        task = self._search_tasks.pop(sub_id, None)
        if task is not None:
            task.cancel()
        # Forward CLOSE to the backing relay for passthrough or mixed subs (which
        # have/will have a matching backing subscription).
        if self.backing is not None and sub and (not sub["search"] or sub["mixed"]):
            await self._send_backing(raw)

    async def _handle_publish(self, msg: list, raw: str) -> None:
        if self.backing is not None:
            await self._send_backing(raw)
            return
        # No backing relay: acknowledge the publish as rejected rather than drop.
        event = msg[1] if len(msg) > 1 and isinstance(msg[1], dict) else {}
        event_id = event.get("id", "")
        await self._send_client(
            json.dumps(["OK", event_id, False, "unavailable: relay not reachable"])
        )

    # -- io helpers ---------------------------------------------------------
    async def _forward_backing(self, raw: str) -> None:
        if self.backing is not None:
            await self._send_backing(raw)

    async def _send_backing(self, raw: str) -> None:
        try:
            await self.backing.send(raw)
        except Exception:  # noqa: BLE001
            pass

    async def _send_client(self, raw: str) -> None:
        try:
            await self.client.send(raw)
        except Exception:  # noqa: BLE001
            pass

    async def _teardown(self) -> None:
        if self._closed:
            return
        self._closed = True

        pending = list(self._search_tasks.values()) + self._pumps
        for task in pending:
            task.cancel()
        # Await cancellation so in-flight search tasks fully unwind (and close
        # their fetch sockets) before we drop the connections.
        if pending:
            await asyncio.gather(*pending, return_exceptions=True)

        if self.backing is not None:
            try:
                await self.backing.close()
            except Exception:  # noqa: BLE001
                pass
        try:
            await self.client.close()
        except Exception:  # noqa: BLE001
            pass
