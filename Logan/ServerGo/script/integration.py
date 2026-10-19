#!/usr/bin/env python3
"""Run isolated integration suites; missing dependencies are blocked, never passed."""
import argparse
import datetime
import json
import os
import re
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = {
    "mysql": ["LOGAN_IT_MYSQL_DSN"],
    "oss": ["LOGAN_IT_OSS_MODE", "LOGAN_IT_OSS_ENDPOINT", "LOGAN_IT_OSS_REGION", "LOGAN_IT_OSS_BUCKET", "LOGAN_IT_OSS_PREFIX", "LOGAN_IT_OSS_ACCESS_KEY_ID", "LOGAN_IT_OSS_ACCESS_KEY_SECRET", "LOGAN_IT_OSS_PRIVATE_BUCKET"],
}

def classify(lines, returncode):
    tests = {}
    for line in lines.splitlines():
        try:
            event = json.loads(line)
        except (ValueError, TypeError):
            continue
        if event.get("Test") and event.get("Action") in ("pass", "fail", "skip"):
            tests[event["Test"]] = event["Action"]
    status = "passed" if returncode == 0 and tests and all(v == "pass" for v in tests.values()) else "failed"
    return status, tests


def redact(text, environ):
    # Logs are private regardless; never preserve DSNs, keys, tokens or endpoints.
    names = ("LOGAN_IT_MYSQL_DSN", "LOGAN_IT_OSS_ENDPOINT", "LOGAN_IT_OSS_ACCESS_KEY_ID", "LOGAN_IT_OSS_ACCESS_KEY_SECRET", "LOGAN_IT_OSS_SESSION_TOKEN")
    for name in names:
        value = environ.get(name, "")
        if value:
            text = text.replace(value, "[redacted]")
    # SDK diagnostics may contain a generated presigned URL rather than a configured endpoint.
    text = re.sub(r'https?://[^\s<>"\x27]+', '[redacted-url]', text)
    return text


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--suite", choices=("mysql", "oss", "all"), default="all")
    parser.add_argument("--out", type=Path, help="new, private evidence directory")
    args = parser.parse_args()
    output = args.out or Path(tempfile.mkdtemp(prefix="logan-integration-"))
    if args.out:
        output.mkdir(mode=0o700, parents=True, exist_ok=False)
    output.chmod(0o700)
    suites = list(REQUIRED) if args.suite == "all" else [args.suite]
    summary = {"started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "selected_suites": suites, "results": []}
    interrupted = False
    for suite in suites:
        item = {"suite": suite, "status": "blocked", "tests": {}}
        missing = [key for key in REQUIRED[suite] if not os.environ.get(key)]
        if os.environ.get("LOGAN_IT_ALLOW_WRITES") != "1":
            missing.append("LOGAN_IT_ALLOW_WRITES=1")
        if missing or not shutil.which("go"):
            item["reason"] = "missing requirements: " + ", ".join(missing + ([] if shutil.which("go") else ["go"]))
        else:
            command = ["go", "test", "-json", "-count=1", "-tags", "stdjson,integration", "-timeout", "5m", "./tests/integration/" + suite]
            item["command"] = command
            start = time.monotonic()
            try:
                process = subprocess.Popen(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, start_new_session=True)
                try:
                    stdout, _ = process.communicate(timeout=330)
                except (subprocess.TimeoutExpired, KeyboardInterrupt):
                    os.killpg(process.pid, signal.SIGKILL)
                    process.communicate()
                    raise
                result = subprocess.CompletedProcess(command, process.returncode, stdout)
                item["exit_code"] = result.returncode
                item["status"], item["tests"] = classify(result.stdout, result.returncode)
                log = redact(result.stdout, os.environ)
            except subprocess.TimeoutExpired as error:
                item["status"], item["exit_code"] = "failed", 124
                raw = error.stdout or b""
                log = redact(raw.decode(errors="replace") if isinstance(raw, bytes) else raw, os.environ)
                item["reason"] = "integration command exceeded outer timeout"
            except KeyboardInterrupt:
                item["status"], item["exit_code"] = "failed", 130
                item["reason"], log = "operator interrupted the integration process group", "interrupted\n"
                interrupted = True
            item["elapsed_seconds"] = round(time.monotonic() - start, 3)
            path = output / (suite + ".log")
            path.write_text(log)
            path.chmod(0o600)
            item["log"] = path.name
        summary["results"].append(item)
        print(suite + ": " + item["status"])
        if item.get("reason"):
            print("  " + item["reason"])
        if interrupted:
            break
    path = output / "summary.json"
    path.write_text(json.dumps(summary, indent=2) + "\n")
    path.chmod(0o600)
    print("Evidence: " + str(path))
    statuses = [r["status"] for r in summary["results"]]
    return 130 if interrupted else 1 if "failed" in statuses else 2 if "blocked" in statuses else 0

if __name__ == "__main__":
    raise SystemExit(main())
