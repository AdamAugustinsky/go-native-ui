package ws_test

import (
	"context"
	"time"

	"gonativeui/app/ws"
)

// Example keeps one socket to an agent: a reader goroutine reusing its
// buffer, and writes from another.
func Example() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := ws.Dial(ctx, "wss://agent.example/api/ws", ws.Options{
		Headers:    map[string]string{"Authorization": "Bearer token"},
		MaxMessage: 32 << 20,
	})
	if err != nil {
		return // errors.Is(err, errors.ErrUnsupported) until N2
	}
	defer c.Close(ws.CloseNormal, "")
	go func() {
		var buf []byte
		for {
			_, msg, err := c.ReadMessage(buf)
			if err != nil {
				return
			}
			buf = msg // reused by the next read: copy what outlives it
		}
	}()
	_ = c.WriteMessage(ws.Text, []byte(`{"type":"hello"}`))
}
