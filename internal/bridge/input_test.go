package bridge_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/protocol"
	"golang.org/x/crypto/ssh"
)

func TestBridgeInputStreamsInOrderAndMatchesAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		var received []string
		var answers []func(error)
		opened := 0
		w.cfg.Handlers = bridge.Handlers{bridge.InputChannel: func(_ *bridge.Conn, incoming ssh.NewChannel) {
			opened++
			w.workers.Go(func() {
				bridge.ServeInput(incoming, func(data json.RawMessage, answer func(error)) {
					var write protocol.TerminalWriteMessage
					if err := json.Unmarshal(data, &write); err != nil {
						t.Error(err)
					}
					if write.TerminalID != "t-1" || write.Cmd != protocol.CmdTerminalWrite {
						t.Errorf("input request = %+v", write)
					}
					received = append(received, write.Data)
					answers = append(answers, answer)
				})
			})
		}}
		w.start()
		client, _ := bridged(t, w, nil)
		synctest.Wait()
		if opened != 0 {
			t.Fatal("input channel opened before a send")
		}
		inputs := []string{"a", "é", "\x03", strings.Repeat("paste", bridge.MaxCallRequest)}
		counts := make([]int, len(inputs))
		results := make([]error, len(inputs))
		var answeredOrder []int
		for i, data := range inputs {
			client.Send(protocol.TerminalWriteMessage{Cmd: protocol.CmdTerminalWrite, TerminalID: "t-1", Data: data}, func(err error) {
				counts[i]++
				results[i] = err
				answeredOrder = append(answeredOrder, i)
			})
		}
		synctest.Wait()
		if opened != 1 || !reflect.DeepEqual(received, inputs) {
			t.Fatalf("opened %d channels, received %d inputs in send order: %v", opened, len(received), reflect.DeepEqual(received, inputs))
		}
		if len(answeredOrder) != 0 {
			t.Fatalf("answered before outpost answered: %v", answeredOrder)
		}
		for i := len(answers) - 1; i >= 0; i-- {
			var err error
			if i == 1 {
				err = errors.New("terminal t-1 is not here")
			}
			answers[i](err)
		}
		synctest.Wait()
		if !reflect.DeepEqual(answeredOrder, []int{3, 2, 1, 0}) {
			t.Fatalf("answers = %v", answeredOrder)
		}
		_ = client.Close()
		synctest.Wait()
		for i, count := range counts {
			if count != 1 {
				t.Errorf("input %d answered %d times", i, count)
			}
			if i == 1 {
				var refused *bridge.CallRejected
				if !errors.As(results[i], &refused) || refused.Reason != "terminal t-1 is not here" {
					t.Errorf("refused input: %v", results[i])
				}
			} else if results[i] != nil {
				t.Errorf("input %d: %v", i, results[i])
			}
		}
	})
}

func TestBridgeInputSendsWhileAnAnswerCallbackWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		received := make(chan string, 2)
		w.cfg.Handlers = bridge.Handlers{bridge.InputChannel: func(_ *bridge.Conn, incoming ssh.NewChannel) {
			w.workers.Go(func() {
				bridge.ServeInput(incoming, func(data json.RawMessage, answer func(error)) {
					var key string
					if err := json.Unmarshal(data, &key); err != nil {
						t.Error(err)
					}
					received <- key
					answer(nil)
				})
			})
		}}
		w.start()
		client, _ := bridged(t, w, nil)
		entered, release := make(chan struct{}), make(chan struct{})
		client.Send("a", func(err error) {
			if err != nil {
				t.Error(err)
			}
			close(entered)
			<-release
		})
		<-entered
		answered := make(chan error, 1)
		client.Send("b", func(err error) { answered <- err })
		if first, second := <-received, <-received; first != "a" || second != "b" {
			t.Fatalf("received %q, %q", first, second)
		}
		close(release)
		if err := <-answered; err != nil {
			t.Fatal(err)
		}
	})
}

func TestBridgeInputEndsEveryUnansweredSend(t *testing.T) {
	for _, phase := range []string{"opening", "writing", "answering"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := pair(t)
				var answers []func(error)
				w.cfg.Handlers = bridge.Handlers{bridge.InputChannel: func(_ *bridge.Conn, incoming ssh.NewChannel) {
					if phase == "opening" {
						return
					}
					if phase == "writing" {
						_, requests, err := incoming.Accept()
						if err != nil {
							t.Error(err)
							return
						}
						go ssh.DiscardRequests(requests)
						return
					}
					w.workers.Go(func() {
						bridge.ServeInput(incoming, func(_ json.RawMessage, answer func(error)) { answers = append(answers, answer) })
					})
				}}
				w.start()
				client, _ := bridged(t, w, nil)
				inputs := []string{"first", "second"}
				if phase == "writing" {
					inputs[0] = strings.Repeat("x", 4<<20)
				}
				counts := make([]int, len(inputs))
				for i, input := range inputs {
					client.Send(input, func(err error) {
						counts[i]++
						if !errors.Is(err, bridge.ErrUnanswered) {
							t.Errorf("input %d: %v", i, err)
						}
					})
				}
				synctest.Wait()
				if phase == "answering" && len(answers) != len(inputs) {
					t.Fatalf("received %d inputs, want %d", len(answers), len(inputs))
				}
				_ = client.Close()
				synctest.Wait()
				for _, answer := range answers {
					answer(nil)
				}
				synctest.Wait()
				for i, count := range counts {
					if count != 1 {
						t.Errorf("input %d answered %d times", i, count)
					}
				}
				ended := make(chan error, 1)
				client.Send("after end", func(err error) { ended <- err })
				if err := <-ended; !errors.Is(err, bridge.ErrUnanswered) {
					t.Fatalf("send after bridge ended: %v", err)
				}
			})
		})
	}
}
