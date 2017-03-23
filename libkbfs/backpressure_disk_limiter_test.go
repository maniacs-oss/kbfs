// Copyright 2017 Keybase Inc. All rights reserved.
// Use of this source code is governed by a BSD
// license that can be found in the LICENSE file.

package libkbfs

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/keybase/client/go/logger"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
)

// TestBackpressureTrackerCounters checks that the tracker's counters
// are updated properly for each public method.
func TestBackpressureTrackerCounters(t *testing.T) {
	bt, err := newBackpressureTracker(0.1, 0.9, 0.25, 100, 200)
	require.NoError(t, err)

	// semaphoreMax = min(k(U+F), L) = min(0.25(0+200), 100) = 50.
	require.Equal(t, int64(0), bt.used)
	require.Equal(t, int64(200), bt.free)
	require.Equal(t, int64(50), bt.semaphoreMax)
	require.Equal(t, int64(50), bt.semaphore.Count())

	// Increase U by 10, so that increases sM by 0.25*10 = 2.5, so
	// sM is now 52.

	avail := bt.onEnable(10)
	require.Equal(t, int64(42), avail)

	require.Equal(t, int64(10), bt.used)
	require.Equal(t, int64(200), bt.free)
	require.Equal(t, int64(52), bt.semaphoreMax)
	require.Equal(t, int64(42), bt.semaphore.Count())

	// Decrease U by 9, so that decreases sM by 0.25*9 = 2.25, so
	// sM is back to 50.

	bt.onDisable(9)

	require.Equal(t, int64(1), bt.used)
	require.Equal(t, int64(200), bt.free)
	require.Equal(t, int64(50), bt.semaphoreMax)
	require.Equal(t, int64(49), bt.semaphore.Count())

	// Increase U by 440, so that increases sM by 0.25*110 = 110,
	// so sM maxes out at 100, and semaphore should go negative.

	avail = bt.onEnable(440)
	require.Equal(t, int64(-341), avail)

	require.Equal(t, int64(441), bt.used)
	require.Equal(t, int64(200), bt.free)
	require.Equal(t, int64(100), bt.semaphoreMax)
	require.Equal(t, int64(-341), bt.semaphore.Count())

	// Now revert that increase.

	bt.onDisable(440)

	require.Equal(t, int64(1), bt.used)
	require.Equal(t, int64(200), bt.free)
	require.Equal(t, int64(50), bt.semaphoreMax)
	require.Equal(t, int64(49), bt.semaphore.Count())

	// This should be a no-op.
	avail = bt.onEnable(0)
	require.Equal(t, int64(49), avail)

	require.Equal(t, int64(1), bt.used)
	require.Equal(t, int64(200), bt.free)
	require.Equal(t, int64(50), bt.semaphoreMax)
	require.Equal(t, int64(49), bt.semaphore.Count())

	// So should this.
	bt.onDisable(0)

	require.Equal(t, int64(1), bt.used)
	require.Equal(t, int64(200), bt.free)
	require.Equal(t, int64(50), bt.semaphoreMax)
	require.Equal(t, int64(49), bt.semaphore.Count())

	// Add more free resources and put a block successfully.

	bt.updateFree(400)

	avail, err = bt.beforeBlockPut(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, int64(89), avail)

	require.Equal(t, int64(1), bt.used)
	require.Equal(t, int64(400), bt.free)
	require.Equal(t, int64(100), bt.semaphoreMax)
	require.Equal(t, int64(89), bt.semaphore.Count())

	bt.afterBlockPut(10, true)

	require.Equal(t, int64(11), bt.used)
	require.Equal(t, int64(400), bt.free)
	require.Equal(t, int64(100), bt.semaphoreMax)
	require.Equal(t, int64(89), bt.semaphore.Count())

	// Then try to put a block but fail it.

	avail, err = bt.beforeBlockPut(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, int64(80), avail)

	require.Equal(t, int64(11), bt.used)
	require.Equal(t, int64(400), bt.free)
	require.Equal(t, int64(100), bt.semaphoreMax)
	require.Equal(t, int64(80), bt.semaphore.Count())

	bt.afterBlockPut(9, false)

	require.Equal(t, int64(11), bt.used)
	require.Equal(t, int64(400), bt.free)
	require.Equal(t, int64(100), bt.semaphoreMax)
	require.Equal(t, int64(89), bt.semaphore.Count())

	// Finally, delete a block.

	bt.onBlocksDelete(11)

	require.Equal(t, int64(0), bt.used)
	require.Equal(t, int64(400), bt.free)
	require.Equal(t, int64(100), bt.semaphoreMax)
	require.Equal(t, int64(100), bt.semaphore.Count())

	// This should be a no-op.
	bt.onBlocksDelete(0)

	require.Equal(t, int64(0), bt.used)
	require.Equal(t, int64(400), bt.free)
	require.Equal(t, int64(100), bt.semaphoreMax)
	require.Equal(t, int64(100), bt.semaphore.Count())
}

// TestQuotaBackpressureTrackerCounters checks that the quota tracker's
// counters are updated properly for each public method.
func TestQuotaBackpressureTrackerCounters(t *testing.T) {
	qbt, err := newQuotaBackpressureTracker(0.1, 0.9)
	require.NoError(t, err)

	require.Equal(t, int64(0), qbt.unflushedBytes)
	require.Equal(t, int64(0), qbt.remoteUsedBytes)
	require.Equal(t, int64(math.MaxInt64), qbt.quotaBytes)

	qbt.onJournalEnable(10)
	require.Equal(t, int64(10), qbt.unflushedBytes)
	require.Equal(t, int64(0), qbt.remoteUsedBytes)
	require.Equal(t, int64(math.MaxInt64), qbt.quotaBytes)

	qbt.onJournalDisable(9)
	require.Equal(t, int64(1), qbt.unflushedBytes)
	require.Equal(t, int64(0), qbt.remoteUsedBytes)
	require.Equal(t, int64(math.MaxInt64), qbt.quotaBytes)

	// Add more free resources and put a block successfully.

	qbt.updateRemote(10, 100)

	require.Equal(t, int64(1), qbt.unflushedBytes)
	require.Equal(t, int64(10), qbt.remoteUsedBytes)
	require.Equal(t, int64(100), qbt.quotaBytes)

	qbt.afterBlockPut(10, true)

	require.Equal(t, int64(11), qbt.unflushedBytes)
	require.Equal(t, int64(10), qbt.remoteUsedBytes)
	require.Equal(t, int64(100), qbt.quotaBytes)

	// Then try to put a block but fail it.

	qbt.afterBlockPut(9, false)

	require.Equal(t, int64(11), qbt.unflushedBytes)
	require.Equal(t, int64(10), qbt.remoteUsedBytes)
	require.Equal(t, int64(100), qbt.quotaBytes)

	// Finally, flush a block.

	qbt.onBlocksFlush(10)

	require.Equal(t, int64(1), qbt.unflushedBytes)
	require.Equal(t, int64(10), qbt.remoteUsedBytes)
	require.Equal(t, int64(100), qbt.quotaBytes)
}

// TestDefaultDoDelayCancel checks that defaultDoDelay respects
// context cancellation.
func TestDefaultDoDelayCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(
		context.Background(), individualTestTimeout)
	cancel()

	err := defaultDoDelay(ctx, individualTestTimeout)
	require.Equal(t, ctx.Err(), errors.Cause(err))
}

func makeTestBackpressureDiskLimiterParams() backpressureDiskLimiterParams {
	return backpressureDiskLimiterParams{
		minThreshold:      0.1,
		maxThreshold:      0.9,
		quotaMinThreshold: 0.8,
		quotaMaxThreshold: 1.2,
		journalFrac:       0.25,
		diskCacheFrac:     0.1,
		byteLimit:         400,
		fileLimit:         40,
		maxDelay:          8 * time.Second,
		delayFn: func(context.Context, time.Duration) error {
			return nil
		},
		freeBytesAndFilesFn: func() (int64, int64, error) {
			return math.MaxInt64, math.MaxInt64, nil
		},
		quotaFn: func(context.Context) (int64, int64) {
			return 0, math.MaxInt64
		},
	}
}

func TestBackpressureConstructorError(t *testing.T) {
	log := logger.NewTestLogger(t)
	fakeErr := errors.New("Fake error")
	params := makeTestBackpressureDiskLimiterParams()
	params.delayFn = nil
	params.freeBytesAndFilesFn = func() (int64, int64, error) {
		return 0, 0, fakeErr
	}
	_, err := newBackpressureDiskLimiter(log, params)
	require.Equal(t, fakeErr, err)
}

// TestBackpressureDiskLimiterBeforeBlockPut checks that
// backpressureDiskLimiter.beforeBlockPut keeps track of and returns
// the available bytes/files correctly.
func TestBackpressureDiskLimiterBeforeBlockPut(t *testing.T) {
	log := logger.NewTestLogger(t)
	params := makeTestBackpressureDiskLimiterParams()
	params.byteLimit = 88
	params.fileLimit = 20
	bdl, err := newBackpressureDiskLimiter(log, params)
	require.NoError(t, err)

	availBytes, availFiles, err := bdl.beforeBlockPut(
		context.Background(), 10, 2)
	require.NoError(t, err)
	// (byteLimit=88) * (journalFrac=0.25) - 10 = 12.
	require.Equal(t, int64(12), availBytes)
	// (fileLimit=20) * (journalFrac=0.25) - 2 = 3.
	require.Equal(t, int64(3), availFiles)
}

// TestBackpressureDiskLimiterBeforeBlockPutByteError checks that
// backpressureDiskLimiter.beforeBlockPut handles errors correctly
// when getting the byte semaphore; in particular, that we return the
// right info even with a non-nil error.
func TestBackpressureDiskLimiterBeforeBlockPutByteError(t *testing.T) {
	log := logger.NewTestLogger(t)
	params := makeTestBackpressureDiskLimiterParams()
	params.byteLimit = 40
	params.fileLimit = 4
	bdl, err := newBackpressureDiskLimiter(log, params)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	availBytes, availFiles, err := bdl.beforeBlockPut(ctx, 11, 1)
	require.Equal(t, ctx.Err(), errors.Cause(err))
	require.Equal(t, int64(10), availBytes)
	require.Equal(t, int64(1), availFiles)

	require.Equal(t, int64(10), bdl.journalTrackers.byte.semaphore.Count())
	require.Equal(t, int64(1), bdl.journalTrackers.file.semaphore.Count())
}

// TestBackpressureDiskLimiterBeforeBlockPutFileError checks that
// backpressureDiskLimiter.beforeBlockPut handles errors correctly
// when acquiring the file semaphore; in particular, that we don't
// leak either bytes or files if either semaphore times out.
func TestBackpressureDiskLimiterBeforeBlockPutFileError(t *testing.T) {
	log := logger.NewTestLogger(t)
	params := makeTestBackpressureDiskLimiterParams()
	params.byteLimit = 40
	params.fileLimit = 4
	bdl, err := newBackpressureDiskLimiter(log, params)
	require.NoError(t, err)

	// We're relying on the fact that a semaphore acquire will
	// succeed if it is immediately fulfillable, so that the byte
	// acquire will succeed.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	availBytes, availFiles, err := bdl.beforeBlockPut(ctx, 10, 2)
	require.Equal(t, ctx.Err(), errors.Cause(err))
	require.Equal(t, int64(10), availBytes)
	require.Equal(t, int64(1), availFiles)

	require.Equal(t, int64(10), bdl.journalTrackers.byte.semaphore.Count())
	require.Equal(t, int64(1), bdl.journalTrackers.file.semaphore.Count())
}

// TestBackpressureDiskLimiterGetDelay tests the delay calculation.
func TestBackpressureDiskLimiterGetDelay(t *testing.T) {
	log := logger.NewTestLogger(t)
	params := makeTestBackpressureDiskLimiterParams()
	params.byteLimit = math.MaxInt64
	params.fileLimit = math.MaxInt64
	bdl, err := newBackpressureDiskLimiter(log, params)
	require.NoError(t, err)

	now := time.Now()

	func() {
		bdl.lock.Lock()
		defer bdl.lock.Unlock()
		// byteDelayScale should be 25/(.25(350 + 25)) =
		// 0.267, which turns into a delay fraction of
		// (0.267-0.1)/(0.9-0.1) = 0.209.
		bdl.journalTrackers.byte.used = 25
		bdl.journalTrackers.byte.free = 350
		// fileDelayScale should be 50/(.25(350 + 50)) = 0.5,
		// which turns into a delay fraction of
		// (0.5-0.1)/(0.9-0.1) = 0.5.
		bdl.journalTrackers.file.used = 50
		bdl.journalTrackers.file.free = 350
		// quotaDelayScale should be (80+10)/100 = 0.9, which
		// turns into a delay fraction of (0.9-0.8)/(1.2-0.8)
		// = 0.25.
		bdl.journalTrackers.quota.unflushedBytes = 80
		bdl.journalTrackers.quota.remoteUsedBytes = 10
		bdl.journalTrackers.quota.quotaBytes = 100
	}()

	ctx := context.Background()
	delay := bdl.getDelayLocked(ctx, now)
	require.InEpsilon(t, float64(4), delay.Seconds(), 0.01)

	func() {
		bdl.lock.Lock()
		defer bdl.lock.Unlock()
		// Swap byte and file delay fractions.
		bdl.journalTrackers.byte.used = 50
		bdl.journalTrackers.byte.free = 350

		bdl.journalTrackers.file.used = 25
		bdl.journalTrackers.file.free = 350
	}()

	delay = bdl.getDelayLocked(ctx, now)
	require.InEpsilon(t, float64(4), delay.Seconds(), 0.01)

	func() {
		bdl.lock.Lock()
		defer bdl.lock.Unlock()
		// Reduce byte and delay fractions.
		bdl.journalTrackers.byte.used = 25
		bdl.journalTrackers.byte.free = 350

		bdl.journalTrackers.file.used = 25
		bdl.journalTrackers.file.free = 350

		// quotaDelayScale should be (80+20)/100 = 1.0, which
		// turns into a delay fraction of (0.9-0.8)/(1.2-0.8)
		// = 0.5.
		bdl.journalTrackers.quota.unflushedBytes = 80
		bdl.journalTrackers.quota.remoteUsedBytes = 20
		bdl.journalTrackers.quota.quotaBytes = 100
	}()

	delay = bdl.getDelayLocked(ctx, now)
	require.InEpsilon(t, float64(4), delay.Seconds(), 0.01)
}

// TestBackpressureDiskLimiterGetDelayWithDeadline makes sure the
// delay calculation takes into account the context deadline.
func TestBackpressureDiskLimiterGetDelayWithDeadline(t *testing.T) {
	log := logger.NewTestLogger(t)
	params := makeTestBackpressureDiskLimiterParams()
	params.byteLimit = math.MaxInt64
	params.fileLimit = math.MaxInt64
	bdl, err := newBackpressureDiskLimiter(log, params)
	require.NoError(t, err)

	now := time.Now()

	func() {
		bdl.lock.Lock()
		defer bdl.lock.Unlock()
		// fileDelayScale should be 50/(.25(350 + 50)) = 0.5,
		// which turns into a delay fraction of
		// (0.5-0.1)/(0.9-0.1) = 0.5.
		bdl.journalTrackers.file.used = 50
		bdl.journalTrackers.file.free = 350
	}()

	deadline := now.Add(5 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	delay := bdl.getDelayLocked(ctx, now)
	require.InEpsilon(t, float64(2), delay.Seconds(), 0.01)
}

type backpressureTestType int

const (
	byteTest backpressureTestType = iota
	fileTest
)

func (t backpressureTestType) String() string {
	switch t {
	case byteTest:
		return "byteTest"
	case fileTest:
		return "fileTest"
	default:
		return fmt.Sprintf("backpressureTestType(%d)", t)
	}
}

// testBackpressureDiskLimiterLargeDiskDelay checks the delays when
// pretending to have a large disk.
func testBackpressureDiskLimiterLargeDiskDelay(
	t *testing.T, testType backpressureTestType) {
	var lastDelay time.Duration
	delayFn := func(ctx context.Context, delay time.Duration) error {
		lastDelay = delay
		return nil
	}

	const blockBytes = 100
	const blockFiles = 10

	// Set the bottleneck, based on the test type; i.e. set
	// parameters so that semaphoreMax for the bottleneck always
	// has value 10 * blockX when called in beforeBlockPut, and
	// every block put beyond the min threshold leads to an
	// increase in timeout of 1 second up to the max.
	var byteLimit, fileLimit int64
	switch testType {
	case byteTest:
		// Make bytes be the bottleneck.
		byteLimit = 10 * blockBytes
		fileLimit = 20 * blockFiles
	case fileTest:
		// Make files be the bottleneck.
		byteLimit = 20 * blockBytes
		fileLimit = 10 * blockFiles
	default:
		panic(fmt.Sprintf("unknown test type %s", testType))
	}

	log := logger.NewTestLogger(t)
	params := makeTestBackpressureDiskLimiterParams()
	params.byteLimit = byteLimit * 4
	params.fileLimit = fileLimit * 4
	params.delayFn = delayFn
	bdl, err := newBackpressureDiskLimiter(log, params)
	require.NoError(t, err)

	byteSnapshot, fileSnapshot := bdl.getByteFileSnapshotsForTest()
	require.Equal(t, bdlSnapshot{
		used:  0,
		free:  math.MaxInt64,
		max:   byteLimit,
		count: byteLimit,
	}, byteSnapshot)
	require.Equal(t, bdlSnapshot{
		used:  0,
		free:  math.MaxInt64,
		max:   fileLimit,
		count: fileLimit,
	}, fileSnapshot)

	ctx := context.Background()

	var bytesPut, filesPut int64

	checkCountersAfterBeforeBlockPut := func(
		i int, availBytes, availFiles int64) {
		byteSnapshot, fileSnapshot := bdl.getByteFileSnapshotsForTest()
		expectedByteCount := byteLimit - bytesPut - blockBytes
		expectedFileCount := fileLimit - filesPut - blockFiles
		require.Equal(t, expectedByteCount, availBytes)
		require.Equal(t, expectedFileCount, availFiles)
		require.Equal(t, bdlSnapshot{
			used:  bytesPut,
			free:  math.MaxInt64,
			max:   byteLimit,
			count: expectedByteCount,
		}, byteSnapshot, "i=%d", i)
		require.Equal(t, bdlSnapshot{
			used:  filesPut,
			free:  math.MaxInt64,
			max:   fileLimit,
			count: expectedFileCount,
		}, fileSnapshot, "i=%d", i)
	}

	checkCountersAfterBlockPut := func(i int) {
		byteSnapshot, fileSnapshot := bdl.getByteFileSnapshotsForTest()
		require.Equal(t, bdlSnapshot{
			used:  bytesPut,
			free:  math.MaxInt64,
			max:   byteLimit,
			count: byteLimit - bytesPut,
		}, byteSnapshot, "i=%d", i)
		require.Equal(t, bdlSnapshot{
			used:  filesPut,
			free:  math.MaxInt64,
			max:   fileLimit,
			count: fileLimit - filesPut,
		}, fileSnapshot, "i=%d", i)
	}

	// The first two puts shouldn't encounter any backpressure...

	for i := 0; i < 2; i++ {
		availBytes, availFiles, err :=
			bdl.beforeBlockPut(ctx, blockBytes, blockFiles)
		require.NoError(t, err)
		require.Equal(t, 0*time.Second, lastDelay)
		checkCountersAfterBeforeBlockPut(i, availBytes, availFiles)

		bdl.afterBlockPut(ctx, blockBytes, blockFiles, true)
		bytesPut += blockBytes
		filesPut += blockFiles
		checkCountersAfterBlockPut(i)
	}

	// ...but the next eight should encounter increasing
	// backpressure...

	for i := 1; i < 9; i++ {
		availBytes, availFiles, err :=
			bdl.beforeBlockPut(ctx, blockBytes, blockFiles)
		require.NoError(t, err)
		require.InEpsilon(t, float64(i), lastDelay.Seconds(),
			0.01, "i=%d", i)
		checkCountersAfterBeforeBlockPut(i, availBytes, availFiles)

		bdl.afterBlockPut(ctx, blockBytes, blockFiles, true)
		bytesPut += blockBytes
		filesPut += blockFiles
		checkCountersAfterBlockPut(i)
	}

	// ...and the last one should stall completely, if not for the
	// cancelled context.

	ctx2, cancel2 := context.WithCancel(ctx)
	cancel2()
	availBytes, availFiles, err := bdl.beforeBlockPut(
		ctx2, blockBytes, blockFiles)
	require.Equal(t, ctx2.Err(), errors.Cause(err))
	require.Equal(t, 8*time.Second, lastDelay)

	// This does the same thing as checkCountersAfterBlockPut(),
	// but only by coincidence; contrast with similar block in
	// TestBackpressureDiskLimiterSmallDisk below.
	expectedByteCount := byteLimit - bytesPut
	expectedFileCount := fileLimit - filesPut
	require.Equal(t, expectedByteCount, availBytes)
	require.Equal(t, expectedFileCount, availFiles)
	byteSnapshot, fileSnapshot = bdl.getByteFileSnapshotsForTest()
	require.Equal(t, bdlSnapshot{
		used:  bytesPut,
		free:  math.MaxInt64,
		max:   byteLimit,
		count: expectedByteCount,
	}, byteSnapshot)
	require.Equal(t, bdlSnapshot{
		used:  filesPut,
		free:  math.MaxInt64,
		max:   fileLimit,
		count: expectedFileCount,
	}, fileSnapshot)
}

func TestBackpressureDiskLimiterLargeDiskDelay(t *testing.T) {
	t.Run(byteTest.String(), func(t *testing.T) {
		testBackpressureDiskLimiterLargeDiskDelay(t, byteTest)
	})
	t.Run(fileTest.String(), func(t *testing.T) {
		testBackpressureDiskLimiterLargeDiskDelay(t, fileTest)
	})
}

// TestBackpressureDiskLimiterJournalAndDiskCache checks that the limiter
// correctly handles the interaction between changes to the disk cache and the
// journal.
func TestBackpressureDiskLimiterJournalAndDiskCache(t *testing.T) {
	t.Parallel()
	var lastDelay time.Duration
	delayFn := func(ctx context.Context, delay time.Duration) error {
		lastDelay = delay
		return nil
	}

	const blockBytes int64 = 100
	// Big number, but no risk of overflow
	maxFreeBytes := int64(1 << 30)

	// Set the bottleneck; i.e. set parameters so that semaphoreMax for the
	// bottleneck always has value 10 * blockBytes when called in
	// beforeBlockPut, and every block put beyond the min threshold leads to an
	// increase in timeout of 1 second up to the max.
	byteLimit := 10 * blockBytes
	// arbitrarily large number
	var fileLimit int64 = math.MaxInt64

	log := logger.NewTestLogger(t)
	params := makeTestBackpressureDiskLimiterParams()
	// 4 = 1/(journalFrac=0.25)
	params.byteLimit = byteLimit * 4
	params.fileLimit = fileLimit
	params.delayFn = delayFn
	params.freeBytesAndFilesFn = func() (int64, int64, error) {
		return maxFreeBytes, math.MaxInt64, nil
	}
	bdl, err := newBackpressureDiskLimiter(log, params)
	require.NoError(t, err)

	byteSnapshot, _ := bdl.getByteFileSnapshotsForTest()
	require.Equal(t, bdlSnapshot{
		used:  0,
		free:  maxFreeBytes,
		max:   byteLimit,
		count: byteLimit,
	}, byteSnapshot)

	ctx := context.Background()

	var journalBytesPut int64
	var diskCacheBytesPut int64

	checkCountersAfterBeforeBlockPut := func(
		i int, availBytes int64) {
		byteSnapshot, _ := bdl.getByteFileSnapshotsForTest()
		expectedByteCount := byteLimit - journalBytesPut - blockBytes
		require.Equal(t, expectedByteCount, availBytes)
		require.Equal(t, bdlSnapshot{
			used:  journalBytesPut,
			free:  maxFreeBytes + diskCacheBytesPut,
			max:   byteLimit,
			count: expectedByteCount,
		}, byteSnapshot, "i=%d", i)
	}

	checkCountersAfterBlockPut := func(i int) {
		byteSnapshot, _ := bdl.getByteFileSnapshotsForTest()
		require.Equal(t, bdlSnapshot{
			used:  journalBytesPut,
			free:  maxFreeBytes + diskCacheBytesPut,
			max:   byteLimit,
			count: byteLimit - journalBytesPut,
		}, byteSnapshot, "i=%d", i)
	}

	// The first two puts shouldn't encounter any backpressure...

	for i := 0; i < 2; i++ {
		// Ensure the disk block cache doesn't interfere with the journal
		// limits.
		_, err := bdl.beforeDiskBlockCachePut(ctx, blockBytes)
		require.NoError(t, err)
		bdl.afterDiskBlockCachePut(ctx, blockBytes, true)
		diskCacheBytesPut += blockBytes

		availBytes, _, err :=
			bdl.beforeBlockPut(ctx, blockBytes, 1)
		require.NoError(t, err)
		require.Equal(t, 0*time.Second, lastDelay)
		checkCountersAfterBeforeBlockPut(i, availBytes)

		bdl.afterBlockPut(ctx, blockBytes, 1, true)
		journalBytesPut += blockBytes
		checkCountersAfterBlockPut(i)

		// TODO: track disk cache puts as well
	}

	// ...but the next eight should encounter increasing
	// backpressure...

	for i := 1; i < 9; i++ {
		// Ensure the disk block cache doesn't interfere with the journal
		// limits.
		_, err := bdl.beforeDiskBlockCachePut(ctx, blockBytes)
		require.NoError(t, err)
		bdl.afterDiskBlockCachePut(ctx, blockBytes, true)
		diskCacheBytesPut += blockBytes

		availBytes, _, err :=
			bdl.beforeBlockPut(ctx, blockBytes, 1)
		require.NoError(t, err)
		require.InEpsilon(t, float64(i), lastDelay.Seconds(),
			0.01, "i=%d", i)
		checkCountersAfterBeforeBlockPut(i, availBytes)

		bdl.afterBlockPut(ctx, blockBytes, 1, true)
		journalBytesPut += blockBytes
		checkCountersAfterBlockPut(i)
	}

	// ...and the last one should stall completely, if not for the
	// cancelled context.

	ctx2, cancel2 := context.WithCancel(ctx)
	cancel2()
	availBytes, _, err := bdl.beforeBlockPut(
		ctx2, blockBytes, 1)
	require.Equal(t, ctx2.Err(), errors.Cause(err))
	require.Equal(t, 8*time.Second, lastDelay)

	// This does the same thing as checkCountersAfterBlockPut(),
	// but only by coincidence; contrast with similar block in
	// TestBackpressureDiskLimiterSmallDisk below.
	expectedByteCount := byteLimit - journalBytesPut
	require.Equal(t, expectedByteCount, availBytes)
	byteSnapshot, _ = bdl.getByteFileSnapshotsForTest()
	require.Equal(t, bdlSnapshot{
		used:  journalBytesPut,
		free:  maxFreeBytes + diskCacheBytesPut,
		max:   byteLimit,
		count: expectedByteCount,
	}, byteSnapshot)
}

// TestBackpressureDiskLimiterSmallDiskDelay checks the delays when
// pretending to have a small disk.
func testBackpressureDiskLimiterSmallDiskDelay(
	t *testing.T, testType backpressureTestType) {
	var lastDelay time.Duration
	delayFn := func(ctx context.Context, delay time.Duration) error {
		lastDelay = delay
		return nil
	}

	const blockBytes = 80
	const blockFiles = 8

	// Set the bottleneck, based on the test type; i.e. set
	// parameters so that semaphoreMax for the bottleneck always
	// has value 10 * blockX when called in beforeBlockPut, and
	// every block put beyond the min threshold leads to an
	// increase in timeout of 1 second up to the max.
	var diskBytes, diskFiles int64
	// Multiply by 4 to compensate for the 0.25 limitFrac.
	switch testType {
	case byteTest:
		// Make bytes be the bottleneck.
		diskBytes = 40 * blockBytes
		diskFiles = 400 * blockFiles
	case fileTest:
		// Make files be the bottleneck.
		diskBytes = 400 * blockBytes
		diskFiles = 40 * blockFiles
	default:
		panic(fmt.Sprintf("unknown test type %s", testType))
	}

	var bdl *backpressureDiskLimiter

	getFreeBytesAndFilesFn := func() (int64, int64, error) {
		// When called for the first time from the
		// constructor, bdl will be nil.
		if bdl == nil {
			return diskBytes, diskFiles, nil
		}

		// When called in subsequent times from
		// beforeBlockPut, simulate the journal taking up
		// space.
		return diskBytes - bdl.journalTrackers.byte.used,
			diskFiles - bdl.journalTrackers.file.used, nil
	}

	log := logger.NewTestLogger(t)
	params := makeTestBackpressureDiskLimiterParams()
	params.byteLimit = math.MaxInt64
	params.fileLimit = math.MaxInt64
	params.delayFn = delayFn
	params.freeBytesAndFilesFn = getFreeBytesAndFilesFn
	bdl, err := newBackpressureDiskLimiter(log, params)
	require.NoError(t, err)

	byteSnapshot, fileSnapshot := bdl.getByteFileSnapshotsForTest()
	require.Equal(t, bdlSnapshot{
		used:  0,
		free:  diskBytes,
		max:   diskBytes / 4,
		count: diskBytes / 4,
	}, byteSnapshot)
	require.Equal(t, bdlSnapshot{
		used:  0,
		free:  diskFiles,
		max:   diskFiles / 4,
		count: diskFiles / 4,
	}, fileSnapshot)

	ctx := context.Background()

	var bytesPut, filesPut int64

	checkCountersAfterBeforeBlockPut := func(
		i int, availBytes, availFiles int64) {
		expectedByteCount := diskBytes/4 - bytesPut - blockBytes
		expectedFileCount := diskFiles/4 - filesPut - blockFiles
		require.Equal(t, expectedByteCount, availBytes)
		require.Equal(t, expectedFileCount, availFiles)
		byteSnapshot, fileSnapshot := bdl.getByteFileSnapshotsForTest()
		require.Equal(t, bdlSnapshot{
			used:  bytesPut,
			free:  diskBytes - bytesPut,
			max:   diskBytes / 4,
			count: expectedByteCount,
		}, byteSnapshot, "i=%d", i)
		require.Equal(t, bdlSnapshot{
			used:  filesPut,
			free:  diskFiles - filesPut,
			max:   diskFiles / 4,
			count: expectedFileCount,
		}, fileSnapshot, "i=%d", i)
	}

	checkCountersAfterBlockPut := func(i int) {
		// freeBytes is only updated on beforeBlockPut, so we
		// have to compensate for that.
		byteSnapshot, fileSnapshot := bdl.getByteFileSnapshotsForTest()
		require.Equal(t, bdlSnapshot{
			used:  bytesPut,
			free:  diskBytes - bytesPut + blockBytes,
			max:   diskBytes/4 + blockBytes/4,
			count: diskBytes/4 + blockBytes/4 - bytesPut,
		}, byteSnapshot, "i=%d", i)
		require.Equal(t, bdlSnapshot{
			used:  filesPut,
			free:  diskFiles - filesPut + blockFiles,
			max:   diskFiles/4 + blockFiles/4,
			count: diskFiles/4 + blockFiles/4 - filesPut,
		}, fileSnapshot, "i=%d", i)
	}

	// The first two puts shouldn't encounter any backpressure...

	for i := 0; i < 2; i++ {
		availBytes, availFiles, err :=
			bdl.beforeBlockPut(ctx, blockBytes, blockFiles)
		require.NoError(t, err)
		require.Equal(t, 0*time.Second, lastDelay)
		checkCountersAfterBeforeBlockPut(i, availBytes, availFiles)

		bdl.afterBlockPut(ctx, blockBytes, blockFiles, true)
		bytesPut += blockBytes
		filesPut += blockFiles
		checkCountersAfterBlockPut(i)
	}

	// ...but the next eight should encounter increasing
	// backpressure...

	for i := 1; i < 9; i++ {
		availBytes, availFiles, err :=
			bdl.beforeBlockPut(ctx, blockBytes, blockFiles)
		require.NoError(t, err)
		require.InEpsilon(t, float64(i), lastDelay.Seconds(),
			0.01, "i=%d", i)
		checkCountersAfterBeforeBlockPut(i, availBytes, availFiles)

		bdl.afterBlockPut(ctx, blockBytes, blockFiles, true)
		bytesPut += blockBytes
		filesPut += blockFiles
		checkCountersAfterBlockPut(i)
	}

	// ...and the last one should stall completely, if not for the
	// cancelled context.

	ctx2, cancel2 := context.WithCancel(ctx)
	cancel2()
	availBytes, availFiles, err :=
		bdl.beforeBlockPut(ctx2, blockBytes, blockFiles)
	require.Equal(t, ctx2.Err(), errors.Cause(err))
	require.Equal(t, 8*time.Second, lastDelay)

	expectedByteCount := diskBytes/4 - bytesPut
	expectedFileCount := diskFiles/4 - filesPut
	require.Equal(t, expectedByteCount, availBytes)
	require.Equal(t, expectedFileCount, availFiles)
	byteSnapshot, fileSnapshot = bdl.getByteFileSnapshotsForTest()
	require.Equal(t, bdlSnapshot{
		used:  bytesPut,
		free:  diskBytes - bytesPut,
		max:   diskBytes / 4,
		count: expectedByteCount,
	}, byteSnapshot)
	require.Equal(t, bdlSnapshot{
		used:  filesPut,
		free:  diskFiles - filesPut,
		max:   diskFiles / 4,
		count: expectedFileCount,
	}, fileSnapshot)
}

func TestBackpressureDiskLimiterSmallDiskDelay(t *testing.T) {
	t.Run(byteTest.String(), func(t *testing.T) {
		testBackpressureDiskLimiterSmallDiskDelay(t, byteTest)
	})
	t.Run(fileTest.String(), func(t *testing.T) {
		testBackpressureDiskLimiterSmallDiskDelay(t, fileTest)
	})
}

// TestBackpressureDiskLimiterNearQuota checks the delays when
// pretending to near and over the quota limit.
func TestBackpressureDiskLimiterNearQuota(t *testing.T) {
	var lastDelay time.Duration
	delayFn := func(ctx context.Context, delay time.Duration) error {
		lastDelay = delay
		return nil
	}

	const blockBytes = 100
	const blockFiles = 10
	const remoteUsedBytes = 400
	const quotaBytes = 1000

	log := logger.NewTestLogger(t)
	params := makeTestBackpressureDiskLimiterParams()
	params.byteLimit = math.MaxInt64
	params.fileLimit = math.MaxInt64
	params.maxDelay = 4 * time.Second
	params.delayFn = delayFn
	params.quotaFn = func(_ context.Context) (int64, int64) {
		return remoteUsedBytes, quotaBytes
	}
	bdl, err := newBackpressureDiskLimiter(log, params)
	require.NoError(t, err)

	quotaSnapshot := bdl.getQuotaSnapshotForTest()
	require.Equal(t, bdlSnapshot{
		used: 0,
		free: math.MaxInt64,
	}, quotaSnapshot)

	ctx := context.Background()

	var bytesPut int64

	checkCounters := func(i int) {
		quotaSnapshot := bdl.getQuotaSnapshotForTest()
		used := remoteUsedBytes + bytesPut
		require.Equal(t, bdlSnapshot{
			used: used,
			free: quotaBytes - used,
		}, quotaSnapshot, "i=%d", i)
	}

	// The first five puts shouldn't encounter any backpressure...

	for i := 0; i < 5; i++ {
		_, _, err := bdl.beforeBlockPut(ctx, blockBytes, blockFiles)
		require.NoError(t, err)
		require.Equal(t, 0*time.Second, lastDelay, "i=%d", i)
		checkCounters(i)

		bdl.afterBlockPut(ctx, blockBytes, blockFiles, true)
		bytesPut += blockBytes
		checkCounters(i)
	}

	// ...but the next four should encounter increasing
	// backpressure...

	for i := 1; i <= 4; i++ {
		_, _, err := bdl.beforeBlockPut(ctx, blockBytes, blockFiles)
		require.NoError(t, err)
		require.InEpsilon(t, float64(i), lastDelay.Seconds(),
			0.01, "i=%d", i)
		checkCounters(i)

		bdl.afterBlockPut(ctx, blockBytes, blockFiles, true)
		bytesPut += blockBytes
		checkCounters(i)
	}

	// ...and the last one should encounter the max backpressure.

	_, _, err = bdl.beforeBlockPut(ctx, blockBytes, blockFiles)
	require.NoError(t, err)
	require.Equal(t, 4*time.Second, lastDelay)
	checkCounters(0)
}
