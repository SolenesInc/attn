package who

import "github.com/victorarias/attn/internal/protocol"

type Requester struct {
	profileID string
	asking    protocol.SessionID
	actor     Actor
}

func RequestFromApp(profileID string) Requester {
	return Requester{profileID: profileID, actor: User()}
}
func (r Requester) ProfileID() string                         { return r.profileID }
func (r Requester) Actor() Actor                              { return r.actor }
func (r Requester) Party() (Party, bool)                      { return r.actor.Party() }
func (r Requester) AskingSession() (protocol.SessionID, bool) { return r.asking, r.asking != "" }
