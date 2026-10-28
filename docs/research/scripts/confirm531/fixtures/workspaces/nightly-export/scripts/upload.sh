#!/bin/sh
# Upload the nightly tarball to Corvane with a multipart upload.
set -eu
file="$1"
CHUNK_MB=8
corvane-cli multipart-upload --bucket nightly-dumps --part-size "${CHUNK_MB}MiB" "$file"
