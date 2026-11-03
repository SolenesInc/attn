CREATE TEMP TABLE notification_sessions AS
SELECT notifications.id AS notification_id, json_extract(action.value, '$.target_id') AS session_id
FROM notifications, json_each(nullif(notifications.actions_json, '')) AS action
WHERE notifications.kind = 'task_failed' AND notifications.profile_id = ''
  AND json_extract(action.value, '$.kind') = 'open_session';

UPDATE notifications SET profile_id = (
    SELECT s.profile_id FROM notification_sessions AS n JOIN sessions AS s ON s.id = n.session_id
    WHERE n.notification_id = notifications.id AND s.profile_id != ''
)
WHERE id IN (
    SELECT n.notification_id FROM notification_sessions AS n JOIN sessions AS s ON s.id = n.session_id
    WHERE s.profile_id != ''
);

DELETE FROM notifications
WHERE profile_id = '' AND (
    id IN (
        SELECT n.notification_id FROM notification_sessions AS n
        WHERE NOT EXISTS (SELECT 1 FROM sessions AS s WHERE s.id = n.session_id)
    )
    OR (kind = 'task_failed' AND source_id IN (SELECT id FROM jobs WHERE kind = 'garden_review_classify'))
);

DROP TABLE notification_sessions;
