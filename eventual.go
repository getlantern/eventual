// Package eventual provides values that eventually have a value.
package eventual

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// Forever indicates that Get should wait forever
	Forever = -1
)

// Value is an eventual value, meaning that callers wishing to access the value
// block until the value is available.
type Value interface {
	// Set sets this Value to the given val.
	Set(val interface{})

	// SetIfEmpty sets this Value to the given val if and only if this Value
	// hasn't yet been set and returns true if the new value was set.
	SetIfEmpty(val interface{}) bool

	// Get waits up to timeout for the value to be set and returns it, or returns
	// nil if it times out or Cancel() is called. valid will be false in latter
	// case. If timeout is 0, Get won't wait. If timeout is -1, Get will wait
	// forever.
	Get(timeout time.Duration) (ret interface{}, valid bool)

	// GetOrInit is like Get but if it hasn't obtained a value within initAfter,
	// it will run the given initializer function on a goroutine in order to
	// initialize the value.
	GetOrInit(timeout time.Duration, initAfter time.Duration, init func(Value)) (ret interface{}, valid bool)

	// Cancel cancels this value, signaling any waiting calls to Get() that no
	// value is coming. If no value was set before Cancel() was called, all future
	// calls to Get() will return nil, false. Subsequent calls to Set after Cancel
	// have no effect.
	Cancel()
}

// Getter is a functional interface for the Value.Get function
type Getter func(time.Duration) (interface{}, bool)

type value struct {
	state   atomic.Value
	waiters []chan interface{}
	mutex   sync.Mutex
}

type stateholder struct {
	val      interface{}
	set      bool
	canceled bool
}

// NewValue creates a new Value.
func NewValue() Value {
	result := &value{waiters: make([]chan interface{}, 0)}
	result.state.Store(&stateholder{})
	return result
}

// DefaultGetter builds a Getter that always returns the supplied value.
func DefaultGetter(val interface{}) Getter {
	return func(time.Duration) (interface{}, bool) {
		return val, true
	}
}

// DefaultUnsetGetter builds a Getter that always !ok.
func DefaultUnsetGetter() Getter {
	return func(time.Duration) (interface{}, bool) {
		return nil, false
	}
}

func (v *value) Set(val interface{}) {
	v.set(val, false)
}

func (v *value) SetIfEmpty(val interface{}) bool {
	return v.set(val, true)
}

func (v *value) set(val interface{}, onlyIfEmpty bool) bool {
	v.mutex.Lock()
	defer v.mutex.Unlock()

	state := v.getState()
	settable := !state.canceled && (!onlyIfEmpty || !state.set)
	if settable {
		v.setState(&stateholder{
			val:      val,
			set:      true,
			canceled: false,
		})

		if v.waiters != nil {
			// Notify anyone waiting for value
			for _, waiter := range v.waiters {
				waiter <- val
			}
			// Clear waiters
			v.waiters = nil
		}

		return true
	}

	return false
}

func (v *value) Cancel() {
	v.mutex.Lock()
	defer v.mutex.Unlock()

	state := v.getState()
	v.setState(&stateholder{
		val:      state.val,
		set:      state.set,
		canceled: true,
	})

	if v.waiters != nil {
		// Notify anyone waiting for value
		for _, waiter := range v.waiters {
			close(waiter)
		}
		// Clear waiters
		v.waiters = nil
	}
}

func (v *value) Get(timeout time.Duration) (ret interface{}, valid bool) {
	return v.GetOrInit(timeout, 0, nil)
}

func (v *value) GetOrInit(timeout time.Duration, initAfter time.Duration, init func(Value)) (ret interface{}, valid bool) {
	state := v.getState()

	// First check for existing value using atomic operations (for speed)
	if state.set {
		// Value found, use it
		return state.val, true
	} else if state.canceled {
		// Value was canceled, return false
		return nil, false
	}

	if timeout == 0 {
		// Don't wait
		return nil, false
	}

	// If we didn't find an existing value, try again but this time using locking
	v.mutex.Lock()
	state = v.getState()

	if state.set {
		// Value found, use it
		v.mutex.Unlock()
		return state.val, true
	} else if state.canceled {
		// Value was canceled, return false
		v.mutex.Unlock()
		return nil, false
	}

	if timeout == -1 {
		// Wait essentially forever
		timeout = time.Duration(math.MaxInt64)
	}

	// Value not found, register to be notified once value is set
	valCh := make(chan interface{}, 1)
	v.waiters = append(v.waiters, valCh)
	v.mutex.Unlock()

	var remainingTimeout time.Duration
	runInitializer := init != nil
	if runInitializer {
		// Since we'll run an initializer eventually, reduce the initial timeout to
		// initAfter and record the remaining timeout for after we've run the
		// initializer.
		remainingTimeout = timeout - initAfter
		timeout = initAfter
	}

	// Wait up to timeout for value to get set
	select {
	case v, ok := <-valCh:
		return v, ok
	case <-time.After(timeout):
		if runInitializer {
			// Run the initializer and then try again
			go init(v)
			select {
			case v, ok := <-valCh:
				return v, ok
			case <-time.After(remainingTimeout):
				return nil, false
			}
		}
	}

	// We'll never actually reach this, just need it to make compiler happy
	return nil, false
}

func (v *value) getState() *stateholder {
	state := v.state.Load()
	if state == nil {
		return nil
	}
	return state.(*stateholder)
}

func (v *value) setState(state *stateholder) {
	v.state.Store(state)
}
