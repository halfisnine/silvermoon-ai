"""Check the published package in an isolated, disposable SQLite container."""
import json
import subprocess
import sys
import time

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
    page = subprocess.check_output(
        ["docker", "exec", container, "wget", "-qO-", "-T", "3",
         "http://127.0.0.1:3000/"], text=True
    )
    if "<html" not in page.lower() or '<div id="root"' not in page:
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
