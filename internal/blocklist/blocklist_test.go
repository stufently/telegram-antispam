package blocklist

import (
	"sync"
	"testing"
)

func TestNewIsEmptyAndSafe(t *testing.T) {
	b := New()
	if b.Listed(5) {
		t.Fatal("Listed(5) on fresh store must be false")
	}
	if b.Len() != 0 {
		t.Fatalf("Len()=%d want 0 on fresh store", b.Len())
	}
}

func TestSwapAndListed(t *testing.T) {
	b := New()
	b.Swap(BuildSet([]int64{5, 9}))

	if !b.Listed(5) {
		t.Error("Listed(5)=false want true")
	}
	if b.Listed(6) {
		t.Error("Listed(6)=true want false")
	}
	if b.Listed(0) {
		t.Error("Listed(0)=true want false (zero userID always false)")
	}
	if b.Len() != 2 {
		t.Fatalf("Len()=%d want 2", b.Len())
	}
}

func TestSwapNilStoresEmptySet(t *testing.T) {
	b := New()
	b.Swap(nil)
	if b.Listed(5) {
		t.Fatal("Listed(5) after Swap(nil) must be false")
	}
	if b.Len() != 0 {
		t.Fatalf("Len()=%d want 0 after Swap(nil)", b.Len())
	}
}

// TestConcurrentListedAndSwap exercises Listed and Swap concurrently.
// The test itself has no assertions beyond "no panic" -- its purpose is to
// run clean under `go test -race`.
func TestConcurrentListedAndSwap(t *testing.T) {
	b := New()
	b.Swap(BuildSet([]int64{1, 2, 3}))

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			b.Listed(int64(i % 5))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			if i%2 == 0 {
				b.Swap(BuildSet([]int64{int64(i), int64(i + 1)}))
			} else {
				b.Swap(BuildSet([]int64{}))
			}
		}
	}()

	wg.Wait()
}
