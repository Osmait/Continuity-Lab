#!/bin/sh
set -eu

curl -fsS http://localhost:8080/readyz >/dev/null
name="chaos/run-$(date +%s)-$$"
./bin/continuityctl repo create "$name" --json >/dev/null
root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT

sequence() {
	./bin/continuityctl repo inspect "$name" --json | python -c 'import json,sys; print(json.load(sys.stdin)["head"]["sequence"])'
}
wait_node() {
	port="$1"
	for _attempt in $(seq 1 90); do
		curl -fsS "http://localhost:$port/readyz" >/dev/null && break
		sleep 1
	done
	curl -fsS "http://localhost:$port/readyz" >/dev/null
}
restart_a() {
	docker compose restart node-a >/dev/null
	wait_node 18081
}

git init -q -b main "$root/source"
git -C "$root/source" config user.name "Continuity Lab"
git -C "$root/source" config user.email lab@example.test
echo base >"$root/source/data"
git -C "$root/source" add .
git -C "$root/source" commit -q -m base
git -C "$root/source" remote add origin "http://localhost:18081/git/$name.git"
git -C "$root/source" push -q -u origin main

# Every failure before CAS must remain invisible.
for failpoint in after_payload_upload before_entry_upload after_entry_upload before_head_cas; do
	before="$(sequence)"
	echo "$failpoint" >>"$root/source/data"
	git -C "$root/source" commit -q -am "$failpoint"
	./bin/continuityctl failpoint set "$failpoint" --node node-a --mode once --json >/dev/null
	if git -C "$root/source" push origin main >"$root/$failpoint.log" 2>&1; then
		echo "$failpoint unexpectedly acknowledged" >&2
		exit 1
	fi
	test "$(sequence)" -eq "$before"
	git -C "$root/source" reset -q --hard origin/main
done

# Every failure after CAS is authoritative although the client sees uncertainty.
index=0
for failpoint in after_head_cas before_local_ref_commit after_local_ref_commit before_http_success; do
	index=$((index + 1))
	before="$(sequence)"
	echo "$failpoint" >>"$root/source/data"
	git -C "$root/source" commit -q -am "$failpoint"
	committed="$(git -C "$root/source" rev-parse HEAD)"
	./bin/continuityctl failpoint set "$failpoint" --node node-a --mode once --json >/dev/null
	if git -C "$root/source" push origin main >"$root/$failpoint.log" 2>&1; then
		echo "$failpoint should produce an uncertain client failure" >&2
		exit 1
	fi
	test "$(sequence)" -eq $((before + 1))
	restart_a
	rm -rf "$root/recovered-$index"
	git clone -q "http://localhost:18082/git/$name.git" "$root/recovered-$index"
	test "$(git -C "$root/recovered-$index" rev-parse HEAD)" = "$committed"
	git -C "$root/recovered-$index" fsck --full
	git -C "$root/source" fetch -q origin
done

# Gossip loss leaves a warm replica behind, then a strong read repairs it.
rm -rf "$root/gossip-client"
git clone -q "http://localhost:18082/git/$name.git" "$root/gossip-client"
id="$(printf %s "$name" | sha256sum | cut -d' ' -f1)"
before="$(sequence)"
./bin/continuityctl failpoint set drop_all_gossip --node node-a --mode once --json >/dev/null
echo gossip-drop >>"$root/source/data"
git -C "$root/source" commit -q -am gossip-drop
git -C "$root/source" push -q origin main
sleep 2
node_b_sequence="$(docker compose exec -T node-b sh -c "grep -o '\"applied_sequence\": [0-9]*' /var/lib/continuity/state/$id.json | grep -o '[0-9]*'")"
test "$node_b_sequence" -eq "$before"
git -C "$root/gossip-client" fetch -q
test "$(git -C "$root/gossip-client" rev-parse origin/main)" = "$(git -C "$root/source" rev-parse HEAD)"

# Deliberate local-pack corruption is detected and rebuilt from MinIO.
./bin/continuityctl failpoint set corrupt_next_local_pack --node node-b --mode once --json >/dev/null
git -C "$root/gossip-client" fetch -q
git -C "$root/gossip-client" fsck --full

# Killing the rendezvous-preferred node must still allow a gateway push.
preferred="$(
	python - "$id" <<'PY'
import hashlib,sys
repo=sys.argv[1]
print(max(('node-a','node-b','node-c'), key=lambda n: hashlib.sha256(repo.encode()+b'\0'+n.encode()).digest()))
PY
)"
case "$preferred" in node-a) preferred_port=18081 ;; node-b) preferred_port=18082 ;; *) preferred_port=18083 ;; esac
docker compose stop "$preferred" >/dev/null
sleep 3
echo fallback >>"$root/source/data"
git -C "$root/source" commit -q -am fallback
git -C "$root/source" push -q http://localhost:8080/git/"$name".git HEAD:main
docker compose start "$preferred" >/dev/null
wait_node "$preferred_port"

# Snapshot publication racing a push may win or leave an orphan, never corrupt refs.
git -C "$root/source" fetch -q origin
echo compact-race >>"$root/source/data"
git -C "$root/source" commit -q -am compact-race
(./bin/continuityctl repo compact "$name" --json >"$root/compact.log" 2>&1 || true) &
compact_pid=$!
git -C "$root/source" push -q http://localhost:8080/git/"$name".git HEAD:main
wait "$compact_pid" || true
latest="$(git -C "$root/source" rev-parse HEAD)"
rm -rf "$root/after-compact"
git clone -q "http://localhost:18083/git/$name.git" "$root/after-compact"
test "$(git -C "$root/after-compact" rev-parse HEAD)" = "$latest"
git -C "$root/after-compact" fsck --full

# Strict reads and writes fail while MinIO cannot establish truth.
docker compose stop minio >/dev/null
if git clone -q "http://localhost:18082/git/$name.git" "$root/should-fail" 2>/dev/null; then
	echo "strict clone succeeded while MinIO was down" >&2
	docker compose start minio
	exit 1
fi
if git -C "$root/source" push -q http://localhost:18081/git/"$name".git HEAD:main 2>/dev/null; then
	echo "push succeeded while MinIO was down" >&2
	docker compose start minio
	exit 1
fi
docker compose start minio >/dev/null
for _attempt in $(seq 1 90); do
	curl -fsS http://localhost:9000/minio/health/ready >/dev/null && break
	sleep 1
done
for port in 18081 18082 18083; do wait_node "$port"; done
for _attempt in $(seq 1 90); do
	curl -fsS http://localhost:8080/readyz >/dev/null && break
	sleep 1
done
curl -fsS http://localhost:8080/readyz >/dev/null

rm -rf "$root/final"
git clone -q "http://localhost:18083/git/$name.git" "$root/final"
test "$(git -C "$root/final" rev-parse HEAD)" = "$latest"
git -C "$root/final" fsck --full
printf 'CHAOS PASS: all pre/post-CAS failpoints, gossip loss, corruption, fallback, compaction race, MinIO outage\n'
