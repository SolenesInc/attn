package effectrpc

type ServerInfo struct {
	Server   string `json:"server"`
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}
