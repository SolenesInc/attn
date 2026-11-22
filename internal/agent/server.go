package agent

import "github.com/victorarias/attn/internal/harness/appserver"

type ServerDeclarer interface {
	Server() appserver.Declaration
}

func DeclaredServer(d Driver) (appserver.Declaration, bool) {
	declarer, ok := d.(ServerDeclarer)
	if !ok {
		return appserver.Declaration{}, false
	}
	return declarer.Server(), true
}
