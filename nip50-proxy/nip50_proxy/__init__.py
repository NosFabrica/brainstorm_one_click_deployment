"""Brainstorm NIP-50 proxy.

A small, stateless WebSocket relay shim that adds NIP-50 (search) on top of an
existing Nostr relay (neofry). It intercepts REQs whose filter carries a string
`search` field, runs a Web-of-Trust-ranked profile search via brainstorm-server,
returns the matching signed kind-0 events fetched from the backing relay, and
forwards all other relay traffic untouched.

Scope (v1): profiles (kind 0) only; no AUTH; optional `observer:` token for the
ranking point of view with silent fallback to the house perspective; no
score-warming side effects.
"""

__version__ = "0.1.0"
