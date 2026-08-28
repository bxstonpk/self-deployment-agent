import time

import pytest

from mcp_server.idempotency import IdempotencyConflict, IdempotencyStore


def test_unseen_key_returns_none():
    store = IdempotencyStore(ttl_seconds=60)
    assert store.get_cached("k1", ("a", "b")) is None


def test_same_key_same_input_returns_cached_result():
    store = IdempotencyStore(ttl_seconds=60)
    store.store("k1", ("a", "b"), {"result": 1})
    assert store.get_cached("k1", ("a", "b")) == {"result": 1}


def test_same_key_different_input_raises_conflict():
    store = IdempotencyStore(ttl_seconds=60)
    store.store("k1", ("a", "b"), {"result": 1})
    with pytest.raises(IdempotencyConflict):
        store.get_cached("k1", ("a", "different"))


def test_entry_expires_after_ttl():
    store = IdempotencyStore(ttl_seconds=0.05)
    store.store("k1", ("a",), {"result": 1})
    assert store.get_cached("k1", ("a",)) == {"result": 1}
    time.sleep(0.1)
    assert store.get_cached("k1", ("a",)) is None
