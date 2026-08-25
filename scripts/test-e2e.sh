#!/bin/sh
set -eu

curl -fsS http://localhost:8080/readyz >/dev/null
name="e2e/run-$(date +%s)-$$"
./bin/continuityctl repo create "$name" --json >/dev/null
root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT

git init -q -b main "$root/source"
git -C "$root/source" config user.name "Continuity Lab"
git -C "$root/source" config user.email lab@example.test
echo 1 >"$root/source/history.txt"
git -C "$root/source" add .
git -C "$root/source" commit -q -m "commit 1"
i=2
while [ "$i" -le 100 ]; do
	echo "$i" >>"$root/source/history.txt"
	git -C "$root/source" commit -q -am "commit $i"
	i=$((i + 1))
done
dd if=/dev/zero of="$root/source/large.bin" bs=1048576 count=2 status=none
git -C "$root/source" add large.bin
git -C "$root/source" commit -q -m "large object"
git -C "$root/source" branch feature
git -C "$root/source" tag -a v1 -m v1
git -C "$root/source" remote add origin "http://localhost:8080/git/$name.git"
git -C "$root/source" push --atomic -u origin main feature v1

# Annotated tag forced update, branch/tag deletion, and force-push.
git -C "$root/source" tag -f -a v1 -m v1-updated HEAD~1
git -C "$root/source" push --force origin v1
git -C "$root/source" push --delete origin feature v1
git -C "$root/source" reset -q --hard HEAD~1
git -C "$root/source" push --force origin main

GIT_PROTOCOL=version=2 git clone -q "http://localhost:8080/git/$name.git" "$root/clone"
test "$(git -C "$root/source" rev-parse HEAD)" = "$(git -C "$root/clone" rev-parse HEAD)"
test "$(git -C "$root/clone" rev-list --count HEAD)" -eq 100
git -C "$root/clone" fsck --full

for port in 18081 18082 18083; do
	git clone -q "http://localhost:$port/git/$name.git" "$root/node-$port"
	test "$(git -C "$root/source" rev-parse HEAD)" = "$(git -C "$root/node-$port" rev-parse HEAD)"
	git -C "$root/node-$port" fsck --full
done

if git clone -q http://localhost:8080/git/does/not-exist.git "$root/missing" 2>/dev/null; then
	echo "nonexistent repository clone unexpectedly succeeded" >&2
	exit 1
fi

./bin/continuityctl repo verify "$name" --json >/dev/null
./scripts/test-concurrency.sh
printf 'E2E PASS: %s (100 commits, force/tag/branch/multi-ref/protocol-v2/direct replicas/concurrency)\n' "$name"
