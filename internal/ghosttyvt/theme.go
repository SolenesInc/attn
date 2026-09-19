package ghosttyvt

type ColorTheme struct {
	Foreground     uint32
	Background     uint32
	Cursor         uint32
	ANSIPalette    [16]uint32
	HasForeground  bool
	HasBackground  bool
	HasCursor      bool
	HasANSIPalette bool
}
