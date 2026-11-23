# teltonika-sim

A Teltonika GPS tracker simulator that behaves like an FMB920 on the wire.

It is a test tool for **any** server that speaks the Teltonika TCP protocol. It opens real TCP
connections, sends a real IMEI handshake, encodes real Codec 8 / Codec 8 Extended AVL packets with
a real CRC-16/IBM, waits for real ACKs, keeps un-ACKed records in an in-memory flash buffer, answers
Codec 12 GPRS commands, and reconnects with exponential backoff — so it exercises your server's
handshake, framing, ACK and reconnect logic rather than a mock of it.

It also ships a minimal reference receiver (`serve`), so you can sanity-check the simulator itself,
and offline `encode` / `decode` tools for building fixtures and reading captures.

## Install

```sh
go install github.com/astianmuchui/teltonika-sim/cmd/teltonika-sim@latest
```

Or from a checkout:

```sh
go build -o teltonika-sim ./cmd/teltonika-sim   # or: make build
```

Go 1.23 or newer. The only third-party dependency is `github.com/alim-zanibekov/teltonika`, used
for its `ioelements` definitions table (so IO element byte sizes are never hardcoded) and, in the
tests, as an independent decoder to check our encoder's bytes against.

## Quick start

Two terminals:

```sh
teltonika-sim serve --listen :5027
teltonika-sim run --server 127.0.0.1:5027 --devices 10
```

Or both at once, with a summary from each at the end:

```sh
make run-local                      # 10 devices against a local receiver for 60s
DEVICES=50 DURATION=5m make run-local
```

## What goes over the wire

```
handshake   device -> [uint16 len][IMEI ascii]          e.g. 000F "356307042441013"
            server -> 0x01 accept | 0x00 reject

AVL packet  [00000000][4B data length][codec id][N records][records...][N records][4B CRC]
            CRC-16/IBM (ARC), poly 0xA001 reflected, init 0x0000, over codec id .. N records 2
            server -> 4B uint32 = number of records accepted

record      [8B unix ms][1B priority][15B gps][io elements]
            gps: lon int32 1e-7, lat int32 1e-7, alt int16 m, angle uint16, sat uint8, speed uint16
            codec 8  io: 1B event id, 1B total, then 1/2/4/8 byte groups with 1B counts and 1B ids
            codec 8e io: 2B event id, 2B total, then 1/2/4/8 byte groups plus a variable length
                         group, all counts, ids and lengths 2B

codec 12    [00000000][4B len][0x0C][0x01][type][4B size][text][0x01][4B CRC]
            type 0x05 command (server to device), 0x06 response (device to server)
```

Both the encoder and the decoder are written here from the spec; the library's encoder is never
called. The test suite checks our bytes against the library's decoder for Codec 8, 8E and 8E with
a variable length element, and re-encodes the Teltonika reference packet byte for byte.

One protocol ambiguity worth knowing: an ACK of zero records and the preamble of a Codec 12 frame
are both four zero bytes. The device reads four bytes, and if they are zero it waits 500 ms for a
length word. If that arrives it is a command; if nothing arrives it is treated as an ACK of zero
records, which is an ACK mismatch, so the device disconnects and resends.

## Subcommands

### `run` — simulate devices against a server

```sh
teltonika-sim run --server host:5027 --devices 50 --codec 8e --batch 10 --fragment
```

One goroutine per device: connect, handshake, send loop, disconnect, backoff, reconnect. Records
stay in the flash buffer until the ACK count matches the records sent. Each connection has a single
writer goroutine, so ACK flow and Codec 12 responses never interleave bytes.

| flag | default | meaning |
| --- | --- | --- |
| `--server` | `127.0.0.1:5027` | server to connect to |
| `--devices` | `1` | number of simulated devices |
| `--imei` | | use this one IMEI instead of generating them |
| `--imei-prefix` | `35630704` | prefix for generated IMEIs; the rest is a counter plus a Luhn check digit |
| `--codec` | `8e` | `8` or `8e` (8E is the FMB920 default) |
| `--batch` | `1` | maximum records per packet |
| `--interval` | `10s` | record interval while moving |
| `--interval-stopped` | `60s` | record interval while stopped |
| `--ack-timeout` | `30s` | how long to wait for an ACK before disconnecting |
| `--flush-timeout` | `5s` | deadline for the final flush on shutdown |
| `--buffer-max` | `10000` | flash buffer capacity in records; oldest dropped with a warning |
| `--backoff-min` / `--backoff-max` | `1s` / `60s` | reconnect backoff bounds, with jitter |
| `--dial-timeout` | `10s` | TCP connect and handshake timeout |
| `--route` | | GPX file to play back instead of a random walk |
| `--loop` | `false` | restart the GPX track when it ends |
| `--center` | `-1.2864,36.8172` | random walk centre, `lat,lon` |
| `--radius-km` | `5` | random walk radius |
| `--max-speed` | `60` | random walk top speed in km/h |
| `--altitude` | `1670` | base altitude in metres, drifts by 20 m |
| `--model` | `FMB920` | device model used to look up IO element sizes |
| `--operator` | `63902` | active GSM operator code, IO element 241 |
| `--with-nx` | `false` | add a variable length beacon element (8E only) |
| `--io-profile` | | JSON file overriding which IO elements are sent and their ranges |
| `--seed` | `1` | random seed; the same seed replays the same run |
| `--duration` | `0` | stop after this long; 0 runs until interrupted |
| `--summary-every` | `10s` | how often to print the summary table |
| `--metrics` | | serve Prometheus metrics, e.g. `:9100` |
| `--verbose` | `false` | debug logging plus a hex dump of every packet sent and every byte read |
| `--log-json` | `false` | log in JSON instead of text |

Chaos flags, all off by default:

| flag | meaning |
| --- | --- |
| `--fragment` | write each packet in random 1–64 byte chunks with 0–20 ms gaps |
| `--coalesce` | write two queued packets in a single `Write` call |
| `--bad-crc-rate 0.05` | corrupt the CRC on 5% of packets; a correct server must not ACK them |
| `--drop-rate 0.02` | cut the connection mid-packet |
| `--offline 5m` | after the first ACK, go offline for 5m while still generating records, then reconnect and flush the backlog |
| `--clock-skew 2h` | shift record timestamps |
| `--no-fix-rate 0.1` | 10% of records with no GNSS fix (satellites 0, lat/lon 0, speed 0) |
| `--garbage` | send an HTTP GET instead of an IMEI, then exit |
| `--slow-reader` | delay reading ACKs by 0.5–2s to exercise server write deadlines |

### `serve` — minimal reference receiver

```sh
teltonika-sim serve --listen :5027 --ack-wrong-rate 0.05 --ack-delay 2s \
                    --send-command getinfo --command-every 30s
```

Accepts the handshake, decodes packets with our decoder, validates the CRC (**no ACK on a bad
CRC**), ACKs with the record count, logs every record, and prints the same summary table as `run`.

| flag | default | meaning |
| --- | --- | --- |
| `--listen` | `:5027` | address to listen on |
| `--reject-imei` | | IMEI to reject at the handshake; repeatable or comma separated |
| `--ack-wrong-rate` | `0` | fraction of packets ACKed with the wrong record count |
| `--ack-delay` | `0` | wait this long before ACKing |
| `--send-command` | | Codec 12 command to send to every device |
| `--command-every` | `30s` | how often to send it |
| `--disconnect-rate` | `0` | fraction of packets after which the connection is dropped |
| `--seed` | `1` | random seed for the fault injection |
| `--duration`, `--summary-every`, `--metrics`, `--verbose`, `--log-json` | | as for `run` |

### `decode` — read a captured packet

```sh
teltonika-sim decode 00000000000000A98E02...BF30
teltonika-sim decode -f capture.bin --json
```

Takes hex as an argument or `-f file` (raw bytes, or a file of hex). Prints every frame in the
input, Codec 12 included, names each IO element from the definitions table and applies its
multiplier, and shows the ACK the server should send. `--json` prints the same thing as JSON.

### `encode` — print one packet as hex

```sh
teltonika-sim encode --lat -1.2921 --lon 36.8219 --speed 42 --angle 90 --codec 8e --with-nx
teltonika-sim encode --lat -1.29 --lon 36.82 --io 239=1,66=12500 --plain
```

Builds a single-record packet with a full FMB920 IO set. `--io id=value,...` overrides or adds
elements (sizes come from the definitions table), `--event` sets the event IO id, `--time` takes
RFC3339 or Unix milliseconds, `--no-fix` clears the GPS element, and `--plain` prints only the hex.

## Examples

**One device, verbose, against your own server**

```sh
teltonika-sim run --server 10.0.0.5:5027 --imei 356307042441013 --verbose
```

**500-device load test with batching and metrics**

```sh
teltonika-sim run --server 10.0.0.5:5027 --devices 500 --batch 10 \
                  --interval 5s --metrics :9100
curl -s localhost:9100/metrics | grep records
```

**Offline backlog: build up records, then flush them in big packets**

```sh
teltonika-sim run --server 127.0.0.1:5027 --devices 5 --offline 2m --batch 50 --interval 5s
```

Each device sends normally, then after its first ACK goes offline for 2 minutes while still
generating records, then reconnects and drains the backlog 50 records at a time. The summary's
`records buffered` row shows the backlog growing and emptying.

**Chaos run**

```sh
teltonika-sim run --server 127.0.0.1:5027 --devices 20 \
                  --fragment --coalesce --bad-crc-rate 0.05 --drop-rate 0.02 \
                  --no-fix-rate 0.1 --clock-skew 2h --slow-reader
```

A healthy server should show: no ACKs for the corrupted packets, clean recovery from mid-packet
disconnects, and no decode errors from fragmentation or coalescing.

**Codec 12 round trip through the reference receiver**

```sh
teltonika-sim serve --listen :5027 --send-command getinfo --command-every 10s
teltonika-sim run --server 127.0.0.1:5027 --devices 1 --verbose
```

The receiver logs `command sent`, the device logs `command received` then `command answered`, and
the receiver logs the `codec 12 message` it got back. Devices answer `getinfo`, `getver`, `getgps`,
`getio` and `setdigout <x>` (`setdigout 10` replies `DOUT1:1 DOUT2:0`); anything else gets
`Unknown command`.

**Decode a captured packet**

```sh
teltonika-sim decode -f capture.bin
teltonika-sim encode --lat -1.29 --lon 36.82 --plain > fixture.hex
teltonika-sim decode -f fixture.hex --json | jq '.[0].records[0].io'
```

**Play back a GPX track**

```sh
teltonika-sim run --server 127.0.0.1:5027 --route examples/nairobi-loop.gpx --loop --devices 4
```

With several devices the track is shared and each device starts at an even offset along it, so they
do not stack on top of each other.

## Movement

Without `--route`, each device does a random walk around `--center` inside `--radius-km`: speed
0–60 km/h with mean reversion to a cruising target, smooth turns, no teleporting, and a hard
guarantee that it never leaves the radius (it steers back from 80% of the radius outward and refuses
any step that would cross it). Devices take random stops (ignition on, movement 0, 30–180 s) and
occasional ignition-off parking (5–20 min). Altitude drifts ±20 m around `--altitude`, satellites
are 8–14 with a fix, and HDOP is 0.6–1.5.

With `--route file.gpx`, the track is played back at its own speed: position is interpolated along
the great circle between trackpoints, and bearing and speed come from consecutive points. `--loop`
restarts at the end; without it the device parks at the last point. Tracks without usable `<time>`
elements are played at a 50 km/h cruise, which the startup log tells you about.

## IO elements

Every device sends an FMB920-shaped set, and **each element's byte size comes from the
`ioelements` definitions for `--model`** — nothing is hardcoded, and an unknown ID is a startup
error rather than a malformed packet.

| ID | element | ID | element |
| --- | --- | --- | --- |
| 239 | Ignition | 67 | Battery Voltage (mV, 3900–4100) |
| 240 | Movement | 68 | Battery Current (mA) |
| 21 | GSM Signal (0–5) | 113 | Battery Level (%) |
| 200 | Sleep Mode | 16 | Total Odometer (m, from actual distance travelled) |
| 69 | GNSS Status | 241 | Active GSM Operator (`--operator`) |
| 181 | GNSS PDOP (×10) | 205 | GSM Cell ID |
| 182 | GNSS HDOP (×10) | 206 | GSM Area Code |
| 66 | External Voltage (mV, 12000–14400 with ignition on, ~12400 off) | | |

An ignition change produces an immediate extra record with event IO ID 239 and priority 1.
`--with-nx` adds one variable length element (ID 385, random beacon payload) on Codec 8E.

`--io-profile file.json` overrides the set. Values are raw wire values, exactly what `decode`
shows in the `raw` column:

```json
{
  "replace": false,
  "drop": [205, 206],
  "elements": [
    {"id": 66, "min": 11800, "max": 12200},
    {"id": 1, "min": 0, "max": 1}
  ],
  "beacon": {"id": 385, "min_bytes": 8, "max_bytes": 48}
}
```

`drop` removes elements, `elements` replaces an element's value with a random value in `[min, max]`
(adding the element if it was not in the default set), and `replace: true` sends *only* the listed
elements. Every ID must exist in the definitions for the model. See `examples/io-profile.json`.

## Output

One structured log line per event: connected, handshake ok/rejected, packet sent (records, bytes),
ack ok / mismatch / timeout, reconnect, command received/answered. `--verbose` adds a hex dump of
every packet sent and every byte received.

Every `--summary-every` (default 10s) and once on exit, both subcommands print the same table:

```
---- teltonika-sim run ---- uptime 1m0s ----
  devices connected           0/10          handshakes ok/rejected    10/0
  records generated           60            records received          0
  records sent                60            records acked             60
  records buffered            0             records dropped           0
  resends                     0             packets sent              60 (6.5 KiB)
  packets received            60 (240 B)    crc corrupted on purpose  0
  packets with bad crc        0             acks sent/wrong           0/0
  ack mismatches              0             ack timeouts              0
  reconnects                  0             disconnects               10
  connect failures            0             commands sent             0
  commands received/answered  0/0           ack latency p50/p99       768µs/1.35ms (60 samples)
```

`serve` prints the same table from its side, where `records received` and `acks sent` carry the
counts, so the two can be reconciled directly.

`--metrics :9100` exposes the same counters at `/metrics` in Prometheus text format
(hand written, no client library), labelled by role:

```
teltonika_sim_records_acked_total{role="run"} 60
teltonika_sim_ack_latency_seconds{role="run",quantile="0.5"} 0.000768
```

On SIGINT (or when `--duration` elapses) devices stop generating, attempt a final flush with a 5s
deadline, and close.

## Tests

```sh
go vet ./... && go test ./...      # or: make check
```

- CRC-16 over the Teltonika reference packet is `0xBF30`, and `CRC16("123456789")` is the
  CRC-16/ARC check value `0xBB3D`.
- Our decoder reads the reference packet correctly, and matches the library's decoder field by field.
- Round trip: random records for Codec 8, 8E and 8E with a variable length group, through our
  encoder, then through both the library's decoder and ours — every field equal, lat/lon within 1e-7.
- Re-encoding the reference packet's decoded records reproduces it byte for byte.
- Codec 12 command and response round trip, including the published `getinfo` frame
  `000000000000000F0C010500000007676574696E666F0100004312`.
- The Luhn IMEI generator produces valid, unique 15-digit IMEIs.
- The device state machine against an in-process fake server: handshake, ACK drains the buffer,
  wrong ACK resends the same records after reconnecting, `0x00` rejection backs off and retries,
  and a corrupted CRC gets no ACK and is resent with a good one.
- End to end: `serve` and `run` in process, 20 devices, `--fragment` and `--coalesce`, 5 seconds,
  then ACKed == generated with zero resends.
- The random walk never leaves its radius, and GPX speed matches distance over time.

The race detector needs cgo and a C toolchain; where one is available, `go test -race ./...` is
worth running too.

## Layout

```
cmd/teltonika-sim/   subcommands and flags, wiring only
internal/codec/      codec 8 / 8e / 12 encode and decode, CRC-16, framing, handshake, ACK
internal/device/     per-device connection state machine, flash buffer, Luhn IMEIs, commands
internal/route/      GPX playback and random walk
internal/io/         IO element generation from the definitions table
internal/receiver/   the reference server behind `serve`
internal/stats/      counters, summary table, Prometheus exposition
```

No global state; every package takes a config struct. Logging is `log/slog` throughout.
