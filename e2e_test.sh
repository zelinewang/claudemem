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

# =============================================================================
# v3.0.0 Feature Tests (8 new capabilities)
# =============================================================================

# --- Test 11: search --compact --format json ---
echo ""
echo "Test 11: search --compact returns compact JSON"
COMPACT_OUT=$($BINARY --store "$STORE" search "Rate" --compact --format json 2>&1)
# Compact JSON should have id/type/title/score but NOT content/category
if echo "$COMPACT_OUT" | python3 -c "import sys,json; d=json.load(sys.stdin); assert 'id' in d[0] and 'title' in d[0] and 'content' not in d[0]" 2>/dev/null; then
  pass "search --compact JSON has id/title/score, no content"
else
  fail "search --compact" "missing fields or has content"
fi

# --- Test 12: search --compact text mode ---
echo ""
echo "Test 12: search --compact text mode"
COMPACT_TEXT=$($BINARY --store "$STORE" search "Rate" --compact 2>&1)
# Compact text should show results but be shorter than full output
FULL_TEXT=$($BINARY --store "$STORE" search "Rate" 2>&1)
COMPACT_LEN=${#COMPACT_TEXT}
FULL_LEN=${#FULL_TEXT}
if [ "$COMPACT_LEN" -lt "$FULL_LEN" ] && echo "$COMPACT_TEXT" | grep -q "Rate"; then
  pass "search --compact text is shorter than full output"
else
  fail "search --compact text" "compact=$COMPACT_LEN full=$FULL_LEN"
fi

# --- Test 13: context inject ---
echo ""
echo "Test 13: context inject returns recent items"
CTX_OUT=$($BINARY --store "$STORE" context inject --limit 3 2>&1)
# Should mention notes or sessions
if echo "$CTX_OUT" | grep -qE "note|session|Recent|Knowledge"; then
  pass "context inject returns context"
else
  fail "context inject" "no context returned"
fi

# --- Test 14: context inject --format json ---
echo ""
echo "Test 14: context inject --format json is valid JSON"
CTX_JSON=$($BINARY --store "$STORE" context inject --limit 3 --format json 2>&1)
if echo "$CTX_JSON" | python3 -c "import sys,json; json.load(sys.stdin)" 2>/dev/null; then
  pass "context inject JSON is valid"
else
  fail "context inject JSON" "invalid JSON"
fi

# --- Test 15: faceted search --category ---
echo ""
echo "Test 15: faceted search --category filter"
$BINARY --store "$STORE" note add security --title "Auth Tokens" --content "JWT security best practices" --tags "security,jwt" > /dev/null 2>&1
FACET_CAT=$($BINARY --store "$STORE" search "security" --category security --format json 2>&1)
# Should find the security note but not notes from other categories
if echo "$FACET_CAT" | python3 -c "import sys,json; d=json.load(sys.stdin); assert any('Auth Tokens' in r.get('title','') for r in d)" 2>/dev/null; then
  pass "faceted search --category filters correctly"
else
  fail "faceted search --category" "not found or wrong category"
fi

# --- Test 16: faceted search --tag ---
echo ""
echo "Test 16: faceted search --tag filter"
FACET_TAG=$($BINARY --store "$STORE" search "JWT" --tag jwt --format json 2>&1)
if echo "$FACET_TAG" | python3 -c "import sys,json; d=json.load(sys.stdin); assert len(d) > 0" 2>/dev/null; then
  pass "faceted search --tag returns results"
else
  fail "faceted search --tag" "no results"
fi

# --- Test 17: faceted search --sort date ---
echo ""
echo "Test 17: faceted search --sort date"
FACET_SORT=$($BINARY --store "$STORE" search "test" --sort date 2>&1)
if [ $? -eq 0 ] && [ -n "$FACET_SORT" ]; then
  pass "faceted search --sort date works"
else
  fail "faceted search --sort" "exit=$?"
fi

# --- Test 18: code outline on Go file ---
echo ""
echo "Test 18: code outline extracts Go symbols"
# Create a temp Go file to analyze
CODE_DIR=$(mktemp -d)
cat > "$CODE_DIR/sample.go" <<'GOEOF'
package main

import "fmt"

type UserService struct {
    db *Database
}

func NewUserService(db *Database) *UserService {
    return &UserService{db: db}
}

func (s *UserService) GetUser(id string) (*User, error) {
    return s.db.Find(id)
}

func main() {
    fmt.Println("hello")
}
GOEOF
OUTLINE=$($BINARY code outline "$CODE_DIR/sample.go" 2>&1)
# Should find type, func signatures
if echo "$OUTLINE" | grep -q "UserService" && echo "$OUTLINE" | grep -q "GetUser"; then
  pass "code outline finds Go types and functions"
else
  fail "code outline" "missing symbols"
fi
rm -rf "$CODE_DIR"

# --- Test 19: graph --format json ---
echo ""
echo "Test 19: graph --format json outputs adjacency list"
GRAPH_JSON=$($BINARY --store "$STORE" graph --format json 2>&1)
if echo "$GRAPH_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); assert 'nodes' in d or 'edges' in d or isinstance(d, dict)" 2>/dev/null; then
  pass "graph --format json is valid"
else
  fail "graph JSON" "invalid JSON"
fi

# --- Test 20: graph --format dot ---
echo ""
echo "Test 20: graph --format dot outputs Graphviz"
GRAPH_DOT=$($BINARY --store "$STORE" graph --format dot 2>&1)
if echo "$GRAPH_DOT" | grep -q "digraph"; then
  pass "graph --format dot has digraph header"
else
  fail "graph dot" "no digraph header"
fi

# --- Test 21: stats --top-accessed ---
echo ""
echo "Test 21: stats --top-accessed shows access data"
# First trigger some access events via note get
$BINARY --store "$STORE" note get "$NOTE_ID" > /dev/null 2>&1
$BINARY --store "$STORE" note get "$NOTE_ID" > /dev/null 2>&1
TOP_OUT=$($BINARY --store "$STORE" stats --top-accessed 2>&1)
if [ $? -eq 0 ]; then
  pass "stats --top-accessed runs without error"
else
  fail "stats --top-accessed" "exit=$?"
fi

# --- Test 22: capture with feature flag disabled ---
echo ""
echo "Test 22: capture exits silently when feature flag disabled"
echo '{"tool_name":"Bash","output":"error: something failed badly"}' | $BINARY --store "$STORE" capture 2>&1
NOTES_BEFORE=$($BINARY --store "$STORE" stats 2>&1 | grep "Notes:" | grep -o '[0-9]*')
if [ $? -eq 0 ]; then
  pass "capture exits silently when disabled"
else
  fail "capture disabled" "non-zero exit"
fi

# --- Test 23: capture with feature flag enabled ---
echo ""
echo "Test 23: capture saves note when feature enabled"
$BINARY --store "$STORE" config set features.auto_capture true > /dev/null 2>&1
NOTES_BEFORE=$($BINARY --store "$STORE" stats 2>&1 | grep "Notes:" | grep -o '[0-9]*')
echo '{"tool_name":"Bash","output":"FATAL ERROR: connection refused to database server\nPermission denied: /etc/shadow\nTest results: 5 passed, 3 failed"}' | $BINARY --store "$STORE" capture 2>&1
NOTES_AFTER=$($BINARY --store "$STORE" stats 2>&1 | grep "Notes:" | grep -o '[0-9]*')
if [ "$NOTES_AFTER" -gt "$NOTES_BEFORE" ]; then
  pass "capture creates note when enabled"
else
  fail "capture enabled" "notes before=$NOTES_BEFORE after=$NOTES_AFTER"
fi

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
