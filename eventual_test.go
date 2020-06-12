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

// Honestly, this test is only here because it allows us to reach 100% coverage =D
func TestWithefault(t *testing.T) {
	const (
		timeUntilSet = 20 * time.Millisecond
		defaultValue = "default value"
		initialValue = "initial value"
	)

	goroutines := grtrack.Start()
	v := WithDefault(defaultValue)
	go func() {
		time.Sleep(timeUntilSet)
		v.Set(initialValue)
	}()

	shortTimeoutCtx, cancel := context.WithTimeout(context.Background(), timeUntilSet/2)
	defer cancel()

	result, err := v.Get(shortTimeoutCtx)
	require.NoError(t, err, "Get with short timeout should have gotten no error")
	require.Equal(t, defaultValue, result, "Get with short timeout should have gotten default value")

	result, err = v.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, initialValue, result)

	goroutines.CheckAfter(t, 50*time.Millisecond)
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
