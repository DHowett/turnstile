package turnstile

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

var fakeRequest = &http.Request{RemoteAddr: "decoy"}

type CountedTurnstileHandler int

func (c *CountedTurnstileHandler) Reject(h http.Handler, w http.ResponseWriter, r *http.Request) {
	*c = *c + 1
}

type CountedHTTPHandler int

func (c *CountedHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	*c = *c + 1
}

func TestAPI(t *testing.T) {
	th := Allow(10)
	if th.count != 10 {
		t.Error("Count on new throttling instance is not 10")
	}
	th2 := th.Per(10 * time.Second)
	if th2.per != 10*time.Second {
		t.Error("Duration on second throttling instance is not 10s")
	}

	th3 := th2.Allow(30)
	if th2.count != 10 {
		t.Error("instance mutated by setter.")
	}

	_ = th3
}

func TestThrottling(t *testing.T) {
	t.Parallel()
	var then CountedTurnstileHandler

	th := Allow(1).Then(&then)

	th.ServeHTTP(nil, fakeRequest)
	if then != 0 {
		t.Error("'Then' was called after a single hit.")
	}

	th.ServeHTTP(nil, fakeRequest)

	if then != 1 {
		t.Error("'Then' was not called.")
	}
}

func TestHandlerCall(t *testing.T) {
	t.Parallel()
	var httpHandler CountedHTTPHandler

	th := Allow(1).To(&httpHandler)

	th.ServeHTTP(nil, fakeRequest)
	if httpHandler < 1 {
		t.Error("'original handler' was not called.")
	}
}

func TestPass(t *testing.T) {
	t.Parallel()
	var httpHandler CountedHTTPHandler

	th := Allow(1).To(&httpHandler).Then(Pass)

	th.ServeHTTP(nil, fakeRequest)
	if httpHandler < 1 {
		t.Error("'original handler' was not called.")
	}

	th.ServeHTTP(nil, fakeRequest)
	if httpHandler < 2 {
		t.Error("'original handler' was not called after Pass")
	}
}

func TestTimedDecay(t *testing.T) {
	t.Parallel()
	var then CountedTurnstileHandler
	var httpHandler CountedHTTPHandler

	th := Allow(1).Per(1 * time.Second).To(&httpHandler).Then(&then)

	th.ServeHTTP(nil, fakeRequest)
	if then > 0 {
		t.Error("'Then' was called after a single hit.")
	}

	th.ServeHTTP(nil, fakeRequest)

	if then < 1 {
		t.Error("'Then' was not called.")
	}

	time.Sleep(2 * time.Second)
	th.ServeHTTP(nil, fakeRequest)

	if then > 1 {
		t.Error("'Then' was called despite timeout period.")
	}
}

func TestFollowing(t *testing.T) {
	t.Parallel()
	var then CountedTurnstileHandler

	th := Allow(1)
	follower := th.Follower().Then(&then)

	th.ServeHTTP(nil, fakeRequest)
	if then > 0 {
		t.Error("'Then' was called on followee?!")
	}

	follower.ServeHTTP(nil, fakeRequest)
	if then < 1 {
		t.Error("'Then' function was not called by follower.")
	}
}

func TestExtendBan(t *testing.T) {
	t.Parallel()
	var then CountedTurnstileHandler
	th := Allow(2).Per(1*time.Second).Then(&then, ExtendBan(5*time.Second))

	th.ServeHTTP(nil, fakeRequest)
	th.ServeHTTP(nil, fakeRequest)
	th.ServeHTTP(nil, fakeRequest)
	if then < 1 {
		t.Error("'Then' function was not called.")
	}

	time.Sleep(2 * time.Second)

	th.ServeHTTP(nil, fakeRequest)
	if then < 2 {
		t.Error(then, "Request was accepted before 5 seconds passed!")
	}

	time.Sleep(6 * time.Second)

	th.ServeHTTP(nil, fakeRequest)
	if then > 2 {
		t.Error("Request was rejected after 5 seconds passed!")
	}
}

func TestUnlimited(t *testing.T) {
	t.Parallel()
	var then CountedTurnstileHandler
	th := Allow(Unlimited).Per(1*time.Second).Then(&then, ExtendBan(5*time.Second))

	th.ServeHTTP(nil, fakeRequest)
	th.ServeHTTP(nil, fakeRequest)
	th.ServeHTTP(nil, fakeRequest)
	if then > 0 {
		t.Error("unlimited turnstile triggered?")
	}

	th.ServeHTTP(nil, fakeRequest)
	if then > 0 {
		t.Error("unlimited turnstile triggered?")
	}

	th.ServeHTTP(nil, fakeRequest)
	if then > 0 {
		t.Error("unlimited turnstile triggered?")
	}
}

func TestToOnly(t *testing.T) {
	t.Parallel()
	var httpHandler CountedHTTPHandler
	th := To(&httpHandler)

	th.ServeHTTP(nil, fakeRequest)
	th.ServeHTTP(nil, fakeRequest)
	if httpHandler < 2 {
		t.Error("didn't call through to handler-only turnstile handler.")
	}
}

func TestPerToOnly(t *testing.T) {
	t.Parallel()
	var httpHandler CountedHTTPHandler
	th := Per(1 * time.Second).To(&httpHandler)

	th.ServeHTTP(nil, fakeRequest)
	th.ServeHTTP(nil, fakeRequest)
	if httpHandler < 2 {
		t.Error("didn't call through to handler-only turnstile handler.")
	}
}

/*
func TestExtendBanWithoutTimeComponent(t *testing.T) {
	var then CountedTurnstileHandler
	th := Allow(2).Then(&then, ExtendBan(5*time.Second))

	th.ServeHTTP(nil, fakeRequest)
	th.ServeHTTP(nil, fakeRequest)
	th.ServeHTTP(nil, fakeRequest)
	if then < 1 {
		t.Error("'Then' function was not called.")
	}

	time.Sleep(2 * time.Second)

	th.ServeHTTP(nil, fakeRequest)
	if then < 2 {
		t.Error(then, "Request was accepted before 5 seconds passed!")
	}

	time.Sleep(6 * time.Second)

	th.ServeHTTP(nil, fakeRequest)
	if then > 2 {
		t.Error("Request was rejected after 5 seconds passed!")
	}
	t.Log(th, th.state)
}
*/

func BenchmarkServeHTTP(b *testing.B) {
	th := Allow(1).Per(1 * time.Second)
	for i := 0; i < b.N; i++ {
		th.ServeHTTP(nil, fakeRequest)
	}
}

func Example() {
	request := &http.Request{RemoteAddr: "decoy"}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("handler called.")
	})
	emitLog := TurnstileFunc(func(h http.Handler, w http.ResponseWriter, r *http.Request) {
		fmt.Println("turnstile closed!")
	})

	ts := Allow(2).To(handler).Then(emitLog)

	ts.ServeHTTP(nil, request)
	ts.ServeHTTP(nil, request)
	ts.ServeHTTP(nil, request)
	ts.ServeHTTP(nil, request)

	// Output: handler called.
	// handler called.
	// turnstile closed!
	// turnstile closed!
}
