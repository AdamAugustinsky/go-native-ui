package secret_test

import (
	"errors"
	"log"
	"time"

	"gonativeui/app/secret"
	"gonativeui/task"
)

// Keeping a token in the system's credential store: the Keychain on macOS,
// Credential Manager on Windows, the Secret Service on Linux. Every call can
// block (the user may be asked to unlock the Keychain), so call it from a
// task worker, never from the UI thread and never per frame.
func Example() {
	store := secret.Open("dev.example.chat") // the app ID from app.AppConfig
	const account = "alice@example.com"

	if err := store.Set(account, []byte("s3cret-token")); err != nil { // add or replace
		log.Print(err)
	}
	tok, err := store.Get(account)
	switch {
	case errors.Is(err, secret.ErrNotFound): // never set: ask the user to sign in
	case err != nil:
		log.Print(err)
	default:
		_ = tok // the bytes are the caller's
	}
	store.Delete(account) // on sign-out
}

// The calls belong on a worker: a task.Pool runs the lookup and the frame
// polls the answer.
func ExampleStore_Get() {
	type lookup struct{ Found bool } // a result holds no pointers
	pool := task.NewPool(task.Options{}, func(t task.Token, account string) lookup {
		_, err := secret.Open("dev.example.chat").Get(account)
		return lookup{Found: err == nil}
	})
	defer pool.Close(time.Second)
	pool.Submit("alice@example.com", 0) // in an event handler; Poll each frame
}
