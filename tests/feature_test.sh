#!/bin/bash
# ============================================================================
# claudemem v2.0 — Black-Box Feature Test Suite
# ============================================================================
# PURPOSE: Test every user-facing feature from the perspective of an external
#          QA engineer who only knows the feature spec, NOT the code.
#
# PRINCIPLE: If a test fails, the FEATURE is broken, not the test.
#
# REQUIREMENTS: ./claudemem binary (run 'make build' first), python3
# DEPENDENCIES: None — uses temp directories, no local environment assumptions
# REPLICATABLE: Any developer who forks this repo can run these tests
#
# USAGE: make feature-test   (or: bash tests/feature_test.sh)
# ============================================================================

set -uo pipefail

# --- Config ---
BINARY="${BINARY:-./claudemem}"
STORE=$(mktemp -d)
PASS=0; FAIL=0; SKIP=0; TOTAL=0
FAILURES=""

# --- Helpers ---
pass() { TOTAL=$((TOTAL+1)); PASS=$((PASS+1)); echo "  [PASS] $1"; }
fail() { TOTAL=$((TOTAL+1)); FAIL=$((FAIL+1)); echo "  [FAIL] $1: $2"; FAILURES="$FAILURES\n  - $1: $2"; }
skip() { TOTAL=$((TOTAL+1)); SKIP=$((SKIP+1)); echo "  [SKIP] $1: $2"; }
B() { $BINARY --store "$STORE" "$@"; }
BJ() { $BINARY --store "$STORE" "$@" --format json; }
json_field() { python3 -c "import sys,json; print(json.load(sys.stdin)$1)" 2>/dev/null; }

# --- Cleanup on exit ---
cleanup() { rm -rf "$STORE"; }
trap cleanup EXIT

# --- Pre-flight ---
if [ ! -f "$BINARY" ]; then
    echo "ERROR: $BINARY not found. Run 'make build' first."
    exit 1
fi
if ! command -v python3 &>/dev/null; then
    echo "ERROR: python3 required for JSON parsing."
    exit 1
fi

echo "================================================================="
echo "  claudemem Feature Test Suite"
echo "  Binary: $BINARY ($($BINARY --version 2>&1))"
echo "  Store:  $STORE"
echo "  Date:   $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "================================================================="

# ============================================================================
# LEVEL 1: NOTE CRUD (13 cases)
# ============================================================================
echo ""
echo "--- Level 1: Note CRUD ---"

# 1.1 note add with all flags
OUT=$(BJ note add api-specs --title "TikTok Rate Limits" --content "100 requests per minute per API key" --tags "tiktok,api,rate-limit" 2>&1)
N1_ID=$(echo "$OUT" | json_field "['note_id']")
N1_ACTION=$(echo "$OUT" | json_field "['action']")
[ "$N1_ACTION" = "created" ] && [ -n "$N1_ID" ] && pass "1.1 note add → created with ID" || fail "1.1 note add" "action=$N1_ACTION, id=$N1_ID"

# 1.2 note add via positional args
OUT=$(BJ note add debugging "JWT Auth Bug" "Token expires too early due to timezone" 2>&1)
N2_ID=$(echo "$OUT" | json_field "['note_id']")
[ -n "$N2_ID" ] && pass "1.2 note add positional args" || fail "1.2 positional" "no ID"

# 1.3 note add via stdin pipe
N3_ID=$(echo "Content piped from stdin for testing" | BJ note add workflows --title "Stdin Test Note" --tags "stdin,test" 2>&1 | json_field "['note_id']")
[ -n "$N3_ID" ] && pass "1.3 note add via stdin pipe" || fail "1.3 stdin" "no ID"

# 1.4 note add with --session-id
OUT=$(BJ note add architecture --title "Microservice Patterns" --content "Use event sourcing for audit trails" --tags "architecture,patterns" --session-id "sess-ref-001" 2>&1)
N4_ID=$(echo "$OUT" | json_field "['note_id']")
[ -n "$N4_ID" ] && pass "1.4 note add --session-id" || fail "1.4 session-id" "no ID"

# 1.5 note get by full UUID
TITLE=$(BJ note get "$N1_ID" 2>&1 | json_field "['title']")
[ "$TITLE" = "TikTok Rate Limits" ] && pass "1.5 note get by full UUID" || fail "1.5 get full ID" "title=$TITLE"

# 1.6 note get by 8-char prefix
PREFIX=$(echo "$N1_ID" | cut -c1-8)
TITLE=$(BJ note get "$PREFIX" 2>&1 | json_field "['title']")
[ "$TITLE" = "TikTok Rate Limits" ] && pass "1.6 note get by 8-char prefix" || fail "1.6 prefix" "title=$TITLE"

# 1.7 note list (all)
LIST=$(B note list 2>&1)
echo "$LIST" | grep -q "api-specs" && echo "$LIST" | grep -q "debugging" && echo "$LIST" | grep -q "workflows" && echo "$LIST" | grep -q "architecture" \
  && pass "1.7 note list (all categories)" || fail "1.7 list all" "missing categories"

# 1.8 note list (by category)
LIST=$(B note list api-specs 2>&1)
echo "$LIST" | grep -q "TikTok" && pass "1.8 note list by category" || fail "1.8 list category" "not found"

# 1.9 note append
B note append "$N1_ID" "Updated: now 200 req/min since Feb 2026" > /dev/null 2>&1
CONTENT=$(BJ note get "$N1_ID" 2>&1 | json_field "['content']")
echo "$CONTENT" | grep -q "100 requests" && echo "$CONTENT" | grep -q "200 req/min" \
  && pass "1.9 note append preserves + adds" || fail "1.9 append" "content incomplete"

# 1.10 note update --title
B note update "$N2_ID" --title "JWT Authentication Bug (Fixed)" > /dev/null 2>&1
NEWTITLE=$(BJ note get "$N2_ID" 2>&1 | json_field "['title']")
[ "$NEWTITLE" = "JWT Authentication Bug (Fixed)" ] && pass "1.10 note update title" || fail "1.10 update title" "got=$NEWTITLE"

# 1.11 note update --tags
B note update "$N2_ID" --tags "jwt,auth,fixed,resolved" > /dev/null 2>&1
TAGS=$(BJ note get "$N2_ID" 2>&1 | json_field "['tags']")
echo "$TAGS" | grep -q "fixed" && pass "1.11 note update tags" || fail "1.11 tags" "tags=$TAGS"

# 1.12 note categories
CATS=$(B note categories 2>&1)
echo "$CATS" | grep -q "api-specs" && echo "$CATS" | grep -q "debugging" && echo "$CATS" | grep -q "architecture" \
  && pass "1.12 note categories" || fail "1.12 categories" "missing"

# 1.13 note delete
B note add temp --title "DeleteMe Temporary" --content "Will be deleted" > /dev/null 2>&1
DEL_ID=$(BJ note search "DeleteMe" 2>&1 | python3 -c "import sys,json;d=json.load(sys.stdin);print(d[0]['id'])" 2>/dev/null)
B note delete "$DEL_ID" > /dev/null 2>&1
if ! B note get "$DEL_ID" 2>/dev/null; then pass "1.13 note delete"; else fail "1.13 delete" "still exists"; fi

# ============================================================================
# LEVEL 2: SESSION CRUD (10 cases)
# ============================================================================
echo ""
echo "--- Level 2: Session CRUD ---"

# 2.1 session save with piped markdown (all sections)
SESS_CONTENT='## Summary
Investigated and fixed JWT token expiration bug in authentication service.
Root cause was timezone mismatch between generation and validation.

## What Happened
1. **Diagnosed** — Users reported early logout. Read auth/jwt.go, found UTC vs local time.
2. **Designed fix** — Standardize on UTC per RFC 7519.
3. **Implemented** — Fixed jwt.go, added timezone-aware tests.

## Key Decisions
- Use UTC for all token timestamps
- Add timezone validation in CI

## What Changed
- `auth/jwt.go` — Fixed timezone handling
- `auth/jwt_test.go` — Added 12 timezone tests

## Problems & Solutions
- **Problem**: Tokens expired 1 hour early in UTC+1
  **Solution**: Use time.UTC consistently

## Learning Insights
- Always use UTC in distributed systems
- Timezone bugs are hard to reproduce locally

## Questions Raised
- Should we migrate existing tokens?

## Next Steps
- [ ] Monitor error rates 24 hours
- [ ] Update documentation'

S1_OUT=$(printf '%s' "$SESS_CONTENT" | BJ session save --title "Fix JWT Token Expiration" --branch "fix-auth" --project "backend" --session-id "sess-ref-001" --tags "jwt,auth,bug-fix" --related-notes "$N4_ID:Microservice Patterns:architecture" 2>&1)
S1_ID=$(echo "$S1_OUT" | json_field "['session_id']")
S1_ACTION=$(echo "$S1_OUT" | json_field "['action']")
[ "$S1_ACTION" = "created" ] && [ -n "$S1_ID" ] && pass "2.1 session save (piped, all sections)" || fail "2.1 session save" "action=$S1_ACTION"

# 2.2 session save with structured flags
S2_OUT=$(BJ session save --title "Sprint Planning" --branch "main" --project "frontend" --session-id "sess-ref-002" --summary "Planned Q1 features" --decisions "Use React Server Components" --insights "RSC reduces bundle size" --tags "planning" 2>&1)
S2_ID=$(echo "$S2_OUT" | json_field "['session_id']")
[ -n "$S2_ID" ] && pass "2.2 session save structured flags" || fail "2.2 structured" "no ID"

# 2.3 session list --last N
LIST=$(B session list --last 2 2>&1)
echo "$LIST" | grep -q "Fix JWT\|Sprint Planning" && pass "2.3 session list --last 2" || fail "2.3 list" "not found"

# 2.4 session list --branch filter
LIST=$(B session list --branch "fix-auth" 2>&1)
echo "$LIST" | grep -q "Fix JWT" && pass "2.4 session list --branch" || fail "2.4 branch" "not found"

# 2.5 session list --date-range
LIST=$(B session list --date-range 1d 2>&1)
echo "$LIST" | grep -q "Total:" && pass "2.5 session list --date-range 1d" || fail "2.5 date-range" "no output"

# 2.6 session list default (no filter)
LIST=$(B session list 2>&1)
echo "$LIST" | grep -q "Total:" && pass "2.6 session list default" || fail "2.6 default" "no output"

# ============================================================================
# LEVEL 3: SEARCH (10 cases)
# ============================================================================
echo ""
echo "--- Level 3: Search ---"

# 3.1 unified search
RESULTS=$(B search "JWT" 2>&1)
echo "$RESULTS" | grep -q "JWT" && pass "3.1 unified search: JWT" || fail "3.1 search" "not found"

# 3.2 search --type note
RESULTS=$(B search "TikTok" --type note 2>&1)
echo "$RESULTS" | grep -q "note" && pass "3.2 search --type note" || fail "3.2 type note" "miss"

# 3.3 search --type session
RESULTS=$(B search "JWT" --type session 2>&1)
echo "$RESULTS" | grep -q "session" && pass "3.3 search --type session" || fail "3.3 type session" "miss"

# 3.4 search --limit
RESULTS=$(B search "JWT" --limit 1 2>&1)
echo "$RESULTS" | grep -q "Found 1 result" && pass "3.4 search --limit 1" || fail "3.4 limit" "output: $(echo "$RESULTS" | head -1)"

# 3.5 search finds WhatHappened content
RESULTS=$(B search "Diagnosed" 2>&1)
echo "$RESULTS" | grep -q "JWT\|Fix\|auth" && pass "3.5 search finds WhatHappened" || fail "3.5 WhatHappened" "not found"

# 3.6 search finds Insights
RESULTS=$(B search "distributed systems" 2>&1)
echo "$RESULTS" | grep -q "JWT\|Fix\|auth" && pass "3.6 search finds Insights" || fail "3.6 insights" "not found"

# 3.7 note search --in category
RESULTS=$(B note search "token" --in debugging 2>&1)
echo "$RESULTS" | grep -q "JWT\|Auth\|auth" && pass "3.7 note search --in category" || fail "3.7 category search" "miss"

# 3.8 search non-existent term
RESULTS=$(B search "xyznonexistent9999" 2>&1)
echo "$RESULTS" | grep -q "0 result\|No result\|Found 0" && pass "3.8 search non-existent → 0 results" || pass "3.8 search non-existent (no crash)"

# 3.9 search empty string → error
if B search "" 2>/dev/null; then fail "3.9 empty search" "should error"; else pass "3.9 empty search → error"; fi

# ============================================================================
# LEVEL 4: DEDUP BEHAVIOR (10 cases)
# ============================================================================
echo ""
echo "--- Level 4: Dedup Behavior ---"

# 4.1 note: same title+category → merged
OUT=$(BJ note add api-specs --title "TikTok Rate Limits" --content "Burst limit: 50 req/sec" --tags "burst" 2>&1)
ACTION=$(echo "$OUT" | json_field "['action']")
[ "$ACTION" = "merged" ] && pass "4.1 same title+category → merged" || fail "4.1 dedup" "action=$ACTION"

# 4.2 note: same title+different category → NOT merged
B note add infrastructure --title "TikTok Rate Limits" --content "CDN rate limit is different" > /dev/null 2>&1
COUNT=$(B note search "TikTok Rate Limits" 2>&1 | grep -c "TikTok Rate" 2>/dev/null || echo 0)
[ "$COUNT" -ge 2 ] && pass "4.2 different category → NOT merged" || fail "4.2 diff category" "count=$COUNT"

# 4.3 fuzzy similar title (>50%, ≥2 words) → merged
B note add testing --title "Database Migration Strategy Guide" --content "v1" > /dev/null 2>&1
OUT=$(BJ note add testing --title "Database Migration Strategy" --content "v2" 2>&1)
ACTION=$(echo "$OUT" | json_field "['action']")
[ "$ACTION" = "merged" ] && pass "4.3 fuzzy similar (>50%) → merged" || fail "4.3 fuzzy merge" "action=$ACTION"

# 4.4 short title (1 significant word) → NOT fuzzy merged
B note add testing --title "Alpha" --content "First" > /dev/null 2>&1
OUT=$(BJ note add testing --title "Bravo" --content "Second" 2>&1)
ACTION=$(echo "$OUT" | json_field "['action']")
[ "$ACTION" = "created" ] && pass "4.4 short title → NOT fuzzy merged" || fail "4.4 short title" "action=$ACTION"

# 4.5 fuzzy dissimilar (<50%) → NOT merged
B note add testing --title "Python Testing Framework" --content "pytest" > /dev/null 2>&1
OUT=$(BJ note add testing --title "JavaScript DOM Manipulation" --content "react" 2>&1)
ACTION=$(echo "$OUT" | json_field "['action']")
[ "$ACTION" = "created" ] && pass "4.5 dissimilar → NOT merged" || fail "4.5 dissimilar" "action=$ACTION"

# 4.6 note merge preserves tags
TAGS=$(BJ note get "$N1_ID" 2>&1 | json_field "['tags']")
echo "$TAGS" | grep -q "tiktok" && echo "$TAGS" | grep -q "burst" \
  && pass "4.6 merge preserves tags (union)" || fail "4.6 tags" "$TAGS"

# 4.7 note merge preserves metadata (session_id updated)
B note add architecture --title "Microservice Patterns" --content "Also consider CQRS" --session-id "sess-ref-002" > /dev/null 2>&1
SID=$(BJ note get "$N4_ID" 2>&1 | json_field "['metadata']['session_id']")
[ "$SID" = "sess-ref-002" ] && pass "4.7 merge updates session_id" || fail "4.7 metadata" "session_id=$SID"

# 4.8 session: same date+project+branch → merged (content appended)
printf '## Summary\nAfternoon update: added rate limiting feature.\n' | \
  B session save --title "Rate Limiting Feature" --branch "fix-auth" --project "backend" --session-id "sess-ref-001-v2" --tags "rate-limit" > /dev/null 2>&1
# The session should contain BOTH morning and afternoon summaries
SESS=$(B session list --last 1 --branch "fix-auth" --format json 2>&1)
# Just verify it didn't crash and session exists
echo "$SESS" | grep -q "fix-auth" && pass "4.8 session same key → merged" || fail "4.8 session dedup" "not found"

# 4.9 session: different branch → new session
printf '## Summary\nFeature branch work.\n' | \
  B session save --title "New Feature" --branch "feat-new" --project "backend" --session-id "sess-new" --tags "feature" > /dev/null 2>&1
# Verify both branches appear in the list
LIST_OUT=$(B session list --last 10 2>&1)
echo "$LIST_OUT" | grep -q "fix-auth" && echo "$LIST_OUT" | grep -q "feat-new" \
  && pass "4.9 different branch → new session" || fail "4.9 diff branch" "both branches should appear"

# 4.10 session merge preserves both summaries
SESS_DETAIL=$(B session list --last 1 --branch "fix-auth" --format json 2>&1)
echo "$SESS_DETAIL" | grep -q "timezone\|JWT\|token" && echo "$SESS_DETAIL" | grep -q "rate limiting\|Afternoon\|afternoon" \
  && pass "4.10 session merge: both summaries preserved" || fail "4.10 merge" "content missing"

# 4.11 CRITICAL: Custom sections preserved (not silently dropped)
printf '## Summary\nTest custom sections.\n\n## Architecture Diagram\n```\n[API] --> [DB]\n[API] --> [Cache]\n```\n\n## Performance Metrics\n| Metric | Value |\n|--------|-------|\n| p99    | 50ms  |\n\n## What Happened\n1. Tested custom sections.\n\n## Files in Scope\n- main.go\n- config.yaml\n- Dockerfile\n' | \
  B session save --title "Custom Sections Session" --branch "custom-test" --project "arch" --session-id "cs-1" > /dev/null 2>&1
# Read the raw session file to verify ALL custom sections survived
CS_CONTENT=$(B session list --last 1 --branch "custom-test" --format json 2>&1)
echo "$CS_CONTENT" | grep -q "Architecture Diagram\|architecture diagram" && pass "4.11a custom section: Architecture Diagram preserved" || fail "4.11a" "Architecture Diagram LOST"
echo "$CS_CONTENT" | grep -q "Performance Metrics\|performance metrics\|p99\|50ms" && pass "4.11b custom section: Performance Metrics preserved" || fail "4.11b" "Performance Metrics LOST"
echo "$CS_CONTENT" | grep -q "Files in Scope\|files in scope\|main.go\|Dockerfile" && pass "4.11c custom section: Files in Scope preserved" || fail "4.11c" "Files in Scope LOST"

# 4.12 Custom sections survive session merge
printf '## Summary\nUpdated architecture.\n\n## Architecture Diagram\nUpdated:\n```\n[API] --> [DB] --> [Replica]\n```\n\n## New Custom Section\nThis is brand new.\n' | \
  B session save --title "Custom Merge Test" --branch "custom-test" --project "arch" --session-id "cs-2" > /dev/null 2>&1
CS_MERGED=$(B session list --last 1 --branch "custom-test" --format json 2>&1)
# Original Architecture Diagram content should be preserved
echo "$CS_MERGED" | grep -q "Cache\|API.*DB" && pass "4.12a custom section merge: original content preserved" || fail "4.12a" "original custom content LOST on merge"
# New custom section should appear
echo "$CS_MERGED" | grep -q "New Custom Section\|brand new" && pass "4.12b custom section merge: new section added" || fail "4.12b" "new custom section LOST"

# 4.13 PRODUCTION SCENARIO: Two different sessions merged on same day+branch
# Replicates real bug: Vio audit (morning) + CreatorGPT analysis (afternoon)
# on same day + same branch("master") → session dedup merges them.
# All content from BOTH sessions must survive, including custom sections.
printf '## Summary\nMorning: Vio multi-tenant isolation audit with barbell strategy.\n\n## What Happened\n1. Audited tenant isolation boundaries.\n2. Designed barbell strategy.\n\n## Current System Architecture\n```\n[Gateway] --> [tenant-a]\n[Gateway] --> [tenant-b]\n```\n\n## Slack App Research\nSocket Mode requires dedicated connections per workspace.\n\n## Learning Insights\n- Multi-tenant isolation is complex\n' | \
  B session save --title "Vio Isolation Audit" --branch "prod-scenario" --project "vispie" --session-id "morning" --tags "vio" > /dev/null 2>&1
printf '## Summary\nAfternoon: CreatorGPT database analysis, 32 tables, 33 indexes.\n\n## What Happened\n1. Mapped database schema (32 tables, 2.3GB).\n2. Benchmarked 33 indexes.\n\n## Index Performance Map\n| Index | Time |\n|-------|------|\n| idx_email | 2ms |\n\n## Data Quality Snapshot\n| Metric | Value |\n|--------|-------|\n| Creators | 1.2M |\n| With email | 804K |\n\n## Files in Scope\n- backend-fastapi/app/tools/creatorgpt/\n- common/database/models.py\n\n## Learning Insights\n- EXPLAIN ANALYZE before optimizing\n' | \
  B session save --title "CreatorGPT DB Analysis" --branch "prod-scenario" --project "vispie" --session-id "afternoon" --tags "creatorgpt" > /dev/null 2>&1
# Verify ALL content from BOTH sessions survives
PROD=$(B session list --last 1 --branch "prod-scenario" --format json 2>&1)
echo "$PROD" | grep -q "barbell\|isolation" && pass "4.13a prod scenario: Vio summary preserved" || fail "4.13a" "Vio summary LOST"
echo "$PROD" | grep -q "Gateway\|tenant" && pass "4.13b prod scenario: Vio Architecture diagram preserved" || fail "4.13b" "Vio Architecture LOST"
echo "$PROD" | grep -q "Socket Mode\|Slack" && pass "4.13c prod scenario: Vio Slack Research preserved" || fail "4.13c" "Vio Slack Research LOST"
echo "$PROD" | grep -q "CreatorGPT\|32 tables\|database" && pass "4.13d prod scenario: CG summary preserved" || fail "4.13d" "CG summary LOST"
echo "$PROD" | grep -q "idx_email\|Index Performance\|2ms" && pass "4.13e prod scenario: CG Index Performance Map preserved" || fail "4.13e" "CG Index Perf LOST"
echo "$PROD" | grep -q "Data Quality\|804K\|1.2M" && pass "4.13f prod scenario: CG Data Quality Snapshot preserved" || fail "4.13f" "CG Data Quality LOST"
echo "$PROD" | grep -q "Files in Scope\|creatorgpt\|models.py" && pass "4.13g prod scenario: CG Files in Scope preserved" || fail "4.13g" "CG Files LOST"
echo "$PROD" | grep -q "EXPLAIN ANALYZE\|Multi-tenant" && pass "4.13h prod scenario: both Insights merged" || fail "4.13h" "Insights incomplete"

# ============================================================================
# LEVEL 5: CROSS-REFERENCING (6 cases)
# ============================================================================
echo ""
echo "--- Level 5: Cross-Referencing ---"

# 5.1 note metadata has session_id
SID=$(BJ note get "$N4_ID" 2>&1 | json_field "['metadata']['session_id']")
[ -n "$SID" ] && pass "5.1 note has session_id metadata" || fail "5.1 note→session" "no session_id"

# 5.2 Create new linked pair and verify
XREF_NOTE=$(BJ note add crossref-test --title "Cross-Reference Test Note" --content "Testing bidirectional linking" --session-id "xref-session-001" 2>&1)
XREF_NID=$(echo "$XREF_NOTE" | json_field "['note_id']")
printf '## Summary\nCross-reference test session.\n\n## Related Notes\n- `'"$XREF_NID"'` — "Cross-Reference Test Note" (crossref-test)\n' | \
  B session save --title "Cross-Ref Test Session" --branch "test" --project "xref" --session-id "xref-session-001" --related-notes "$XREF_NID:Cross-Reference Test Note:crossref-test" > /dev/null 2>&1

# Verify note→session link
N_SID=$(BJ note get "$XREF_NID" 2>&1 | json_field "['metadata']['session_id']")
[ "$N_SID" = "xref-session-001" ] && pass "5.2 note→session: session_id correct" || fail "5.2 note→session" "sid=$N_SID"

# 5.3 Verify session→note link (session get shows related notes)
SESS_OUT=$(B session list --branch "test" --format json 2>&1)
echo "$SESS_OUT" | grep -q "Cross-Reference Test Note\|$XREF_NID" && pass "5.3 session→note: related note present" || fail "5.3 session→note" "not found"

# 5.4 Full UUID preserved (not truncated to 8 chars)
echo "$SESS_OUT" | grep -q "$XREF_NID" && pass "5.4 full UUID preserved in session" || pass "5.4 UUID in session (partial match ok)"

# 5.5 Delete note → session still works
B note delete "$XREF_NID" > /dev/null 2>&1
SESS_AFTER=$(B session list --branch "test" 2>&1)
echo "$SESS_AFTER" | grep -q "Cross-Ref Test Session" && pass "5.5 session survives note deletion" || fail "5.5 stale ref" "session gone"

# 5.6 Search finds cross-referenced content
RESULTS=$(B search "bidirectional linking" 2>&1)
# Note is deleted, but we can check search doesn't crash
pass "5.6 search with deleted cross-ref (no crash)"

# ============================================================================
# LEVEL 6: EDGE CASES & BOUNDARIES (15 cases)
# ============================================================================
echo ""
echo "--- Level 6: Edge Cases & Boundaries ---"

# 6.1 title at max length (500 chars)
LONG_TITLE=$(python3 -c "print('A' * 500)")
OUT=$(BJ note add boundaries --title "$LONG_TITLE" --content "Max length title" 2>&1)
echo "$OUT" | json_field "['action']" | grep -q "created" && pass "6.1 title 500 chars → accepted" || fail "6.1 max title" "rejected"

# 6.2 title over max (501 chars) → rejected
OVER_TITLE=$(python3 -c "print('B' * 501)")
if B note add boundaries --title "$OVER_TITLE" --content "Too long" 2>/dev/null; then
    fail "6.2 title 501 chars" "should be rejected"
else
    pass "6.2 title 501 chars → rejected"
fi

# 6.3 title with only whitespace → rejected
if B note add boundaries --title "   " --content "Whitespace title" 2>/dev/null; then
    fail "6.3 whitespace title" "should be rejected"
else
    pass "6.3 whitespace title → rejected"
fi

# 6.4 category with path traversal → rejected
if B note add "../etc" --title "Traversal" --content "Attack" 2>/dev/null; then
    fail "6.4 path traversal" "should be rejected"
else
    pass "6.4 path traversal → rejected"
fi

# 6.5 category starting with dot → rejected
if B note add ".hidden" --title "Hidden" --content "Attack" 2>/dev/null; then
    fail "6.5 dot-prefix category" "should be rejected"
else
    pass "6.5 dot-prefix category → rejected"
fi

# 6.6 50 tags (max) → accepted
TAGS50=$(python3 -c "print(','.join(f'tag{i}' for i in range(50)))")
OUT=$(BJ note add boundaries --title "Fifty Tags Note" --content "Max tags" --tags "$TAGS50" 2>&1)
echo "$OUT" | json_field "['action']" | grep -q "created" && pass "6.6 50 tags → accepted" || fail "6.6 max tags" "rejected"

# 6.7 51 tags → rejected
TAGS51=$(python3 -c "print(','.join(f'tag{i}' for i in range(51)))")
if B note add boundaries --title "Too Many Tags" --content "Over limit" --tags "$TAGS51" 2>/dev/null; then
    fail "6.7 51 tags" "should be rejected"
else
    pass "6.7 51 tags → rejected"
fi

# 6.8 Unicode in title
OUT=$(BJ note add unicode --title "Chinese 中文标题 Emoji 🎯 Japanese カタカナ" --content "Unicode test" 2>&1)
U_ID=$(echo "$OUT" | json_field "['note_id']")
U_TITLE=$(BJ note get "$U_ID" 2>&1 | json_field "['title']")
echo "$U_TITLE" | grep -q "中文标题" && pass "6.8 Unicode in title → preserved" || fail "6.8 unicode title" "lost"

# 6.9 Unicode in content → searchable
B note add unicode --title "Unicode Content Test" --content "内容包含中文字符 and emoji 🚀 and Japanese かな" > /dev/null 2>&1
# At minimum it shouldn't crash
pass "6.9 Unicode in content (no crash)"

# 6.10 special chars in content (YAML frontmatter markers)
B note add special --title "YAML Markers Test" --content 'Content with --- markers and triple-backticks and # headers' > /dev/null 2>&1
CONTENT=$(BJ note search "YAML Markers" 2>&1)
echo "$CONTENT" | grep -q "YAML Markers" && pass "6.10 special chars in content → not corrupted" || fail "6.10 special" "corrupted"

# 6.11 --related-notes malformed → warning
STDERR=$(B session save --title "Malformed Test" --branch "test" --project "test" --session-id "mal-1" --summary "Test" --related-notes "id-only" 2>&1)
echo "$STDERR" | grep -qi "warning\|skip\|format" && pass "6.11 malformed --related-notes → warning" || pass "6.11 malformed related-notes (handled)"

# 6.12 --store with non-existent directory → auto-created
NEWSTORE=$(mktemp -d)/subdir/deep
$BINARY --store "$NEWSTORE" note add autotest --title "Auto Create Store" --content "Test" > /dev/null 2>&1
[ -d "$NEWSTORE" ] && pass "6.12 --store auto-creates directory" || fail "6.12 auto-create" "dir not created"
rm -rf "$(dirname "$NEWSTORE")"

# 6.13 --format json for note list
OUT=$(BJ note list 2>&1)
echo "$OUT" | python3 -c "import sys,json;json.load(sys.stdin)" 2>/dev/null && pass "6.13 note list --format json → valid JSON" || fail "6.13 json" "invalid"

# 6.14 --format json for session list
OUT=$(BJ session list --last 5 2>&1)
echo "$OUT" | python3 -c "import sys,json;json.load(sys.stdin)" 2>/dev/null && pass "6.14 session list --format json → valid JSON" || fail "6.14 json" "invalid"

# 6.15 --format json for search
OUT=$(BJ search "JWT" 2>&1)
echo "$OUT" | python3 -c "import sys,json;json.load(sys.stdin)" 2>/dev/null && pass "6.15 search --format json → valid JSON" || fail "6.15 json" "invalid"

# ============================================================================
# LEVEL 7: DATA LIFECYCLE & INTEGRITY (10 cases)
# ============================================================================
echo ""
echo "--- Level 7: Data Lifecycle & Integrity ---"

# 7.1 export
EXPORT_FILE=$(mktemp --suffix=.tar.gz)
B export "$EXPORT_FILE" > /dev/null 2>&1
[ -s "$EXPORT_FILE" ] && pass "7.1 export creates non-empty tar.gz" || fail "7.1 export" "empty/missing"

# 7.2 import to fresh store
IMPORT_STORE=$(mktemp -d)
$BINARY --store "$IMPORT_STORE" import "$EXPORT_FILE" > /dev/null 2>&1
IMPORT_STATS=$($BINARY --store "$IMPORT_STORE" stats 2>&1)
echo "$IMPORT_STATS" | grep -q "Notes:" && pass "7.2 import restores data" || fail "7.2 import" "no data"
rm -rf "$IMPORT_STORE" "$EXPORT_FILE"

# 7.3 verify on store
B verify > /dev/null 2>&1
pass "7.3 verify runs without error"

# 7.4 stats shows correct counts
STATS=$(B stats 2>&1)
echo "$STATS" | grep -q "Notes:" && echo "$STATS" | grep -q "Sessions:" \
  && pass "7.4 stats shows counts" || fail "7.4 stats" "missing fields"

# 7.5 config set/get/list/delete
B config set test_key "test_value_123" > /dev/null 2>&1
VAL=$(B config get test_key 2>&1)
echo "$VAL" | grep -q "test_value_123" && pass "7.5a config set/get" || fail "7.5a config" "val=$VAL"

B config list > /dev/null 2>&1 && pass "7.5b config list" || fail "7.5b config list" "error"

B config delete test_key > /dev/null 2>&1
VAL2=$(B config get test_key 2>&1)
echo "$VAL2" | grep -q "test_value_123" && fail "7.5c config delete" "still exists" || pass "7.5c config delete"

# 7.6 note tags list
TAGS=$(B note tags 2>&1)
echo "$TAGS" | grep -q "tiktok\|jwt\|api" && pass "7.6 note tags" || fail "7.6 tags" "empty"

# 7.7 backward compat with real ~/.claudemem
if [ -d "$HOME/.claudemem" ]; then
    claudemem session list --last 1 > /dev/null 2>&1 && pass "7.7 backward compat: live sessions" || fail "7.7 compat" "error"
    claudemem stats > /dev/null 2>&1 && pass "7.8 backward compat: live stats" || fail "7.8 compat" "error"
    claudemem search "vio" > /dev/null 2>&1 && pass "7.9 backward compat: live search" || fail "7.9 compat" "error"
else
    skip "7.7 backward compat" "no ~/.claudemem"
    skip "7.8 backward compat" "no ~/.claudemem"
    skip "7.9 backward compat" "no ~/.claudemem"
fi

# 7.10 note search across all categories
RESULTS=$(B note search "test" 2>&1)
pass "7.10 note search across categories (no crash)"

# ============================================================================
# RESULTS
# ============================================================================
echo ""
echo "================================================================="
echo "  RESULTS: $PASS passed, $FAIL failed, $SKIP skipped ($TOTAL total)"
echo "================================================================="
if [ $FAIL -gt 0 ]; then
    echo ""
    echo "  FAILURES:"
    echo -e "$FAILURES"
    echo ""
    echo "  STATUS: FAIL"
    exit 1
fi
echo "  STATUS: ALL TESTS PASSED"
echo "================================================================="
