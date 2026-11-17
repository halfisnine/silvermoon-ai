"""Check the published package in an isolated, disposable SQLite container."""
import json
import subprocess
import sys
import time
from html.parser import HTMLParser

image, expected_version = sys.argv[1:]
version = subprocess.check_output(
    ["docker", "run", "--rm", "--network=none", image, "--version"], text=True
).strip()
if version != expected_version:
    raise SystemExit(f"Unexpected version: {version}")

container = subprocess.check_output(
    ["docker", "run", "-d", "--network=none", "--read-only",
     "--tmpfs", "/data:rw,size=128m", "--tmpfs", "/tmp:rw,size=64m",
     "--memory=768m", "--cpus=2", "--cap-drop=ALL",
     "--security-opt=no-new-privileges", image], text=True
).strip()

try:
    frontend_version = subprocess.check_output(
        ["docker", "exec", container, "cat", "/usr/share/silvermoon/frontend-version"], text=True
    ).strip()
    if frontend_version != expected_version:
        raise RuntimeError("Frontend build version differs from the backend version")
    status = None
    for _ in range(60):
        probe = subprocess.run(
            ["docker", "exec", container, "wget", "-qO-", "-T", "2",
             "http://127.0.0.1:3000/api/status"], capture_output=True, text=True
        )
        if probe.returncode == 0:
            status = json.loads(probe.stdout)
            break
        state = subprocess.check_output(
            ["docker", "inspect", "--format", "{{.State.Running}}", container], text=True
        ).strip()
        if state != "true":
            raise RuntimeError("Container exited before becoming ready")
        time.sleep(1)
    if status is None or status.get("success") is not True:
        raise RuntimeError("Status endpoint did not become ready")
    if status["data"]["version"] != expected_version:
        raise RuntimeError("Status version differs from the compiled version")
    if status["data"].get("system_name") != "银月" or status["data"].get("logo") != "/silvermoon/moonlight.webp":
        raise RuntimeError("Fresh installation does not use Silvermoon branding before setup")
    logo = subprocess.check_output(
        ["docker", "exec", container, "wget", "-qO-", "-T", "3",
         "http://127.0.0.1:3000/silvermoon/moonlight.webp"]
    )
    if logo[:4] != b"RIFF" or logo[8:12] != b"WEBP":
        raise RuntimeError("Default Silvermoon logo is missing from the image")
    page = subprocess.check_output(
        ["docker", "exec", container, "wget", "-qO-", "-T", "3",
         "http://127.0.0.1:3000/"], text=True
    )
    class PageParser(HTMLParser):
        has_root = False

        def handle_starttag(self, tag, attrs):
            if tag == "div" and dict(attrs).get("id") == "root":
                self.has_root = True

    parsed = PageParser()
    parsed.feed(page)
    if "<html" not in page.lower() or not parsed.has_root:
        raise RuntimeError("Embedded frontend is missing")
    setup = json.loads(subprocess.check_output(
        ["docker", "exec", container, "wget", "-qO-", "-T", "3",
         "http://127.0.0.1:3000/api/setup"], text=True
    ))
    if setup.get("success") is not True or setup["data"]["status"] is not False:
        raise RuntimeError("Fresh container is not awaiting setup")
    print(f"Image verified: {expected_version}; frontend and fresh SQLite startup passed.")
except Exception:
    # This container has no credentials, upstream accounts or mounted host data.
    subprocess.run(["docker", "logs", "--tail=40", container], check=False)
    raise
finally:
    subprocess.run(["docker", "rm", "-f", container], check=True, stdout=subprocess.DEVNULL)
