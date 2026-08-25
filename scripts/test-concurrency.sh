#!/bin/sh
set -eu

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
stamp="$(date +%s)-$$"

# Independent refs: all 20 must serialize and commit.
name="concurrency/independent-$stamp"
./bin/continuityctl repo create "$name" --json >/dev/null
git init -q -b main "$root/base"
git -C "$root/base" config user.name Lab
git -C "$root/base" config user.email lab@example.test
echo base >"$root/base/data"
git -C "$root/base" add .
git -C "$root/base" commit -q -m base
git -C "$root/base" remote add origin "http://localhost:8080/git/$name.git"
git -C "$root/base" push -q origin main
i=1
while [ "$i" -le 20 ]; do
	git -C "$root/base" branch "branch-$i" main
	i=$((i + 1))
done
i=1
while [ "$i" -le 20 ]; do
	port=$((18081 + (i - 1) % 3))
	(git -C "$root/base" push -q "http://localhost:$port/git/$name.git" "refs/heads/branch-$i:refs/heads/branch-$i" >"$root/ind-output-$i" 2>&1 && echo ok >"$root/ind-result-$i") || echo fail >"$root/ind-result-$i" &
	i=$((i + 1))
done
wait || true
ind_ok=0
for result in "$root"/ind-result-*; do [ "$(cat "$result")" = ok ] && ind_ok=$((ind_ok + 1)); done
test "$ind_ok" -eq 20
sequence="$(./bin/continuityctl repo inspect "$name" --json | python -c 'import json,sys; print(json.load(sys.stdin)["head"]["sequence"])')"
test "$sequence" -eq 21

# Same old OID: exactly one of 20 contenders may commit.
race="concurrency/same-ref-$stamp"
./bin/continuityctl repo create "$race" --json >/dev/null
git -C "$root/base" remote set-url origin "http://localhost:8080/git/$race.git"
git -C "$root/base" push -q origin main
for i in $(seq 1 20); do
	git clone -q "http://localhost:8080/git/$race.git" "$root/race-$i"
	git -C "$root/race-$i" config user.name Lab
	git -C "$root/race-$i" config user.email lab@example.test
	echo "$i" >>"$root/race-$i/data"
	git -C "$root/race-$i" commit -q -am "contender $i"
done
for i in $(seq 1 20); do
	port=$((18081 + (i - 1) % 3))
	(git -C "$root/race-$i" push -q "http://localhost:$port/git/$race.git" HEAD:main >"$root/same-output-$i" 2>&1 && echo ok >"$root/same-result-$i") || echo fail >"$root/same-result-$i" &
done
wait || true
same_ok=0
for result in "$root"/same-result-*; do [ "$(cat "$result")" = ok ] && same_ok=$((same_ok + 1)); done
test "$same_ok" -eq 1
sequence="$(./bin/continuityctl repo inspect "$race" --json | python -c 'import json,sys; print(json.load(sys.stdin)["head"]["sequence"])')"
test "$sequence" -eq 2
printf 'CONCURRENCY PASS: independent=20/20, same-ref winners=1/20\n'
