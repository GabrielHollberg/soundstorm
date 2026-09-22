#!/usr/bin/env python3
"""Check every committed PNG decodes.

This exists because nineteen of the twenty-one screenshots in docs/shots were
broken for months and nothing said so. Something had read them as text and
written them back, which on Windows drops every carriage return - and a PNG's
eight byte signature is 89 50 4E 47 0D 0A 1A 0A, so the first casualty is the
signature itself. GitHub renders a broken PNG as a broken-image icon, which
looks like a bad link rather than a damaged file, and the README is where
somebody decides whether to install this.

The check is the file's own: PNG carries a CRC32 per chunk and states its
dimensions, so "this image is intact" is a question with an answer rather than
something to eyeball. It also inflates the pixel data and compares its length
against the header, which catches damage the CRCs would not.

    python3 scripts/check-images.py [paths...]

Nothing here is specific to screenshots. Point it at any PNG.
"""

import glob
import os
import struct
import sys
import zlib

SIGNATURE = b'\x89PNG\r\n\x1a\n'
CHANNELS = {0: 1, 2: 3, 3: 1, 4: 2, 6: 4}


def check(path):
    """Return None if the file is a sound PNG, or a string saying what is wrong."""
    with open(path, 'rb') as fh:
        d = fh.read()

    if d[:8] != SIGNATURE:
        if d[:7] == b'\x89PNG\n\x1a\n':
            # Named exactly, because the cause is not guessable from the
            # symptom and the fix is to put the byte back rather than to
            # re-export the image.
            return 'the signature has lost its carriage return (read as text somewhere)'
        return 'not a PNG: starts %s' % ' '.join('%02x' % b for b in d[:8])

    pos, header, pixels, seen_end = 8, None, bytearray(), False
    while pos + 12 <= len(d):
        length = struct.unpack('>I', d[pos:pos + 4])[0]
        kind = d[pos + 4:pos + 8]
        body = d[pos + 8:pos + 8 + length]
        if len(body) != length:
            return 'chunk %s at %d claims %d bytes and has %d' % (
                kind.decode('latin-1'), pos, length, len(body))
        stated = d[pos + 8 + length:pos + 12 + length]
        if len(stated) != 4:
            return 'chunk %s at %d has no checksum' % (kind.decode('latin-1'), pos)
        if zlib.crc32(kind + body) != struct.unpack('>I', stated)[0]:
            return 'chunk %s at %d fails its own checksum' % (kind.decode('latin-1'), pos)
        if kind == b'IHDR':
            header = struct.unpack('>IIBBBBB', body)
        elif kind == b'IDAT':
            pixels += body
        pos += 12 + length
        if kind == b'IEND':
            seen_end = True
            break

    if header is None:
        return 'no IHDR'
    if not seen_end:
        return 'no IEND: the file stops mid-image'
    if pos != len(d):
        return '%d bytes after IEND' % (len(d) - pos)

    width, height, depth, colour = header[0], header[1], header[2], header[3]
    if colour not in CHANNELS:
        return 'unknown colour type %d' % colour
    try:
        raw = zlib.decompress(bytes(pixels))
    except zlib.error as err:
        return 'the pixel data will not inflate: %s' % err
    stride = 1 + (width * CHANNELS[colour] * depth + 7) // 8
    if len(raw) != stride * height:
        return 'inflated to %d bytes, but %dx%d needs %d' % (
            len(raw), width, height, stride * height)
    return None


def main(argv):
    paths = argv[1:]
    if not paths:
        paths = sorted(glob.glob('docs/**/*.png', recursive=True))
        paths += sorted(glob.glob('internal/**/*.png', recursive=True))
    if not paths:
        print('No PNGs found. Run this from the repository root.', file=sys.stderr)
        return 2

    bad = 0
    for path in paths:
        problem = check(path)
        if problem:
            bad += 1
            print('BROKEN %-28s %s' % (os.path.basename(path), problem))
        else:
            print('ok     %s' % os.path.basename(path))

    print()
    if bad:
        print('%d of %d images are damaged.' % (bad, len(paths)))
        return 1
    print('%d images, all intact.' % len(paths))
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv))
