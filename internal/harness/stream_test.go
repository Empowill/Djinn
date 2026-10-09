package harness

import (
	"sync/atomic"
	"testing"
	"time"
)

// closeRecorder is a process input that tells whether it was closed.
type closeRecorder struct{ closed atomic.Bool }

func (c *closeRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (c *closeRecorder) Close() error                { c.closed.Store(true); return nil }

// After a result that left a message without one, a turn that only thinks (claude's thinking_tokens lines, thinking
// blocks shown or withheld) keeps the input open past the settle period: the turn is under way.
func TestSettleWaitsForAThinkingTurn(t *testing.T) {
	const settle = 50 * time.Millisecond
	result := `{"type":"result","subtype":"success","is_error":false}`
	for name, thought := range map[string]string{
		"none":     "",
		"progress": `{"type":"system","subtype":"thinking_tokens","estimated_tokens":50}`,
		"withheld": `{"type":"assistant","message":{"role":"assistant","content":[{"type":"redacted_thinking","data":"x"}]}}`,
		"shown":    `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Let me see."}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			stdin := &closeRecorder{}
			p := &process{stdin: stdin, lines: make(chan line), done: make(chan struct{})}
			w := &streamWorker{
				a: streamAgent{name: "claude", parser: claudeParser{}, settle: settle}, p: p,
				events: make(chan Event, 64), done: make(chan struct{}), pending: 2,
			}
			go w.read()
			go func() {
				for range w.events {
				}
			}()
			p.lines <- line{text: result} // The prompt's result; the message waits for a turn.
			if thought != "" {
				p.lines <- line{text: thought}
			}
			time.Sleep(4 * settle)
			if got := stdin.closed.Load(); got != (thought == "") {
				t.Errorf("input closed = %v after %v; want %v", got, 4*settle, thought == "")
			}
			p.lines <- line{text: result}
			close(p.lines)
			close(p.done)
			<-w.done
			if !stdin.closed.Load() {
				t.Error("input left open after the last result")
			}
			if res := w.Wait(); res.Err != nil {
				t.Errorf("error = %v; want none", res.Err)
			}
		})
	}
}
