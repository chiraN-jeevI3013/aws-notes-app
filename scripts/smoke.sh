#!/usr/bin/env bash
# CRUD smoke test. Usage: BASE_URL=http://localhost:8080 scripts/smoke.sh
set -euo pipefail
BASE="${BASE_URL:-http://localhost:8080}"
pass=0
check() { # check <name> <expected-status> <actual-status>
  if [[ "$2" == "$3" ]]; then echo "PASS $1 ($3)"; pass=$((pass+1)); else echo "FAIL $1: want $2 got $3"; exit 1; fi
}
req() { # req <method> <path> [json] -> sets CODE and BODY
  local out
  out=$(curl -s -w $'\n%{http_code}' -X "$1" "$BASE$2" -H 'Content-Type: application/json' ${3:+-d "$3"})
  CODE="${out##*$'\n'}"; BODY="${out%$'\n'*}"
}
jget() { python3 -c "import sys,json; print(json.load(sys.stdin)$1)"; }

req GET /health;                                     check health 200 "$CODE"
req POST /folders '{"name":"Smoke Folder"}';         check create-folder 201 "$CODE"
FID=$(echo "$BODY" | jget "['id']")
req POST /folders '{"name":"  "}';                   check create-folder-invalid 400 "$CODE"
req GET /folders;                                    check list-folders 200 "$CODE"
echo "$BODY" | grep -q "$FID" || { echo "FAIL folder not listed"; exit 1; }
req POST "/folders/$FID/notes" '{"title":"Hello","text":"first"}'; check create-note 201 "$CODE"
NID=$(echo "$BODY" | jget "['id']")
req POST /folders/nope/notes '{"title":"x"}';        check create-note-missing-folder 404 "$CODE"
req GET "/folders/$FID/notes";                       check list-notes 200 "$CODE"
[[ $(echo "$BODY" | jget "[0]['text']") == "first" ]] || { echo "FAIL note text"; exit 1; }
req PUT "/folders/$FID/notes/$NID" '{"title":"Hello2","text":"edited"}'; check update-note 200 "$CODE"
[[ $(echo "$BODY" | jget "['text']") == "edited" ]] || { echo "FAIL update text"; exit 1; }
req PUT "/folders/$FID/notes/nope" '{"title":"x"}';  check update-missing-note 404 "$CODE"
req DELETE "/folders/$FID/notes/$NID";               check delete-note 204 "$CODE"
req GET "/folders/$FID/notes";                       [[ $(echo "$BODY" | jget ".__len__()") == 0 ]] || { echo "FAIL note not deleted: $BODY"; exit 1; }
req POST "/folders/$FID/notes" '{"title":"Keep","text":""}'; check create-note-2 201 "$CODE"
req DELETE "/folders/$FID";                          check delete-folder 204 "$CODE"
req GET "/folders/$FID/notes";                       check notes-gone-with-folder 404 "$CODE"
echo "ALL $pass CHECKS PASSED against $BASE"
