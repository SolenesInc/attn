UPDATE quick_captures
SET submission = json_remove(submission, '$.sender_token')
WHERE json_type(submission, '$.sender_token') IS NOT NULL;
