#!/usr/bin/env python3
"""Coarse power spectrum from a host-side rtl_tcp server.

The RTL-SDR dongle stays on the host, attached to rtl_tcp. This script runs
inside the moat container and consumes the sample stream over the network —
the only path a sandboxed container can take, since the dongle has no serial
interface at all.

Usage (from examples/serial-sdr, with `rtl_tcp -a 127.0.0.1 -p 1234` on the
host):

    moat run
"""

import math
import os
import socket
import struct
import sys
import time

RTL_TCP_HOST = os.environ.get("MOAT_HOST_GATEWAY", "moat-host")
RTL_TCP_PORT = int(os.environ.get("SDR_PORT", "1234"))

# rtl_tcp command codes (rtl_tcp/rtl_sdr protocol)
CMD_SET_FREQ = 0x01
CMD_SET_SAMPLE_RATE = 0x02
CMD_SET_GAIN_MODE = 0x04
CMD_SET_GAIN = 0x05

# rtlsdr_get_tuner_type() values, so the greeting names the hardware.
TUNER_NAMES = {
    1: "E4000",
    2: "FC0012",
    3: "FC0013",
    4: "FC2580",
    5: "R820T",
    6: "R828D",
}


def rtl_command(sock, command, value):
    """Send one rtl_tcp control command (5-byte packed struct, network order)."""
    sock.sendall(struct.pack(">BI", command, value))


def recv_exact(sock, n, what):
    """Read exactly n bytes, or exit — a close mid-read means the server died."""
    buf = b""
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            sys.exit(f"rtl_tcp closed the connection while receiving {what}")
        buf += chunk
    return buf


def connect():
    """Connect to the host-side rtl_tcp server, or exit saying what to fix."""
    print(f"connecting to rtl_tcp at {RTL_TCP_HOST}:{RTL_TCP_PORT}")
    try:
        return socket.create_connection((RTL_TCP_HOST, RTL_TCP_PORT), timeout=10)
    except ConnectionRefusedError:
        # The one setup mistake this example makes easy: running the container
        # before the host-side server. Nothing is listening on the port, so
        # say what to start rather than letting the traceback stand in for
        # documentation.
        sys.exit(
            f"connection refused — no rtl_tcp server on port {RTL_TCP_PORT}.\n"
            "  Start it on the host first (see this directory's README):\n"
            "    rtl_tcp -a 127.0.0.1 -p 1234\n"
            "  If it exits immediately with 'No supported devices found',\n"
            "  the dongle is not attached or another SDR app is holding it."
        )
    except socket.gaierror:
        sys.exit(
            f"cannot resolve {RTL_TCP_HOST} — the moat proxy should have mapped\n"
            "  this name to your host. This is a moat bug, not a setup problem."
        )
    except OSError as err:
        sys.exit(f"connecting to rtl_tcp: {err}")


def handshake(sock):
    """Read the 12-byte greeting, or exit naming the likely cause."""
    # The server sends magic 'RTL0' raw, then tuner type and gain-step count
    # through htonl — big-endian, like every field in this protocol.
    header = recv_exact(sock, 12, "the greeting header")
    magic, tuner, gains = struct.unpack(">4sII", header)
    if magic != b"RTL0":
        sys.exit(
            f"greeting magic was {magic!r}, not b'RTL0' — something other than\n"
            f"  rtl_tcp is listening on port {RTL_TCP_PORT}."
        )
    name = TUNER_NAMES.get(tuner, f"type {tuner}")
    print(f"rtl_tcp up: tuner {name}, {gains} gain steps")


def read_spectrum(sock, seconds=2.0):
    """Read I/Q bytes for `seconds`, return 64-bin power spectrum in dB."""
    deadline = time.monotonic() + seconds
    samples = []
    while time.monotonic() < deadline:
        chunk = sock.recv(65536)
        if not chunk:
            sys.exit("rtl_tcp closed the connection mid-sweep — did the host-side server exit?")
        samples.append(chunk)
    iq = b"".join(samples)

    # 8-bit unsigned I/Q pairs → per-bin power, averaged into 64 bins.
    # Keep it numpy-free so the example runs on a bare python image.
    bins = [0.0] * 64
    counts = [0] * 64
    for i in range(0, len(iq) - 1, 2):
        i_val = iq[i] - 128
        q_val = iq[i + 1] - 128
        b = (i * 64 // len(iq)) % 64
        bins[b] += i_val * i_val + q_val * q_val
        counts[b] += 1
    return [10 * math.log10(v / c) if c and v > 0 else -100.0 for v, c in zip(bins, counts)]


def main():
    sock = connect()
    handshake(sock)

    rtl_command(sock, CMD_SET_FREQ, 100_000_000)       # 100 MHz FM broadcast
    rtl_command(sock, CMD_SET_SAMPLE_RATE, 1_024_000)  # 1.024 Msps
    rtl_command(sock, CMD_SET_GAIN_MODE, 1)            # manual gain
    rtl_command(sock, CMD_SET_GAIN, 300)               # 30.0 dB (tenth-dB units)

    for sweep in range(3):
        spectrum = read_spectrum(sock, seconds=2.0)
        peak_bin = max(range(64), key=lambda b: spectrum[b])
        bar = " ".join("#" * max(0, min(30, int(spectrum[b] + 40) // 2)) or "." for b in range(0, 64, 4))
        print(f"sweep {sweep + 1}: {bar}  peak bin {peak_bin} ({spectrum[peak_bin]:.1f} dB)")

    print("done")
    sock.close()


if __name__ == "__main__":
    main()
