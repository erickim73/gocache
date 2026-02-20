import time
import unittest

from gocache import (
    encode_command,
    parse_response,
    GoCacheClient,
    GoCacheCommandError,
    GoCacheConnectionError,
)

KEY_PREFIX = "test:gocache:"

def prefixed(name: str) -> str:
    """Return a namespaced key so test keys are easy to identify and clean up."""
    return f"{KEY_PREFIX}{name}"

def server_available() -> bool:
    """Return True if a GoCache server is reachable on localhost:6379."""
    try:
        with GoCacheClient("localhost", 6379) as c:
            return c.ping() == "PONG"
    except GoCacheConnectionError:
        return False
    
# 1. unit tests
class TestRespEncoder(unittest.TestCase):
    """Tests for encode_command(). No network required."""

    def test_ping_encodes_as_single_element_array(self):
        # PING has no arguments, so it is *1 followed by one bulk string.
        result = encode_command("PING")
        self.assertEqual(result, b"*1\r\n$4\r\nPING\r\n")

    def test_get_encodes_with_one_argument(self):
        result = encode_command("GET", "mykey")
        self.assertEqual(result, b"*2\r\n$3\r\nGET\r\n$5\r\nmykey\r\n")

    def test_set_encodes_with_two_arguments(self):
        result = encode_command("SET", "key", "value")
        self.assertEqual(result, b"*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n")

    def test_del_encodes_multiple_keys(self):
        # DEL with three keys should produce a *4 array (command + 3 keys)
        result = encode_command("DEL", "k1", "k2", "k3")
        self.assertEqual(
            result,
            b"*4\r\n$3\r\nDEL\r\n$2\r\nk1\r\n$2\r\nk2\r\n$2\r\nk3\r\n",
        )

    def test_set_with_ex_encodes_five_element_array(self):
        # SET key value EX 60 is a five-element RESP array
        result = encode_command("SET", "key", "val", "EX", 60)
        self.assertEqual(
            result,
            b"*5\r\n$3\r\nSET\r\n$3\r\nkey\r\n$3\r\nval\r\n$2\r\nEX\r\n$2\r\n60\r\n",
        )

    def test_bulk_string_length_matches_byte_length_not_char_length(self):
        # "こ" is one character but three UTF-8 bytes. The RESP length prefix
        # must reflect bytes, not characters — otherwise the server misreads the frame.
        result = encode_command("SET", "k", "こ")
        # "こ" encodes to 3 bytes in UTF-8
        self.assertIn(b"$3\r\n\xe3\x81\x93\r\n", result)
        
# 2. unit tests
class TestRespParser(unittest.TestCase):
    """Tests for parse_response(). No network required."""

    # simple strings (+)

    def test_simple_string_returns_str(self):
        self.assertEqual(parse_response(b"+OK\r\n"), "OK")

    def test_pong_simple_string(self):
        self.assertEqual(parse_response(b"+PONG\r\n"), "PONG")

    def test_simple_string_with_spaces(self):
        self.assertEqual(parse_response(b"+hello world\r\n"), "hello world")

    # errors (-)

    def test_error_returns_exception_instance(self):
        result = parse_response(b"-ERR unknown command\r\n")
        self.assertIsInstance(result, Exception)

    def test_error_message_is_preserved(self):
        result = parse_response(b"-ERR unknown command\r\n")
        self.assertEqual(str(result), "ERR unknown command")

    # integers (:)

    def test_integer_returns_int(self):
        self.assertEqual(parse_response(b":42\r\n"), 42)

    def test_zero_integer(self):
        self.assertEqual(parse_response(b":0\r\n"), 0)

    def test_negative_integer(self):
        # some RESP responses (e.g. SRANDMEMBER with negative count) use
        # negative integers. The parser should handle these correctly.
        self.assertEqual(parse_response(b":-1\r\n"), -1)

    # bulk strings ($)

    def test_bulk_string_returns_str(self):
        self.assertEqual(parse_response(b"$5\r\nhello\r\n"), "hello")

    def test_bulk_string_empty(self):
        # $0\r\n\r\n is a valid empty bulk string, distinct from null.
        self.assertEqual(parse_response(b"$0\r\n\r\n"), "")

    def test_null_bulk_string_returns_none(self):
        # $-1\r\n is how GoCache/Redis signals a missing key on GET.
        self.assertIsNone(parse_response(b"$-1\r\n"))

    def test_bulk_string_with_spaces(self):
        self.assertEqual(parse_response(b"$11\r\nhello world\r\n"), "hello world")

    # arrays (*)

    def test_array_returns_list(self):
        data = b"*3\r\n$3\r\nfoo\r\n$3\r\nbar\r\n$3\r\nbaz\r\n"
        self.assertEqual(parse_response(data), ["foo", "bar", "baz"])

    def test_empty_array(self):
        self.assertEqual(parse_response(b"*0\r\n"), [])

    def test_null_array_returns_none(self):
        self.assertIsNone(parse_response(b"*-1\r\n"))

    def test_mixed_type_array(self):
        # arrays can contain elements of different RESP types.
        # e.g. *3 with a simple string, integer, and bulk string.
        data = b"*3\r\n+OK\r\n:1\r\n$5\r\nhello\r\n"
        self.assertEqual(parse_response(data), ["OK", 1, "hello"])

    def test_nested_array(self):
        # a RESP array can contain other arrays — used by commands like EXEC.
        # outer *2, inner *2 + *2
        data = b"*2\r\n*2\r\n$1\r\na\r\n$1\r\nb\r\n*2\r\n$1\r\nc\r\n$1\r\nd\r\n"
        self.assertEqual(parse_response(data), [["a", "b"], ["c", "d"]])

    # unknown type byte

    def test_unknown_type_raises_value_error(self):
        with self.assertRaises(ValueError):
            parse_response(b"!bad\r\n")
            
# 3. integration tests

@unittest.skipUnless(server_available(), "GoCache server not reachable on localhost:6379")
class TestGoCacheClient(unittest.TestCase):
    """
    Integration tests that require a running GoCache server.
    Skipped automatically if the server is not up.

    Each test gets a fresh client in setUp() and cleans up its own keys in
    tearDown() so tests are fully independent and leave no residue.
    """

    def setUp(self):
        """open a connection before each test."""
        self.client = GoCacheClient("localhost", 6379)
        # Track every key written so tearDown can delete them all.
        self._keys_written: list[str] = []

    def tearDown(self):
        """delete all keys written during the test, then close the connection."""
        if self._keys_written:
            self.client.delete(*self._keys_written)
        self.client.close()

    def k(self, name: str) -> str:
        """return a prefixed key and register it for cleanup."""
        key = prefixed(name)
        if key not in self._keys_written:
            self._keys_written.append(key)
        return key

    # --- ping ---

    def test_ping_returns_pong(self):
        self.assertEqual(self.client.ping(), "PONG")

    # --- set and get ---

    def test_set_returns_ok(self):
        result = self.client.set(self.k("set_ok"), "value")
        self.assertEqual(result, "OK")

    def test_set_then_get_returns_value(self):
        self.client.set(self.k("roundtrip"), "hello")
        self.assertEqual(self.client.get(self.k("roundtrip")), "hello")

    def test_get_missing_key_returns_none(self):
        # a key that was never written — GET must return None, not raise.
        result = self.client.get(self.k("definitely_missing"))
        self.assertIsNone(result)

    def test_set_overwrites_existing_value(self):
        key = self.k("overwrite")
        self.client.set(key, "first")
        self.client.set(key, "second")
        self.assertEqual(self.client.get(key), "second")

    def test_set_and_get_empty_string(self):
        # empty string is a valid value — must not be confused with None.
        key = self.k("empty_val")
        self.client.set(key, "")
        result = self.client.get(key)
        self.assertEqual(result, "")
        self.assertIsNotNone(result)

    def test_set_and_get_value_with_spaces(self):
        self.client.set(self.k("spaces"), "hello world")
        self.assertEqual(self.client.get(self.k("spaces")), "hello world")

    def test_set_and_get_unicode_value(self):
        self.client.set(self.k("unicode"), "こんにちは")
        self.assertEqual(self.client.get(self.k("unicode")), "こんにちは")

    def test_set_and_get_numeric_string(self):
        # values are always strings — "42" comes back as "42", not 42.
        self.client.set(self.k("numstr"), "42")
        result = self.client.get(self.k("numstr"))
        self.assertEqual(result, "42")
        self.assertIsInstance(result, str)

    # --- delete ---

    def test_delete_existing_key_returns_one(self):
        key = self.k("del_one")
        self.client.set(key, "x")
        n = self.client.delete(key)
        self.assertEqual(n, 1)

    def test_delete_removes_key(self):
        key = self.k("del_removes")
        self.client.set(key, "x")
        self.client.delete(key)
        self.assertIsNone(self.client.get(key))

    def test_delete_missing_key_returns_zero(self):
        n = self.client.delete(self.k("del_missing"))
        self.assertEqual(n, 0)

    def test_delete_multiple_keys_returns_correct_count(self):
        k1, k2, k3 = self.k("del_m1"), self.k("del_m2"), self.k("del_m3_missing")
        self.client.set(k1, "a")
        self.client.set(k2, "b")
        # k3 is intentionally never written
        n = self.client.delete(k1, k2, k3)
        self.assertEqual(n, 2)  # only 2 of the 3 existed

    def test_delete_already_deleted_key_returns_zero(self):
        key = self.k("del_twice")
        self.client.set(key, "x")
        self.client.delete(key)
        n = self.client.delete(key)  # second delete
        self.assertEqual(n, 0)

    # --- TTL / expiration ---

    def test_set_with_ttl_key_exists_immediately(self):
        key = self.k("ttl_immediate")
        self.client.set(key, "ephemeral", ex=10)
        self.assertEqual(self.client.get(key), "ephemeral")

    def test_set_with_ttl_key_expires(self):
        # use a 1-second TTL and wait 2 seconds to confirm expiry.
        # This test is inherently time-dependent but 1s TTL with a 2s wait
        # gives a comfortable margin on any reasonable machine.
        key = self.k("ttl_expires")
        self.client.set(key, "temporary", ex=1)
        self.assertEqual(self.client.get(key), "temporary")  # Still alive
        time.sleep(2)
        self.assertIsNone(self.client.get(key))              # Now gone

    def test_set_without_ttl_key_does_not_expire(self):
        # a plain SET with no EX should persist indefinitely.
        key = self.k("ttl_none")
        self.client.set(key, "persistent")
        time.sleep(1)
        self.assertEqual(self.client.get(key), "persistent")

    # --- error surfaces ---

    def test_bad_command_raises_gocache_command_error(self):
        # the server returns a RESP '-' error for unknown commands.
        # the client must raise GoCacheCommandError, not return an Exception object.
        with self.assertRaises(GoCacheCommandError):
            self.client._send("NOT_A_REAL_COMMAND")
            self.client._read_response()

    def test_connection_refused_raises_gocache_connection_error(self):
        # nothing is listening on port 19999.
        with self.assertRaises(GoCacheConnectionError):
            GoCacheClient("localhost", 19999)

    def test_context_manager_closes_connection(self):
        # after the `with` block, the socket should be closed. A second command
        # on the same client object should fail with an OSError (not silently succeed).
        with GoCacheClient("localhost", 6379) as c:
            c.ping()
        # Socket is now closed — any operation should raise
        with self.assertRaises((OSError, GoCacheConnectionError)):
            c.ping()

    # --- sequential commands on one connection ---

    def test_multiple_commands_on_same_connection(self):
        # verifies the buffer management is correct: each response is consumed
        # cleanly so the next command does not see leftover bytes.
        keys = [self.k(f"seq_{i}") for i in range(5)]
        for i, key in enumerate(keys):
            self.client.set(key, str(i))
        for i, key in enumerate(keys):
            self.assertEqual(self.client.get(key), str(i))
            
            
if __name__ == "__main__":
    unittest.main(verbosity=2)