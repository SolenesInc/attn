package daemon

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	testDocNS   = "app/approval-gate"
	testDocColl = "requests"
)

func docCall(t *testing.T, run func(net.Conn)) protocol.Response {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		run(server)
		_ = server.Close()
	}()
	defer client.Close()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func defineTestCollection(t *testing.T, d *Daemon) {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocDefine(c, &protocol.DocDefineMessage{
			Cmd: protocol.CmdDocDefine,
			Schema: protocol.DocumentCollectionSchema{
				Namespace:  testDocNS,
				Collection: testDocColl,
				Fields:     []protocol.DocumentFieldSpec{{Name: "status", Type: "string"}},
			},
		})
	})
	if !resp.Ok {
		t.Fatalf("define: %v", protocol.Deref(resp.Error))
	}
}

func putDoc(t *testing.T, d *Daemon, id, body string) {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: testDocNS, Collection: testDocColl, ID: id, Body: body,
		})
	})
	if !resp.Ok {
		t.Fatalf("put %s: %v", id, protocol.Deref(resp.Error))
	}
}

func deleteDoc(t *testing.T, d *Daemon, id string) bool {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocDelete(c, &protocol.DocDeleteMessage{
			Cmd: protocol.CmdDocDelete, Namespace: testDocNS, Collection: testDocColl, ID: id,
		})
	})
	if !resp.Ok {
		t.Fatalf("delete %s: %v", id, protocol.Deref(resp.Error))
	}
	return resp.DocDeleteResult.Existed
}

func testQuery() protocol.DocumentQuery {
	return protocol.DocumentQuery{Namespace: testDocNS, Collection: testDocColl}
}
