package eventual

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getlantern/grtrack"
	"github.com/stretchr/testify/require"
)

func TestSingle(t *testing.T) {
	const (
		timeUntilSet = 20 * time.Millisecond
		initialValue = "initial value"
		nextValue    = "next value"
	)

	goroutines := grtrack.Start()
	v := NewValue()
	go func() {
		time.Sleep(timeUntilSet)
		v.Set(initialValue)
	}()

	shortTimeoutCtx, cancel := context.WithTimeout(context.Background(), timeUntilSet/2)
	defer cancel()

	_, err := v.Get(shortTimeoutCtx)
	require.Error(t, err, "Get with short timeout should have timed out")

	result, err := v.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, initialValue, result)

	v.Set(nextValue)
	result, err = v.Get(DontWait)
	require.NoError(t, err, "Get with expired context should have succeeded")
	require.Equal(t, nextValue, result)

	goroutines.CheckAfter(t, 50*time.Millisecond)
}

func TestNoSet(t *testing.T) {
	goroutines := grtrack.Start()
	v := NewValue()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := v.Get(ctx)
	require.Error(t, err, "Get before Set should return error")

	goroutines.CheckAfter(t, 50*time.Millisecond)
}

func TestCancel(t *testing.T) {
	v := NewValue()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := v.Get(ctx)
	require.Error(t, err, "Get should respect context cancellation")
}

func TestConcurrent(t *testing.T) {
	const concurrency = 200

	goroutines := grtrack.Start()
	v := NewValue()

	var (
		sets               int32
		setGroup, getGroup sync.WaitGroup
	)

	go func() {
		setGroup.Add(1)
		// Do some concurrent setting to make sure that works.
		for i := 0; i < concurrency; i++ {
			go func() {
				// Wait for the wait group so all goroutines run at once.
				setGroup.Wait()
				v.Set("some value")
				atomic.AddInt32(&sets, 1)
			}()
		}
		setGroup.Done()
	}()

	failureChan := make(chan string, concurrency)
	for i := 0; i < concurrency; i++ {
		getGroup.Add(1)
		go func() {
			defer getGroup.Done()
			r, err := v.Get(context.Background())
			if err != nil {
				failureChan <- err.Error()
			} else if r != "some value" {
				failureChan <- fmt.Sprintf("wrong result: %s", r)
			}
		}()
	}
	getGroup.Wait()
	close(failureChan)

	failures := map[string]int{}
	for failure := range failureChan {
		failures[failure]++
	}
	for msg, count := range failures {
		t.Logf("%d failures with message '%s'", count, msg)
	}
	if len(failures) > 0 {
		t.FailNow()
	}

	goroutines.CheckAfter(t, 50*time.Millisecond)
	require.EqualValues(t, concurrency, atomic.LoadInt32(&sets))
}

func TestCond(t *testing.T) {
	const (
		totalWaiters  = 30
		numberTimeout = 10
		numberCancel  = 10
		waitTime      = 50 * time.Millisecond
		timeout       = waitTime / 2
	)
	var (
		c                = newCond()
		testFailures     = make(chan string, totalWaiters)
		currentlyWaiting int // protected by c.Locker
		broadcastTime    = time.Now().Add(waitTime)
	)

	// Note that our use of c.Locker implicitly tests whether the lock is properly returned.

	i := 0
	for ; i < numberTimeout; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			c.Lock()
			currentlyWaiting++
			if err := c.wait(ctx); err == nil {
				testFailures <- "should have timed out and received error"
			}
			c.Lock()
			currentlyWaiting--
			c.Unlock()
		}()
	}
	for ; i < numberTimeout+numberCancel; i++ {
		go func() {
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(timeout)
				cancel()
			}()

			c.Lock()
			currentlyWaiting++
			if err := c.wait(ctx); err == nil {
				testFailures <- "should have cancelled and received error"
			}
			c.Lock()
			currentlyWaiting--
			c.Unlock()
		}()
	}
	for ; i < totalWaiters; i++ {
		go func() {
			c.Lock()
			currentlyWaiting++
			if err := c.wait(context.Background()); err != nil {
				testFailures <- "should not have received error"
			}
			currentlyWaiting--
			c.Unlock()
		}()
	}

	time.Sleep(timeout / 2)
	c.Lock()
	require.Equal(t, totalWaiters, currentlyWaiting)
	c.Unlock()

	time.Sleep(timeout)
	c.Lock()
	require.Equal(t, totalWaiters-numberTimeout-numberCancel, currentlyWaiting)
	c.Unlock()

	time.Sleep(time.Until(broadcastTime))
	c.Lock()
	c.broadcast()
	// We need to unlock before waiters can proceed.
	c.Unlock()

	time.Sleep(10 * time.Millisecond)
	c.Lock()
	require.Equal(t, 0, currentlyWaiting)
	c.Unlock()

	close(testFailures)
	failures := map[string]int{}
	for failure := range testFailures {
		failures[failure]++
	}
	for failure, count := range failures {
		t.Logf("%d failures with message '%s'", count, failure)
	}
	if len(failures) > 0 {
		t.FailNow()
	}

	// wait should return immediately now
	require.NoError(t, c.wait(context.Background()))
}

// Honestly, this test is only here because it allows us to reach 100% coverage =D
func TestWithDefault(t *testing.T) {
	v := WithDefault("default")
	r, err := v.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "default", r)

	v.Set("new")
	r, err = v.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "new", r)
}

func BenchmarkGet(b *testing.B) {
	v := NewValue()
	v.Set("foo")
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.Get(ctx)
	}
}
