CREATE TABLE phone_pairings (
    device_id TEXT NOT NULL,
    profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    device_sign_pub BLOB NOT NULL,
    device_noise_pub BLOB NOT NULL,
    name TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    PRIMARY KEY (device_id, profile_id)
);
