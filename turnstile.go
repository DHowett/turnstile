// Package turnstile implements count- and time-based access control for HTTP requests.
package turnstile

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Unlimited represents an unlimited number of accesses to a Turnstile.
const Unlimited = uint(0)

// Ever represents all that ever was or will be. Turnstiles set to Per(Ever) will never re-open once closed.
const Ever = time.Duration(0)

// Remote is a convenience type representing a remote address.
type Remote string

// RemoteFunc represents a function that will return the remote address for an HTTP request.
type RemoteFunc func(*http.Request) Remote

type tsHandle struct {
	count uint
	timer *time.Timer
}

type tsState struct {
	remotesFromFunc RemoteFunc
	once            sync.Once
	remotes         map[Remote]*tsHandle
	rejections      map[Remote]struct{}
	mtx             sync.Mutex
	ch              chan Remote
}

func (th *tsState) init() {
	th.once.Do(func() {
		th.remotes = make(map[Remote]*tsHandle)
		th.rejections = make(map[Remote]struct{})
		th.ch = make(chan Remote)
		go th.reapRemotes()
	})
}

func (th *tsState) reapRemotes() {
	for r := range th.ch {
		th.mtx.Lock()
		delete(th.remotes, r)
		delete(th.rejections, r)
		th.mtx.Unlock()
	}
}

func (th *tsState) remoteFrom(r *http.Request) Remote {
	if th.remotesFromFunc != nil {
		return th.remotesFromFunc(r)
	}
	components := strings.Split(r.RemoteAddr, ":")
	return Remote(strings.Join(components[:len(components)-1], ":"))
}

func (th *tsState) expire(remote Remote, dur time.Duration) {
	tsh, ok := th.remotes[remote]
	if !ok {
		return
	}

	if tsh.timer != nil {
		tsh.timer.Reset(dur)
	} else {
		tsh.timer = time.AfterFunc(dur, func() {
			th.ch <- remote
			tsh.timer = nil
		})
	}

}

func (th *tsState) count(p *Turnstile, r *http.Request) {
	th.init()
	remote := th.remoteFrom(r)

	th.mtx.Lock()
	defer th.mtx.Unlock()

	tsh, _ := th.remotes[remote]

	if tsh == nil {
		tsh = &tsHandle{}
		th.remotes[remote] = tsh
	}

	tsh.count++

	if tsh.count > p.count {
		// This remote has hit its reject limit for this turnstile.
		th.rejections[remote] = struct{}{}
	}

	if p.per > Ever {
		th.expire(remote, p.per)
	}
}

func (th *tsState) allow(p *Turnstile, r *http.Request) bool {
	if p.count == Unlimited {
		return true
	}

	th.init()
	th.mtx.Lock()
	defer th.mtx.Unlock()

	_, rejected := th.rejections[th.remoteFrom(r)]
	return !rejected
}

// A TurnstileHandler manages what happens to a HTTP session after it has been rejected by a Turnstile.
type TurnstileHandler interface {
	Reject(http.Handler, http.ResponseWriter, *http.Request)
}

// TurnstileFunc is an adapter that allows a function to be used as a TurnstileHandler.
type TurnstileFunc func(http.Handler, http.ResponseWriter, *http.Request)

func (tf TurnstileFunc) Reject(h http.Handler, w http.ResponseWriter, r *http.Request) {
	tf(h, w, r)
}

type statefulTurnstileHandler struct {
	state *tsState
	f     func(*tsState, http.Handler, http.ResponseWriter, *http.Request)
}

func (sh *statefulTurnstileHandler) Reject(h http.Handler, w http.ResponseWriter, r *http.Request) {
	sh.f(sh.state, h, w, r)
}

// A Turnstile is an immutable structure that implements counted access control to HTTP handlers.
// It is, itself, a http.Handler.
type Turnstile struct {
	state     *tsState
	count     uint
	per       time.Duration
	h         http.Handler
	then      []TurnstileHandler
	following *Turnstile
}

func newTurnstile() *Turnstile {
	return &Turnstile{state: new(tsState)}
}

// Allow returns a new Turnstile that allows the specified number of accesses.
func Allow(count uint) *Turnstile {
	return newTurnstile().Allow(count)
}

// Per returns a new Turnstile whose accesses may span the provided time horizon.
func Per(per time.Duration) *Turnstile {
	return newTurnstile().Per(per)
}

// To returns a new Turnstile that forwards its requests to the provided http.Handler
func To(h http.Handler) *Turnstile {
	return newTurnstile().To(h)
}

// Allow returns a copy of this Turnstile, adjusted to allow the specified number of accesses.
func (p *Turnstile) Allow(count uint) *Turnstile {
	n := *p
	n.count = count
	return &n
}

// Per returns a copy of this Turnstile, adjusted to measure its accesses over the specified time horizon.
func (p *Turnstile) Per(per time.Duration) *Turnstile {
	n := *p
	n.per = per
	return &n
}

// To returns a copy of this Turnstile, with its forwarding handler set to the provided http.Handler
func (p *Turnstile) To(h http.Handler) *Turnstile {
	n := *p
	n.h = h
	return &n
}

// Then returns a copy of this Turnstile that will perform the provided actions after its limit is reached.
func (p *Turnstile) Then(then ...TurnstileHandler) *Turnstile {
	n := *p
	n.then = then
	return &n
}

// Follower returns a new Turnstile that makes its allowance decisions based on this Turnstile.
//
// The new Turnstile does not count accesses or provide a time horizon.
func (p *Turnstile) Follower() *Turnstile {
	return &Turnstile{
		state:     p.state,
		following: p,
	}
}

func (p *Turnstile) allow(r *http.Request) bool {
	v := true
	v = v && p.state.allow(p, r)
	if p.following != nil {
		v = v && p.following.allow(r)
	}
	return v
}

// ServeHTTP is provided for conformance with the http.Handler interface.
func (p *Turnstile) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.count != Unlimited {
		p.state.count(p, r)
	}

	if !p.allow(r) && len(p.then) > 0 {
		for _, th := range p.then {
			// Inject state if necessary
			if sth, ok := th.(*statefulTurnstileHandler); ok {
				sth.state = p.state
			}
			th.Reject(p.h, w, r)
		}
	} else {
		if p.h != nil {
			p.h.ServeHTTP(w, r)
		}
	}
}

func pass(h http.Handler, w http.ResponseWriter, r *http.Request) {
	if h != nil {
		h.ServeHTTP(w, r)
	}
}

func deny(h http.Handler, w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(420)
}

// ExtendBan returns a TurnstileHandler that will continue to reject connections from a given remote for the provided duration (instead of the Turnstile's native duration).
// A remote will be reconsidered for requests d duration after its *last* request.
func ExtendBan(d time.Duration) TurnstileHandler {
	return &statefulTurnstileHandler{
		f: func(state *tsState, h http.Handler, w http.ResponseWriter, r *http.Request) {
			state.mtx.Lock()
			state.expire(state.remoteFrom(r), d)
			state.mtx.Unlock()
		},
	}
}

// Pass is a TurnstileHandler that does not modify the HTTP session whatsoever.
var Pass TurnstileHandler = TurnstileFunc(pass)

// Deny is a TurnstileHandler that terminates the incoming HTTP session with status code 420 ("Enhance your Calm").
var Deny TurnstileHandler = TurnstileFunc(deny)
