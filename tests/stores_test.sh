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
mkdir -p "$FAKE_HOME" "$P" "$S" "$WORK/plain"
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
CP config set stores.read "$WORK/plain,$S" >/dev/null
OUT=$(CP search bbkw --type note --format json 2>"$WORK/err")
if grep -qF "warning: $WORK/plain (stores.read) is not a claudemem store" "$WORK/err" && [ "$(json "len(d)" <<<"$OUT")" = "2" ] && [ -z "$(ls -A "$WORK/plain")" ]; then
    pass "a listed directory that is not a store is skipped with a warning, and left as it is"
else
    fail "directory that is not a store" "out=[$OUT] err=[$(cat "$WORK/err")]"
fi
CP config set stores.read "relative/dir,$S" >/dev/null
OUT=$(CP search bbkw --type note --format json 2>"$WORK/err")
if grep -q "stores.read: store path \"relative/dir\" must be absolute" "$WORK/err" && [ "$(json "len(d)" <<<"$OUT")" = "2" ]; then
    pass "a relative path in the setting is skipped with a warning; the other stores are read"
else
    fail "relative path in the setting" "out=[$OUT] err=[$(cat "$WORK/err")]"
fi
if OUT=$(CP --read-stores relative/dir search bbkw 2>&1); then
    fail "relative path in --read-stores" "accepted: $OUT"
elif grep -q "must be absolute" <<<"$OUT"; then
    pass "a relative path in --read-stores is refused"
else
    fail "relative path in --read-stores" "$OUT"
fi
C --store "$FAKE_HOME/mem" note add misc --title "Home note" --content "homekw" >/dev/null
CP config set stores.read "~/mem,$S,$S" >/dev/null
GOT=$(CP search homekw --format json | json "d[0]['store']")
CNT=$(CP search bbkw --type note --format json | json "len(d)")
[ "$GOT" = "~/mem" ] && [ "$CNT" = "2" ] && pass "~/ is the home directory, named ~; a store listed twice is read once" \
    || fail "~/ and repeats" "store=[$GOT] count=[$CNT]"
CP config set stores.read "$S" >/dev/null

echo ""
echo "6b. a store read is only read"
tables() { python3 -c "import sqlite3,sys; c=sqlite3.connect('file:'+sys.argv[1]+'?mode=ro', uri=True); print(' '.join(r[0] for r in c.execute(\"select name from sqlite_master where type='table' order by name\")))" "$1/.index/search.db"; }
CS config set features.semantic_search true >/dev/null
TABLES_BEFORE=$(tables "$S")
CP search bbkw --semantic --format json >/dev/null 2>&1; CP search bbkw >/dev/null 2>&1; CP context inject >/dev/null 2>&1
if [ -n "$TABLES_BEFORE" ] && [ "$TABLES_BEFORE" = "$(tables "$S")" ] && ! grep -qw vectors <<<"$(tables "$S")"; then
    pass "reading a store with semantic search on builds no vector tables in it"
else
    fail "store read gets vector tables" "before=[$TABLES_BEFORE] after=[$(tables "$S")]"
fi
CS config set features.semantic_search false >/dev/null
cp "$P/config.json" "$WORK/primary-config.json"
printf '{"features.semantic_search": tru' >"$P/config.json"
OUT=$(CP context inject --format json 2>"$WORK/err"); CODE=$?
if [ "$CODE" = "0" ] && grep -q "cannot read stores.read" "$WORK/err" \
    && [ "$(json "sorted(n['title'] for n in d['recent_notes'] if 'runbook' in n['title'])" <<<"$OUT")" = "['Primary runbook']" ]; then
    pass "a malformed config is a warning: context inject still runs on the primary"
else
    fail "malformed config" "exit=$CODE out=[$OUT] err=[$(cat "$WORK/err")]"
fi
cp "$WORK/primary-config.json" "$P/config.json"
BAD="$WORK/corrupt"
C --store "$BAD" note add ops --title "Corrupt store note" --content "bbkw corrupt" >/dev/null
rm -f "$BAD/.index/search.db-wal" "$BAD/.index/search.db-shm"
head -c 8192 /dev/zero | tr '\0' 'x' >"$BAD/.index/search.db"
OUT=$(CP --read-stores "$BAD,$S" search bbkw --type note --format json 2>"$WORK/err"); CODE=$?
if [ "$CODE" = "0" ] && grep -qF "warning: store $BAD (--read-stores) cannot be opened" "$WORK/err" && [ "$(json "len(d)" <<<"$OUT")" = "2" ]; then
    pass "a store read that cannot be opened is skipped with a warning"
else
    fail "corrupt store read" "exit=$CODE out=[$OUT] err=[$(cat "$WORK/err")]"
fi

echo ""
echo "7. move"
MF="$WORK/move-from"
MT="$WORK/move-to"
mkdir -p "$MF" "$MT" "$WORK/plain"
CF() { C --store "$MF" "$@"; }
CT() { C --store "$MT" "$@"; }
CT stats >/dev/null  # an empty store: a directory a command has run in
M_TEAM=$(CF note add ops --title "Team runbook" --content "mvkw steps" --tags team --format json | json "d['note_id']")
M_KEEP=$(CF note add life --title "Holiday plan" --content "mvkw beach" --tags personal --format json | json "d['note_id']")
M_SESS=$(CF session save --title "Team review" --branch main --project p --session-id sid-mv --summary "mvkw review" --related-notes "$M_TEAM:Team runbook:ops" --format json | json "d['session_id']")
M_MIXED=$(CF session save --title "Mixed review" --branch main --project p --session-id sid-mx --summary "mvkw mixed" --related-notes "${M_KEEP:0:8}:Holiday plan:life" --format json | json "d['session_id']")
printf '# entries for the target\n%s  # Team review\n%s\n%s\n' "$M_SESS" "$M_TEAM" "$M_MIXED" > "$WORK/move-ids.txt"
if OUT=$(CF move --to "$WORK/plain" --file "$WORK/move-ids.txt" 2>&1); then
    fail "move to a directory that is not a store" "accepted: $OUT"
elif grep -q "is not a claudemem store" <<<"$OUT" && [ -z "$(ls -A "$WORK/plain")" ]; then
    pass "move refuses a target that is not a claudemem store, and leaves it as it is"
else
    fail "move to a directory that is not a store" "$OUT"
fi
FILES_BEFORE=$(find "$MF" "$MT" -type f -name '*.md' | sort)
OUT=$(CF move --to "$MT" --file "$WORK/move-ids.txt" --dry-run --format json 2>/dev/null)
GOT=$(json "'%d moved, %d refused, dry run %s' % (len(d['moved']), len(d['refused']), d['dry_run'])" <<<"$OUT")
TARGET_NOTES=$(CT stats --format json | json "d['total_notes']")
if [ "$GOT" = "2 moved, 1 refused, dry run True" ] && [ "$FILES_BEFORE" = "$(find "$MF" "$MT" -type f -name '*.md' | sort)" ] && [ "$TARGET_NOTES" = "0" ]; then
    pass "move --dry-run reports two to move and the mixed session refused, and changes nothing"
else
    fail "move --dry-run" "[$GOT] target notes=[$TARGET_NOTES]"
fi
CF note get "$M_TEAM" >/dev/null  # a read, logged in the access log here
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
GOT=$(CT stats --top-accessed --format json | json "[a['access_count'] for a in d['top_accessed'] if a['id'] == '$M_TEAM']")
[ "$GOT" = "[1]" ] && pass "the access log stays behind: the target counts only its own read of the moved note" || fail "access log carried" "[$GOT]"
cp "$MT/notes/ops/team-runbook.md" "$MF/notes/ops/team-runbook.md"
python3 -c "import sys; p = sys.argv[1]; s = open(p).read(); open(p, 'w').write(s.replace('mvkw steps', 'edited here'))" "$MF/notes/ops/team-runbook.md"
printf '%s\n' "$M_TEAM" > "$WORK/team-id.txt"
OUT=$(CF move --to "$MT" --file "$WORK/team-id.txt" 2>&1); CODE=$?
if [ "$CODE" = "1" ] && grep -q "1 left to clean up" <<<"$OUT" && grep -q "a different copy of its file sits in this store" <<<"$OUT" \
    && [ -f "$MF/notes/ops/team-runbook.md" ]; then
    pass "a different copy of a moved file left here is kept and reported, exit 1"
else
    fail "different leftover copy" "exit=$CODE $OUT"
fi
rm -f "$MF/notes/ops/team-runbook.md"
M_LINK=$(CF note add linked --title "Linked runbook" --content "mvkw linked" --tags team --format json | json "d['note_id']")
mkdir -p "$MT/notes"
ln -s "$MF/notes/linked" "$MT/notes/linked"
printf '%s\n' "$M_LINK" > "$WORK/link-id.txt"
OUT=$(CF move --to "$MT" --file "$WORK/link-id.txt" 2>&1); CODE=$?
if [ "$CODE" = "1" ] && grep -q "symlink" <<<"$OUT" && CF note get "$M_LINK" >/dev/null 2>&1 && ! grep -q "Usage:" <<<"$OUT"; then
    pass "a file reached through a symlink between the stores is refused and kept, without a usage block"
else
    fail "symlinked category" "exit=$CODE $OUT"
fi
rm -f "$MT/notes/linked"
CF config set stores.read "$MT" >/dev/null
GOT=$(CF search mvkw --type note --format json | json "' '.join(sorted(e['title'] + '@' + e['store'] for e in d))")
[ "$GOT" = "Holiday plan@$MF Linked runbook@$MF Team runbook@$MT" ] && pass "a store that reads the target finds the moved note, named by the target" \
    || fail "search after the move" "[$GOT]"

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
