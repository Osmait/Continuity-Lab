#!/bin/sh
set -eu

out=".tmp/multiarch"
rm -rf "$out"
mkdir -p "$out"
for arch in amd64 arm64; do
	for command in gateway node continuityctl continuity-hook; do
		CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -o "$out/$command-$arch" "./cmd/$command"
	done
done
python - <<'PY'
import json, subprocess
images = [
    "node:24-bookworm-slim",
    "golang:1.26.7-bookworm",
    "quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z",
]
for image in images:
    manifest = json.loads(subprocess.check_output(["docker", "manifest", "inspect", image]))
    platforms = {(m.get("platform", {}).get("os"), m.get("platform", {}).get("architecture")) for m in manifest.get("manifests", [])}
    missing = {("linux", "amd64"), ("linux", "arm64")} - platforms
    if missing:
        raise SystemExit(f"{image} lacks required platforms: {sorted(missing)}")
print("MULTIARCH PASS: UI/Go builds and base manifests support linux/amd64 + linux/arm64")
PY
rm -rf "$out"
