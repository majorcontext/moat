# Serial device access

Gives an agent access to one approved USB serial device — here an ESP32 dev board.

See the [serial devices guide](https://majorcontext.com/moat/guides/serial-devices) for
how approval and pinning work.

## Setup

Plug the board in and find its USB ID:

```bash
moat device list
```

Copy the `vid` and `pid` it prints into `moat.yaml`. The values there are for a CP2102
bridge (common on LilyGO T-Display and T-Beam boards); yours may differ.

## Verify the board is reachable

```bash
moat run -- sh -c 'pip install --quiet esptool && esptool --port "$MOAT_SERIAL_ESP32_URL" chip_id'
```

Expected: esptool connects, resets the chip, and prints its type and MAC address. The
reset is the part that matters — it proves DTR and RTS reached the board.

## Flash firmware

```bash
moat run -- sh -c 'pip install --quiet esptool && esptool --port "$MOAT_SERIAL_ESP32_URL" write_flash 0x0 firmware.bin'
```

## Read the session log

```bash
moat logs <run-id>
```

Attach, detach, baud changes, and DTR/RTS transitions are recorded for every session.

## Check that pinning works

This is the security property worth verifying by hand, because it only shows up with two
boards of the same model.

1. Run the `chip_id` command above with the first board. It pins that board's serial
   number.
2. Unplug it, plug in a second board of the same model, and run the command again.

Expected: the run fails *before the container is created*, naming both serial numbers and
telling you to run `moat device forget esp32`. It must not flash the second board.

```bash
moat device forget esp32   # then the second board is approved on the next run
```

## Notes

- Use `$MOAT_SERIAL_ESP32_URL`, not a device path. Control lines (DTR/RTS) have no
  pseudo-terminal representation, so a device path could enumerate the board and still
  never flash it.
- `MOAT_SERIAL_DEVICES` lists every device name available to the run.
- One run holds a device at a time; a second run asking for it fails with a clear message.
