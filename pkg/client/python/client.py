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


# tests

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