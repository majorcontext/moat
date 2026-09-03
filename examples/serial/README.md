# Serial device access — LilyGO T-Display S3

Gives an agent access to one approved USB serial device — here a LilyGO
T-Display S3 (ESP32-S3) — without passing any hardware into the container.

See the [serial devices guide](https://majorcontext.com/moat/guides/serial-devices)
for how approval and pinning work.

## Setup

Build and install the CLI from this branch, then plug the board in and find
its USB ID:

```bash
go build -o /usr/local/bin/moat ./cmd/moat   # or: make build-cli
moat proxy restart                            # the broker runs inside the proxy daemon
moat device list
```

Expected for a T-Display S3:

```
DEVICE        USB ID     SERIAL  PIN  DESCRIPTION
/dev/ttyACM0  303a:1001  ...     -    USB JTAG/serial
```

The values in `moat.yaml` are for the S3's native USB (`303a:1001`). If your
board shows a different ID (`10c4:ea60`, `1a86:55d4`, ...), copy what
`moat device list` prints.

## 1. Verify the board is reachable

From this directory:

```bash
moat run -- sh -c 'pip install --quiet esptool && esptool --port "$MOAT_SERIAL_ESP32_URL" chip_id'
```

Expected: esptool connects over RFC2217 and prints the chip type and MAC
address. If the app firmware is running, esptool drives DTR/RTS over the
broker to reset the chip into its bootloader — that is the part worth
watching, because a pty cannot carry those lines.

If the board is already in ROM download mode (hold BOOT, tap RST), the same
command works without any DTR/RTS toggling.

**No hardware? Test the plumbing anyway:**

```bash
moat run -- sh -c 'python3 -c "import serial; s = serial.serial_for_url(\"$MOAT_SERIAL_ESP32_URL\", timeout=2); s.baudrate = 115200; print(\"connected:\", s); print(\"port settings applied:\", s.get_settings())"'
```

`pip install pyserial` first if that errors. This proves the broker answers,
negotiates RFC2217, and applies line settings — the only parts that need
moat to be right — without depending on the board's state.

## 2. Flash or read the board

```bash
moat run -- sh -c 'pip install --quiet esptool && esptool --port "$MOAT_SERIAL_ESP32_URL" flash_id'
```

See the [esptool remote serial ports doc](https://docs.espressif.com/projects/esptool/en/latest/esp32/remote-serial-ports.html)
for what works over RFC2217. One caveat that page carries: esptool cannot
apply its *native-USB* behaviors to a remote port because the URL does not
expose USB IDs. On the S3's USB-Serial-JTAG interface the classic UART reset
sequence is what matters, so `chip_id`/`flash_id` and flashing over UART work;
stub flashing over native-USB paths does not apply here.

## 3. Console on the serial line

Anything that takes a pyserial URL works on the line as well:

```bash
moat run -- sh -c 'pip install --quiet pyserial && python3 -m serial.tools.miniterm --eol CRLF "$MOAT_SERIAL_ESP32_URL" 115200'
```

Exit with `ctrl+]`. Watch the boot log after tapping RST.

## 4. What the run recorded

Attach, detach, baud changes, and DTR/RTS transitions are recorded per
session:

```bash
moat logs <run-id>          # device events appear in the session log
moat audit <run-id>         # attach/detach are in the tamper-evident chain
```

## Check that pinning works

The security property worth verifying by hand, because it only shows up with
two boards of the same model:

1. Run the `chip_id` command above. The first run pins that board's USB serial
   number (for the S3, the value printed in `moat device list`'s SERIAL
   column).
2. Unplug it, plug in a second T-Display S3, and run the command again.

Expected: the run fails *before the container is created*, naming both serial
numbers and telling you to run `moat device forget esp32`. It must not talk
to the second board.

```bash
moat device forget esp32   # then the second board is approved on the next run
```

## Notes

- Use `$MOAT_SERIAL_ESP32_URL`, not a device path. DTR/RTS have no
  pseudo-terminal representation, so a device path could open the board and
  still never reset it into the bootloader.
- `MOAT_SERIAL_DEVICES` lists every device name available to the run.
- One run holds a device at a time; a second run asking for it fails with a
  clear message.
- If `moat device list` shows nothing on Linux, check the cable — the S3's
  USB-C orientation is reversible but some charge-only cables carry no data.
