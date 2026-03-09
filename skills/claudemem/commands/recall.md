---
description: Search persistent memory for relevant context about a topic
argument-hint: Topic or keyword to search for
---

Search claudemem for anything related to the given topic, showing cross-references between notes and sessions.

## Instructions

1. **If a topic/keyword was provided**, search for it:
```bash
# Search (hybrid FTS5 + semantic is automatic when enabled)
claudemem search "<topic>" --compact --format json --limit 10

# Fetch full content for relevant results
claudemem note get <id>
```

2. **If no topic provided**, show recent activity overview:
```bash
claudemem context inject --format json --limit 5
```

3. **Present results** in a clear, organized format:
   - Group by type: **Notes** (knowledge) and **Sessions** (work reports)
   - Show the most relevant content inline (not just titles)
   - For highly relevant results, read full content: `claudemem note get <id>` or `claudemem session get <id>`

4. **Show cross-references** when available:
   - If a **note** has `metadata.session_id` → mention which session it came from:
     `"From session: <session-title> (<date>)"`
   - If a **session** has `Related Notes` → list the notes extracted from it:
     `"Notes extracted: <note-title-1>, <note-title-2>"`
   - Offer to look up the linked session/notes for deeper context

5. **Offer to dive deeper** into any specific result if the user wants more detail.
