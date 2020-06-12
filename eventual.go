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
	return &value{nil, newCond()}
}

// WithDefault creates a new value set to the input default.
func WithDefault(defaultValue interface{}) Value {
	v := NewValue()
	v.Set(defaultValue)
	return v
}

type value struct {
	v     interface{}
	isSet *cond
}

func (v *value) Set(i interface{}) {
	v.isSet.Lock()
	v.v = i
	v.isSet.broadcast()
	v.isSet.Unlock()
}

func (v *value) Get(ctx context.Context) (interface{}, error) {
	v.isSet.Lock()
	for v.v == nil {
		// cond.wait releases the lock. The lock is returned iff err == nil.
		if err := v.isSet.wait(ctx); err != nil {
			return nil, err
		}
	}
	_v := v.v
	v.isSet.Unlock()
	return _v, nil
}

// cond is like sync.Cond, but accepts a context when waiting.
type cond struct {
	sync.Locker
	signaled bool
	waiters  []chan struct{}
}

func newCond() *cond {
	return &cond{&sync.Mutex{}, false, []chan struct{}{}}
}

// wait for the signal. c.Locker MUST be held when this function is called.
//
// In general, wait behaves like sync.Cond.Wait and returns nil. However, if the context expires
// before the condition is signaled, then an error is returned and the calling goroutine will NOT
// hold c.Locker.
func (c *cond) wait(ctx context.Context) error {
	if c.signaled {
		return nil
	}
	waitChan := make(chan struct{})
	c.waiters = append(c.waiters, waitChan)
	c.Unlock()
	select {
	case <-waitChan:
		c.Lock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// broadcast behaves like sync.Cond.Broadcast, with the exception that c.Locker MUST be held when
// this function is called. The lock will still be held when this function returns.
func (c *cond) broadcast() {
	c.signaled = true
	for _, waitChan := range c.waiters {
		close(waitChan)
	}
	c.waiters = []chan struct{}{}
}
