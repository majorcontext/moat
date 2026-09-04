# Serial device access — any ESP32-family board

Gives an agent access to one approved USB serial device — an ESP32-family
dev board, an ESP8266, or any other board esptool supports — without
passing any hardware into the container.

See the [serial devices guide](https://majorcontext.com/moat/guides/serial-devices)
for how approval and pinning work.

## Setup

Build and install the CLI, then plug the board in and find its USB ID:

```bash
make build-cli && install -m 0755 moat /usr/local/bin/moat
moat proxy restart                            # the broker runs inside the proxy daemon
moat device list
```

For example, a LilyGO T-Display S3 (the board this example was verified on)
shows up on its native USB:

```
DEVICE                  USB ID     SERIAL NUMBER      PIN  DESCRIPTION
/dev/cu.usbmodem83201   303a:1001  E0:72:A1:A2:32:48  -    USB JTAG/serial debug unit
```

The value in `moat.yaml`'s `match:` is the only board-specific part of this
example — copy what `moat device list` prints for your board. Everything
else (verify script, reset behavior, esptool commands) is the same across
the family, because esptool autodetects the chip over the line: `chip-id`
identifies ESP8266, ESP32, S2, S3, C2, C3, C5, C6, C61, H2, H4, P4 and
friends without configuration.

Boards reach USB two ways and both work the same here:

- through an external bridge chip (CP2102, CH340, CH9102F — most plain
  dev kits), where DTR/RTS are real UART modem lines, or
- through the chip's own USB-Serial-JTAG peripheral (ESP32-S3/C3/C6
  boards), which interprets the same CDC line states.

Either way, esptool drives the auto-reset over RFC2217 to enter the
bootloader. A device path inside the container could not: a pseudo-terminal
carries no DTR/RTS, which is why the example uses the broker's
`rfc2217://` URL instead.

## 1. Verify the board is reachable

From this directory:

```bash
moat run
```

That's the whole command — `moat.yaml`'s `command:` runs `verify.sh`, which
calls `esptool chip-id` against `$MOAT_SERIAL_BOARD_URL`. esptool installs
at image build time via the `pip:esptool` dependency; nothing is installed
at run time.

Expected: esptool connects over RFC2217 and prints the chip type and MAC
address. If the app firmware is running, esptool drives DTR/RTS over the
broker to reset the chip into its bootloader — that is the part worth
watching, because a pty cannot carry those lines.

verify.sh filters one message esptool prints: `Failed to get VID/PID of a
device on rfc2217://...`. It is informational, not an error — esptool asks
the port for its USB VID/PID to pick a reset strategy, and an `rfc2217://`
URL has no USB identity by construction (the device lives on the host and
is deliberately not visible in the container). esptool falls back to the
standard (classic UART) reset sequence, which is the one that works over
RFC2217, and prints the message again on every lookup because it caches the
answer only on success. Run esptool without the filter to see them:

```bash
moat run -- sh -c 'esptool --port "$MOAT_SERIAL_BOARD_URL" chip-id'
```

If the board is already in ROM download mode (hold BOOT, tap RST), the same
command works without any DTR/RTS toggling.

For anything not baked into the image, pass the command inline instead —
`command:` in moat.yaml is an argv list, not a shell string, so
`$MOAT_SERIAL_BOARD_URL` does not expand there and the shell wrapper does
the expanding:

```bash
moat run -- sh -c 'pip install --quiet esptool && esptool --port "$MOAT_SERIAL_BOARD_URL" chip-id'
```

**No hardware? Test the plumbing anyway:**

```bash
moat run -- sh -c 'python3 -c "import serial; s = serial.serial_for_url(\"$MOAT_SERIAL_BOARD_URL\", timeout=2); s.baudrate = 115200; print(\"connected:\", s); print(\"port settings applied:\", s.get_settings())"'
```

`pip install pyserial` first if that errors. This proves the broker answers,
negotiates RFC2217, and applies line settings — the only parts that need
moat to be right — without depending on the board's state.

## 2. Flash or read the board

```bash
moat run -- sh -c 'esptool --port "$MOAT_SERIAL_BOARD_URL" flash-id'
```

See the [esptool remote serial ports doc](https://docs.espressif.com/projects/esptool/en/latest/esp32/remote-serial-ports.html)
for what works over RFC2217. One caveat that page carries: esptool cannot
apply its *native-USB* behaviors to a remote port because the URL does not
expose USB IDs. Boards on a bridge chip are unaffected, and on native-USB
boards the classic UART reset sequence is what matters anyway, so
`chip-id`/`flash-id` and flashing over UART work for both kinds.

## 3. Console on the serial line

Anything that takes a pyserial URL works on the line as well:

```bash
moat run -- sh -c 'pip install --quiet pyserial && python3 -m serial.tools.miniterm --eol CRLF "$MOAT_SERIAL_BOARD_URL" 115200'
```

Exit with `ctrl+]`. Watch the boot log after tapping RST. (Adding
`pip:pyserial` to `dependencies:` drops the install from this command too.)

## 4. What the run recorded

Attach, detach, baud changes, and DTR/RTS transitions are recorded per
session:

```bash
moat audit <run-id>         # attach/detach are in the tamper-evident chain
```

Every event — including the per-signal chatter — also lands in the run's
`devices.jsonl` (`~/.moat/runs/<run-id>/devices.jsonl`), and `record: full`
payload bytes in `serial-<name>.capture` beside it. Both are JSONL; `jq` reads
them directly:

```bash
jq -r '.kind + " " + .detail' ~/.moat/runs/<run-id>/devices.jsonl
```

## Check that pinning works

The security property worth verifying by hand, because it only shows up with
two boards of the same model:

1. Run the `chip-id` command above. The first run pins that board's USB
   serial number (the value printed in `moat device list`'s SERIAL NUMBER
   column).
2. Unplug it, plug in a second board of the same model, and run the command
   again.

Expected: the run fails *before the container is created*, naming both
serial numbers and telling you to run `moat device forget board`. It must
not talk to the second board.

```bash
moat device forget board   # then the second board is approved on the next run
```

## Notes

- Use `$MOAT_SERIAL_BOARD_URL`, not a device path. DTR/RTS have no
  pseudo-terminal representation, so a device path could open the board and
  still never reset it into the bootloader.
- `MOAT_SERIAL_DEVICES` lists every device name available to the run.
- One run holds a device at a time; a second run asking for it fails with a
  clear message.
- If `moat device list` shows nothing on Linux, check the cable — some
  charge-only USB cables carry no data.
