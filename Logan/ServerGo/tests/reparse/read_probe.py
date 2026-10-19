"""Internal-network HTTP probe. Never follows redirects to an unvalidated host."""
import base64
import json
import sys
import time
import urllib.error
import urllib.request
from urllib.parse import urlsplit


def run(host, mode, cases, allowed_oss):
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    def get(url):
        try:
            r = opener.open(url, timeout=4)
        except urllib.error.HTTPError as error:
            r = error
        raw = r.read()
        mime = r.headers.get('Content-Type', '').split(';')[0].strip().lower()
        result = {'status': r.status, 'mime': mime,
                  'cors': {k.lower(): v for k, v in r.headers.items() if k.lower().startswith('access-control-')}}
        if mime == 'application/json':
            result['body'] = json.loads(raw)
        else:
            result['bytes'] = base64.b64encode(raw).decode()
        if r.headers.get('Location'):
            result['location'] = r.headers['Location']
        return result
    base = 'http://' + host + ':8080'
    end = time.monotonic() + 80
    while True:
        try:
            if get(base + cases[0]['path'])['status'] == 200:
                break
        except Exception:
            pass
        if time.monotonic() > end:
            raise RuntimeError('HTTP startup timeout')
        time.sleep(.5)
    result = {'responses': {case['name']: get(base + case['path']) for case in cases}}
    if mode == 'go':
        result['readinessStatus'] = get(base + '/readyz')['status']
        if result['readinessStatus'] != 503:
            raise AssertionError('Go readiness must remain 503')
    redirect = result['responses']['signed-download']
    if redirect['status'] != 302:
        raise AssertionError('signed download must redirect')
    url = urlsplit(redirect['location'])
    if url.scheme != 'http' or url.hostname not in allowed_oss or url.port != 8080:
        raise AssertionError('signed URL escaped isolated OSS fixture')
    result['object'] = get(redirect['location'])
    return result


if __name__ == '__main__':
    print(json.dumps(run(*json.loads(sys.argv[1])), ensure_ascii=False))
