ALTER TABLE desktops ADD COLUMN ephemeral INTEGER NOT NULL DEFAULT 0;

UPDATE desktops
SET ephemeral = 1
WHERE name != ''
  AND id NOT IN (
    profile_id || '/desktop_1',
    profile_id || '/desktop_2',
    profile_id || '/desktop_3',
    profile_id || '/desktop_4',
    profile_id || '/desktop_5',
    profile_id || '/desktop_6',
    profile_id || '/desktop_7',
    profile_id || '/desktop_8',
    profile_id || '/desktop_9'
  )
  AND (SELECT COUNT(*) FROM launch_desktops ld WHERE ld.desktop_id = desktops.id) = 1
  AND EXISTS (
    SELECT 1
    FROM launch_desktops ld
    WHERE ld.desktop_id = desktops.id
      AND (
        (ld.kind = 'automation' AND EXISTS (
          SELECT 1 FROM automation_definitions a
          WHERE CAST(a.id AS TEXT) = ld.item_id
            AND a.deleted_at = ''
            AND trim(a.name) = desktops.name
        ))
        OR (ld.kind = 'crew' AND EXISTS (
          SELECT 1 FROM crew_members c
          WHERE c.member_key = ld.item_id
            AND c.retired_at = ''
            AND trim(c.name) = desktops.name
        ))
      )
  );
