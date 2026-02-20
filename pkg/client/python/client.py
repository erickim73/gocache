import socket

# custom exception hierarchy
class GoCacheError(Exception):
    """Base class for all GoCache client errors"""

class GoCacheConnectionError(GoCacheError):
    """Raised when the client cannot connect to or communicate with the server"""

class GoCacheCommandError(GoCacheError):
    """Raised when the server returns a RESP '-' error response."""

def raw_ping(host: str = "localhost", port: int = 7000) -> None:
    """
    opens a TCP socket, sends a RESP-encoded PING, and prints the raw bytes
    """
    
    # ask OS for a TCP socket
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    
    # perform TCP handshake with the gocache server
    sock.connect((host, port))
    
    # ping encoded as resp
    ping_bytes = b"*1\r\n$4\r\nPING\r\n"
    
    # write bytes into socket
    sock.sendall(ping_bytes)
    
    # block until server responds, read up to 1024 bytes
    response = sock.recv(1024)
    
    # print raw bytes - expect b'+PONG\r\n'
    print(f"Raw response: {response}")
    
    sock.close()
    
def encode_command(*args) -> bytes:
    """
    encodes any command and its argument into RESP array format
    """
    
    # start with array prefix: number of arguments
    parts = [f"*{len(args)}\r\n".encode()]
    
    for arg in args:
        # encode each argument as a string so we can measure byte length
        arg_bytes = str(arg).encode()
        
        # bulk string prefix: $<length>\r\n followed by the data and \r\n
        parts.append(f"${len(arg_bytes)}\r\n".encode())
        parts.append(arg_bytes + b"\r\n")
        
    return b"".join(parts)

def parse_response(data: bytes):
    """
    parse a raw resp response into a native Python value
    """
    value, _ = _parse(data, 0)
    return value

def _parse(data: bytes, pos: int):
    """
    internal recursive parser. returns (parsed_value, new_position)
    """
    type_byte = chr(data[pos])
    pos += 1
    
    if type_byte == "+":
        # simple string: read until \r\n
        return _read_line(data, pos)

    elif type_byte == "-":
        # error: read message, wrap in Exception
        message, pos = _read_line(data, pos)
        return Exception(message), pos
    
    elif type_byte == ":":
        # integer: read line and cast to int
        line, pos = _read_line(data, pos)
        return int(line), pos
    
    elif type_byte == "$":
        # bulk string: read length prefix, then read exactly that many bytes
        length_str, pos = _read_line(data, pos)
        length = int(length_str)
        
        if length == -1:
            # null bulk string
            return None, pos
    
        # read exactly 'length' bytes, then skip trailing \r\n
        value = data[pos : pos + length].decode()
        pos += length + 2
        return value, pos

    elif type_byte == "*":
        # array: read count, then recursively parse that many elements
        count_str, pos = _read_line(data, pos)
        count = int(count_str)
        
        if count == -1:
            # null array
            return None, pos
        
        items = []
        for _ in range(count):
            item, pos = _parse(data, pos)
            items.append(item)
        return items, pos
    
    else:
        raise ValueError(f"Unknown RESP type byte: {type_byte!r} at position {pos - 1}")
    
def _read_line(data: bytes, pos: int) -> tuple[str, int]:
    """
    read bytes from 'pos' until \r\n, returns the decoded string and position after the \r\n terminator
    """
    end = data.index(b"\r\n", pos)
    line = data[pos:end].decode()
    return line, end + 2


class GoCacheClient:
    """
    A client for GoCache server
    
    Usage:
        # Direct instantiation
        client = GoCacheClient("localhost", 6379)
        client.set("key", "value")
        client.close()

        # Preferred — context manager closes the socket automatically
        with GoCacheClient("localhost", 6379) as client:
            client.set("key", "value")
            print(client.get("key"))
    """
    def __init__(self, host: str = "localhost", port: int = 7000) -> None:
        # store connection parameters so close() and reconnect logic know where to re-establish socket
        self.host = host
        self.port = port

        # create tcp socket and connect
        self._sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        
        # wrap connect() so a refused connection surfaces a GoCacheConnectionError
        try:
            self._sock.connect((host, port))
        except ConnectionRefusedError:
            raise GoCacheConnectionError(
                f"Could not connection to GoCache at {host}:{port}. "
                "Is the server running?"
            )
            
        # a bytearray buffer that accumulates raw bytes from the socket
        self._buffer = bytearray()
    
    def close(self) -> None:
        """
        Close the TCP connection and release the socket
        """
        self._sock.close()
        
    def __enter__(self):
        """
        Called when entering a 'with' block
        """
        return self
    
    def __exit__(self, exc_type, exc_val, exc_tb) -> bool:
        """
        Called when leaving a 'with' block, whether normally or via an exception
        """
        self.close()
        return False
    
    # private socket methods
    
    def _send(self, *args) -> None:
        """
        Serialize a command to RESP and write it to the socket
        """
        try:
            self._sock.sendall(encode_command(*args))
        except OSError as e:
            raise GoCacheConnectionError(f"Failed to send command to server: {e}") from e
        
    def _read_response(self):
        """
        Read a complete RESP message from the socket and return a parsed Python value
        """
        # drain socket until we have full resp message
        self._fill_buffer_until_complete()
        
        # parse whatever complete message is at the front of the buffer
        value, consumed = _parse(bytes(self._buffer), 0)
        
        # discard the bytes we just consumed
        del self._buffer[:consumed]
        
        # if server returned a resp error, parser gives us an Exception object
        if isinstance(value, Exception):
            raise GoCacheCommandError(str(value))
        
        return value
    
    def _fill_buffer_until_complete(self) -> None:
        """
        Keep reading from socket until self._buffer contains exactly one complete resp message
        """
        
        while True:
            # try to parse what we have so far
            try:
                _parse(bytes(self._buffer), 0)
                return # parse succeeded -> buffer contains a complete message
            except (ValueError, IndexError):
                pass # incomplete data - fall through to read more bytes

            # read next chunk from socket
            try:
                chunk = self._sock.recv(4096)
            except OSError as e:
                raise GoCacheConnectionError(f"Connection lost while reading: {e}") from e

            # an empty recv() means the server closed the connection
            if not chunk:
                raise GoCacheConnectionError(
                    "Server closed the connection unexpectedly."
                )
                
            self._buffer.extend(chunk)
            
    # public command methods
    
    def ping(self) -> str:
        """
        Send PING. Returns 'PONG' on a healthy connection
        """
        self._send("PING")
        return self._read_response()

    def get(self, key: str):
        """
        Get the value for a key. Returns the string value, or None if the key doesn't exist
        """
        self._send("GET", key)
        return self._read_response()
    
    def set(self, key: str, value: str, ex: int | None = None) -> str:
        """
        Set a key to a value. Returns 'OK' on success
        """
        # conditionally append EX only when a TTL is specified
        if ex is not None:
            self._send("SET", key, value, "EX", ex)
        else:
            self._send("SET", key, value)
        return self._read_response()
    
    def delete(self, *keys: str) -> int:
        """
        Delete one or more keys. Returns the number of keys that were deleted
        """
        self._send("DEL", *keys)
        return self._read_response()
    
# ============== tests =============================

if __name__ == "__main__":
    print("=== Encoder Tests ===")
    
    result = encode_command("PING")
    expected = b"*1\r\n$4\r\nPING\r\n"
    print(f"PING:           {result}")
    print(f"Correct: {result == expected}\n")
    
    result = encode_command("SET", "key", "value")
    expected = b"*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"
    print(f"SET key value:  {result}")
    print(f"Correct: {result == expected}\n")
    
    result = encode_command("GET", "key")
    expected = b"*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"
    print(f"GET key:        {result}")
    print(f"Correct: {result == expected}\n")
    
    print("=== Parser Tests ===")

    # simple string
    val = parse_response(b"+PONG\r\n")
    print(f"+PONG\r\n        → {val!r}  (expected 'PONG', correct: {val == 'PONG'})")

    # error
    val = parse_response(b"-ERR unknown command\r\n")
    print(f"-ERR ...        → {val!r}  (expected Exception, correct: {isinstance(val, Exception)})")

    # integer
    val = parse_response(b":42\r\n")
    print(f":42\r\n          → {val!r}  (expected 42, correct: {val == 42})")

    # bulk string
    val = parse_response(b"$5\r\nhello\r\n")
    print(f"$5\r\nhello\r\n   → {val!r}  (expected 'hello', correct: {val == 'hello'})")

    # null bulk string (missing key)
    val = parse_response(b"$-1\r\n")
    print(f"$-1\r\n          → {val!r}  (expected None, correct: {val is None})")

    # array
    val = parse_response(b"*3\r\n$3\r\nfoo\r\n$3\r\nbar\r\n$3\r\nbaz\r\n")
    print(f"*3 array        → {val!r}  (expected ['foo','bar','baz'], correct: {val == ['foo','bar','baz']})")

    # null array
    val = parse_response(b"*-1\r\n")
    print(f"*-1\r\n          → {val!r}  (expected None, correct: {val is None})")

    # raw PING (requires live GoCache server) ---
    print("\n=== Raw PING (requires running GoCache server) ===")
    try:
        raw_ping()
    except ConnectionRefusedError:
        print("Server not running — skipping live test.")
        
    
    # --- Test GoCacheClient class
    print("\n=== GoCacheClient class tests (requires running GoCache server) ===")
    try:
        with GoCacheClient("localhost", 6379) as client:
            # ping
            result = client.ping()
            print(f"ping()              → {result!r}  (correct: {result == 'PONG'})")
            
            # set() and get()
            client.set("phase2:key", "hello")
            result = client.get("phase2:key")
            print(f"get(existing key)   → {result!r}  (correct: {result == 'hello'})")
            
            # get() on missing key returns None
            result = client.get("phase2:missing")
            print(f"get(missing key)    → {result!r}  (correct: {result is None})")
            
            # delete() returns count of deleted keys
            result = client.delete("phase2:key")
            print(f"delete(1 key)       → {result!r}  (correct: {result == 1})")
            
            # delete() of already-gone key returns 0
            result = client.delete("phase2:key")
            print(f"delete(missing)     → {result!r}  (correct: {result == 0})")
            
            # server error becomes GoCacheCommandError
            try:
                client._send("INVALID_CMD")
                client._read_response()
            except GoCacheCommandError as e:
                print(f"server error raised  → GoCacheCommandError: {e}  ✓")
                
        # connection refused becomes GoCacheConnectionError
        try:
            GoCacheClient("localhost", 19999) # Nothing listening here
        except GoCacheConnectionError as e:
            print(f"bad connect raised   → GoCacheConnectionError: {e}  ✓")

    except GoCacheConnectionError:
        print("Server not running — skipping GoCacheClient tests.")
