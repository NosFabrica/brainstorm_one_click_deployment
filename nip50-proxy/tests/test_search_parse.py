"""Unit tests for the NIP-50 search-string parser.

The parser must tokenize identically to brainstorm.world (tapestry) so the same
clients work against both relays. Run with `python -m tests.test_search_parse`
(from the nip50-proxy/ dir) or `pytest`.
"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from nip50_proxy.search import parse_search_string  # noqa: E402


def test_plain_query():
    assert parse_search_string("vitor pamplona") == ("vitor pamplona", None)


def test_observer_extracted_and_stripped():
    q, obs = parse_search_string("jack observer:abc123")
    assert q == "jack"
    assert obs == "abc123"


def test_observer_can_be_npub():
    q, obs = parse_search_string("alice observer:npub1xyz")
    assert q == "alice"
    assert obs == "npub1xyz"


def test_sort_and_filter_stripped_but_ignored():
    q, obs = parse_search_string(
        "vitor observer:abc sort:followers:desc filter:rank:gte:2"
    )
    assert q == "vitor"
    assert obs == "abc"


def test_empty_observer_value_is_none():
    q, obs = parse_search_string("hello observer:")
    assert q == "hello"
    assert obs is None


def test_empty_string():
    assert parse_search_string("") == ("", None)


def test_only_observer_token_yields_empty_query():
    q, obs = parse_search_string("observer:abc")
    assert q == ""
    assert obs == "abc"


def test_extra_whitespace_collapsed():
    q, obs = parse_search_string("  foo   bar  ")
    assert q == "foo bar"
    assert obs is None


def _run():
    tests = [v for k, v in sorted(globals().items()) if k.startswith("test_")]
    failures = 0
    for t in tests:
        try:
            t()
            print(f"ok   {t.__name__}")
        except AssertionError as exc:
            failures += 1
            print(f"FAIL {t.__name__}: {exc}")
    print(f"\n{len(tests) - failures}/{len(tests)} passed")
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(_run())
