#!/bin/bash
# End-to-end CLI tests for claudemem
# Run: make build && bash e2e_test.sh
# No private environment details — uses temp directories only.

set -uo pipefail
# NOTE: not using set -e; we handle errors manually per test

BINARY="./claudemem"
STORE=$(mktemp -d)
PASS=0
FAIL=0

pass() { echo "  ✓ $1"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $1: $2"; FAIL=$((FAIL+1)); }

if [ ! -f "$BINARY" ]; then
    echo "ERROR: $BINARY not found. Run 'make build' first."
    exit 1
fi

echo "Running E2E tests..."
echo "Store: $STORE"

# --- Test 1: note add --session-id ---
echo ""
echo "Test 1: note add with session-id"
NOTE_OUT=$($BINARY --store "$STORE" note add api-docs --title "Rate Limits" --content "100 req/min" --tags "api" --session-id "ref-001" --format json 2>&1)
# JSON has spaces: "note_id": "uuid"
NOTE_ID=$(echo "$NOTE_OUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['note_id'])" 2>/dev/null || echo "")
if [ -n "$NOTE_ID" ]; then pass "note add --session-id returns note_id ($NOTE_ID)"; else fail "note add --session-id" "no note_id"; fi

# --- Test 2: note get shows session_id (JSON) ---
echo ""
echo "Test 2: note get shows session_id metadata (JSON)"
NOTE_DETAIL=$($BINARY --store "$STORE" note get "$NOTE_ID" --format json 2>&1)
if echo "$NOTE_DETAIL" | grep -q "session_id"; then pass "note get shows session_id"; else fail "note get metadata" "session_id not found in JSON"; fi

# --- Test 3: session save with piped markdown ---
echo ""
echo "Test 3: session save with piped markdown (all sections)"
printf '## Summary\nE2E test session summary.\n\n## What Happened\n1. Phase one: set up test\n2. Phase two: ran tests\n3. Phase three: verified\n\n## Key Decisions\n- Use temp store for isolation\n\n## Learning Insights\n- E2E tests are valuable\n- Temp dirs prevent contamination\n\n## Next Steps\n- [ ] Commit the tests\n' | \
  $BINARY --store "$STORE" session save --title "E2E Test Session" --branch "test" --project "e2e" --session-id "ref-001" --tags "e2e,test" 2>&1
if [ $? -eq 0 ]; then pass "session save with piped markdown"; else fail "session save" "non-zero exit"; fi

# --- Test 4: session list ---
echo ""
echo "Test 4: session list shows session"
SESSION_LIST=$($BINARY --store "$STORE" session list --last 5 2>&1)
if echo "$SESSION_LIST" | grep -q "E2E Test Session"; then pass "session list"; else fail "session list" "not found"; fi

# --- Test 5: search finds WhatHappened ---
echo ""
echo "Test 5: search finds content from What Happened section"
SEARCH_OUT=$($BINARY --store "$STORE" search "Phase one" 2>&1)
if echo "$SEARCH_OUT" | grep -q "E2E Test Session"; then pass "search finds WhatHappened"; else fail "search WhatHappened" "not found"; fi

# --- Test 6: search --type filter ---
echo ""
echo "Test 6: search --type note filter"
$BINARY --store "$STORE" note add debugging --title "Search Filter Test" --content "uniquefilterword123" --tags "test" > /dev/null 2>&1
SEARCH_NOTE=$($BINARY --store "$STORE" search "uniquefilterword123" --type note 2>&1)
if echo "$SEARCH_NOTE" | grep -q "Search Filter Test"; then pass "search --type note"; else fail "search --type" "not found"; fi

# --- Test 7: note dedup preserves session_id ---
echo ""
echo "Test 7: note dedup preserves session_id"
$BINARY --store "$STORE" note add api-docs --title "Rate Limits" --content "Updated to 200 req/min" --session-id "ref-002" > /dev/null 2>&1
DEDUP_NOTE=$($BINARY --store "$STORE" note get "$NOTE_ID" --format json 2>&1)
if echo "$DEDUP_NOTE" | grep -q "ref-002"; then pass "dedup preserves latest session_id"; else fail "dedup metadata" "session_id not updated in JSON"; fi

# --- Test 8: stats ---
echo ""
echo "Test 8: stats command"
STATS=$($BINARY --store "$STORE" stats 2>&1)
if echo "$STATS" | grep -q "Notes:"; then pass "stats works"; else fail "stats" "no output"; fi

# --- Test 9: note get with 8-char prefix ---
echo ""
echo "Test 9: note get with ID prefix"
NOTE_PREFIX=$(echo "$NOTE_ID" | cut -c1-8)
PREFIX_GET=$($BINARY --store "$STORE" note get "$NOTE_PREFIX" 2>&1)
if echo "$PREFIX_GET" | grep -q "Rate Limits"; then pass "note get by 8-char prefix"; else fail "prefix get" "not found"; fi

# --- Test 10: note categories ---
echo ""
echo "Test 10: note categories listing"
CATS=$($BINARY --store "$STORE" note categories 2>&1)
if echo "$CATS" | grep -q "api-docs"; then pass "note categories"; else fail "categories" "api-docs not found"; fi

# --- Cleanup ---
rm -rf "$STORE"

echo ""
echo "======================================="
echo "Results: $PASS passed, $FAIL failed"
if [ $FAIL -gt 0 ]; then
    echo "FAIL: Some E2E tests failed"
    exit 1
fi
echo "All E2E tests passed!"
echo "======================================="
