#!/bin/sh
set -eu

name="stale/run-$(date +%s)-$$"
./bin/continuityctl repo create "$name" --json >/dev/null
root="$(mktemp -d)"
replacement="continuity-stale-node-$$"
cleanup() {
	docker rm -f "$replacement" >/dev/null 2>&1 || true
	docker compose start minio node-c >/dev/null 2>&1 || true
	rm -rf "$root"
}
trap cleanup EXIT

git init -q -b main "$root/source"
git -C "$root/source" config user.name "Continuity Lab"
git -C "$root/source" config user.email lab@example.test
echo one >"$root/source/data"
git -C "$root/source" add .
git -C "$root/source" commit -q -m one
git -C "$root/source" remote add origin "http://localhost:18083/git/$name.git"
git -C "$root/source" push -q -u origin main
git clone -q "http://localhost:18083/git/$name.git" "$root/warm"
expected="$(git -C "$root/source" rev-parse HEAD)"

docker compose stop node-c >/dev/null
docker compose run -d --rm --no-deps --service-ports --name "$replacement" -e CONTINUITY_ALLOW_STALE_READS=true node-c node >/dev/null
for _attempt in $(seq 1 90); do
	curl -fsS http://localhost:18083/readyz >/dev/null && break
	sleep 1
done

docker compose stop minio >/dev/null
rm -rf "$root/stale-clone"
git clone -q "http://localhost:18083/git/$name.git" "$root/stale-clone"
test "$(git -C "$root/stale-clone" rev-parse HEAD)" = "$expected"
git -C "$root/stale-clone" fsck --full

echo two >>"$root/source/data"
git -C "$root/source" commit -q -am two
if git -C "$root/source" push origin main >/dev/null 2>&1; then
	echo "write unexpectedly succeeded through stale-read override" >&2
	exit 1
fi

docker compose start minio >/dev/null
for _attempt in $(seq 1 90); do
	curl -fsS http://localhost:9000/minio/health/ready >/dev/null && break
	sleep 1
done
docker rm -f "$replacement" >/dev/null 2>&1 || true
docker compose start node-c >/dev/null
for _attempt in $(seq 1 90); do
	curl -fsS http://localhost:18083/readyz >/dev/null && break
	sleep 1
done
for _attempt in $(seq 1 90); do
	curl -fsS http://localhost:8080/readyz >/dev/null && break
	sleep 1
done
curl -fsS http://localhost:8080/readyz >/dev/null
trap - EXIT
rm -rf "$root"
echo "STALE READ PASS: verified warm read allowed, write remained strict"
