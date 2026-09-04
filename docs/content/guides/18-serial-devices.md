---
title: "Serial devices"
navTitle: "Serial devices"
description: "Give an agent access to an approved USB serial device, such as an ESP32 dev board."
keywords: ["moat", "serial", "usb", "esp32", "esptool", "rfc2217", "hardware", "uart"]
---

# Serial devices

Give an agent access to a USB serial device attached to your machine — an ESP32 or
Arduino dev board, a UART adapter, a modem — without giving it the rest of your hardware.

The device broker runs inside the proxy daemon, which outlives the CLI. If you just
installed or upgraded moat, restart it once so the running daemon is the new binary:

```bash
$ moat proxy restart
```

A run whose moat.yaml has `devices:` fails immediately — before the container is
created — if the daemon is too old to serve them.

## 1. Find the device

```bash
$ moat device list
DEVICE                   USB ID     SERIAL NUMBER      PIN  DESCRIPTION
/dev/cu.usbserial-14220  10c4:ea60  0001              -    CP2102 USB to UART Bridge

Pick a name for the device and add it to moat.yaml. The name is
yours to choose — it becomes MOAT_SERIAL_<NAME>_URL inside the run:

  devices:
    - name: cp2102-usb-to-uart-bridge
      match: {usb: "10c4:ea60"}

The first run that uses the device pins it to this hardware; the PIN column
shows the name it is pinned under.
```

## 2. Declare it in moat.yaml

```yaml
name: board-dev

dependencies:
  - python

devices:
  - name: board
    match: {usb: "10c4:ea60"}
```

## 3. Use it

Each device is exposed as an RFC2217 URL in `MOAT_SERIAL_<NAME>_URL`. Pass the URL to
whatever tool would have taken the device path:

```bash
$ moat run -- sh -c 'esptool --port "$MOAT_SERIAL_BOARD_URL" chip-id'
```

The command can also live in moat.yaml. `command:` is an argv list, not a shell string —
`$MOAT_SERIAL_BOARD_URL` does not expand in it — so reference the env var from a script
the command runs:

```yaml
command: ["sh", "/workspace/verify.sh"]
```

`MOAT_SERIAL_DEVICES` lists the names of all devices available to the run.

## Why a URL and not /dev/ttyUSB0

Flashing a board is not a byte stream. `esptool` drives the DTR and RTS control lines to
put an ESP32 into its bootloader, and changes the baud rate mid-session.

A pseudo-terminal cannot carry either signal — `TIOCMGET` on a pty returns `ENOTTY`,
because a pty has no modem control lines at all. A device path inside the container would
enumerate correctly and then fail to flash, which looks like broken hardware rather than a
missing feature.

RFC2217 is the standard solution: it carries baud rate, DTR, RTS, and break over TCP.
Espressif [documents it](https://docs.espressif.com/projects/esptool/en/latest/esp32/esptool/remote-serial-ports.html)
as supporting DTR/RTS auto-reset "the same as for a local serial port," and recommends it
for remote serial. Any pyserial-based tool accepts an `rfc2217://` URL in place of a port.

One message to expect rather than fix: esptool prints `Failed to get VID/PID of a
device on rfc2217://...` on each connect. It asks the port for its USB IDs to pick a
reset strategy; an `rfc2217://` URL has no USB identity by construction — the device
lives on the host, not in the container — so esptool falls back to the standard UART
reset sequence, which is the one that works over RFC2217. The message repeats because
esptool caches the answer only on success. It is informational, so the
[serial example](https://github.com/majorcontext/moat/tree/main/examples/serial)
filters it from its demo output; no esptool flag suppresses it (checked against 5.4.0:
`--before` does not skip the lookup, and the `custom_reset_sequence` config option
covers only the two lookups in reset-strategy selection, not the four during chip
detection and chip-info printing).

## Approval and pinning

Nothing is exposed unless `moat.yaml` asks for it.

The `match` block selects a device by USB vendor and product ID, which identifies the
*model*, not the unit. The first run to use a device name records that specific device's
serial number. Every later run must present the same device:

```
$ moat run -- esptool chip-id
Error: cannot use the serial devices this run requires:
  board: serial device does not match its pin: "board" was pinned to serial 0001
  but the attached device reports 0002
    If you intended to swap devices, run: moat device forget board
```

This is a hard failure, not a warning. Two boards of the same model are indistinguishable
by USB ID, so without pinning an agent could flash the wrong one.

After deliberately swapping hardware:

```bash
$ moat device forget board
```

### Devices without a serial number

Cheap CH340 and CP2102 clones often ship without a serial number. Those are pinned to the
physical USB port instead, and `moat device list` says so:

```
DEVICE        USB ID     SERIAL NUMBER         PIN  DESCRIPTION
/dev/ttyUSB0  1a86:7523  - (pins by port 1-3)  -    USB Serial
```

A port pin approves *whatever is plugged into that port*, so moving the device to another
port fails until you either move it back or run `moat device forget`.

## What an agent can do with a serial device

An agent with a serial line can reflash the device, and can therefore brick or reprogram
it. That is inherent to the request — flashing is the point.

The mitigation is consent at the device level: you choose which device, and moat enforces
that it stays the same device. Sandboxing does not help here, and moat does not pretend
otherwise.

Two further boundaries are worth stating plainly:

- **Only ttys are exposed.** The broker refuses to open anything that is not a tty
  character device, so storage, HID, and smartcard devices cannot be reached through this
  mechanism at all.
- **The RFC2217 port is reachable from the host.** The protocol has no authentication, so
  access is scoped by reachability: each device gets its own port, and only the owning
  run's container is permitted to reach it through moat's network policy. Other processes
  running on your machine can still connect to it.

## One run at a time

A device is claimed exclusively while a run holds it. A second run that wants the same
device fails rather than interleaving bytes on the same line:

```
Error: registering run with proxy daemon: daemon returned 409: serial device "board" (/dev/ttyUSB0) is already in use by run run_015e8e26...
```

The claim is released when the run stops — including when the run dies without
unregistering (a killed CLI, a crashed machine): the daemon's liveness checker reaps the
dead container and releases its devices.

## Observability

Attach, detach, line-setting changes, DTR/RTS transitions, and byte counters are recorded
for every session. Set `record: full` to capture the payload bytes too:

```yaml
devices:
  - name: board
    match: {usb: "10c4:ea60"}
    record: full
```

Full capture is opt-in because serial traffic carries firmware images and device
credentials.

## Devices that are not serial at all

USB hardware with no serial interface never appears in the `devices:` list. This is not
moat failing to detect it — an SDR dongle (RTL2832U), a keyboard, or a USB drive has no
tty and no CDC class, so there is nothing for a serial broker to serve. `moat device
list` shows such devices in a separate "Other USB devices" section so a plugged-in
device is visible. Streaming hardware like an SDR belongs on the host, with the container
consuming its samples over the network: run a sample server on the host (for an SDR,
`rtl_tcp`), and allow its port with `network: host:`.

## Platform support

Serial devices work on Linux and macOS, with Docker and with Apple containers. Nothing
here requires privileged mode, and it does not disable the gVisor sandbox, because no
device is passed into the container — the broker holds the device on the host and speaks
TCP to the container.

General USB passthrough is not supported and is not planned: gVisor does not forward host
devices, Apple's container runtime has no USB passthrough, and the Linux devices cgroup
cannot filter on USB identity, so an allowlist could not be enforced.
