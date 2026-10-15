#!/bin/bash
# claudemem — tag filter, tag edit and bulk tag black-box tests.
#
# One store can serve a filtered view (a second profile that sees only the entries tagged
# vis-shared). These checks run the CLI the way such a wrapper does, on a throwaway store.
# Run: make build && bash tests/tag_filters_test.sh   (or BINARY=/path/to/claudemem bash ...)
# REQUIREMENTS: the claudemem binary, python3. Uses a temp directory only.

set -uo pipefail

BINARY="${BINARY:-./claudemem}"
STORE=$(mktemp -d)
FAKE_HOME=$(mktemp -d)
PASS=0; FAIL=0

pass() { PASS=$((PASS+1)); echo "  [PASS] $1"; }
fail() { FAIL=$((FAIL+1)); echo "  [FAIL] $1: $2"; }
B() { "$BINARY" --store "$STORE" "$@"; }
BJ() { "$BINARY" --store "$STORE" "$@" --format json; }
json() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)" 2>/dev/null; }

cleanup() { rm -rf "$STORE" "$FAKE_HOME"; }
trap cleanup EXIT

if [ ! -x "$BINARY" ]; then
    echo "ERROR: $BINARY not found. Run 'make build' first."
    exit 1
fi

echo "Tag filter tests — binary: $BINARY ($("$BINARY" --version 2>&1))"

# --- Fixture: 3 notes, 2 sessions ---
N_SHARED=$(BJ note add ops --title "Deploy runbook" --content "tagkw how to deploy" --tags "vis-shared,ops" | json "d['note_id']")
N_PERSONAL=$(BJ note add life --title "Private holiday" --content "tagkw beach in May" --tags "vis-personal" | json "d['note_id']")
N_OLD=$(BJ note add misc --title "Old label" --content "tagkw stale label" --tags "vis-shared-old" | json "d['note_id']")
S_SHARED=$(BJ session save --title "Release review" --branch main --project p --session-id sid-a --summary "tagkw shipped" --tags "vis-shared" | json "d['session_id']")
S_PERSONAL=$(BJ session save --title "Secret diary" --branch main --project p --session-id sid-b --summary "tagkw private" --tags "vis-personal" | json "d['session_id']")
if [ -z "$N_SHARED" ] || [ -z "$N_PERSONAL" ] || [ -z "$N_OLD" ] || [ -z "$S_SHARED" ] || [ -z "$S_PERSONAL" ]; then
    echo "ERROR: fixture creation failed"
    exit 1
fi

echo ""
echo "1. context inject --tag"
OUT=$(BJ context inject --tag vis-shared 2>&1)
NOTES=$(echo "$OUT" | json "','.join(n['title'] for n in d['recent_notes'])")
SESS=$(echo "$OUT" | json "','.join(s['title'] for s in d['recent_sessions'])")
COUNTS=$(echo "$OUT" | json "'%d/%d' % (d['stats']['total_notes'], d['stats']['total_sessions'])")
if [ "$NOTES" = "Deploy runbook" ] && [ "$SESS" = "Release review" ] && [ "$COUNTS" = "1/1" ]; then
    pass "context inject --tag vis-shared injects only the shared note and session, counts 1/1"
else
    fail "context inject --tag" "notes=[$NOTES] sessions=[$SESS] counts=$COUNTS"
fi
OUT=$(BJ context inject 2>&1)
COUNTS=$(echo "$OUT" | json "'%d/%d/%s' % (d['stats']['total_notes'], d['stats']['total_sessions'], 'tags' in d)")
[ "$COUNTS" = "3/2/False" ] && pass "context inject without --tag is unchanged (3 notes, 2 sessions, no tags key)" \
    || fail "context inject without --tag" "$COUNTS"

echo ""
echo "2. sessions carry tags"
TAGS=$(BJ session get "$S_SHARED" | json "','.join(d['tags'])")
[ "$TAGS" = "vis-shared" ] && pass "session save --tags stores the tags" || fail "session save --tags" "tags=[$TAGS]"
OUT=$(B session list --tag vis-shared 2>&1)
if grep -q "Release review" <<<"$OUT" && ! grep -q "Secret diary" <<<"$OUT"; then
    pass "session list --tag filters"
else
    fail "session list --tag" "$OUT"
fi
OUT=$(B session search tagkw --tag vis-shared 2>&1)
if grep -q "Release review" <<<"$OUT" && ! grep -q "Secret diary" <<<"$OUT"; then
    pass "session search --tag filters"
else
    fail "session search --tag" "$OUT"
fi
IDS=$(BJ search tagkw --type session --tag vis-shared | json "','.join(r['id'] for r in d)")
[ "$IDS" = "$S_SHARED" ] && pass "search --type session --tag returns only the shared session" \
    || fail "search --type session --tag" "ids=[$IDS]"
ERR_UNTAGGED=$(B session get "${S_PERSONAL:0:8}" --tag vis-shared 2>&1 >/dev/null; echo "exit=$?")
ERR_ABSENT=$(B session get ffffffff --tag vis-shared 2>&1 >/dev/null; echo "exit=$?")
if grep -q "session not found" <<<"$ERR_UNTAGGED" && [ "${ERR_ABSENT//ffffffff/${S_PERSONAL:0:8}}" = "$ERR_UNTAGGED" ] && ! grep -q "Secret" <<<"$ERR_UNTAGGED"; then
    pass "session get --tag on a personal session reads exactly like an absent id"
else
    fail "session get --tag (untagged)" "$ERR_UNTAGGED"
fi

echo ""
echo "3. note get --tag"
OUT=$(B note get "${N_SHARED:0:8}" --tag vis-shared 2>&1)
grep -q "Deploy runbook" <<<"$OUT" \
    && pass "note get --tag prints a shared note" || fail "note get --tag (shared)" "$OUT"
ERR_UNTAGGED=$(B note get "$N_PERSONAL" --tag vis-shared 2>&1 >/dev/null; echo "exit=$?")
ERR_ABSENT=$(B note get ffffffff-ffff-ffff-ffff-ffffffffffff --tag vis-shared 2>&1 >/dev/null; echo "exit=$?")
if grep -q "note not found" <<<"$ERR_UNTAGGED" && [ "${ERR_ABSENT//ffffffff-ffff-ffff-ffff-ffffffffffff/$N_PERSONAL}" = "$ERR_UNTAGGED" ] && ! grep -q "Private" <<<"$ERR_UNTAGGED"; then
    pass "note get --tag on a personal note reads exactly like an absent id"
else
    fail "note get --tag (untagged)" "$ERR_UNTAGGED"
fi

echo ""
echo "4. tag edits"
B note update "$N_OLD" --tags "a,b" >/dev/null 2>&1
B note update "$N_OLD" --tags "c" >/dev/null 2>&1
TAGS=$(BJ note get "$N_OLD" | json "','.join(d['tags'])")
[ "$TAGS" = "c" ] && pass "note update --tags replaces the whole list" || fail "note update --tags" "tags=[$TAGS]"
UPDATED_BEFORE=$(BJ note get "$N_OLD" | json "d['updated']")
B note update "$N_OLD" --add-tag "vis-shared,d" >/dev/null 2>&1
B note update "$N_OLD" --remove-tag "c" >/dev/null 2>&1
TAGS=$(BJ note get "$N_OLD" | json "','.join(d['tags'])")
UPDATED_AFTER=$(BJ note get "$N_OLD" | json "d['updated']")
if [ "$TAGS" = "vis-shared,d" ] && [ -n "$UPDATED_BEFORE" ] && [ "$UPDATED_BEFORE" = "$UPDATED_AFTER" ]; then
    pass "note update --add-tag/--remove-tag keep the other tags and the updated time"
else
    fail "note update --add-tag/--remove-tag" "tags=[$TAGS] updated $UPDATED_BEFORE -> $UPDATED_AFTER"
fi
B note update "$N_OLD" --tags "vis-shared-old" >/dev/null 2>&1
B session update "${S_PERSONAL:0:8}" --add-tag "x" >/dev/null 2>&1
TAGS=$(BJ session get "$S_PERSONAL" | json "','.join(d['tags'])")
[ "$TAGS" = "vis-personal,x" ] && pass "session update --add-tag keeps the other tags" || fail "session update --add-tag" "tags=[$TAGS]"
B session update "${S_PERSONAL:0:8}" --remove-tag "x" >/dev/null 2>&1

echo ""
echo "5. whole tags only"
IDS=$(BJ search tagkw --tag vis-shared | json "' '.join(sorted(r['id'] for r in d))")
WANT=$(printf '%s\n' "$N_SHARED" "$S_SHARED" | sort | tr '\n' ' ' | sed 's/ $//')
[ "$IDS" = "$WANT" ] && pass "search --tag vis-shared does not match vis-shared-old" || fail "search --tag exactness" "ids=[$IDS]"

echo ""
echo "6. tag apply (3 notes, 2 sessions)"
EDITS="$STORE/labels.jsonl"
{
    printf '{"id": "%s", "kind": "note", "add": ["vis-shared"]}\n' "$N_SHARED"
    printf '{"id": "%s", "kind": "note", "add": ["vis-shared"], "remove": ["vis-personal"]}\n' "$N_PERSONAL"
    printf '{"id": "%s", "kind": "note", "add": ["vis-shared"], "remove": ["vis-shared-old"]}\n' "$N_OLD"
    printf '{"id": "%s", "kind": "session", "add": ["vis-shared"]}\n' "$S_SHARED"
    printf '{"id": "%s", "kind": "session", "add": ["vis-shared"], "remove": ["vis-personal"]}\n' "$S_PERSONAL"
} > "$EDITS"
REPORT=$(BJ tag apply --file "$EDITS" --dry-run 2>&1)
COUNTS=$(echo "$REPORT" | json "'%d/%d/%d/%d/%d/%d' % (d['entries'], d['changed'], d['notes_changed'], d['sessions_changed'], d['unchanged'], len(d['unknown']))")
TAGGED=$(BJ search tagkw --tag vis-shared | json "len(d)")
if [ "$COUNTS" = "5/3/2/1/2/0" ] && [ "$TAGGED" = "2" ]; then
    pass "tag apply --dry-run: 5 entries, 3 would change (2 notes, 1 session), 2 unchanged; nothing written"
else
    fail "tag apply --dry-run" "counts=$COUNTS tagged=$TAGGED"
fi
OUT=$(B tag apply --file "$EDITS" 2>&1)
TAGGED=$(BJ search tagkw --tag vis-shared | json "len(d)")
LEFT=$(BJ search tagkw --tag vis-personal | json "len(d)")
if grep -q "^tag apply: 5 entries, 3 changed (2 notes, 1 sessions), 2 unchanged, 0 unknown, 0 failed" <<<"$OUT" && [ "$TAGGED" = "5" ] && [ "$LEFT" = "0" ]; then
    pass "tag apply updates the tags and search --tag finds all 5"
else
    fail "tag apply" "$OUT (tagged=$TAGGED, personal left=$LEFT)"
fi
OUT=$(B tag apply --file "$EDITS" 2>&1)
grep -q "0 changed (0 notes, 0 sessions), 5 unchanged" <<<"$OUT" \
    && pass "tag apply again changes nothing" || fail "tag apply idempotence" "$OUT"
printf '{"id": "00000000-0000-0000-0000-000000000000", "kind": "note", "add": ["x"]}\n' > "$STORE/unknown.jsonl"
if OUT=$(B tag apply --file "$STORE/unknown.jsonl" 2>&1); then
    fail "tag apply unknown id" "exit 0"
elif grep -q "unknown, line 1: entry not found" <<<"$OUT"; then
    pass "tag apply reports an unknown id and exits 1"
else
    fail "tag apply unknown id" "$OUT"
fi

echo ""
echo "7. stats --tag"
OUT=$(B stats --tag vis-shared 2>&1)
if grep -q "^Notes:    3$" <<<"$OUT" && grep -q "^Sessions: 2$" <<<"$OUT" && ! grep -qE "Deploy runbook|Private holiday|Release review|Secret diary|Storage:" <<<"$OUT"; then
    pass "stats --tag prints the tagged counts and no titles"
else
    fail "stats --tag" "$OUT"
fi

echo ""
echo "8. writes never merge across the vis-shared label"
N_P2=$(BJ note add life --title "Garden plans" --content "PRIVATE-GARDEN" --tags "vis-personal" | json "d['note_id']")
OUT=$(BJ note add life --title "Garden plans" --content "SHARED-GARDEN" --tags "vis-shared" 2>&1)
ACTION=$(echo "$OUT" | json "d['action'] + ' ' + d['note_id']")
CONTENT=$(BJ note get "$N_P2" | json "d['content']")
if [ "${ACTION%% *}" = "created" ] && [ "${ACTION#* }" != "$N_P2" ] && ! grep -q "SHARED-GARDEN" <<<"$CONTENT"; then
    pass "note add with a personal note's title from the shared side creates its own note"
else
    fail "note add across the label" "action=[$ACTION] personal content=[$CONTENT]"
fi
B session save --title "Diary two" --branch main --project p --session-id sid-c --summary "PRIVATE-DIARY" --tags "vis-personal" >/dev/null 2>&1
if OUT=$(B session save --title "Company wrapup" --branch main --project p --session-id sid-c --summary "SHARED-WRAPUP" --tags "vis-shared" 2>&1); then
    fail "session save across the label" "merged: $OUT"
elif grep -q "refusing to save" <<<"$OUT" && ! grep -q "Diary two" <<<"$OUT"; then
    pass "session save into a personal session id from the shared side is refused, naming nothing"
else
    fail "session save across the label" "$OUT"
fi
SHARED_RESUME=$(BJ session resolve --project p --tag vis-shared 2>/dev/null | json "d['session_id']")
PERSONAL_RESUME=$(BJ session resolve --project p 2>/dev/null | json "d['session_id']")
if [ "$PERSONAL_RESUME" = "sid-c" ] && [ -n "$SHARED_RESUME" ] && [ "$SHARED_RESUME" != "sid-c" ]; then
    pass "session resolve resumes only a session on the caller's side of the label"
else
    fail "session resolve --tag" "shared=[$SHARED_RESUME] personal=[$PERSONAL_RESUME]"
fi

echo ""
echo "9. help and the usage block after an error never print the home path"
HELP=$(HOME="$FAKE_HOME" "$BINARY" --help 2>&1)
RUN_ERR=$(HOME="$FAKE_HOME" "$BINARY" --store "$STORE" note get no-such-id 2>&1)
FLAG_ERR=$(HOME="$FAKE_HOME" "$BINARY" --store "$STORE" note get --no-such-flag 2>&1)
ALL_OUT="$HELP$RUN_ERR$FLAG_ERR"
if grep -q -- "--store" <<<"$ALL_OUT" && ! grep -qF "$FAKE_HOME" <<<"$ALL_OUT"; then
    pass "--help and the usage after a command or flag error show --store without the home path"
else
    fail "home path in help or usage" "$(grep -F -- "--store" <<<"$ALL_OUT")"
fi

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
