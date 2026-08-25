#!/bin/sh
set -eu

command -v docker >/dev/null 2>&1 || {
	echo "docker is required" >&2
	exit 1
}
docker compose version >/dev/null
command -v git >/dev/null 2>&1 || {
	echo "git is required" >&2
	exit 1
}

git_version="$(git version | awk '{print $3}')"
printf 'host Git: %s\n' "$git_version"

docker compose build
docker compose up -d

for url in http://localhost:8080/readyz http://localhost:18081/readyz http://localhost:18082/readyz http://localhost:18083/readyz; do
	tries=0
	until curl -fsS "$url" >/dev/null; do
		tries=$((tries + 1))
		if [ "$tries" -ge 90 ]; then
			docker compose ps
			docker compose logs --tail=100
			echo "timed out waiting for $url" >&2
			exit 1
		fi
		sleep 2
	done
done

docker compose run --rm --no-deps gateway continuityctl storage conformance
cat <<'MSG'
Continuity Lab is ready:
  Gateway:       http://localhost:8080
  MinIO API:     http://localhost:9000
  MinIO console: http://localhost:9001
  Nodes:         http://localhost:18081 http://localhost:18082 http://localhost:18083
MSG
