# Serial devices — review punch list

**Branch:** `feat/serial-devices` (29 commits, ~8,200 lines)
**Review date:** 2026-09-04
**Status:** not mergeable as-is — 10 P0 items, 11 P1 items
**Companions:** `2026-08-15-serial-devices-design.md` (the contract), `2026-08-15-serial-devices-plan.md`

This file is ephemeral like the other two in `docs/plans/` — do not commit it, and delete
it once the punch list is worked off.

## How to work this list

Items are ordered by priority, and within P0 roughly by dependency. Each item is
self-contained: symptom, location, fix, and what "done" means. Most findings were
confirmed by running probe code, not just by reading — where a probe result is quoted,
reproduce it first so you know you are fixing the real thing, and again after the fix.

Ground rules for every item:

- **Write the companion case** (CLAUDE.md invariant #1). A test asserting one direction
  needs its mirror. Several P0s here exist *because* a one-sided test passed against an
  impossible value (see P0-8).
- **Fixtures come from real captures, not from the implementation.** This branch has now
  shipped two bugs whose unit tests passed against values the system cannot produce
  (P0-8, and the earlier `ioreg` plane bug). If you write a sysfs or ioreg fixture, paste
  real output.
- **Docs are part of the fix.** Three P0s here are code that is weaker than what the guide
  already promises. Fixing the code without correcting the doc, or vice versa, is half a fix.
- After each batch: `make lint` then `make test-unit`. Both must be clean before you move on.

Verification commands used throughout:

```bash
go build ./...
go test -race ./internal/serialbroker/ ./internal/serialtest/ ./internal/rfc2217/ \
  ./internal/serialdev/... ./internal/serialport/ ./internal/config/ ./internal/run/ \
  ./internal/daemon/ ./internal/storage/ ./internal/audit/ ./internal/deps/ ./cmd/moat/cli/
GOOS=linux go vet ./internal/serialdev/... ./internal/serialport/...   # linux-only files
make lint
```

Leave no `zz_probe*` files behind; the tree must be clean except for the three
`docs/plans/` files.

---

# P0 — blockers

## P0-1. The unauthenticated RFC2217 port binds every interface, and its documented access control does not exist

**Severity:** high — remote code execution on the attached hardware from the LAN.

**Where:** `internal/serialbroker/broker.go:66-68, 97-99, 118`; `cmd/moat/cli/daemon.go:169`
(constructs the broker with no `BindAddr`); `internal/daemon/server.go:352`
(`AllowedHostPorts`); `docs/content/guides/18-serial-devices.md:152-155`.

**Symptom (confirmed):** `bindAddr` defaults to `0.0.0.0`, so a listener comes up on
`[::]:<port>`. A probe dialed the host's routable interface address (`192.168.4.25`) and
the connection was accepted. RFC2217 has no authentication, so anyone routable to the
machine can run `esptool --port rfc2217://<host>:<port> write-flash ...` while a run is
active, or connect first and deny the container its device.

The compensating control does not exist. `rc.AllowedHostPorts` is read in exactly one
place — `gatekeeper/proxy.isAllowedHostPort`, reached only from
`checkNetworkPolicyForRequest`, i.e. the HTTP/CONNECT path. RFC2217 is raw TCP and never
transits the proxy, so **adding the serial port to `AllowedHostPorts` is a no-op for
serial reachability.** This makes two claims false:

- `internal/daemon/server.go:316` comment: "That allowance is the access control ... only
  the owning run's container is permitted".
- `docs/content/guides/18-serial-devices.md:152-155`: "only the owning run's container is
  permitted to reach it through moat's network policy".

Same root cause as issue #461.

**Note on why `0.0.0.0` was reasonable-looking:** the credential proxy also binds
`0.0.0.0` (`cmd/moat/cli/daemon.go:254`), because the daemon starts before any run or
container network exists and cannot know the gateway address. The asymmetry is that the
proxy has token auth and the broker has none. So "match the proxy" is not a defence here.

**Fix.** Serial listeners are created per-run at registration time, not at daemon start,
so the gateway address *is* knowable by then — but only by the CLI, which owns the network.

1. Add an additive field to the register request (e.g. `SerialBindAddr string` on
   `RegisterRequest`, `omitempty`) — additive-only keeps the daemon API rule in
   `internal/daemon/api.go` intact.
2. Have the CLI resolve the container-facing host address for the run's runtime and
   network and send it. `internal/container` already has the pieces:
   `dockerNetworkManager.NetworkGateway` (`docker.go:1335`),
   `appleNetworkManager.NetworkGateway` (`apple_network.go:186`), and
   `probeDefaultGateway` (`apple.go:84`).
3. `Broker.Listen` binds that address per listener. Keep `BindAddr` as the fallback, but
   change the default from `0.0.0.0` to something that fails closed rather than open — and
   if the address cannot be resolved, refuse the device with an actionable error instead of
   silently binding wide.
4. Verify reachability on **both** runtimes before calling this done. Docker-on-Linux
   host-net, Docker Desktop, and Apple `container` reach the host by different addresses;
   this is exactly the class of bug that produced commits `8b2daed` and `ac9d4a5`.

**Also required:** correct `docs/content/guides/18-serial-devices.md:152-155` and the
`server.go:316` comment to state the boundary that actually exists. If after the fix the
port is reachable only from the gateway address, say that. Do not claim `AllowedHostPorts`
gates it unless P0-2 makes that true.

**Tests:** assert the listener's bound address is the configured one and *not* wildcard;
companion case — an unresolvable/empty gateway must refuse the device, not bind `0.0.0.0`.

**Done when:** a probe dialing a routable non-gateway interface address is refused, the
container still reaches its device on both runtimes, and the guide describes the real
boundary.

## P0-2. `network.policy: strict` silently blocks the serial port

**Severity:** high — the feature and moat's only enforcing network mode are mutually exclusive.

**Where:** `internal/run/manager_create.go:360` (`FirewallEnabled` iff policy is strict);
`internal/container/docker.go:787,824` and `internal/container/apple.go:575`
(`SetupFirewall` accepts `lo`, ESTABLISHED, UDP/53, TCP to the *proxy* port, then DROPs);
`internal/daemon/server.go:352`.

**Symptom (confirmed by reading):** `SetupFirewall` never receives `AllowedHostPorts`, and
serial listeners are on OS-assigned ephemeral ports that are never added to the ruleset.
A strict run with `devices:` has its connection to `rfc2217://moat-host:<port>` dropped.
No warning, no error — esptool reports a dead port and the user reads it as broken
hardware. It happens to work today on Docker-on-Linux host-net only because `-o lo` is
whitelisted, which is why hardware verification and `examples/serial` (no `policy:`) never
hit it.

**Fix.** Plumb `AllowedHostPorts` into `SetupFirewall` and emit accept rules for those
ports on both runtimes. This is the fix that also gives P0-1 a real container-side
boundary under strict, and it partially addresses #461.

If you judge that too large for this branch, the fallback is to **fail fast**: reject
`devices:` + `network.policy: strict` at the `Create` gate with an error that names the
interaction and the workaround. A silent drop is not an acceptable end state either way.

**Tests:** strict + devices produces accept rules containing the serial port (or a clear
refusal); companion — permissive + devices produces no firewall rules, and strict without
devices is unchanged. Assert on the generated rule set, not on log strings.

**Done when:** a strict run with a device either works or fails with an actionable message,
and `docs/content/guides/18-serial-devices.md` documents the interaction.

## P0-3. The exclusive claim is keyed by config name, not by device identity

**Severity:** high — breaks the one guarantee the claim exists to provide, in both directions.

**Where:** `internal/serialbroker/broker.go:80` (`byDevice map[string]*listener // device
name -> listener`), `:114-116`, `:129`, `:147-148`, `:167-171`.

**Symptom (both directions confirmed by probe):**

- *Two sessions on one physical device.* Two entries with different `name:` and the same
  `match.usb` — in one `moat.yaml` or across two runs — both get listeners and both open
  the port. Probe: run-a/`esp32` and run-b/`board`, same `Device.Path`, both `Listen` calls
  succeeded, `opens=2`, and the device saw `"AAAABBBB"` interleaved from two clients. The
  only backstop is `TIOCEXCL`, so the loser gets
  `claiming exclusive access to /dev/...: resource busy` at connect time — precisely the
  "confusing open failure" the design said to replace
  (design doc, "Exclusive claim": *"A second run gets 'device esp32 is in use by run
  abc123', not a confusing open failure"*). Trivially reachable: `resolveDevices`
  (`internal/run/devices.go:85`) matches purely on `match.usb`, so `- name: flash` and
  `- name: monitor` with one VID:PID resolve to the same node.
- *False conflict on different devices.* run-a/`esp32` → ttyUSB0 and run-b/`esp32` →
  ttyUSB1 (two different boards) is refused: `serial device "esp32" is already in use by
  run run-a`. Generic names collide across unrelated projects.

**Fix.** Key `byDevice` on resolved device identity — the device path at minimum, better
the `serialdev.Device` identity (VID/PID/serial, falling back to port path). Keep `Name`
for messages only. The conflict message must still name the device and the owning run.

**Tests:** two names → one path conflicts (or is deduped to one listener, if you prefer
that semantic — decide and document); companion — one name → two different devices does
*not* conflict; and the existing `TestListenTwiceForTheSameDeviceFails`
(`broker_test.go:379`) needs its missing mirror: after `Revoke`, a *different* run can
claim the device. Assert the message contains the run ID and device name, not just
`err != nil` (see P2-3).

## P0-4. Every device error message is unreachable: the daemon's 409 body is discarded

**Severity:** high — violates CLAUDE.md "Error Messages"; makes P0-3's and P0-5's
diagnostics invisible.

**Where:** `internal/daemon/server.go:289`
(`writeJSON(w, http.StatusConflict, RegisterResponse{Error: err.Error()})`) vs
`internal/daemon/client.go:71` (`return nil, fmt.Errorf("daemon returned %d", resp.StatusCode)`
before the body is decoded).

**Symptom (confirmed by probe):** final user-facing output is
`creating run: registering run with proxy daemon: daemon returned 409`. The documented
message (`docs/content/guides/18-serial-devices.md:163`:
`Error: serial device "board" is already in use by run ...`) and the `moat proxy restart`
hint for a broker-less daemon are both unreachable.

**Fix.** Decode `RegisterResponse` on non-2xx and surface `regResp.Error` when present,
falling back to the status code when the body is empty or unparseable. Note the
established convention elsewhere in this codebase is 2xx + `Error` field, which
`manager_create.go` already reads — either conform `listenSerial` to that convention or
teach the client to read the body on 409. Apply the same treatment to the other
`daemon returned %d` sites (`client.go:100,117,157,174`) only if it is safe; the register
path is the required one.

**Tests:** a 409 with an `Error` body surfaces that text; companion — a 409 with an empty
body still produces a sane error mentioning the status.

## P0-5. A crashed CLI leaks the device claim permanently, with no escape hatch

**Severity:** high — device unusable until `moat proxy restart`; compounds with reaper flakiness.

**Where:** `serialBroker.Revoke` has exactly three call sites, all inside the
register/unregister HTTP handlers (`internal/daemon/server.go:342,349,426`).
`LivenessChecker` (`internal/daemon/liveness.go:110`) holds no broker reference, and the
`SetOnCleanup` hook it is given only closes the run/audit stores.
`cmd/moat/cli/daemon.go:355`.

**Symptom (confirmed by reading):** `moat run` with `devices:`, then `kill -9` the CLI (or
the container dies while the CLI is gone). Within 30s the liveness checker unregisters the
run, but the listener and the claim survive. Every later run fails with
`serial device "board" is already in use by run <dead-id>`.

It compounds two ways: three transient `docker inspect` failures reap a *live* run, and
`monitorProxyHealth`'s re-registration (`internal/run/manager_monitor.go:137`) then fails
forever against a stale claim held under its own run ID.

**Fix.**

1. Wire `Revoke(runID)` into the liveness reaper's cleanup path so a reaped run releases
   its claims.
2. Make a same-run re-`Listen` idempotent rather than a self-conflict — re-registration of
   an existing run ID must not be refused by its own claim.
3. Consider a CLI escape hatch (`moat device release <name>` or surfacing stale claims in
   `moat device list`) so a user is never stuck waiting on `moat proxy restart`.

**Tests:** reaping a run releases its listeners and a new run can claim the device;
companion — a live run's claim survives a transient inspect failure; and re-registering
the same run ID with the same device succeeds.

## P0-6. `Revoke` during rollback closes every listener of the run, including live ones

**Severity:** high — the container silently loses its device for the rest of the run.

**Where:** `internal/daemon/server.go:342` (`listenSerial` rolls back with
`s.serial.Revoke(rc.RunID)`); `internal/serialbroker/broker.go:141-148`.

**Symptom (mechanism confirmed):** `Revoke(runID)` closes *all* listeners for that run ID,
not just the ones the failing call created. Because `monitorProxyHealth`
(`internal/run/manager_monitor.go:137`) re-registers the *same* run ID and nothing rejects
a duplicate registration, the sequence `Listen(run-a, esp32)` → dial →
`Listen(run-a, esp32)` fails → `Revoke("run-a")` gives the live session EOF and leaves the
port the container was told to use returning `connection refused`.

**Fix.** Roll back only what this call added — return the created listeners from
`listenSerial` (or take a per-attempt token) and close exactly those. Pairs with P0-5's
idempotent re-`Listen`.

**Tests:** a partial-failure registration leaves earlier listeners of the same run intact;
companion — a fully failed registration leaves nothing behind.

## P0-7. Unauthenticated client can OOM or wedge the shared daemon

**Severity:** high — the victim holds every run's credentials, MCP config, and audit stores.

**Where:** `internal/rfc2217/telnet.go:160-165` (unbounded `r.payload` append),
`telnet.go:103-121` (`Read` loops until data bytes arrive, which never happens while a
subnegotiation is open); no read deadline or idle timeout anywhere on the session conn
(`internal/serialbroker/session.go`).

**Symptom (both confirmed):** feeding `IAC SB 44` followed by 32 MiB of payload with no
`IAC SE` retained `33554432` bytes in one live buffer. An endless feed hung the test binary
until the 120s timeout — the pump goroutine spins and grows without ever returning. Given
P0-1, any local or LAN process can do this.

**Fix.**

1. Cap the subnegotiation payload (a few KiB is generous; real com-port payloads are
   ≤ 5 bytes) and abort the subnegotiation past the cap, resyncing rather than swallowing
   the rest of the stream. This is what real telnet servers do.
2. Add a read deadline / idle timeout to the session conn, so a stalled client also stops
   holding the device forever (one session per device is enforced, so a wedged client is a
   denial of the hardware).
3. Guarantee progress in `Read`: a source returning `(0, nil)` currently busy-spins
   (probe: 194,444,849 iterations in 200ms at 100% CPU) and an error accompanying data is
   dropped. Unreachable through `net.TCPConn`, but fix it while you are here — the
   comment at `telnet.go:118-119` understates it.

**Tests:** an oversized subnegotiation is rejected with bounded memory and the reader
resyncs on the next valid frame; companion — a legitimately sized subnegotiation still
parses, including one split across reads. An idle client is disconnected and the device is
released.

## P0-8. The Linux hub filter never fires — and its test asserts an impossible value

**Severity:** high — every downstream USB hub is listed as attached hardware.

**Where:** `internal/serialdev/enumerate_linux_usb.go:61-64`
(`readAttr(dir, "bDeviceClass") == "9"`); fixture at
`internal/serialdev/enumerate_linux_usb_test.go:34,43,118`.

**Symptom (confirmed on a real kernel):** sysfs emits `bDeviceClass` via
`sysfs_emit(buf, "%02x\n", ...)`, i.e. `"09"` — never `"9"`. Read back from a real kernel
through `docker run -v /sys:/hostsys alpine`: `"09"`. Cross-compiled the package and ran a
probe in Docker with a `"09"` fixture: `LEAKED HUB: vid=05e3 pid=0610 desc="USB2.1 Hub"`.
The existing fixture writes `"9"`, so the test passes against a value the kernel cannot
produce. (Darwin is fine here — `ioreg` really does print decimal `9`.)

**Fix.** Parse the attribute as hex (`strconv.ParseUint(s, 16, 8) == 9`) rather than
string-comparing, so both `"9"` and `"09"` classify. **Rewrite the fixture with the real
`"09"`** and keep a `"9"` case only if you want tolerance asserted deliberately.

**Also:** with this fixed, Linux still lists class-0 controller hubs that darwin filters by
product name (`internal/serialdev/ioreg.go:75-77`, added in `1fbc165`). Decide whether the
name backstop belongs on Linux too — the darwin one is load-bearing, not redundant
(confirmed: this Mac's internal `0424:7240`/`0424:7260` hubs report no usable class).

**Tests:** hub with `"09"` is skipped; companion — a non-hub with `"00"` is kept, and a
device with an unreadable/absent `bDeviceClass` is kept rather than dropped.

## P0-9. Serial access dies silently across a daemon restart

**Severity:** high — no error, no re-establishment; the device just stops working mid-run.

**Where:** `internal/daemon/persist.go:26` (`PersistedRun` carries neither `SerialDevices`
nor `AllowedHostPorts`); `internal/run/manager_monitor.go:134`.

**Symptom (confirmed by reading):** `RestoreRuns` re-registers a run with no listeners, and
because the run *is* found in the registry, `monitorProxyHealth` never re-registers it.
Even on the `ErrRunNotFound` path that does re-register, the response's `SerialAddrs` are
discarded (`_, regErr := ...`) and the container's `MOAT_SERIAL_*_URL` was frozen at
container create, so a newly assigned ephemeral port is unreachable anyway.

**Fix.** Persist `SerialDevices` and `AllowedHostPorts` in `PersistedRun` and re-open
listeners on restore. The port must come back **on the same number** or the container's
frozen URL is dead — this is the same "advertised URLs must stay stable" constraint the
routing proxy already has. Either persist and re-bind the exact port (failing loudly if
taken), or accept the limitation and emit a clear error to the run rather than silence.

**Tests:** a restored run's device is reachable at the same URL the container holds;
companion — a restored run whose port is now occupied fails with a named error rather than
silently binding elsewhere.

## P0-10. Dual-UART bridges are unusable on Linux and half-invisible on macOS

**Severity:** high — FT2232H, FT4232H, CP2105, ESP-Prog. Common embedded hardware.

**Where:** `internal/serialdev/enumerate_linux.go:50-57` (`usbParent` returns the *device*
dir base name for every interface), `internal/serialdev/device.go:65` (`Identity()` has no
interface discriminator), `internal/serialdev/ioreg.go:149` (`cur.Path` assigned per
`IOCalloutDevice`, last write wins).

**Symptom (both confirmed):**

- *Linux:* one USB device with two tty interfaces yields two `Device`s whose `Identity()`
  is byte-identical — probe in Docker: both ttys → `{VID:0403 PID:6010 Serial:FT7ABCDE
  PortPath:1-2}`, and `Match` returns `multiple matching serial devices for 0403:6010`.
  `internal/run/devices.go:130` then tells the user "Unplug all but one so moat can pin the
  right device", which is impossible — it is one plug.
- *macOS:* a two-`IOSerialBSDClient` fixture parses to `got 1 serial devices /
  path="/dev/cu.usbserial-B"`. Port A is unreachable.

**Fix.** Add an interface discriminator to device identity — the USB interface number
(`bInterfaceNumber` on Linux, the interface/client index on macOS) — and emit one `Device`
per tty on both platforms. `Identity()` and the pin store must include it so two ports on
one chip pin independently. Decide how `match.usb` disambiguates (an optional
`interface:` selector, or matching by the resulting path) and document it.

**Tests:** a two-interface device yields two distinct `Device`s with distinct identities on
both platforms, and each pins independently; companion — a single-interface device is
unchanged and its identity does not gain a spurious discriminator (do not break existing
pins in `~/.moat/devices.json` — if the identity format changes, handle the migration or
the user's verified T-Display S3 pin will mismatch).

---

# P1 — should fix before merge

## P1-1. `baud:` is parsed, validated, documented, and inert

`internal/config/devices.go:47-48,90-91` accepts it; `docs/content/reference/02-moat-yaml.md:727,738`
documents it as "Initial line rate". It is absent from `daemon.SerialDeviceSpec`
(`internal/daemon/api.go`) and from `serialbroker.Approved` (`broker.go`), so it never
reaches the port. Confirmed: no non-test reader of `DeviceEntry.Baud` exists.

Either plumb it through (spec → `Approved` → `ApplySettings` on open) or remove the field
and its documentation. Do not leave config that silently does nothing. If you plumb it:
test that it takes effect *and* that a client `SET-BAUDRATE` still overrides it.

## P1-2. Two device names collide into one env var, and both are emitted

`internal/config/devices.go:23` permits `-` and `_`; `internal/run/devices.go:275-279`
uppercases and maps `-` → `_`. `validateDevices` (`devices.go:80`) rejects only exact
duplicate names. Confirmed: `esp-32` and `esp_32` both produce `MOAT_SERIAL_ESP_32_URL`,
`SerialEnv` emits **both**, and `config.Load` accepts a `moat.yaml` declaring both with
different `match.usb`. Last-wins hands the user the other board.

Reject post-transform name collisions in `validateDevices` with a message naming both
entries. Companion test: distinct names that do not collide are still accepted.

## P1-3. Pin store has no cross-process lock

`internal/serialdev/pin.go:81-85` guards with an in-process `sync.Mutex` only, and
`flushLocked` (`:163-181`) serializes the whole map, so a stale snapshot overwrites another
process's write. Confirmed: two stores on one path, `Put("board-a")` then `Put("board-b")`
→ `pins-after-two-writers=1`, `board-a-pin-LOST=yes`.

Consequence is security-relevant: a dropped pin silently re-arms TOFU, so the next run
approves whatever is attached — the swap the pin exists to catch goes through. Use `flock`,
the same fix already applied to routing in #452. Companion test: concurrent writers both
survive; a reader sees a consistent file.

## P1-4. Pin store temp file follows symlinks and loses its mode

`internal/serialdev/pin.go:174-178` writes `s.path + ".tmp"` with `os.WriteFile` (follows
symlinks) then renames. Confirmed: with `devices.json.tmp` pre-symlinked to a victim file,
`Put` overwrote the victim and the resulting `devices.json` came out `-rw-r--r--` instead
of 0600. Same-user only (`~/.moat` is `0700`), so this is hardening — but it is a one-line
fix: `os.OpenFile` with `O_CREATE|O_EXCL|O_WRONLY|O_NOFOLLOW`, mode 0600.

## P1-5. `s.mu` is held across a blocking socket write

`internal/serialbroker/session.go:284-286` → `:415-419`: `handleCommand` takes `s.mu` and
calls `reply()` → `conn.Write`. Confirmed with a goroutine dump: `session.reply
(session.go:416) [semacquire]` under `handleCommand (session.go:297)` while the device pump
sat in `conn.Write (session.go:210) [IO wait]`.

A client that stops reading stalls all baud/DTR/RTS handling, and because `record()`
(`:147`) takes the same mutex, it stalls the rx pump too — so the tty is not drained and
UART bytes are lost. Build the reply under the lock, write outside it. Add a write
deadline. (Teardown still works, so this is a stall, not a deadlock.)

## P1-6. No identity re-check when a session re-opens the device

`internal/serialbroker/session.go:70` re-opens `l.approved.Device.Path` — the path frozen
at registration — on every accepted connection. The `Broker` struct (`broker.go:72-82`)
holds no `serialdev.Enumerator` and no `PinStore`, so it cannot re-verify even in
principle.

Unplug the pinned board, plug a different one into the reused node (`/dev/ttyUSB0` index
reuse on Linux), and the next `esptool` invocation attaches to it unchecked. This
contradicts the design's Lifecycle section ("On replug, re-attach only if identity still
matches the pin") and `docs/content/guides/18-serial-devices.md:143-144` ("moat enforces
that it stays the same device").

Give the broker what it needs to re-verify identity against the pin on each `openPort`, or
correct the guide to say identity is checked once at run start. **Do not leave the doc
claiming enforcement that is not there.** Related: the design's unplug/replug lifecycle is
documented nowhere in `docs/content/`.

## P1-7. `DetectMissingDevices` is dead code; there is no consent prompt

`internal/run/devices.go:210` (`DetectMissingDevices`) and `FormatMissingDevices` have no
non-test callers; `cmd/moat/cli/exec.go:315` never calls them. The design's "device
resolution and pin checking happen in pre-flight, mirroring `run.DetectMissingGrants`" is
unimplemented — the `Create` gate does all the work (it does hard-fail correctly, and
before container create, so this is a UX gap not a security hole).

Separately, `Pin.PinnedByPortPath()` (`pin.go:47`) — whose doc comment says "Callers must
surface this in the consent prompt" — has no non-test caller, and
`internal/run/devices.go:147-155` auto-approves on first use with no prompt and no printed
notice. The design requires: *"The consent prompt must say plainly that this pins whatever
is plugged into that port."* Right now a serial-less CH340 clone is pinned by port silently.

Either wire the pre-flight detector and a first-use notice, or delete the dead exports and
update the design doc to match what shipped. Note the drift-guard requirement itself is
**satisfied and well done** (`TestDetectMissingDevicesMatchesResolve`, 9 cases, both
directions) — do not regress it.

## P1-8. `HasSerialDevices` forces a rebuild of a byte-identical image

`internal/deps/builder.go:50`. Confirmed: `ImageSpec{NeedsSSH}` vs
`{NeedsSSH, HasSerialDevices}` → `dockerfile identical=true`, tags `8f879832703c608f` vs
`6460da309f9d8757`. `HasSerialDevices` changes nothing in the Dockerfile beyond
`needsInit`, and the comment immediately below it argues correctly that this is exactly why
`NeedsProxy` must *not* add a suffix. Adding `devices:` to a config that already bakes
moat-init costs a full rebuild for nothing.

Fix the same way `NeedsProxy` was fixed. The `NeedsProxy` half of that change is sound and
`tagSSH == tagSSHProxy` is the right companion assertion — mirror it for serial.

## P1-9. RFC2217 modem-state notification is wrong and incomplete

`internal/rfc2217/comport.go:13-31`: the "client-to-server" const block mixes both
namespaces — `CmdNotifyLineState = 6` is client→server while `CmdNotifyModemState = 107` is
server→client, in the same block. The only send helper (`serialbroker/session.go:416`) adds
100 itself, so `reply(CmdNotifyModemState, ...)` would put **207** on the wire.
`SERVER_NOTIFY_LINESTATE = 106` and the client→server `NOTIFY_MODEMSTATE = 7` are both
absent, so pyserial's poll request (`rfc2217.py:910`) cannot be matched.

Verified against pyserial: reading `.cts`/`.dsr`/`.ri`/`.cd` on an `rfc2217://` port raises
`SerialException("remote sends no NOTIFY_MODEMSTATE")` on first access, because nothing
ever sends an unsolicited notify. esptool does not read those lines (grepped, no hits), so
this is a latent trap plus a missing feature rather than a current break.

Split the two namespaces into separate const blocks with the correct values, and decide
whether to implement modem-state notification. Also add the missing SET-CONTROL query codes
(0, 4, 7, 10, 13, 14-19), which currently render as `"unknown"` and are silently ignored.

## P1-10. `ioreg` parser attributes descendant properties to the enclosing device

`internal/serialdev/ioreg.go:112-150`: only `+-o` lines gate the stack, so every property
line under a device — hub-port nubs, driver nubs, interface clients — writes into the
enclosing `IOUSBHostDevice`, last-write-wins across the whole subtree.

Confirmed with a fixture built from this Mac's real hub topology: `USB2 Hub@08300000` (own
`locationID` `0x08300000`) parsed out as `port=0x08320000` — a downstream port's value.
`PortPath` is the design's identity fallback for serial-less devices, so this corrupts a
pin. On the real dump it is masked only because `IOUSBHostInterface` happens to print last
and repeats the device's own `locationID` — pure ordering luck.

Bind properties to the block immediately following the device's `+-o` line, or make these
keys first-write-wins. Companion test: a device with nested children keeps its own
`locationID`; a device with no children is unchanged.

## P1-11. An empty `PortPath` makes a serial-less pin match anything

`internal/serialdev/ioreg.go:128-130` leaves `PortPath` empty when `locationID` is absent
or not base-10 parseable, and the device is still emitted (`:160` requires only VID/PID).
`Pin.Verify` with `Serial == ""` then compares `"" != ""` → false → **pass**. Confirmed:
`parseIoreg` emits `port=""` for a block with no `locationID`.

So a serial-less clone pinned this way approves any board with the same VID:PID, while
`PinnedByPortPath()` still reports `true` and the message reads "pinned to port " with
nothing after it. Fail `Verify` on an empty pinned `PortPath`, and drop devices that have
neither `Serial` nor `PortPath` from the pinnable set. Companion test: a device with a real
`PortPath` still pins and verifies.

**Also in `ioreg.go` while you are in there (low):**
- `:144-145` — an empty `kUSBProductString` clobbers a good description. Confirmed on this
  host: a RØDE NT-USB Mini lists with a blank description because ioreg prints nothing
  after `=` for non-ASCII strings. The `+-o` node name carries the right label and is never
  read. Assign only when non-empty, as `USB Product Name` at `:141` already does.
- `:188-193` — `decimalToHexID` returns `""` on any parse failure and `:160` then drops the
  device with no diagnostic, even when the other ID parsed fine. Also `ParseUint(s,10,32)`
  accepts values > 0xffff and `%04x` then emits 5+ digits that can never match
  `config.usbIDRe`'s 4-digit form.
- `:102` — the 4 MiB scanner cap fails the *whole* enumeration on one oversized line. Real
  `ioreg -w0` output on this host already contains a **305,660-byte** line. 13x headroom,
  but the failure mode is "no devices at all". Skip the line instead.
- `:11-12` — the doc comment still describes `ioreg -p IOUSB -l -w0`, which `769569d`
  replaced.

---

# P2 — test gaps that matter

## P2-1. `SetModem` and `SendBreak` have zero coverage

`internal/serialport/port_unix.go:96,129` and `termios_darwin.go:84` are 0.0%. Every
DTR/RTS assertion in the suite is against `serialtest.FakePort`, which *records* instead of
applying — so the `TIOCMBIC`/`TIOCMBIS` ioctl argument convention is unverified for the
modem lines.

This is the exact bug class that already shipped twice on darwin (`64cfd81`: `TIOCFLUSH`
takes its selector by pointer), and `TestFlushBuffersDiscardsPendingInput`
(`port_unix_test.go:154`) exists specifically to pin down that convention for flush. Do the
same for the modem lines and break. The design doc's warning applies: *"invisible until
someone tries to flash a board."*

## P2-2. Parity odd/even is never exercised

`internal/serialbroker/settings.go` has no `_test.go` at all. `parityFromWire` Odd (`:23`),
Even (`:25`) and the invalid-value rejection (`:27`) are uncovered, as are `parityToWire`'s
Odd/Even and `formatSettings`' `O`/`E` labels. `TestPySerialOpenSequence`
(`broker_test.go:659`) sends parity `1` only. An odd↔even swap or a silently accepted
mark/space parity ships green, and the symptom is framing corruption on real hardware.

## P2-3. The broker's `error`-event path is never exercised

`session.go:386,398,406,416` are uncovered, so `emitError` is called by **no test**. The
design's unplug lifecycle ("broker sees `EIO`/`ENXIO`, emits `ERROR`") is untested. The
fake has `SetFlushError` but no way to fail `SetModem`/`ApplySettings`/`Read` — extend it.

Also uncovered and worth adding: the `conflict` event at `session.go:54`
(`TestSecondConnectionIsRefusedWhileOneIsActive:252` checks only that the socket closes),
the rx-error branch at `:216-219`, `Revoke` on unregister (`server.go:426` — the design's
"Stop. Detach, release the claim" step), `Broker.Close` double-close idempotency
(`broker.go:162`), and `Listen` after `Close` (`broker.go:111`).

## P2-4. `moat device forget` has no test

`cmd/moat/cli/device.go:261` — the remedy every mismatch error message points at. Neither
the removal nor the "no device named %q is pinned" branch is covered. It hard-codes
`DefaultPinPath()`, so make it injectable first (the `Create` gate has the same problem:
`manager_create.go:702-718` hard-codes `serialdev.DefaultPinPath()` and
`serialdev.NewEnumerator()`, which is why the gate side of the "device absent fails before
create" pair is untested).

## P2-5. Two vacuous tests

- `cmd/moat/cli/device_test.go:45` (`TestDeviceListShowsUnpinnedDevicesAsDash`):
  `if !strings.Contains(out, "\t") && !strings.Contains(out, "-")`. The output is a
  tabwriter table, so it always contains a tab — the test passes for *any* output and says
  nothing about the PIN column.
- `internal/rfc2217/comport_test.go:45,53`: `ControlName` is asserted only `!= ""`, never
  against the label. It is the `detail` field of every `modem`/`break` audit entry and
  `devices.jsonl` row, so returning `dtr-on` for RTS-off passes.

## P2-6. Hostile-input and event-plumbing branches

Uncovered: `rfc2217/telnet.go:144` (two-byte `IAC <NOP/DM>`), `:175` (the `IAC <other>`
mid-subnegotiation case the source comment calls out); `session.go:291` (baud `0` =
query-current), `:348` (undefined PURGE value), `:266`/`:269` (`WILL <unsupported>` →
`DONT`, and the `WONT/DONT` no-reply case whose comment says "answering would loop" — the
loop hazard itself is untested), `:46` (connection arriving after `listener.close()`),
`:210` (client write failure mid-pump).

Also: the broker → `devices.jsonl` → audit fan-out lives entirely in an inline closure at
`cmd/moat/cli/daemon.go:169-248`, including the audit-kind filter and the
`Event.Record → DeviceData.RecordMode` mapping. No broker test asserts `Event.Record` is
populated at all (`broker_test.go:916` checks VID/PID/serial but not `Record`), so the
design's "the audit chain records that full capture was enabled" is verified at neither end
of the seam. Extract the closure so it can be tested.

**Housekeeping:** `internal/serialdev/zz_probe_test.go` (161 lines, 5 `TestZZ*`) is
untracked and not gitignored, and `TestZZRealIoreg` silently skips on a `/tmp` file. Remove
it. The hardware e2e (`internal/e2e/serial_test.go:154-160`) opens the real
`~/.moat/devices.json` and calls `pins.Forget("dut")`, deleting a real user pin — point it
at a temp pin path.

---

# P3 — docs and examples

## P3-1. Three unfilled `#NNN` placeholders — CI-blocking

`CHANGELOG.md:21,35,36`. Per CLAUDE.md, CI fails on the placeholder. Also:

- `:36` documents a "Fix" to `moat device list`, a command shipping for the first time in
  this same release — users could not have been affected. Fold it into the Added entry; the
  "previously Y happened" pattern cannot apply.
- `:35` is a sentence fragment: "previously, a run with `network: host`, ... but no grants
  and no agent registered with the proxy daemon, whose URL uses the synthetic hostname
  `moat-proxy`." The main clause has no verb, so it never says what went wrong.

## P3-2. `moat logs` is claimed to show device events. It does not.

`examples/serial/README.md:131`. `moat logs` reads `logs.jsonl` only
(`cmd/moat/cli/logs.go:73`); device events go exclusively to `devices.jsonl`
(`internal/storage/storage.go:541`), and **nothing in the CLI reads it** —
`ReadDeviceEvents` has no non-test caller.

The adjacent `moat audit` claim is accurate, but `cmd/moat/cli/audit.go:183-236` has no
`case audit.EntryDevice`, so device entries fall through to `default` and print as raw JSON
truncated at 80 chars — cutting off `serial` and `record_mode`, the fields the audit trail
exists to prove. Add the case, and either give `devices.jsonl` a reader or stop referring
to one.

Relatedly, `record: full` attach entries claim capture happened even when `openRecorder`
failed and the session degraded to events-only (`session.go:82-91` degrades;
`cmd/moat/cli/daemon.go:245` still stamps `RecordMode: e.Record`). Only a separate `error`
entry records the truth.

## P3-3. Undocumented shipped behavior

- `docs/content/guides/11-observability.md:174-181` artifact table lists only
  `logs.jsonl`/`network.jsonl`/`traces.jsonl`. `devices.jsonl` and
  `serial-<name>.capture` are new per-run artifacts, documented nowhere.
- `docs/content/reference/03-environment.md` documents `MOAT_URL_*`, `MOAT_RUN_ID` etc. but
  not `MOAT_SERIAL_<NAME>_URL` or `MOAT_SERIAL_DEVICES`.
- `MOAT_SERIAL_TEST_DEVICE` (the hardware e2e gate) is undocumented.
- Unplug/replug behavior is documented nowhere, though the design has a Lifecycle section
  for it (see P1-6).

## P3-4. Broken link, wrong run-ID format, column drift

- `docs/content/guides/18-serial-devices.md:84` links
  `.../esptool/en/latest/esp32/esptool/remote-serial-ports.html` → **404** (verified). The
  correct path is the one `examples/serial/README.md:107` already uses
  (`.../esp32/remote-serial-ports.html`, verified live). The quoted sentence is
  verbatim-accurate on the real page.
- `:163` shows `run 01HQ...`; real IDs look like `run_015e8e26ae2a`
  (`internal/id/id.go:15`).
- `:113` indents the fix line 4 spaces; actual output is 2 (`pin.go:53`).
- `examples/serial/README.md:141` says "`moat device list`'s SERIAL column"; the column is
  `SERIAL NUMBER` (deliberately — `device.go:112-116`).
- `examples/serial/README.md:12` still says "Build and install the CLI from this branch" —
  the branch-relative phrasing `9aa0cbb` set out to remove. Its
  `go build -o /usr/local/bin/moat ... # or: make build-cli` also implies equivalence;
  `make build-cli` builds `./moat` in-tree and installs nothing.
- `internal/serialbroker/broker.go:36` and `internal/storage/storage.go:534` both enumerate
  event kinds and both omit `purge`, which is emitted at `session.go:346,351`.

## P3-5. `examples/serial-sdr/spectrum.py` has wrong rtl_tcp command codes

`spectrum.py:28-29` defines `CMD_SET_GAIN_MODE = 0x04` and `CMD_SET_GAIN = 0x05`. Canonical
rtl_tcp (`steve-m/librtlsdr`, `src/rtl_tcp.c` — what the README's `brew install librtlsdr`
provides) maps `0x03` → `set_tuner_gain_mode`, `0x04` → `set_tuner_gain`, `0x05` →
`set_freq_correction`.

So line 129 leaves gain mode on auto and sets tuner gain to 0.1 dB, and line 130 applies a
**300 ppm frequency correction** — about 30 kHz off at 100 MHz. Shift both constants down
by one. (The greeting decode fixed in `2eec625` is correct: `>4sII`, 12 bytes, magic raw
plus two `htonl` fields; the 5-byte `>BI` command frame is correct too.)

## P3-6. `spectrum.py` computes no spectrum

`spectrum.py:99-120` bins average power by **buffer offset** (`b = (i * 64 // len(iq)) %
64`) — 64 time slices, no FFT. The function is named `read_spectrum`, documented as a
"64-bin power spectrum in dB", prints "peak bin", and `examples/serial-sdr/README.md:68-69`
says it "prints a coarse power spectrum". "Peak bin" carries no frequency meaning.

Either do an actual FFT or rename everything to what it measures (a power-vs-time trace).

## P3-7. `examples/serial-sdr/README.md:32` command does not work

`moat run -- python3 dsp.py rtl://$MOAT_HOST_GATEWAY:1234` — the *host* shell expands
`$MOAT_HOST_GATEWAY` (unset on the host) to empty; it needs `sh -c`, which the sibling
`examples/serial` README is careful about. `dsp.py` also does not exist in the directory —
the file is `spectrum.py`.

**Consider splitting `examples/serial-sdr/` out of this branch entirely.** It is an
RTL-SDR/`rtl_tcp` example that shares no code with the serial broker, it carries its own
correctness problems (P3-5, P3-6, P3-7), and it enlarges the review surface of an already
large branch. `63b484c` (the "Other USB devices" section in `moat device list`) is the part
that genuinely belongs here.

---

# Do not change — verified correct

Confirmed sound by probe or by running the code. Do not churn these while working the list:

- **IAC escaping is a proven round-trip identity** — 3.9M fuzz execs, no counterexample;
  firmware-shaped payloads with runs of `0xFF` survive both directions. Chunk-boundary
  state machine verified at every split point of a stream containing escaped data and an
  escaped baud command (2.6M execs). No panic on arbitrary hostile input; `dispatch()`
  length-checks correctly.
- **All com-port command and value codes match RFC 2217 and pyserial** (except the notify
  namespace split in P1-9). Baud is 4-byte big-endian both directions. "Value 0 = request
  current" is handled correctly for baud, datasize, parity, and stopsize.
- **No data races.** Baseline `-race -count=2` clean; an 8-goroutine hammer probe across
  shared device names with `Close` firing mid-flight produced no races, panics, or hangs.
  One session per listener holds under contention (40 rounds × 4 dialers, max concurrent
  open ports = 1).
- **Teardown defer order in `handle` is right** (recorder → `port.Close` → clear `l.cur` →
  `conn.Close`), closes are idempotent throughout, and no goroutine parks on `read(2)`
  after `Close` — `serialport.Open`'s non-blocking fd survives `f.Fd()` calls. This is
  load-bearing but implicit: if anyone ever opens the tty blocking, `session.stop()`
  silently stops working. Worth a comment.
- **Unplug mid-session tears down correctly** and the device reopens cleanly afterwards.
  No unbounded buffering on the data path — backpressure ends at the UART.
- **Darwin ioctl conventions are all correct** (constants decoded and printed):
  `TIOCFLUSH` is `_IOW` so the by-pointer fix in `64cfd81` is right, `TCIOFLUSH=3` is the
  right selector, `TIOCMBIS/BIC` by pointer, `TIOCEXCL/TIOCSBRK/TIOCCBRK` bare `_IO` with
  `IoctlSetInt(..., 0)`. Linux `TCFLSH` by value is also correct.
- **Raw mode is complete on both platforms** and `VMIN`/`VTIME` indices are in range.
  **cu-vs-tty is right**: enumeration reads `IOCalloutDevice`, `Open` uses
  `O_NONBLOCK|O_NOCTTY`, `rawTermios` sets `CLOCAL` — no DCD hang.
- **The tty class check is done on the opened fd**, not the pre-open path
  (`port_unix.go:31-45`), so the design's "the tty check IS the class allowlist" holds and
  the TOCTOU concern does not apply. A FIFO, regular file, or `/dev/mem` all fail `ENOTTY`.
- **VID/PID normalization is correct on both sides**: `config.usbIDRe` requires exactly 4
  hex digits, `VIDPID()` lowercases, darwin emits `%04x`, Linux sysfs emits lowercase
  `%04x`, `Match` lowercases both. `match: {vid: "303A"}` matches.
- **Linux sysfs enumeration is robust**: trailing newlines trimmed, ttys with no USB
  ancestor skipped, and an unreadable `idVendor` on one device drops only that device
  (confirmed in Docker as uid 1000).
- **`-r -p IOService -c IOUSBHostDevice` does not duplicate nested devices** (13 nodes in
  the real dump, each parsed once), and the darwin hub name backstop from `1fbc165` is
  load-bearing, not redundant.
- **Daemon API additions are purely additive** (`SerialDevices`, `SerialAddrs`,
  `CapSerialDevices`, all `omitempty`), with a round-trip test. New CLI + old daemon fails
  fast and actionably at `manager_create.go:707` **before** pins are written or a container
  exists; old CLI + new daemon sends no devices. Capability is advertised only when a
  broker is attached, with its companion test.
- **`SerialEnv(syntheticHostGateway, ...)` is the right choice** — matches `baseurl.go`'s
  convention rather than repeating the `GetHostAddress()` mistake; `NO_PROXY` is irrelevant
  for raw RFC2217.
- **The detector↔validator drift guard is exemplary** —
  `TestDetectMissingDevicesMatchesResolve` covers all nine classification cases in both
  directions and both entry points share `resolveDevices`, so drift is structurally
  impossible. Invariant #2 done better than asked. Do not regress it while fixing P1-7.
- **The pin store's four required directions all exist and assert real behavior**
  (`pin_test.go:11,21,51,65`), including the fix-command text in the mismatch error.
- **`TestPySerialOpenSequence`** (`broker_test.go:615`) replays pyserial's real connect
  dance — keep it. One caveat: its comment at `:626-634` claims to replay "the exact
  command sequence pyserial sends when connecting", but real pyserial starts both BINARY
  options in `INACTIVE` and never sends `DO BINARY`/`WILL BINARY` on connect. Functionally
  harmless; fix the comment.
- **The in-container console pty bridge never shipped, and no doc promises it.** The guide
  explains why the URL is the surface instead (`18-serial-devices.md:73-86`), and
  `internal/config/devices.go:21` carries an honest "once the console bridge lands"
  comment. This was the top-risk item in the review and it is clean. Leave it out —
  `pyserial-miniterm rfc2217://...` already covers the console use case without shipping
  an embedded binary.
- **`devices:` config schema changed from the design** (`{name, match.usb: "vid:pid"}`
  rather than `{serial, match: {vid, pid}}`, per `fc71fde`) and **every** YAML snippet in
  the guide, the reference, and both examples parses against the real `config.Load` —
  verified by loading all six snippets plus both example `moat.yaml` files. No stale field
  names anywhere.
- **`docs/plans/` files are correctly untracked** on this branch, per convention.

---

# Suggested sequencing

1. **Security and lifecycle** — P0-1, P0-2, P0-3, P0-4, P0-5, P0-6, P0-7. These are one
   coherent piece of work and mostly small, localized changes. Land them together with the
   doc corrections they imply, so the guide never describes a boundary that does not exist.
2. **Enumeration and restore** — P0-8, P0-9, P0-10, then P1-10, P1-11. P0-10 may change the
   pin identity format; handle the migration or the verified T-Display S3 pin will mismatch.
3. **Config and hardening** — P1-1 through P1-5, P1-7, P1-8, P1-9.
4. **Tests** — P2-1 through P2-6. P2-1 and P2-2 are the two that would have caught real
   shipped bugs; do them first.
5. **Docs and examples** — P3-1 through P3-7. P3-1 is CI-blocking, so do not push without
   it. Decide on splitting `examples/serial-sdr` before spending time on P3-5 to P3-7.

Before pushing, per CLAUDE.md "Before You Push": self-review the diff, re-read any open PR
review threads, and confirm the tree has no `zz_probe*` debris.
