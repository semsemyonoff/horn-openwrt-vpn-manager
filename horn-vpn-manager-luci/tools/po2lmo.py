#!/usr/bin/env python3
"""Convert .po translation files to LuCI .lmo binary format."""

import struct
import sys
import re


def sfh_hash(data):
    """SuperFastHash by Paul Hsieh — must match LuCI's C implementation."""
    if isinstance(data, str):
        data = data.encode("utf-8")
    length = len(data)
    if length == 0:
        return 0

    h = length & 0xFFFFFFFF
    rem = length & 3
    idx = 0
    for _ in range(length >> 2):
        h = (h + (data[idx] | (data[idx + 1] << 8))) & 0xFFFFFFFF
        tmp = (((data[idx + 2] | (data[idx + 3] << 8)) << 11) ^ h) & 0xFFFFFFFF
        h = ((h << 16) ^ tmp) & 0xFFFFFFFF
        idx += 4
        h = (h + (h >> 11)) & 0xFFFFFFFF

    if rem == 3:
        h = (h + (data[idx] | (data[idx + 1] << 8))) & 0xFFFFFFFF
        h = (h ^ (h << 16)) & 0xFFFFFFFF
        h = (h ^ (data[idx + 2] << 18)) & 0xFFFFFFFF
        h = (h + (h >> 11)) & 0xFFFFFFFF
    elif rem == 2:
        h = (h + (data[idx] | (data[idx + 1] << 8))) & 0xFFFFFFFF
        h = (h ^ (h << 11)) & 0xFFFFFFFF
        h = (h + (h >> 17)) & 0xFFFFFFFF
    elif rem == 1:
        h = (h + data[idx]) & 0xFFFFFFFF
        h = (h ^ (h << 10)) & 0xFFFFFFFF
        h = (h + (h >> 1)) & 0xFFFFFFFF

    h = (h ^ (h << 3)) & 0xFFFFFFFF
    h = (h + (h >> 5)) & 0xFFFFFFFF
    h = (h ^ (h << 4)) & 0xFFFFFFFF
    h = (h + (h >> 17)) & 0xFFFFFFFF
    h = (h ^ (h << 25)) & 0xFFFFFFFF
    h = (h + (h >> 6)) & 0xFFFFFFFF
    return h


def parse_po(path):
    """Parse .po file, yield (msgid, msgstr) pairs."""
    with open(path, "r", encoding="utf-8") as f:
        content = f.read()

    # Split into entries separated by blank lines
    entries = re.split(r"\n\n+", content)
    for entry in entries:
        lines = entry.strip().splitlines()
        if not lines:
            continue

        msgid_parts = []
        msgstr_parts = []
        target = None

        for line in lines:
            if line.startswith("#"):
                continue
            if line.startswith("msgid "):
                target = msgid_parts
                target.append(line[6:])
            elif line.startswith("msgstr "):
                target = msgstr_parts
                target.append(line[7:])
            elif line.startswith('"') and target is not None:
                target.append(line)

        msgid = "".join(s.strip('"') for s in msgid_parts)
        msgstr = "".join(s.strip('"') for s in msgstr_parts)

        # Unescape
        for esc_from, esc_to in [("\\n", "\n"), ("\\t", "\t"), ('\\"', '"'), ("\\\\", "\\")]:
            msgid = msgid.replace(esc_from, esc_to)
            msgstr = msgstr.replace(esc_from, esc_to)

        if msgid and msgstr:
            yield msgid, msgstr


def write_lmo(pairs, path):
    """Write .lmo binary file.

    Format:
      1. Data blob — translated strings concatenated
      2. Index — sorted array of (key_id, val_id, offset, length), each uint32 big-endian
      3. Last 4 bytes — uint32 big-endian offset where index starts
    """
    data = bytearray()
    index = []

    for msgid, msgstr in pairs:
        msgstr_bytes = msgstr.encode("utf-8")
        offset = len(data)
        length = len(msgstr_bytes)
        data.extend(msgstr_bytes)

        key_id = sfh_hash(msgid)
        val_id = sfh_hash(msgstr_bytes)
        index.append((key_id, val_id, offset, length))

    # Sort by key_id for binary search at runtime
    index.sort(key=lambda e: e[0])

    idx_offset = len(data)

    with open(path, "wb") as f:
        f.write(data)
        for key_id, val_id, offset, length in index:
            f.write(struct.pack("!IIII", key_id, val_id, offset, length))
        f.write(struct.pack("!I", idx_offset))


def main():
    if len(sys.argv) != 3:
        print(f"Usage: {sys.argv[0]} input.po output.lmo", file=sys.stderr)
        sys.exit(1)

    pairs = list(parse_po(sys.argv[1]))
    write_lmo(pairs, sys.argv[2])
    print(f"{sys.argv[1]} -> {sys.argv[2]} ({len(pairs)} strings)")


if __name__ == "__main__":
    main()
