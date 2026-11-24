DELETE FROM notifications
WHERE kind = 'task_failed'
  AND (
    id LIKE 'task_failed:garden_review_classify:%'
    OR source_id IN (SELECT id FROM jobs WHERE kind = 'garden_review_classify')
  );
DELETE FROM jobs WHERE kind = 'garden_review_classify';
DELETE FROM tasks WHERE kind = 'garden_review_classify';
