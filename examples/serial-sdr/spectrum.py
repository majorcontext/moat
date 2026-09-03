#!/usr/bin/env python3
"""Coarse power spectrum from a host-side rtl_tcp server.

The RTL-SDR dongle stays on the host, attached to rtl_tcp. This script runs
inside the moat container and consumes the sample stream over the network —
the only path a sandboxed container can take, since the dongle has no serial
interface at all.

Usage (from examples/serial-sdr, with `rtl_tcp -a 127.0.0.1 -p 1234` on the
host):

    moat run -- python3 /workspace/spectrum.py
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


def rtl_command(sock, command, value):
    """Send one rtl_tcp control command."""
    sock.sendall(struct.pack(">BI", command, value))


def read_spectrum(sock, seconds=2.0):
    """Read I/Q bytes for `seconds`, return 64-bin power spectrum in dB."""
    deadline = time.monotonic() + seconds
    samples = []
    while time.monotonic() < deadline:
        chunk = sock.recv(65536)
        if not chunk:
            raise ConnectionError("rtl_tcp closed the connection")
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
    print(f"connecting to rtl_tcp at {RTL_TCP_HOST}:{RTL_TCP_PORT}")
    sock = socket.create_connection((RTL_TCP_HOST, RTL_TCP_PORT), timeout=10)

    # rtl_tcp greets us with a 12-byte header: magic 'RTL0', tuner type, gain count.
    header = b""
    while len(header) < 12:
        header += sock.recv(12 - len(header))
    magic, tuner, gains = struct.unpack("<4sII", header)
    if magic != b"RTL0":
        sys.exit(f"unexpected rtl_tcp header magic {magic!r}")
    print(f"rtl_tcp up: tuner type {tuner}, {gains} gain steps")

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
