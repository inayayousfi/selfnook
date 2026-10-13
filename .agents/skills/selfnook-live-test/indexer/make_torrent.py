# Builds a single-file torrent whose web seed is the given address, so a
# client downloads the file over HTTP without any other peer.
# Usage: make_torrent.py FILE NAME WEB_SEED_URL OUT.torrent
import hashlib, sys
path, name, url, out = sys.argv[1:5]
piece = 4 * 1024 * 1024
pieces, length = b'', 0
with open(path, 'rb') as f:
    while chunk := f.read(piece):
        pieces += hashlib.sha1(chunk).digest()
        length += len(chunk)
def enc(v):
    if isinstance(v, int): return b'i%de' % v
    if isinstance(v, str): v = v.encode()
    if isinstance(v, bytes): return b'%d:%s' % (len(v), v)
    if isinstance(v, list): return b'l' + b''.join(map(enc, v)) + b'e'
    return b'd' + b''.join(enc(k) + enc(v[k]) for k in sorted(v)) + b'e'
info = {'name': name, 'length': length, 'piece length': piece, 'pieces': pieces}
open(out, 'wb').write(enc({'info': info, 'url-list': [url], 'created by': 'selfnook live test'}))
print(length, hashlib.sha1(enc(info)).hexdigest())
