# A minimal Torznab indexer for the live test. It lists one legal release:
# the Blender Foundation's Creative Commons film Sintel, as the torrent that
# make_torrent.py built next to this file.
import http.server, urllib.parse, email.utils, time

TORRENT = open('/data/sintel-1080p.torrent', 'rb').read()
RELEASE = {
    'title': 'Sintel.2010.1080p.WEB-DL.x264-BLENDER',
    'imdb': '1727587', 'tmdb': '45745', 'size': 1172428172,
}

CAPS = '''<?xml version="1.0" encoding="UTF-8"?>
<caps><server title="Selfnook test"/><limits max="100" default="100"/>
<searching><search available="yes" supportedParams="q"/><movie-search available="yes" supportedParams="q,imdbid,tmdbid"/><tv-search available="no"/></searching>
<categories><category id="2000" name="Movies"><subcat id="2040" name="Movies/HD"/></category></categories></caps>'''

def feed(host, items):
    out = ['<?xml version="1.0" encoding="UTF-8"?><rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel><title>Selfnook test</title>']
    for r in items:
        link = 'http://%s/sintel.torrent' % host
        out.append('<item><title>%s</title><guid>%s</guid><link>%s</link><size>%d</size><pubDate>%s</pubDate>'
                   '<enclosure url="%s" length="%d" type="application/x-bittorrent"/>'
                   '<torznab:attr name="category" value="2000"/><torznab:attr name="category" value="2040"/>'
                   '<torznab:attr name="seeders" value="50"/><torznab:attr name="peers" value="60"/>'
                   '<torznab:attr name="imdbid" value="tt%s"/><torznab:attr name="tmdbid" value="%s"/></item>'
                   % (r['title'], link, link, r['size'], email.utils.formatdate(time.time() - 86400), link, r['size'], r['imdb'], r['tmdb']))
    out.append('</channel></rss>')
    return '\n'.join(out)

def matches(q):
    if q.get('imdbid'): return q['imdbid'][0].lstrip('t') == RELEASE['imdb']
    if q.get('tmdbid'): return q['tmdbid'][0] == RELEASE['tmdb']
    text = q.get('q', [''])[0].lower()
    return text == '' or 'sintel' in text

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        url = urllib.parse.urlparse(self.path)
        q = urllib.parse.parse_qs(url.query)
        if url.path.endswith('.torrent'):
            self.reply(TORRENT, 'application/x-bittorrent')
        elif q.get('t', [''])[0] == 'caps':
            self.reply(CAPS.encode(), 'application/xml')
        else:
            self.reply(feed(self.headers['Host'], [RELEASE] if matches(q) else []).encode(), 'application/rss+xml')
    def reply(self, body, kind):
        self.send_response(200)
        self.send_header('Content-Type', kind)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

http.server.ThreadingHTTPServer(('', 80), Handler).serve_forever()
