UPDATE desktops
SET ephemeral = 0
WHERE ephemeral != 0
  AND replace(substr(created_at, 1, 19), 'T', ' ') <= (
    SELECT applied_at
    FROM schema_migrations
    WHERE version = 1795722363489267
  );
