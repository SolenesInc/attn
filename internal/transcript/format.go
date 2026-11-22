package transcript

type Format uint8

const (
	FormatNone Format = iota
	FormatClaude
	FormatCodex
)

func (f Format) SupportsUsage() bool {
	return f == FormatClaude || f == FormatCodex
}
