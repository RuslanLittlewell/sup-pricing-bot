import ipaddress
import json
import socket
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import unquote, urlparse

from playwright.sync_api import sync_playwright


MAX_REQUEST = 64 * 1024
MAX_BODY = 5 * 1024 * 1024
TIMEOUT_MS = 30_000


def validate_public_url(raw_url: str, cache=None) -> str:
    parsed = urlparse(raw_url)
    if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.password:
        raise ValueError("invalid target URL")
    key = (parsed.hostname, parsed.port or (443 if parsed.scheme == "https" else 80))
    if cache is not None and key in cache:
        return raw_url
    for _, _, _, _, sockaddr in socket.getaddrinfo(key[0], key[1], type=socket.SOCK_STREAM):
        if not ipaddress.ip_address(sockaddr[0]).is_global:
            raise ValueError("target resolved to a non-public address")
    if cache is not None:
        cache.add(key)
    return raw_url


def proxy_settings(raw_proxy: str):
    if not raw_proxy:
        return None
    parsed = urlparse(raw_proxy)
    if parsed.scheme not in ("http", "https", "socks5") or not parsed.hostname:
        raise ValueError("invalid proxy URL")
    settings = {"server": f"{parsed.scheme}://{parsed.hostname}:{parsed.port or 8080}"}
    if parsed.username:
        settings["username"] = unquote(parsed.username)
    if parsed.password:
        settings["password"] = unquote(parsed.password)
    return settings


def cookie_list(cookie_header: str, target: str):
    cookies = []
    for item in cookie_header.split(";"):
        if "=" not in item:
            continue
        name, value = item.strip().split("=", 1)
        if name:
            cookies.append({"name": name, "value": value, "url": target})
    return cookies


class Handler(BaseHTTPRequestHandler):
    server_version = "firefox-relay"

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
        if self.path != "/render":
            self.send_error(404)
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length <= 0 or length > MAX_REQUEST:
                raise ValueError("invalid request size")
            payload = json.loads(self.rfile.read(length))
            target = validate_public_url(str(payload.get("url", "")))
            headers = payload.get("headers") or {}
            headers = {str(k): str(v) for k, v in headers.items() if str(k).lower() not in {"host", "content-length", "cookie"}}
            proxy = proxy_settings(str(payload.get("proxy", "")).strip())
            cookie_header = str((payload.get("headers") or {}).get("Cookie", ""))
            validated_hosts = set()

            with sync_playwright() as playwright:
                browser = playwright.firefox.launch(headless=True, proxy=proxy)
                context = browser.new_context(
                    locale="pl-PL",
                    timezone_id="Europe/Warsaw",
                    viewport={"width": 1920, "height": 1080},
                    extra_http_headers=headers,
                )
                if cookie_header:
                    context.add_cookies(cookie_list(cookie_header, target))
                page = context.new_page()

                def guard(route):
                    try:
                        validate_public_url(route.request.url, validated_hosts)
                        route.continue_()
                    except Exception:
                        route.abort()

                page.route("**/*", guard)
                response = page.goto(target, wait_until="domcontentloaded", timeout=TIMEOUT_MS)
                page.wait_for_timeout(5_000)
                final_url = validate_public_url(page.url)
                body = page.content().encode("utf-8")[:MAX_BODY]
                status = response.status if response else 0
                context.close()
                browser.close()

            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("X-Upstream-Status", str(status))
            self.send_header("X-Final-URL", final_url)
            self.end_headers()
            self.wfile.write(body)
        except ValueError as exc:
            self.send_error(400, str(exc))
        except Exception:
            self.send_error(502, "render failed")


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
