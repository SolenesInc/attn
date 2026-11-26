Reverts `3bf6e1112`, removing the desktop-rename pencil from the desktop sidebar's `AUTOMATIONS` section header.

Putting a per-desktop rename pencil in the `AUTOMATIONS` section header truncated `AUTOMATIONS` to `AUTOMATIO...`, read like renaming the section itself, and left any saved name invisible in the sidebar while runs stayed grouped. Permanent or custom-named launch desktops continue to be configured from the automation's Launch desktop setting (or by moving/sharing a desktop in `DESKTOPS`).
