# Serial device access for moat containers

**Status:** approved design, not yet implemented
**Date:** 2026-08-15

## Problem

Agents doing embedded work need to talk to hardware plugged into the host — flashing
an ESP32, opening a serial console, driving a dev board over UART. Today a moat
container has no path to any host device at all.

The request was "expose USB ports, sandboxed and locked down to approved devices."
The investigation below narrows that to serial devices specifically, which is both
what is needed and the only tier that can work on every runtime moat supports.

## Why not USB passthrough

Three constraints kill general USB passthrough for moat.

**gVisor does not forward host devices.** Linux runs default `sandbox:` = gVisor
(`internal/container/docker.go:94-121`). gVisor's compatibility guide is explicit:
"Device files for custom hardware is generally not supported, with the notable
exceptions of NVIDIA GPUs and TPU devices." Native `--device` passthrough would
require every hardware run to set `sandbox: none` and drop to runc — trading moat's
core isolation guarantee for device access.

**macOS cannot do it at any sandbox level.** Apple `container` runs one lightweight
VM per container with no USB passthrough (apple/container#1301 is an open request for
`VZXHCIController`). Docker Desktop for Mac has never supported it (docker/for-mac#5263).
The only route is USB/IP, and macOS has no first-party USB/IP server — the working
recipes use third-party userspace servers over libusb (`pyusbip`, `jiegec/usbip`), which
practitioners describe as flaky and partially implemented, with USB-serial support
specifically patchy. Apple's guest kernel also lacks `vhci-hcd`, so the client half is
missing too.

**The kernel cannot enforce a device allowlist by identity.** The devices cgroup filters
on `major:minor` only. A VID/PID/serial allowlist can therefore never be enforced by the
kernel — it can only be resolved on the host at admission time and translated into node
paths. `--device` gives weaker guarantees than it appears to.

## Approach: a host-side serial broker

Serial devices do not need USB at all. A broker on the host owns the real tty and hands
the container a PTY. No kernel modules, no privileged mode, no `CapAdd`, no `sandbox: none`
— a pty and a TCP socket, both of which gVisor supports. It works identically on Linux,
Docker Desktop, and Apple container.

This also inverts the enforcement problem in our favour: because the broker opens the
device, it enforces on VID/PID/serial — real device identity, which `--device` cannot do.

### Transport must be TCP, not a Unix socket

`AF_UNIX` connections do not traverse virtiofs. On macOS, both Docker Desktop and Apple
container bind-mount through virtiofs, so a socket file shared into the container is inert.
The broker listens on TCP over the host gateway — the path moat already opens for
`network.host:` and already addresses via `GetHostAddress()`.

That makes the broker a listener owned by the **existing proxy daemon**. Daemon lifecycle,
run registration, and the audit store are all reused rather than reinvented.

**Authentication is by reachability, not by token.** RFC2217 has no authentication, so each
run gets its own ephemeral listener and only that run's container is permitted to reach the
port, via the `AllowedHostPorts` mechanism that already backs `network.host:`. The honest
statement of the boundary: other processes on the host can reach the port. Binding to the
container-facing gateway address narrows this but does not close it, and the docs must say
so rather than imply a token check that does not exist.

### Components

**Host: `internal/serialbroker/`**
Opens the real tty (`/dev/tty.usbserial-*` / `/dev/cu.*` on macOS, `/dev/serial/by-id/*`
on Linux), takes an exclusive claim (`TIOCEXCL` plus a daemon-level registry), and serves
RFC2217. Registered per run in `daemon.RunContext`, alongside `AllowedHostPorts`.

**Container: `moat-serial` bridge**
An RFC2217 client that presents a pty: connects to the broker, creates a pty pair, chowns
the slave to the container user, symlinks it to a stable path, drops privileges, and pumps
bytes. Polls the slave's `TCGETS` to forward baud changes. Started by `moat-init` during
its root phase so it can create the symlink. Console-only by construction — see the pty
limits above.

### Measured: what a pty can and cannot carry

An earlier draft of this design put the pty master in packet mode (`TIOCPKT`) to observe
termios changes. A probe on Linux 6.12 disproved it:

```
RESULT packet-mode-accepted=yes
RESULT baud-change-notifies=no                 # TIOCPKT accepted, never fires
RESULT ixon-change-notifies=no
RESULT slave-tcgets-readable=yes ... match=true # polling does work for baud
RESULT slave-tiocmget=no err: inappropriate ioctl for device
```

Three conclusions, in increasing order of importance:

1. `TIOCPKT` is accepted but Linux never delivers `TIOCPKT_IOCTL` on a termios change. The
   constant is a BSD-ism the header defines and the Linux pty driver does not generate.
2. Baud changes *can* be observed by holding an fd on the slave and polling `TCGETS`.
3. **A pty has no modem control lines.** `TIOCMGET` returns `ENOTTY`. DTR and RTS cannot be
   represented on a pty by any mechanism. Since DTR/RTS toggling *is* ESP32 auto-reset, a
   pty alone can never flash a board.

### Protocol: RFC2217

The broker speaks RFC2217 — telnet's com-port-control extension — and nothing else. It
carries baud, data bits, parity, stop bits, DTR/RTS, and break over TCP, which is precisely
the set a pty cannot express. Espressif documents `esptool --port rfc2217://host:port` as
supporting custom baud rates and DTR/RTS auto-reset "the same as for a local serial port,"
and recommends it for remote serial.

One caveat carries a design requirement: network latency can break ESP32 reset timing,
which is why Espressif ships `esp_rfc2217_server.py` with a custom PortManager rather than
relying on the stock pyserial example. The broker must apply the DTR/RTS reset sequence with
the same care — a naive per-signal relay produces intermittent flashing failures.

### What the container sees

Two front ends over the one protocol:

**`rfc2217://<gateway>:<port>` — the working surface.** Full control lines. This is what
flashes boards. `MOAT_SERIAL_<NAME>_URL` is injected into the environment.

**`/dev/moat/serial/<name>` — a console pty**, plus a `/dev/ttyUSB0` compat symlink. Served
by a small in-container bridge that is itself an RFC2217 client. It forwards data and
observes baud changes by polling `TCGETS`; it **cannot** forward DTR/RTS, because the pty
has no such lines to read. Documented as console-only: `picocom`, `screen`, and `cat` work;
flashing does not. Tools that need control lines use the URL.

The container sees no host path, no other device, and cannot enumerate.

### Bridge binary delivery

The bridge must be a prebuilt static linux binary embedded in the CLI — the container may
run any base image, so it cannot be compiled at build time or assumed present.

`feat/moat-init-go-rewrite` already solves exactly this with `internal/initbin/`
(`go:embed` of `moat-init-linux-{amd64,arm64}`, a `gen` step, and `checksums.txt`).
**The bridge should become a subcommand of that binary** (`moat-init serial-bridge`)
rather than shipping a second embedded binary with a parallel copy of the infrastructure.
Until that branch lands, `internal/serialbin/` mirrors its shape; the merge collapses them.

## Configuration

```yaml
devices:
  - serial: esp32           # name → /dev/moat/serial/esp32
    match: { vid: "303a", pid: "1001" }
    baud: 115200            # optional initial line settings
    record: events          # events (default) | full
```

CLI:

- `moat device list` — host serial devices with VID/PID/serial and current pin state
- `moat device forget <name>` — clear a pin

Pins live in `~/.moat/devices.json`, keyed by config name:
`{vid, pid, serial, port_path, first_seen}`.

## Security model

**Deny by default.** No `devices:` entry, no device.

**Enforcement lives in the broker, and that is stronger than the kernel alternative.**
The devices cgroup knows only `major:minor`; the broker knows VID/PID/serial.

**TOFU pinning.** The first run records the device's serial number. Later runs must match.
A mismatch is a **hard failure**, not a warning — a swapped device is precisely the attack
this exists to catch. The error names both serials and points at `moat device forget`.

**Devices with no serial number.** Cheap CH340/CP2102 clones frequently ship without one.
Fall back to pinning the physical port path (`/sys/bus/usb/devices/1-2.3` on Linux, IOKit
`locationID` on macOS). The consent prompt must say plainly that this pins *whatever is
plugged into that port*, not that device.

**Exclusive claim.** One run per device. A second run gets "device esp32 is in use by run
abc123", not a confusing open failure.

**Class enforcement by construction.** The broker only ever opens character devices that
are ttys, and refuses anything else. Storage, HID, and smartcards do not present as ttys,
so the tty check *is* the class allowlist — there is no separate deny list to drift out of
sync.

**Stated plainly in the docs:** an agent with a serial line can reflash and brick the
device. That is inherent to the request. Device-level consent is the mitigation; sandboxing
is not.

## Observability

Default `record: events` — audit-chain entries for pin creation, attach, pin match/mismatch,
detach, unplug, and claim conflict, plus per-session tx/rx byte counters, baud changes, and
DTR/RTS transitions written to `devices.jsonl` in the run's storage directory.

`record: full` (config only, no CLI flag) tees payload bytes. Serial carries firmware images
and device credentials, so the audit chain records that full capture was enabled for the
session.

## Lifecycle

**Start.** Device resolution and pin checking happen in pre-flight, before container create,
mirroring the `run.DetectMissingGrants` pattern. Per codebase invariant #2, the detector and
the `Create`-gate validator must classify identically, with a drift-guard test asserting
both directions.

**Unplug mid-run.** The broker sees `EIO`/`ENXIO`, emits `ERROR`, and the bridge keeps the
pty alive returning `EIO` to readers. On replug, re-attach only if identity still matches
the pin.

**Stop.** Detach, release the claim, emit the audit event.

## Testing

**Mocked by default — no hardware required for any automated test.** The broker's device
handle is an interface. The fake carries data over a real pty pair, so the read/write path
under test is the same one a real tty takes; because a pty cannot express baud or DTR/RTS,
the fake *records* those calls instead of applying them, and tests assert on the recorded
values. Device enumeration is likewise behind an interface with a table-driven fake
supplying VID/PID/serial/port-path.

The DTR/RTS assertions matter more than they look: control lines are exactly what the pty
front end cannot carry, so they are the part most likely to be silently dropped, and their
absence is invisible until someone tries to flash a board.

Per codebase invariant #1, pin tests run in both directions:

- matching serial attaches **and** mismatched serial hard-fails
- missing serial falls back to port path **and** a changed port path fails
- device present at pre-flight passes **and** absent device fails before create

**Live testing requires a dedicated sandbox with a real ESP32 attached** to the host. This
is a manual recipe, not CI: an `examples/serial/` moat.yaml plus documented `esptool
chip_id` / `picocom` steps. The hardware e2e test skips unless `MOAT_SERIAL_TEST_DEVICE`
names a real device.

## Out of scope

Raw USB, `vhci-hcd`, USB/IP, Apple container native passthrough, device sharing between
concurrent runs, network-exported devices.

## Documentation

- `docs/content/reference/02-moat-yaml.md` — `devices:` block
- `docs/content/reference/01-cli.md` — `moat device list` / `forget`
- `docs/content/guides/18-serial-devices.md` — the ESP32 walkthrough
- `CHANGELOG.md` — Added entry with real PR link
