import ipaddress
import json
import socket
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urljoin, urlparse

from curl_cffi import requests


MAX_REQUEST = 64 * 1024
MAX_BODY = 5 * 1024 * 1024
TIMEOUT = 25


def validate_public_url(raw_url: str) -> str:
    parsed = urlparse(raw_url)
    if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.password:
        raise ValueError("invalid target URL")
    port = parsed.port or (443 if parsed.scheme == "https" else 80)
    for family, _, _, _, sockaddr in socket.getaddrinfo(parsed.hostname, port, type=socket.SOCK_STREAM):
        address = ipaddress.ip_address(sockaddr[0])
        if not address.is_global:
            raise ValueError("target resolved to a non-public address")
    return raw_url


class Handler(BaseHTTPRequestHandler):
    server_version = "curl-cffi-relay"

    def log_message(self, *_args):
        return

    def do_GET(self):
        if self.path == "/healthz":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        self.send_error(404)

    def do_POST(self):
        if self.path != "/fetch":
            self.send_error(404)
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length <= 0 or length > MAX_REQUEST:
                raise ValueError("invalid request size")
            payload = json.loads(self.rfile.read(length))
            target = validate_public_url(str(payload.get("url", "")))
            headers = payload.get("headers") or {}
            headers = {str(k): str(v) for k, v in headers.items() if str(k).lower() not in {"host", "content-length"}}
            proxy = str(payload.get("proxy", "")).strip()
            proxies = {"http": proxy, "https": proxy} if proxy else None

            session = requests.Session()
            current = target
            response = None
            for _ in range(6):
                response = session.get(
                    current,
                    headers=headers,
                    proxies=proxies,
                    impersonate="chrome",
                    timeout=TIMEOUT,
                    allow_redirects=False,
                    stream=True,
                )
                if response.status_code not in (301, 302, 303, 307, 308):
                    break
                location = response.headers.get("location")
                response.close()
                if not location:
                    break
                current = validate_public_url(urljoin(current, location))
            else:
                raise ValueError("too many redirects")

            body = bytearray()
            for chunk in response.iter_content(chunk_size=64 * 1024):
                remaining = MAX_BODY - len(body)
                if remaining <= 0:
                    break
                body.extend(chunk[:remaining])
            response.close()
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("X-Upstream-Status", str(response.status_code))
            self.end_headers()
            self.wfile.write(bytes(body))
        except ValueError as exc:
            self.send_error(400, str(exc))
        except Exception:
            self.send_error(502, "upstream fetch failed")


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
