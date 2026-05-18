package waldrainer_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/waldrainer"
	"github.com/stretchr/testify/require"
)

func TestRunDrainsOnTicker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wal := &fakeWAL{
		onDrain: func() {
			cancel()
		},
	}

	waldrainer.Run(ctx, wal, time.Millisecond)

	require.GreaterOrEqual(t, wal.count.Load(), int64(1))
}

type fakeWAL struct {
	count   atomic.Int64
	onDrain func()
}

func (f *fakeWAL) Drain(ctx context.Context) (int, error) {
	f.count.Add(1)
	if f.onDrain != nil {
		f.onDrain()
	}
	return 1, nil
}
