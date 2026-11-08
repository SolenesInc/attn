DELETE FROM settings
WHERE key = 'chief_context_window_cap' OR key GLOB 'chief_model_*' OR key GLOB 'chief_effort_*';

CREATE TEMP TABLE chief_mail(address TEXT PRIMARY KEY);
INSERT OR IGNORE INTO chief_mail SELECT address FROM asks WHERE address GLOB 'chief:*';
INSERT OR IGNORE INTO chief_mail SELECT address FROM inbox_items WHERE address GLOB 'chief:*';
INSERT OR IGNORE INTO chief_mail SELECT address FROM inbox_delivery WHERE address GLOB 'chief:*';
INSERT OR IGNORE INTO chief_mail SELECT address FROM pull_request_watches WHERE address GLOB 'chief:*';
INSERT OR IGNORE INTO chief_mail SELECT 'chief:' || profile_id FROM quick_captures WHERE mailbox_kind = 'chief';

INSERT INTO crew_members(member_key, profile_id, name)
SELECT 'chief-' || substr(replace(id, 'profile-', ''), 1, 34), id, 'Chief'
FROM profiles
WHERE chief_member = '' AND deleted_at = '' AND 'chief:' || id IN (SELECT address FROM chief_mail);

UPDATE profiles SET chief_member = (
  SELECT member_key FROM crew_members WHERE crew_members.profile_id = profiles.id AND name = 'Chief'
)
WHERE chief_member = '' AND deleted_at = '' AND 'chief:' || id IN (SELECT address FROM chief_mail);

CREATE TEMP TABLE chief_address AS
SELECT 'chief:' || id AS legacy, 'member:' || chief_member AS member FROM profiles WHERE chief_member != '';

UPDATE asks SET address = (SELECT member FROM chief_address WHERE legacy = asks.address)
WHERE address IN (SELECT legacy FROM chief_address);
UPDATE OR IGNORE inbox_items SET address = (SELECT member FROM chief_address WHERE legacy = inbox_items.address)
WHERE address IN (SELECT legacy FROM chief_address);
UPDATE OR IGNORE inbox_delivery SET address = (SELECT member FROM chief_address WHERE legacy = inbox_delivery.address)
WHERE address IN (SELECT legacy FROM chief_address);
UPDATE OR IGNORE pull_request_watches SET address = (SELECT member FROM chief_address WHERE legacy = pull_request_watches.address)
WHERE address IN (SELECT legacy FROM chief_address);

UPDATE quick_captures SET mailbox_kind = 'crew_member', mailbox_member_id = (
  SELECT chief_member FROM profiles WHERE profiles.id = quick_captures.profile_id
)
WHERE mailbox_kind = 'chief' AND profile_id IN (SELECT id FROM profiles WHERE chief_member != '');

DELETE FROM asks WHERE address GLOB 'chief:*';
DELETE FROM inbox_items WHERE address GLOB 'chief:*';
DELETE FROM inbox_delivery WHERE address GLOB 'chief:*';
DELETE FROM pull_request_watches WHERE address GLOB 'chief:*';

DROP TABLE chief_mail;
DROP TABLE chief_address;
