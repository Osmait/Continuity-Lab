#!/bin/sh
set -eu

base=${CONTINUITY_GATEWAY_URL:-http://localhost:8080}
run="$(date +%s)-$$"
repo="ui/run-$run"
root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT

for _attempt in $(seq 1 90); do
	if curl -fsS "$base/readyz" >/dev/null 2>&1; then
		break
	fi
	sleep 1
done
curl -fsS "$base/readyz" >/dev/null

curl -fsS -X POST -H 'Content-Type: application/json' \
	-d "{\"name\":\"$repo\",\"default_branch\":\"main\"}" \
	"$base/api/v1/repos" >/dev/null

git init -q -b main "$root/source"
git -C "$root/source" config user.name 'Continuity UI Test'
git -C "$root/source" config user.email ui@example.test
mkdir -p "$root/source/src"
printf '# UI test\n\nRepository browser fixture.\n' >"$root/source/README.md"
printf 'package fixture\n\nconst Ready = true\n' >"$root/source/src/fixture.go"
git -C "$root/source" add .
git -C "$root/source" commit -qm 'add UI browser fixture'
git -C "$root/source" remote add origin "$base/git/$repo.git"
git -C "$root/source" push -q origin main

curl -fsSI "$base/" | grep -qi '^Content-Type: text/html'
curl -fsS "$base/" | grep -q '<div id="root"></div>'
curl -fsS "$base/repos/$repo" | grep -q '<div id="root"></div>'
curl -fsS "$base/admin" | grep -q '<div id="root"></div>'
curl -fsS "$base/admin/wal" | grep -q '<div id="root"></div>'
asset="$(curl -fsS "$base/" | grep -o '/assets/[^" ]*\.js' | head -1)"
test -n "$asset"
curl -fsSI "$base$asset" | grep -qi 'Cache-Control: public, max-age=31536000, immutable'

curl -fsS "$base/api/v1/repos" >"$root/repos.json"
python3 - "$root/repos.json" "$repo" <<'PY'
import json, sys
payload = json.load(open(sys.argv[1]))
assert any(item["name"] == sys.argv[2] for item in payload["repositories"])
PY

curl -fsS --get --data-urlencode view=refs "$base/api/v1/browse/$repo" >"$root/refs.json"
ref="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["default_branch"])' "$root/refs.json")"
curl -fsS --get --data-urlencode view=tree --data-urlencode ref="$ref" "$base/api/v1/browse/$repo" >"$root/tree.json"
curl -fsS --get --data-urlencode view=tree --data-urlencode ref="$ref" --data-urlencode path=src "$base/api/v1/browse/$repo" >"$root/subtree.json"
curl -fsS --get --data-urlencode view=blob --data-urlencode ref="$ref" --data-urlencode path=src/fixture.go "$base/api/v1/browse/$repo" >"$root/blob.json"
curl -fsS --get --data-urlencode view=commits --data-urlencode ref="$ref" --data-urlencode limit=10 "$base/api/v1/browse/$repo" >"$root/commits.json"
oid="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[0]["oid"])' "$root/commits.json")"
curl -fsS --get --data-urlencode view=commit --data-urlencode oid="$oid" "$base/api/v1/browse/$repo" >"$root/commit.json"

python3 - "$root" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
refs = json.load(open(root / "refs.json"))
tree = json.load(open(root / "tree.json"))
subtree = json.load(open(root / "subtree.json"))
blob = json.load(open(root / "blob.json"))
commits = json.load(open(root / "commits.json"))
detail = json.load(open(root / "commit.json"))
assert refs["branches"][0]["name"] == "refs/heads/main"
assert {entry["name"] for entry in tree["entries"]} == {"README.md", "src"}
assert subtree["entries"][0]["path"] == "src/fixture.go"
assert blob["content"].startswith("package fixture") and not blob["binary"]
assert commits[0]["subject"] == "add UI browser fixture"
assert detail["oid"] == commits[0]["oid"] and len(detail["changes"]) == 2
PY

base_commit="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["commit"])' "$root/tree.json")"
python3 - "$base_commit" >"$root/create-edit.json" <<'PY'
import json, sys
json.dump({
    "branch": "refs/heads/main",
    "path": "docs/web-editor.txt",
    "content": "created by the web editor\n",
    "base_commit": sys.argv[1],
    "commit_message": "Create file from web editor",
    "author_name": "Continuity Web Editor",
    "author_email": "web-editor@continuity.local",
    "create": True,
}, sys.stdout)
PY
curl -fsS -X POST -H 'Content-Type: application/json' --data-binary @"$root/create-edit.json" \
	"$base/api/v1/edit/$repo" >"$root/create-result.json"
created_oid="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["commit_oid"])' "$root/create-result.json")"

python3 - "$created_oid" >"$root/update-edit.json" <<'PY'
import json, sys
json.dump({
    "branch": "refs/heads/main",
    "path": "docs/web-editor.txt",
    "content": "updated by the web editor\n",
    "base_commit": sys.argv[1],
    "commit_message": "Update file from web editor",
    "author_name": "Continuity Web Editor",
    "author_email": "web-editor@continuity.local",
    "create": False,
}, sys.stdout)
PY
curl -fsS -X POST -H 'Content-Type: application/json' --data-binary @"$root/update-edit.json" \
	"$base/api/v1/edit/$repo" >"$root/update-result.json"
updated_oid="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["commit_oid"])' "$root/update-result.json")"

after_edit_status="$(curl -sS -o "$root/stale-edit.json" -w '%{http_code}' -X POST -H 'Content-Type: application/json' --data-binary @"$root/update-edit.json" "$base/api/v1/edit/$repo")"
test "$after_edit_status" = 409

curl -fsS --get --data-urlencode view=blob --data-urlencode ref=refs/heads/main --data-urlencode path=docs/web-editor.txt "$base/api/v1/browse/$repo" >"$root/edited-blob.json"
curl -fsS "$base/api/v1/repos/$repo/wal?limit=10" >"$root/edited-wal.json"
git clone -q "$base/git/$repo.git" "$root/edited-clone"
git -C "$root/edited-clone" fsck --full >/dev/null

python3 - "$root" "$created_oid" "$updated_oid" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
created = json.load(open(root / "create-result.json"))
updated = json.load(open(root / "update-result.json"))
blob = json.load(open(root / "edited-blob.json"))
wal = json.load(open(root / "edited-wal.json"))
stale = json.load(open(root / "stale-edit.json"))
assert created["created"] is True and created["commit_oid"] == sys.argv[2]
assert updated["created"] is False and updated["commit_oid"] == sys.argv[3]
assert blob["content"] == "updated by the web editor\n" and blob["commit"] == sys.argv[3]
assert wal["sequence"] == 3 and len(wal["entries_newest_first"]) == 3
assert stale["error"]["code"] == "edit_conflict"
PY

echo "UI PASS: Git/Admin shells, repository browsing, CodeMirror edit/create commits, WAL publication, and stale-edit rejection"
