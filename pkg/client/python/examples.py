import time
from gocache import GoCacheClient, GoCacheConnectionError, GoCacheCommandError

# print a section header so output is easy to follow
def section(title: str) -> None:
    print(f"\n{'─' * 50}")
    print(f"  {title}")
    print(f"{'─' * 50}")
    
# ping - verify connection is alive before doing anything else 
def demo_ping(c: GoCacheClient) -> None:
    section("PING - health check")
    
    response = c.ping()
    print(f"  ping()  →  {response!r}")
    # should respond PONG
    assert response == "PONG", f"Expected 'PONG', got {response!r}"
    print("  ✓ server is reachable")
    
# set and get- the core read/write path
def demo_set_get(c: GoCacheClient) -> None:
    section("SET / GET - basic read-write")
    
    # store a value
    result = c.set("user:1000:name", "Eric")
    print(f"  set('user:1000:name', 'Eric')  →  {result!r}")
    assert result == "OK"
    
    # retrieve it back
    value = c.get("user:1000:name")
    print(f"  get('user:1000:name')          →  {value!r}")
    assert value == "Eric"
    
    # get on a key that was never set returns None
    missing = c.get("user:9999:name")
    print(f"  get('user:9999:name')          →  {missing!r}  (key does not exist)")
    assert missing is None
    
    # value survives as-is
    c.set("misc:unicode", "こんにちは")
    assert c.get("misc:unicode") == "こんにちは"
    print("  ✓ Unicode value round-trips correctly")
    
    c.set("misc:spaces", "hello world  with  spaces")
    assert c.get("misc:spaces") == "hello world  with  spaces"
    print("  ✓ Spaces in values round-trip correctly")
    
    # clean up
    c.delete("user:1000:name", "misc:unicode", "misc:spaces")

# DEL - removing keys
def demo_delete(c: GoCacheClient) -> None:
    section("DEL — removing keys")
    
    c.set("temp:a", "1")
    c.set("temp:b", "2")
    c.set("temp:c", "3")
    
    # delete a single key
    n = c.delete("temp:a")
    print(f"  delete('temp:a')                    →  {n}  (1 key removed)")
    assert n == 1
    assert c.get("temp:a") is None
    
    # delete multiple keys in one call
    n = c.delete("temp:b", "temp:c")
    print(f"  delete('temp:b', 'temp:c')          →  {n}  (2 keys removed)")
    assert n == 2
    
    # delete a key that doesn't exist
    n = c.delete("temp:never_existed")
    print(f"  delete('temp:never_existed')        →  {n}  (key did not exist)")
    assert n == 0
    
    # missing existing and non-existing keys
    c.set("temp:exists", "yes")
    n = c.delete("temp:exists", "temp:ghost_1", "temp:ghost_2")
    print(f"  delete(1 real + 2 missing)          →  {n}  (only the real one counted)")
    assert n == 1
    
    print("  ✓ all delete scenarios behave correctly")

# ttl / expiration
def demo_ttl(c: GoCacheClient) -> None:
    section("SET with EX - key expiration")
    
    # set a key with a 2 second ttl
    c.set("session:token", "abc123", ex=2)
    print("  set('session:token', 'abc123', ex=2)  →  key written with 2s TTL")
    
    # immediately readable
    value = c.get("session:token")
    print(f"  get immediately                       →  {value!r}")
    assert value == "abc123", "Key should exist immediately after SET"
    
    # wait for ttl tl elapse
    print("  waiting 3 seconds for key to expire...")
    time.sleep(3)
    
    # key is now gone. GET returns None
    value = c.get("session:token")
    print(f"  get after expiry                      →  {value!r}  (key has expired)")
    assert value is None, f"Key should have expired, but got: {value!r}"

    print("  ✓ key expired on schedule")
    
# error handling 
def demo_errors() -> None:
    section("Error handling")
    
    # connection refused - bad port, nothing listening
    try:
        GoCacheClient("localhost", 19999)
        print("  should have raised GoCacheConnectionError")
    except GoCacheConnectionError as e:
        print(f"  bad connect  →  GoCacheConnectionError: {e}")
        print("  ✓ connection failure surfaces a clear exception")
        
    # server-side resp error
    with GoCacheClient("localhost", 6379) as c:
        try:
            c._send("DEFINITELY_NOT_A_REAL_COMMAND")
            c._read_response()
            print("  should have raised GoCacheCommandError")
        except GoCacheCommandError as e:
            print(f"  bad command  →  GoCacheCommandError: {e}")
            print("  ✓ server errors surface as GoCacheCommandError")
            
            
# entry point
if __name__ == "__main__":
    print("\nGoCache Python Client — End-to-End Demo")
    print("Make sure GoCache is running: go run cmd/server/main.go\n")

    try:
        with GoCacheClient("localhost", 6379) as client:
            demo_ping(client)
            demo_set_get(client)
            demo_delete(client)
            demo_ttl(client)

        # error demos manage their own connections (they test connection failure)
        demo_errors()

        print("\n" + "=" * 50)
        print("  All demos completed successfully")
        print("=" * 50 + "\n")

    except GoCacheConnectionError as e:
        print(f"\nCould not connect to GoCache: {e}")
        print("  Start the server with: go run cmd/server/main.go\n")