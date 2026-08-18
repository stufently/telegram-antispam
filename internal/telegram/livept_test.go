package telegram

import (
	"testing"
)

func TestBatchIDs(t *testing.T) {
	ids := make([]int, 250)
	for i := range ids {
		ids[i] = i + 1
	}
	batches := batchIDs(ids, 100)
	if len(batches) != 3 || len(batches[0]) != 100 || len(batches[2]) != 50 {
		t.Fatalf("batches: %d sizes %d/%d/%d", len(batches), len(batches[0]), len(batches[1]), len(batches[2]))
	}
}
