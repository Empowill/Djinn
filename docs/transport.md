# Transport

How the window, the browser and the command line reach the djinn server, and why. macOS and Linux get the
fastest path their webview and kernel allow; Windows gets a path that works.

## The choice, by system

| Who                     | macOS, Linux                                          | Windows, and `djinn up --browser` |
| ----------------------- | ----------------------------------------------------- | --------------------------------- |
| Window                  | Wails asset server (`wails://`), in process, no port  | loopback HTTP/1.1, token, Origin  |
| Command line, agents    | Unix socket `$DJINN_HOME/djinn.sock` (0600), HTTP/1.1 | loopback HTTP/1.1, bearer token   |
| Also on the Unix socket | h2c (HTTP/2 without TLS, prior knowledge)             | —                                 |
| Format                  | binary Protobuf (window and command line)             | binary Protobuf                   |
| Compression             | none                                                  | none                              |

- **`wails://` on macOS and Linux.** WebKit hands each chunk of a custom-scheme response to the page as it comes:
  a stream reaches the window message by message, with no port to guard. WebView2 delivers a custom-scheme
  response only once complete, so Windows goes through loopback HTTP (`server.TransportFor`).
- **No compression.** connect-go compresses any response whose client accepts gzip, and both connect-go clients
  (the command line) and browsers (on every `fetch`) say they do. Every client is on the same machine: gzip only
  costs CPU on both ends. `server.Handler` hides `Accept-Encoding` and `Connect-Accept-Encoding` from the
  services, so they answer in the clear; a compressed request is still read. WebKit sends no `Accept-Encoding` on
  `wails://`: the window path was never compressed.
- **Binary Protobuf in the window** (`useBinaryFormat` in `shim/djinn.ts`). The saved workspace is a JSON
  document inside a string field: JSON escapes it again, binary carries it as bytes. The command line already used
  binary (connect-go's default).
- **h2c on the Unix socket, HTTP/1.1 for the command line.** The socket speaks both (`server.Serve` sets
  `http.Protocols` on a Unix listener only): enabling h2c costs an HTTP/1.1 client nothing, and it is what a
  bidirectional Connect stream needs, for an agent that talks back. The command line stays on HTTP/1.1, faster for
  a call and for a server stream. The loopback TCP server speaks HTTP/1.1 only: no browser speaks h2c.
- **The `wails://` handler** (Wails v3.0.0-beta.28, `internal/assetserver/webview`): every request gets its own
  goroutine, so a long stream starves nothing; each `Write` goes straight to a pipe that WebKitGTK reads (Linux)
  or to `didReceiveData` (macOS), and `Flush` has nothing to do. connect-go writes a message in two writes (5-byte
  prefix, then payload), but the page reads them together: 100 values took 100 reads. Nothing to buffer there.
  It answers 501 to a request whose handler wrote neither a header nor a byte, where net/http answers 200; an empty
  message in binary Protobuf is no byte at all, and connect-go then writes nothing (`LoadState` with nothing saved,
  `SaveState`). `server.Handler` writes the 200 itself for the services.

## Measurements

Machine: Intel Core i7-1360P (16 threads), Ubuntu 22.04, Go 1.26.7, WebKitGTK 2.50.4, Wails v3.0.0-beta.28,
connect-go 1.21.0, connect-web 2.2. The machine was shared with other builds: absolute numbers vary up to twice
from one run to the next; the ratios hold. Not measured on macOS.

**Over the Unix socket** — `go tool task bench` (`internal/server/transport_bench_test.go`), median of
`-count 3`. A unary echo whose request and response are a saved workspace of the given size; a cold call is a new
connection and one small call, as one command line does; a stream is 1000 messages of 256 bytes.

| Time per call       | HTTP/1.1 + gzip (before) | **HTTP/1.1** (after) | h2c + gzip | h2c     | h2c, JSON |
| ------------------- | ------------------------ | -------------------- | ---------- | ------- | --------- |
| unary 1 KiB         | 348 µs                   | **134 µs**           | 538 µs     | 256 µs  | 289 µs    |
| unary 32 KiB        | 875 µs                   | **415 µs**           | 1136 µs    | 580 µs  | 1348 µs   |
| unary 512 KiB       | 5.5 ms                   | **3.2 ms**           | 6.7 ms     | 4.4 ms  | 16.1 ms   |
| cold call           | 574 µs                   | **312 µs**           | 917 µs     | 382 µs  |           |
| stream, per message | 223 µs                   | **5.7 µs**           | 176 µs     | 13.6 µs |           |

Compression about doubles a call and multiplies the cost of a stream message by 13 to 40 (over several runs):
each message is compressed on its own. JSON costs up to 3.6 times binary in Go, on large messages. h2c costs 1.2
to 2.4 times HTTP/1.1.

**In the window** — `go tool task check-window -- -bench` (add `-path http` for the loopback path). The page
times, on its own clock, 200 echoes (20 at 512 KiB) and gives the mean; three runs each, median shown. WebKit
rounds `performance.now()` to the millisecond, hence the long runs.

| Mean round trip | `wails://` JSON | **`wails://` binary** | HTTP JSON + gzip | HTTP binary + gzip | HTTP binary |
| --------------- | --------------- | --------------------- | ---------------- | ------------------ | ----------- |
| 1 KiB           | 0.76 ms         | **0.59 ms**           | 0.81 ms          | 0.76 ms            | 0.61 ms     |
| 32 KiB          | 1.86 ms         | **0.98 ms**           | 3.13 ms          | 2.05 ms            | 1.91 ms     |
| 512 KiB         | 16.7 ms         | **6.3 ms**            | 19.1 ms          | 12.6 ms            | 9.6 ms      |

Binary halves the round trip of a workspace of 32 KiB or more; a 1 KiB call is within the noise. A burst of 5000
messages of 256 bytes takes 22 to 28 µs a message on `wails://` (the page reads them in about 10 reads), against
130 µs on loopback HTTP (about two reads a message), in either format.

## Set aside

- **h2c for the command line**: slower for what it does today (above). It comes back with the first bidirectional
  method: a client that needs one sets `http.Protocols.SetUnencryptedHTTP2` on its transport.
- **Tuning HTTP/2** (larger frames and windows): h2c is not on a hot path.
- **Coalescing the prefix and the payload on `wails://`**: the page already reads them together.
- **Compression above a size** (`connect.WithCompressMinBytes`): even 512 KiB is faster in the clear on one
  machine.
- **Windows**: nothing specific beyond the above, which applies there too.

## Protected by

`internal/server/transport_test.go`: the Unix socket speaks HTTP/1.1 and h2c and carries a bidirectional stream
message by message (`TestUnixSocketSpeaksH2C`); the loopback server refuses h2c (`TestLoopbackStaysHTTP1`); a
unary call and a stream come back uncompressed though the client asks for gzip (`TestNoCompression`).
`internal/server/server_test.go`: an empty response still gets its 200 (`TestEmptyResponseIsAnswered`).
`tools/windowcheck`: the same, in the native window. `shim/djinn.test.mjs`: the window sends binary Protobuf and
asks for no compression.
