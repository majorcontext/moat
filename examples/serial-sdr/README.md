# USB SDR radio — why it needs a different mechanism than serial

This example documents what a USB SDR dongle (RTL2832U-based, e.g. RTL-SDR
Blog V3/V4, NooElec) needs from moat, and why the `devices:` serial path does
**not** cover it.

**Short version: an RTL-SDR is not a serial device.** It enumerates as a
vendor-specific USB device (commonly `0bda:2838` or `0bda:2832`) with no CDC
class and no tty of any kind. `librtlsdr` talks to it with libusb control
transfers for register/tuner access and bulk transfers on endpoint `0x81` for
the I/Q sample stream. There is no baud rate, no DTR/RTS, no line settings —
nothing RFC2217 could carry even in principle. The moat serial broker's
tty-only allowlist is doing its job by refusing it.

Do not add an `0bda:2838` entry to `devices:` — the run fails with "no
attached device matches USB ID 0bda:2838". (`moat device list` shows the
dongle in its "Other USB devices" section — listed, but not usable through
`devices:`.)

## What receiving samples would take

The sample stream is roughly 2.4 MB/s at 2.4 Msps (8-bit I+Q). Any mechanism
that carries it out of the sandbox has to move megabytes per second, not
line-at-a-time serial traffic. The plausible shapes are:

1. **Host-side sample server.** A process on the host (outside the sandbox)
   runs `librtlsdr`, exposes the stream over TCP or a local HTTP endpoint,
   and the container consumes it. This is what `rtl_tcp` already does — run
   it on the host and point the container at it:
   ```bash
   rtl_tcp -a 127.0.0.1 -p 1234        # host
   moat run -- python3 dsp.py rtl://$MOAT_HOST_GATEWAY:1234
   ```
   With `network: host: [1234]` in moat.yaml so the port is allowed. The
   agent gets raw samples but cannot reconfigure the tuner beyond what
   `rtl_tcp`'s command channel allows.
2. **A libusb-forwarding broker** (USB/IP or a custom FFI bridge). This is
   the general-USB-passthrough problem the serial design already ruled out
   for moat's runtimes: gVisor does not forward host devices, Apple
   containers have no USB passthrough, and the devices cgroup cannot filter
   by VID/PID. It would also hand the container raw USB, which is strictly
   more than the sample stream.

Option 1 is the one that works today, on every runtime, with no moat
changes. It trades exact device control for stream consumption, which is the
right trade for "listen to this frequency" tasks. The `rtl_tcp` command
channel still lets the agent set frequency, sample rate, and gain — enough to
tune — while firmware-level control (bias tee, reflashing) stays on the host.
Flashing *new firmware* to a device like this (if it had any) would be a
different, serial-shaped problem.

## What this directory contains

A minimal consumer you can point at a host-side `rtl_tcp` server, to make the
network path concrete:

```bash
# On the host, with the dongle attached and rtl-sdr tools installed
# (macOS: `brew install librtlsdr`):
rtl_tcp -a 127.0.0.1 -p 1234

# In another terminal, from this directory:
moat run
```

`moat.yaml` here allows host port 1234 and runs `spectrum.py` — `moat run`
with no arguments picks up the `command:` from the config. The script connects
to `rtl_tcp`, sets a center frequency, and prints a coarse power spectrum every
few seconds — enough to see the dongle is alive and the path through the proxy
works.

## If your "SDR" is a different animal

Some radios do carry a serial console or control UART (some SDRs expose a
CDC-ACM control port next to the sample interface). If `moat device list`
shows your device, the serial path applies to that control port — but it
still cannot carry the sample stream, which will be USB bulk or Ethernet.
Check with:

```bash
moat device list          # main table → serial path applies
ls /dev/ttyUSB* /dev/ttyACM*   # host check, for context
```
