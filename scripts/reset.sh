#!/bin/sh
set -eu
docker compose down --volumes --remove-orphans
rm -rf .tmp
echo "Continuity Lab state removed"
