package daemon

import "strings"

var machineSettingKeyPrefixes = []string{"code_review_setup_offer_", "new_session_destination_"}

const newSessionYoloPrefix = "new_session_yolo_"

func (d *Daemon) storedSettingKey(key string) string {
	self := d.settingsMachineID()
	if key == newSessionYoloPrefix+"local" {
		return newSessionYoloPrefix + self
	}
	for _, prefix := range machineSettingKeyPrefixes {
		if rest, ok := strings.CutPrefix(key, prefix+"local_"); ok {
			return prefix + self + "_" + rest
		}
	}
	return key
}

func (d *Daemon) wireSettingKey(key string) string {
	self := d.settingsMachineID()
	if key == newSessionYoloPrefix+self {
		return newSessionYoloPrefix + "local"
	}
	for _, prefix := range machineSettingKeyPrefixes {
		if rest, ok := strings.CutPrefix(key, prefix+self+"_"); ok {
			return prefix + "local_" + rest
		}
	}
	return key
}

func (d *Daemon) settingsMachineID() string {
	if status := d.enrollmentStatus(); status.IsOutpost() && status.HomeMachineID != "" {
		return status.HomeMachineID
	}
	return d.store.MachineID()
}
