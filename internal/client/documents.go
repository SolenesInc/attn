package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) DocDefine(schema protocol.DocumentCollectionSchema) (*protocol.DocDefineResult, error) {
	resp, err := c.send(protocol.DocDefineMessage{Cmd: protocol.CmdDocDefine, Schema: schema})
	if err != nil {
		return nil, err
	}
	return resp.DocDefineResult, nil
}

func (c *Client) DocUndefine(namespace, collection string) (*protocol.DocUndefineResult, error) {
	resp, err := c.send(protocol.DocUndefineMessage{
		Cmd: protocol.CmdDocUndefine, Namespace: namespace, Collection: collection,
	})
	if err != nil {
		return nil, err
	}
	return resp.DocUndefineResult, nil
}

func (c *Client) DocCollections() (*protocol.DocCollectionsResult, error) {
	resp, err := c.send(protocol.DocCollectionsMessage{Cmd: protocol.CmdDocCollections})
	if err != nil {
		return nil, err
	}
	return resp.DocCollectionsResult, nil
}

func (c *Client) DocPut(namespace, collection, id, body string, expectedRev *int) (*protocol.DocPutResult, error) {
	resp, err := c.send(protocol.DocPutMessage{
		Cmd: protocol.CmdDocPut, Namespace: namespace, Collection: collection, ID: id, Body: body,
		ExpectedRev: expectedRev,
	})
	if err != nil {
		return nil, err
	}
	return resp.DocPutResult, nil
}

func (c *Client) DocGet(namespace, collection, id string) (*protocol.DocGetResult, error) {
	resp, err := c.send(protocol.DocGetMessage{
		Cmd: protocol.CmdDocGet, Namespace: namespace, Collection: collection, ID: id,
	})
	if err != nil {
		return nil, err
	}
	return resp.DocGetResult, nil
}

func (c *Client) DocDelete(namespace, collection, id string, expectedRev *int) (*protocol.DocDeleteResult, error) {
	resp, err := c.send(protocol.DocDeleteMessage{
		Cmd: protocol.CmdDocDelete, Namespace: namespace, Collection: collection, ID: id,
		ExpectedRev: expectedRev,
	})
	if err != nil {
		return nil, err
	}
	return resp.DocDeleteResult, nil
}

func (c *Client) DocQuery(query protocol.DocumentQuery) (*protocol.DocQueryResult, error) {
	resp, err := c.send(protocol.DocQueryMessage{Cmd: protocol.CmdDocQuery, Query: query})
	if err != nil {
		return nil, err
	}
	return resp.DocQueryResult, nil
}

func (c *Client) DocCount(query protocol.DocumentQuery) (*protocol.DocCountResult, error) {
	resp, err := c.send(protocol.DocCountMessage{Cmd: protocol.CmdDocCount, Query: query})
	if err != nil {
		return nil, err
	}
	return resp.DocCountResult, nil
}

type DocWindow struct {
	Delivery  int
	AsOfSeq   int64
	Documents []protocol.StoredDocument
	Changed   []string
}

func revisions(held []protocol.StoredDocument) []protocol.DocumentRevision {
	out := make([]protocol.DocumentRevision, 0, len(held))
	for _, doc := range held {
		out = append(out, protocol.DocumentRevision{ID: doc.ID, Rev: doc.Rev})
	}
	return out
}

type DocSubscriptionEnded struct {
	Code    string
	Message string

	// The one ending safe to retry. Not the same as an empty Code: resubscribing
	// after an unappliable delivery repeats it forever.
	lost bool
}

func (e *DocSubscriptionEnded) Error() string {
	if e.Code == "" {
		return "document subscription ended: " + e.Message
	}
	return "document subscription ended (" + e.Code + "): " + e.Message
}

func DocSubscriptionCode(err error) (string, bool) {
	var ended *DocSubscriptionEnded
	if errors.As(err, &ended) {
		return ended.Code, true
	}
	return "", false
}

func DocConnectionLost(err error) bool {
	var ended *DocSubscriptionEnded
	return errors.As(err, &ended) && ended.lost
}

// Ending is always an error — a stopped live query returning success is a watcher
// exiting 0 over a frozen list.
func (c *Client) DocSubscribe(query protocol.DocumentQuery, held []protocol.StoredDocument, onWindow func(DocWindow) bool) error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return explainConnectError(c.socketPath, err)
	}
	defer conn.Close()

	msg := protocol.DocSubscribeMessage{Cmd: protocol.CmdDocSubscribe, Query: query, Have: revisions(held)}
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return fmt.Errorf("send subscribe: %w", err)
	}

	cache := make(map[string]protocol.StoredDocument, len(held))
	for _, doc := range held {
		cache[doc.ID] = doc
	}
	decoder := json.NewDecoder(conn)
	for {
		var resp protocol.Response
		if err := decoder.Decode(&resp); err != nil {
			return &DocSubscriptionEnded{Message: "the daemon closed the connection", lost: true}
		}
		if !resp.Ok {
			return &DocSubscriptionEnded{
				Code:    protocol.Deref(resp.ErrorCode),
				Message: protocol.Deref(resp.Error),
			}
		}
		if resp.DocSubscribeResult == nil {
			continue
		}
		window, err := applyDocDelivery(cache, resp.DocSubscribeResult)
		if err != nil {
			return err
		}
		if !onWindow(window) {
			return nil
		}
	}
}

func applyDocDelivery(cache map[string]protocol.StoredDocument, result *protocol.DocSubscribeResult) (DocWindow, error) {
	window := DocWindow{
		Delivery:  result.Delivery,
		AsOfSeq:   int64(result.AsOfSeq),
		Documents: make([]protocol.StoredDocument, 0, len(result.Order)),
		Changed:   make([]string, 0, len(result.Upsert)),
	}
	arrived := make(map[string]protocol.StoredDocument, len(result.Upsert))
	for _, doc := range result.Upsert {
		arrived[doc.ID] = doc
		window.Changed = append(window.Changed, doc.ID)
	}
	next := make(map[string]protocol.StoredDocument, len(result.Order))
	for _, id := range result.Order {
		doc, ok := arrived[id]
		if !ok {
			doc, ok = cache[id]
		}
		if !ok {
			return DocWindow{}, &DocSubscriptionEnded{Message: fmt.Sprintf(
				"delivery %d ordered %q without sending its body, and it is not held; resubscribe without a resume token",
				result.Delivery, id)}
		}
		next[id] = doc
		window.Documents = append(window.Documents, doc)
	}
	clear(cache)
	for id, doc := range next {
		cache[id] = doc
	}
	return window, nil
}
