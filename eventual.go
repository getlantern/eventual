package eventual

import (
	"context"
	"sync"
)

// DontWait is an expired context for use in Value.Get. Using DontWait will cause a Value.Get call
// to return immediately. If the value has not been set, a context.Canceled error will be returned.
var DontWait context.Context

func init() {
	var cancel func()
	DontWait, cancel = context.WithCancel(context.Background())
	cancel()
}

// Value is an eventual value, meaning that callers wishing to access the value block until it is
// available.
type Value interface {
	// Set this Value.
	Set(interface{})

	// Get waits for the value to be set. If the context expires first, an error will be returned.
	//
	// This function will return immediately when called with an expired context. In this case, the
	// value will be returned only if it has already been set; otherwise the context error will be
	// returned. For convenience, see DontWait.
	Get(context.Context) (interface{}, error)
}

// NewValue creates a new value.
func NewValue() Value {
	return &value{&sync.Mutex{}, nil, false, nil}
}

// WithDefault creates a new value set to the input default.
func WithDefault(defaultValue interface{}) Value {
	v := NewValue()
	v.Set(defaultValue)
	return v
}

type value struct {
	sync.Locker
	v       interface{}
	set     bool
	waiters []chan interface{}
}

func (v *value) Set(i interface{}) {
	v.Lock()
	v.v = i
	if !v.set {
		// This is our first time setting, inform anyone who is waiting
		for _, waiter := range v.waiters {
			waiter <- i
		}
		v.set = true
	}
	v.Unlock()
}

func (v *value) Get(ctx context.Context) (interface{}, error) {
	v.Lock()
	if v.set {
		// Value already set, use existing
		_v := v.v
		v.Unlock()
		return _v, nil
	}

	// Value not yet set, wait
	waiter := make(chan interface{}, 1)
	v.waiters = append(v.waiters, waiter)
	v.Unlock()
	select {
	case _v := <-waiter:
		return _v, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
