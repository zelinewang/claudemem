#!/bin/bash
# claudemem — black-box tests for a store that reads other stores, and for moving entries between
# stores.
#
# Checks, through the CLI on throwaway stores, that read commands return the entries of every store
# read and name their store, that writes stay in --store, the stores.read / --read-stores rules, and
# that move keeps ids, refuses a session whose related note stays, and can be run again.
# Run: make build && bash tests/stores_test.sh   (or BINARY=/path/to/claudemem bash ...)
# REQUIREMENTS: the claudemem binary, python3. Uses a temp directory only.

set -uo pipefail

BINARY="${BINARY:-./claudemem}"
WORK=$(mktemp -d)
FAKE_HOME="$WORK/home"
P="$WORK/primary"
S="$WORK/shared"
mkdir -p "$FAKE_HOME" "$P" "$S"
PASS=0; FAIL=0

pass() { PASS=$((PASS+1)); echo "  [PASS] $1"; }
fail() { FAIL=$((FAIL+1)); echo "  [FAIL] $1: $2"; }
# the CLI with a throwaway home: the stores are outside it, so they are named by full path
C() { HOME="$FAKE_HOME" "$BINARY" "$@"; }
CP() { C --store "$P" "$@"; }
CS() { C --store "$S" "$@"; }
json() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)" 2>/dev/null; }

cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

if [ ! -x "$BINARY" ]; then
    echo "ERROR: $BINARY not found. Run 'make build' first."
    exit 1
fi

echo "Several-store tests — binary: $BINARY ($("$BINARY" --version 2>&1))"

# --- Fixture: a note and a session in each store; the primary reads the shared store ---
CP config set stores.read "$S" >/dev/null
N_P=$(CP note add ops --title "Primary runbook" --content "bbkw primary" --tags team --format json | json "d['note_id']")
N_S=$(CS note add ops --title "Shared runbook" --content "bbkw shared" --tags "team,ops" --format json | json "d['note_id']")
S_P=$(CP session save --title "Primary session" --branch main --project p --session-id sid-p --summary "bbkw p" --tags team --format json | json "d['session_id']")
S_S=$(CS session save --title "Shared session" --branch main --project p --session-id sid-s --summary "bbkw s" --tags team --format json | json "d['session_id']")
if [ -z "$N_P" ] || [ -z "$N_S" ] || [ -z "$S_P" ] || [ -z "$S_S" ]; then
    echo "ERROR: fixture creation failed"
    exit 1
fi

echo ""
echo "1. search"
GOT=$(CP search bbkw --type note --format json | json "' '.join(e['id'] + '@' + e['store'] for e in d)")
[ "$GOT" = "$N_P@$P $N_S@$S" ] && pass "search returns the notes of both stores, merged by rank, each naming its store" \
    || fail "search over two stores" "[$GOT]"
GOT=$(CP search bbkw --format json | json "len(d)")
[ "$GOT" = "4" ] && pass "search finds both stores' notes and sessions" || fail "search count" "[$GOT]"
OUT=$(CP --read-stores "" search bbkw --type note --format json)
GOT=$(json "' '.join(e['id'] for e in d)" <<<"$OUT")
if [ "$GOT" = "$N_P" ] && ! grep -q '"store"' <<<"$OUT"; then
    pass "--read-stores \"\" reads --store alone, with single-store output"
else
    fail "--read-stores \"\"" "[$OUT]"
fi
OUT=$(CP search bbkw --type note)
grep -qF "store: $S" <<<"$OUT" && pass "text output names the store" || fail "search text" "$OUT"

echo ""
echo "2. notes"
GOT=$(CP note get "$N_S" --format json | json "d['id'] + '@' + d['store']")
[ "$GOT" = "$N_S@$S" ] && pass "note get finds a note of the store read, and names it" || fail "note get" "[$GOT]"
OUT=$(CP note get "${N_S:0:8}")
grep -qF "Store: $S" <<<"$OUT" && pass "note get by id prefix, text names the store" || fail "note get prefix" "$OUT"
GOT=$(CP note list --format json | json "' '.join(e['title'] + '@' + e['store'] for e in d)")
[ "$GOT" = "Primary runbook@$P Shared runbook@$S" ] && pass "note list lists both stores by category and title" \
    || fail "note list" "[$GOT]"
GOT=$(CP note search runbook --format json | json "len(d)")
[ "$GOT" = "2" ] && pass "note search searches both stores" || fail "note search" "[$GOT]"
GOT=$(CP note tags --format json | json "','.join(d)")
[ "$GOT" = "ops,team" ] && pass "note tags lists the tags of both stores once" || fail "note tags" "[$GOT]"

echo ""
echo "3. sessions"
GOT=$(CP session list --format json | json "' '.join(sorted(e['id'] + '@' + e['store'] for e in d))")
WANT=$(printf '%s\n' "$S_P@$P" "$S_S@$S" | sort | tr '\n' ' ' | sed 's/ $//')
[ "$GOT" = "$WANT" ] && pass "session list lists both stores' sessions" || fail "session list" "[$GOT]"
GOT=$(CP session get "$S_S" --format json | json "d['store']")
[ "$GOT" = "$S" ] && pass "session get names the store" || fail "session get" "[$GOT]"
GOT=$(CP session search bbkw --format json | json "len(d)")
[ "$GOT" = "2" ] && pass "session search searches both stores" || fail "session search" "[$GOT]"

echo ""
echo "4. context inject and stats"
GOT=$(CP context inject --format json | json "'%s|%d/%d' % (','.join(d['stores']), d['stats']['total_notes'], d['stats']['total_sessions'])")
[ "$GOT" = "$P,$S|2/2" ] && pass "context inject reads both stores and adds up the counts" || fail "context inject" "[$GOT]"
GOT=$(CP stats --format json | json "'%d/%d|%s' % (d['total_notes'], d['total_sessions'], ','.join('%s:%d' % (s['store'], s['total_notes']) for s in d['stores']))")
[ "$GOT" = "2/2|$P:1,$S:1" ] && pass "stats adds up both stores, with a line per store" || fail "stats" "[$GOT]"
GOT=$(CP stats --tag ops --format json | json "'%d/%d' % (d['total_notes'], d['total_sessions'])")
[ "$GOT" = "1/0" ] && pass "stats --tag counts the tagged entries of both stores" || fail "stats --tag" "[$GOT]"

echo ""
echo "5. writes go to --store alone"
GOT=$(CS stats --top-accessed --format json | json "len(d['top_accessed'])")
[ "$GOT" = "0" ] && pass "reading the shared store through the primary logged no access there" \
    || fail "access log of the store read" "[$GOT] entries"
N_NEW=$(CP note add misc --title "Written through the primary" --content "newkw" --format json | json "d['note_id']")
if CS --read-stores "" note get "$N_NEW" >/dev/null 2>&1; then
    fail "note add" "the note landed in the store read"
elif CP --read-stores "" note get "$N_NEW" >/dev/null 2>&1; then
    pass "note add writes to --store, not to a store it reads"
else
    fail "note add" "the note is in neither store"
fi
if OUT=$(CP note append "$N_S" "more" 2>&1); then
    fail "note append to a read store's note" "accepted: $OUT"
elif grep -qF "run the command again with --store $S" <<<"$OUT" && [ "$(CS note get "$N_S" --format json | json "d['content']")" = "bbkw shared" ]; then
    pass "a write to a read store's entry is refused and names the --store to use"
else
    fail "note append to a read store's note" "$OUT"
fi

echo ""
echo "6. the stores.read setting"
CP config set stores.read "$WORK/gone,$S" >/dev/null
OUT=$(CP search bbkw --type note --format json 2>"$WORK/err")
if grep -qF "warning: store $WORK/gone (stores.read) not found" "$WORK/err" && [ "$(json "len(d)" <<<"$OUT")" = "2" ] && [ ! -e "$WORK/gone" ]; then
    pass "a listed store that does not exist is skipped with a warning, and not created"
else
    fail "missing store" "out=[$OUT] err=[$(cat "$WORK/err")]"
fi
CP config set stores.read "relative/dir" >/dev/null
if OUT=$(CP search bbkw 2>&1); then
    fail "relative path" "accepted: $OUT"
elif grep -q "must be absolute" <<<"$OUT"; then
    pass "a relative store path is refused"
else
    fail "relative path" "$OUT"
fi
C --store "$FAKE_HOME/mem" note add misc --title "Home note" --content "homekw" >/dev/null
CP config set stores.read "~/mem,$S,$S" >/dev/null
GOT=$(CP search homekw --format json | json "d[0]['store']")
CNT=$(CP search bbkw --type note --format json | json "len(d)")
[ "$GOT" = "~/mem" ] && [ "$CNT" = "2" ] && pass "~/ is the home directory, named ~; a store listed twice is read once" \
    || fail "~/ and repeats" "store=[$GOT] count=[$CNT]"
CP config set stores.read "$S" >/dev/null

echo ""
echo "7. move"
MF="$WORK/move-from"
MT="$WORK/move-to"
mkdir -p "$MF" "$MT"
CF() { C --store "$MF" "$@"; }
CT() { C --store "$MT" "$@"; }
M_TEAM=$(CF note add ops --title "Team runbook" --content "mvkw steps" --tags team --format json | json "d['note_id']")
M_KEEP=$(CF note add life --title "Holiday plan" --content "mvkw beach" --tags personal --format json | json "d['note_id']")
M_SESS=$(CF session save --title "Team review" --branch main --project p --session-id sid-mv --summary "mvkw review" --related-notes "$M_TEAM:Team runbook:ops" --format json | json "d['session_id']")
M_MIXED=$(CF session save --title "Mixed review" --branch main --project p --session-id sid-mx --summary "mvkw mixed" --related-notes "${M_KEEP:0:8}:Holiday plan:life" --format json | json "d['session_id']")
printf '# entries for the target\n%s  # Team review\n%s\n%s\n' "$M_SESS" "$M_TEAM" "$M_MIXED" > "$WORK/move-ids.txt"
FILES_BEFORE=$(find "$MF" "$MT" -type f -name '*.md' | sort)
OUT=$(CF move --to "$MT" --file "$WORK/move-ids.txt" --dry-run --format json 2>/dev/null)
GOT=$(json "'%d moved, %d refused, dry run %s' % (len(d['moved']), len(d['refused']), d['dry_run'])" <<<"$OUT")
TARGET_NOTES=$(CT stats --format json | json "d['total_notes']")
if [ "$GOT" = "2 moved, 1 refused, dry run True" ] && [ "$FILES_BEFORE" = "$(find "$MF" "$MT" -type f -name '*.md' | sort)" ] && [ "$TARGET_NOTES" = "0" ]; then
    pass "move --dry-run reports two to move and the mixed session refused, and changes nothing"
else
    fail "move --dry-run" "[$GOT] target notes=[$TARGET_NOTES]"
fi
OUT=$(CF move --to "$MT" --file "$WORK/move-ids.txt" 2>&1); CODE=$?
if [ "$CODE" = "1" ] && grep -q ": 2 moved, 0 already in the target, 0 missing, 1 refused" <<<"$OUT" \
    && grep -qF "refused, line 4: session $M_MIXED: it lists the related note" <<<"$OUT"; then
    pass "move moves the note and its session, refuses the session whose related note stays, exit 1"
else
    fail "move" "exit=$CODE $OUT"
fi
GOT=$(CT note get "$M_TEAM" --format json | json "d['id'] + '|' + d['content']")
if [ "$GOT" = "$M_TEAM|mvkw steps" ] && ! CF note get "$M_TEAM" >/dev/null 2>&1 && CF note get "$M_KEEP" >/dev/null 2>&1; then
    pass "the moved note is in the target under its id and gone from the source; the other note stayed"
else
    fail "moved note" "[$GOT]"
fi
GOT=$(CT session get "$M_SESS" --format json | json "d['related_notes'][0]['id']")
[ "$GOT" = "$M_TEAM" ] && pass "the moved session keeps its related note" || fail "moved session" "[$GOT]"
GOT=$(CF move --to "$MT" --file "$WORK/move-ids.txt" --format json 2>/dev/null | json "'%d/%d/%d' % (len(d['moved']), len(d['already_in_target']), len(d['refused']))")
[ "$GOT" = "0/2/1" ] && pass "running the same file again moves nothing more" || fail "second move" "[$GOT]"
CF config set stores.read "$MT" >/dev/null
GOT=$(CF search mvkw --type note --format json | json "' '.join(sorted(e['title'] + '@' + e['store'] for e in d))")
[ "$GOT" = "Holiday plan@$MF Team runbook@$MT" ] && pass "a store that reads the target finds the moved note, named by the target" \
    || fail "search after the move" "[$GOT]"

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
