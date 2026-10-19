#!/usr/bin/env python3
"""Private-config gate for real Aliyun OSS evidence, never full SDK/API acceptance."""
import argparse
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = (
    "LOGAN_IT_ALLOW_WRITES", "LOGAN_IT_OSS_PRIVATE_BUCKET", "LOGAN_IT_OSS_MODE",
    "LOGAN_IT_OSS_ENDPOINT", "LOGAN_IT_OSS_REGION", "LOGAN_IT_OSS_BUCKET",
    "LOGAN_IT_OSS_PREFIX", "LOGAN_IT_OSS_ACCESS_KEY_ID", "LOGAN_IT_OSS_ACCESS_KEY_SECRET",
)
ALLOWED = set(REQUIRED) | {"LOGAN_IT_OSS_SESSION_TOKEN"}


def load_private_config(path):
    # Open without following symlinks, then inspect the descriptor to avoid a
    # check/open race. Never include file contents or values in an exception.
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "r", encoding="utf-8") as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600 or info.st_uid != os.getuid():
            raise ValueError("config must be an owned regular file with mode 0600")
        if info.st_size > 16384:
            raise ValueError("config exceeds size limit")
        result = {}
        for line in source:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            name, separator, value = line.partition("=")
            if not separator or name not in ALLOWED or name in result or not value or "\x00" in value:
                raise ValueError("config contains invalid, duplicate, or unsupported settings")
            # Literal key=value only: no shell execution, interpolation or quoting.
            result[name] = value
        return result


def preflight(settings):
    missing = [name for name in REQUIRED if not settings.get(name)]
    if missing:
        return "missing settings: " + ", ".join(missing)
    if settings["LOGAN_IT_ALLOW_WRITES"] != "1" or settings["LOGAN_IT_OSS_PRIVATE_BUCKET"] != "1":
        return "write and private-bucket assertions must both be 1"
    if settings["LOGAN_IT_OSS_MODE"] != "aliyun":
        return "real-cloud acceptance requires aliyun mode"
    from urllib.parse import urlsplit
    try:
        endpoint = urlsplit(settings["LOGAN_IT_OSS_ENDPOINT"])
        valid = (endpoint.scheme == "https" and endpoint.hostname and endpoint.hostname.endswith(".aliyuncs.com")
                 and not endpoint.username and not endpoint.password and not endpoint.query and not endpoint.fragment
                 and endpoint.path in ("", "/") and endpoint.port in (None, 443))
    except ValueError:
        valid = False
    if not valid:
        return "real-cloud endpoint must be an Aliyun HTTPS origin"
    prefix = settings["LOGAN_IT_OSS_PREFIX"]
    if not prefix.startswith("logan-it/") or not prefix.endswith("/") or ".." in prefix or any(c in prefix for c in "\\?#\r\n"):
        return "object prefix must be an isolated logan-it/ namespace"
    return None


def redacted(text, settings):
    for value in sorted(settings.values(), key=len, reverse=True):
        if value:
            text = text.replace(value, "[redacted]")
    # Defense in depth if an SDK/transport diagnostic includes a generated URL.
    return re.sub(r'https?[^\s"<>]+', '[redacted-url]', text)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, help="owned 0600 literal key=value config; no ambient credentials")
    parser.add_argument("--run-oss", action="store_true", help="run isolated real OSS writes after preflight")
    parser.add_argument("--out", type=Path, help="new private evidence directory")
    args = parser.parse_args()
    output = args.out or Path(tempfile.mkdtemp(prefix="logan-cloud-e2e-"))
    if args.out:
        output.mkdir(mode=0o700, parents=True, exist_ok=False)
    output.chmod(0o700)
    settings = {}
    reason = "private config path was not supplied"
    if args.config:
        try:
            settings = load_private_config(args.config)
            reason = preflight(settings)
        except (OSError, UnicodeError, ValueError):
            reason = "private config could not be safely loaded; use an owned regular 0600 key=value file"
    results = [{"suite": "real_aliyun_oss", "status": "blocked", "reason": reason or "preflight passed; execution not requested"}]
    if reason is None and args.run_oss:
        # Keep toolchain settings, but never inherit real application or cloud keys.
        environ = {k: v for k, v in os.environ.items() if not k.startswith(("LOGAN_", "AWS_", "OSS_", "ALIBABA_", "ALICLOUD_"))}
        environ.update(settings)
        try:
            run = subprocess.run([sys.executable, str(ROOT / "script/integration.py"), "--suite", "oss", "--out", str(output / "oss")], env=environ, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=350)
            log = redacted(run.stdout, settings)
            status = "passed" if run.returncode == 0 else "blocked" if run.returncode == 2 else "failed"
            results[0] = {"suite": "real_aliyun_oss", "status": status, "exit_code": run.returncode}
        except (OSError, subprocess.TimeoutExpired):
            log = "OSS runner unavailable or exceeded outer deadline\n"
            results[0] = {"suite": "real_aliyun_oss", "status": "failed", "reason": "runner unavailable or outer timeout"}
        logpath = output / "runner.log"
        logpath.write_text(log)
        logpath.chmod(0o600)
    # OSS storage success cannot be promoted to complete SDK/API evidence.
    for suite in ("real_mysql", "real_sdk_upload_download", "all_20_interfaces"):
        results.append({"suite": suite, "status": "blocked", "reason": "not executed by the OSS storage gate; separate end-to-end evidence required"})
    summary = {"status": "failed" if any(r["status"] == "failed" for r in results) else "blocked", "results": results}
    target = output / "summary.json"
    target.write_text(json.dumps(summary, indent=2) + "\n")
    target.chmod(0o600)
    for result in results:
        print(result["suite"] + ": " + result["status"])
    print("Evidence: " + str(target))
    return 1 if summary["status"] == "failed" else 2


if __name__ == "__main__":
    raise SystemExit(main())
