package script

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
)

func Serve(in io.Reader, out io.Writer) error {
	dec := json.NewDecoder(in)
	var first message
	if err := dec.Decode(&first); err != nil {
		return fmt.Errorf("read the job: %w", err)
	}
	if first.Job == nil {
		return fmt.Errorf("the first message is not a job")
	}
	enc := newEncoder(out)
	answers := make(chan Answer)
	closed := make(chan error, 1)
	ended := make(chan Output, 1)

	go func() {
		for {
			var m message
			if err := dec.Decode(&m); err != nil {
				closed <- err
				return
			}
			if m.Answer == nil {
				closed <- fmt.Errorf("a message after the job is not an answer")
				return
			}
			answers <- *m.Answer
		}
	}()
	go func() {
		runtime.LockOSThread()
		ended <- executeOnLockedThread(*first.Job, func(line []byte) Answer {
			_, _ = out.Write(line)
			return <-answers
		})
	}()

	select {
	case output := <-ended:
		return enc.Encode(message{Done: &output})
	case err := <-closed:
		return fmt.Errorf("the pipe closed before the script ended: %w", err)
	}
}
