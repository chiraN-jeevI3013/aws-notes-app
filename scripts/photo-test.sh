#!/usr/bin/env bash
# Photo upload flow test. Usage: BASE_URL=http://localhost:8080 scripts/photo-test.sh
set -euo pipefail
BASE="${BASE_URL:-http://localhost:8080}"
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
jget() { python3 -c "import sys,json; print(json.load(sys.stdin)$1)"; }
post() { curl -s -X POST "$BASE$1" -H 'Content-Type: application/json' -d "$2"; }
ok() { echo "PASS $1"; }
die() { echo "FAIL $1"; exit 1; }

# A tiny real PNG (1x1 pixel).
python3 -c "import base64,sys; sys.stdout.buffer.write(base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=='))" > "$TMP/pic.png"
SIZE=$(wc -c < "$TMP/pic.png" | tr -d ' ')

FID=$(post /folders '{"name":"Photo test"}' | jget "['id']")
NID=$(post "/folders/$FID/notes" '{"title":"With photo","text":""}' | jget "['id']")

# Step 1: ask the API for a presigned upload URL.
RESP=$(post "/folders/$FID/notes/$NID/photo-url" "{\"contentType\":\"image/png\",\"size\":$SIZE}")
URL=$(echo "$RESP" | jget "['uploadUrl']"); KEY=$(echo "$RESP" | jget "['key']")
echo "  key: $KEY"; echo "  uploadUrl: ${URL:0:90}..."
ok "got presigned PUT URL"

# Signature check: lying about the content type must be rejected by S3 itself.
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$URL" -H 'Content-Type: image/jpeg' --data-binary @"$TMP/pic.png")
[[ "$CODE" == 403 ]] && ok "S3 rejects wrong Content-Type (403)" || die "wrong-type PUT got $CODE (want 403)"

# Step 2: upload straight to S3 (the API is not involved).
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$URL" -H 'Content-Type: image/png' --data-binary @"$TMP/pic.png")
[[ "$CODE" == 200 ]] && ok "PUT to S3 ($CODE)" || die "PUT to S3 got $CODE"

# Step 3: tell the API the upload is done.
post "/folders/$FID/notes/$NID/photos" "{\"key\":\"$KEY\"}" | grep -q "$KEY" && ok "confirmed, key saved on note" || die "confirm"

# Step 4: list notes; the photo should come back with a presigned GET URL that serves the same bytes.
VIEW=$(curl -s "$BASE/folders/$FID/notes" | jget "[0]['photos'][0]['url']")
curl -s "$VIEW" -o "$TMP/back.png"
cmp -s "$TMP/pic.png" "$TMP/back.png" && ok "downloaded via presigned GET, bytes match" || die "download mismatch"

# Guard rails.
[[ $(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/folders/$FID/notes/$NID/photo-url" -H 'Content-Type: application/json' -d '{"contentType":"application/pdf","size":10}') == 400 ]] && ok "rejects non-image (400)" || die "type check"
[[ $(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/folders/$FID/notes/$NID/photo-url" -H 'Content-Type: application/json' -d '{"contentType":"image/png","size":99999999}') == 400 ]] && ok "rejects >5MB (400)" || die "size check"
[[ $(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/folders/$FID/notes/$NID/photos" -H 'Content-Type: application/json' -d '{"key":"notes/someone-else/x.png"}') == 400 ]] && ok "rejects foreign key (400)" || die "key check"

# Cleanup: deleting the folder must also delete the photo from S3.
curl -s -o /dev/null -X DELETE "$BASE/folders/$FID"
[[ $(curl -s -o /dev/null -w '%{http_code}' "$VIEW") == 404 ]] && ok "photo deleted from S3 with folder" || die "photo still in S3"
echo "ALL PHOTO CHECKS PASSED against $BASE"
