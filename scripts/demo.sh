#!/bin/sh
set -eu

./bin/continuityctl repo create acme/demo
root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT

git init -q -b main "$root/source"
git -C "$root/source" config user.name "Continuity Lab"
git -C "$root/source" config user.email lab@example.test
echo one >"$root/source/data.txt"
git -C "$root/source" add .
git -C "$root/source" commit -q -m one
git -C "$root/source" remote add origin http://localhost:8080/git/acme/demo.git
git -C "$root/source" push -u origin main

for n in 2 3 4 5; do
	echo "$n" >>"$root/source/data.txt"
	git -C "$root/source" commit -q -am "commit $n"
done
git -C "$root/source" branch demo-branch
git -C "$root/source" tag -a demo-v1 -m demo-v1
git -C "$root/source" push --atomic origin main demo-branch demo-v1

git clone -q http://localhost:8080/git/acme/demo.git "$root/gateway-clone"
git -C "$root/gateway-clone" fsck --full
for port in 18081 18082 18083; do
	git clone -q "http://localhost:$port/git/acme/demo.git" "$root/node-$port"
	git -C "$root/node-$port" fsck --full
done

./bin/continuityctl repo wal acme/demo
./bin/continuityctl cluster status
./bin/continuityctl repo compact acme/demo --json >/dev/null
./bin/continuityctl repo evict acme/demo --node node-b
rm -rf "$root/node-18082"
git clone -q http://localhost:18082/git/acme/demo.git "$root/rematerialized"
test "$(git -C "$root/source" rev-parse HEAD)" = "$(git -C "$root/rematerialized" rev-parse HEAD)"
git -C "$root/rematerialized" fsck --full

echo "DEMO PASS: real push/clone, three replicas, WAL, snapshot, eviction, and rematerialization"
