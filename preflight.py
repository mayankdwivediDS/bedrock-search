#!/usr/bin/env python3
"""
preflight.py — quick health + speed check for a running go-suggest-neo server.

What it does, in order:
  1. Dependencies  — Go toolchain present + `go build ./...` compiles (proves
                     every module dependency resolves). Skippable.
  2. Corpus        — GET /health; reports word count + live version.
  3. Reload        — POST /reload; confirms the in-process reload works and
                     reports the corpus size afterwards.
  4. Speed         — fires a batch of /suggest queries and reports latency
                     percentiles (p50/p90/p95/p99/max) and throughput.

Stdlib only — no pip installs. Run it after a deploy or a /reload to confirm
the API is healthy and fast.

Usage:
    python3 preflight.py
    python3 preflight.py --url http://localhost:8001 --token <ADMIN_TOKEN>
    python3 preflight.py --skip-build          # skip the Go compile check
    python3 preflight.py --requests 500        # bigger speed sample
    python3 preflight.py --queries ap,mark,ban # custom prefixes to hammer

Token resolution (for /reload): --token, else $ADMIN_TOKEN, else parsed from
the .env file next to this script.
"""

import argparse
import json
import os
import statistics
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))

# Windows consoles default to cp1252; make sure we can print cleanly.
try:
    sys.stdout.reconfigure(encoding="utf-8")
except Exception:
    pass

# ── tiny ANSI helpers ────────────────────────────────────────────────────
def _c(code, s):
    return s if os.environ.get("NO_COLOR") else f"\033[{code}m{s}\033[0m"

def ok(s):    return _c("32", s)   # green
def bad(s):   return _c("31", s)   # red
def warn(s):  return _c("33", s)   # yellow
def head(s):  return _c("1;36", s) # bold cyan

PASS = ok("PASS")
FAIL = bad("FAIL")
WARN = warn("WARN")


def load_token(cli_token):
    if cli_token:
        return cli_token
    if os.environ.get("ADMIN_TOKEN"):
        return os.environ["ADMIN_TOKEN"]
    env_path = os.path.join(HERE, ".env")
    if os.path.isfile(env_path):
        with open(env_path, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line.startswith("ADMIN_TOKEN=") and "#" not in line.split("=", 1)[0]:
                    return line.split("=", 1)[1].strip()
    return ""


# A no-proxy opener built once. On Windows, the default opener runs proxy
# auto-detection (WPAD) on every request, which can add ~2s of latency to
# localhost calls and wreck the speed measurement. An explicit empty
# ProxyHandler skips that entirely.
_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def http(method, url, token=None, timeout=10):
    """Return (status, body_bytes). Never raises on HTTP errors."""
    req = urllib.request.Request(url, method=method)
    if token:
        req.add_header("X-API-Key", token)
    try:
        with _OPENER.open(req, timeout=timeout) as r:
            return r.status, r.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()
    except urllib.error.URLError as e:
        return None, str(e).encode()


def get_json(method, url, token=None, timeout=10):
    status, body = http(method, url, token, timeout)
    try:
        return status, json.loads(body)
    except Exception:
        return status, {"raw": body.decode("utf-8", "replace")[:200]}


# ── steps ────────────────────────────────────────────────────────────────
def step_dependencies(skip_build):
    print(head("\n[1/4] Dependencies"))
    all_ok = True

    # Go toolchain
    try:
        out = subprocess.run(["go", "version"], capture_output=True, text=True, timeout=30)
        if out.returncode == 0:
            print(f"  {PASS}  go toolchain: {out.stdout.strip()}")
        else:
            print(f"  {FAIL}  `go version` failed: {out.stderr.strip()}")
            all_ok = False
    except (FileNotFoundError, subprocess.TimeoutExpired) as e:
        print(f"  {FAIL}  Go not found on PATH ({e})")
        all_ok = False

    if skip_build:
        print(f"  {WARN}  build/dependency compile check skipped (--skip-build)")
        return all_ok

    # `go build ./...` proves all module deps resolve and the code compiles.
    print("  ...  running `go build ./...` (proves all dependencies resolve)")
    t0 = time.perf_counter()
    out = subprocess.run(["go", "build", "./..."], cwd=HERE, capture_output=True, text=True, timeout=300)
    dt = time.perf_counter() - t0
    if out.returncode == 0:
        print(f"  {PASS}  go build ./... ({dt:.1f}s) — dependencies OK")
    else:
        print(f"  {FAIL}  go build failed:\n{out.stderr.strip()}")
        all_ok = False
    return all_ok


def step_corpus(url):
    print(head("\n[2/4] Corpus / health"))
    status, body = get_json("GET", f"{url}/health")
    if status != 200:
        print(f"  {FAIL}  GET /health returned {status}: {body}")
        return None
    words = body.get("corpus_words")
    print(f"  {PASS}  server reachable")
    print(f"        version      : {body.get('version', '?')}")
    print(f"        corpus_words : {words}")
    if words == 0:
        print(f"  {WARN}  corpus is empty — upload a CSV before expecting suggestions")
    return words


def step_reload(url, token):
    print(head("\n[3/4] Reload"))
    status, body = get_json("POST", f"{url}/reload", token=token, timeout=60)
    if status == 200:
        print(f"  {PASS}  POST /reload ok — corpus_words now {body.get('corpus_words')}")
        return True
    if status in (401, 403):
        print(f"  {WARN}  /reload needs a valid admin token/IP ({status}: {body.get('error')})")
        print("        (set --token or ADMIN_TOKEN; reachable from an allowed IP)")
        return False
    print(f"  {FAIL}  POST /reload returned {status}: {body}")
    return False


def step_speed(url, queries, n):
    print(head("\n[4/4] Speed"))
    # Spread n requests across the supplied prefixes.
    plan = [queries[i % len(queries)] for i in range(n)]
    latencies = []
    errors = 0

    wall0 = time.perf_counter()
    for q in plan:
        full = f"{url}/suggest?query={urllib.parse.quote(q)}"
        t0 = time.perf_counter()
        status, _ = http("GET", full, timeout=10)
        dt = (time.perf_counter() - t0) * 1000.0  # ms
        if status == 200:
            latencies.append(dt)
        else:
            errors += 1
    wall = time.perf_counter() - wall0

    if not latencies:
        print(f"  {FAIL}  all {n} /suggest requests failed")
        return False

    latencies.sort()

    def pct(p):
        idx = min(len(latencies) - 1, int(round(p / 100.0 * (len(latencies) - 1))))
        return latencies[idx]

    rps = len(latencies) / wall if wall > 0 else 0.0
    print(f"  requests       : {len(latencies)} ok, {errors} failed, across {len(queries)} prefixes")
    print(f"  throughput     : {rps:,.0f} req/s (sequential client)")
    print(f"  client latency : avg {statistics.mean(latencies):.2f}ms  "
          f"min {latencies[0]:.2f}  p50 {pct(50):.2f}  p90 {pct(90):.2f}  "
          f"p95 {pct(95):.2f}  p99 {pct(99):.2f}  max {latencies[-1]:.2f}ms")

    # Server-side truth from /metrics/json (excludes client/network overhead).
    status, body = get_json("GET", f"{url}/metrics/json")
    if status == 200:
        row = body.get("routes", {}).get("GET /suggest")
        if row:
            print(f"  server latency : avg {row['avg_ms']}ms  p50 {row['p50_ms']}  "
                  f"p95 {row['p95_ms']}  p99 {row['p99_ms']}  max {row['max_ms']}ms  "
                  f"(over {row['requests']} reqs, {row['errors']} errors)")

    if errors:
        print(f"  {WARN}  {errors} request(s) failed")
    print(f"  {PASS}  speed check complete")
    return errors == 0


def main():
    ap = argparse.ArgumentParser(description="go-suggest-neo preflight check")
    # Default to 127.0.0.1, NOT localhost: on Windows, "localhost" can add
    # ~2s of IPv6 resolution delay per request and ruin the speed numbers.
    ap.add_argument("--url", default=os.environ.get("PREFLIGHT_URL", "http://127.0.0.1:8001"))
    ap.add_argument("--token", default=None, help="admin token for /reload")
    ap.add_argument("--requests", type=int, default=200, help="number of /suggest calls in the speed test")
    ap.add_argument("--queries", default="ap,mark,ban,app,co,te",
                    help="comma-separated prefixes to query")
    ap.add_argument("--skip-build", action="store_true", help="skip the `go build` dependency check")
    ap.add_argument("--skip-reload", action="store_true", help="don't call /reload")
    args = ap.parse_args()

    url = args.url.rstrip("/")
    token = load_token(args.token)
    queries = [q.strip() for q in args.queries.split(",") if q.strip()]

    print(head(f"go-suggest-neo preflight  ->  {url}"))

    results = {}
    results["dependencies"] = step_dependencies(args.skip_build)

    words = step_corpus(url)
    results["corpus"] = words is not None
    if words is None:
        # Server unreachable — nothing else will work.
        _summary(results)
        sys.exit(1)

    if not args.skip_reload:
        results["reload"] = step_reload(url, token)

    results["speed"] = step_speed(url, queries, args.requests)

    _summary(results)
    sys.exit(0 if all(results.values()) else 1)


def _summary(results):
    print(head("\nSummary"))
    for name, good in results.items():
        print(f"  {PASS if good else FAIL}  {name}")
    overall = all(results.values())
    print("\n" + (ok("PREFLIGHT PASSED") if overall else bad("PREFLIGHT FAILED")))


if __name__ == "__main__":
    main()
