package events

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeqStartsAtOneWithoutGaps(t *testing.T) {
	b := New(10)
	require.EqualValues(t, 1, b.Publish("j", "a", nil).Seq)
	require.EqualValues(t, 2, b.Publish("j", "b", nil).Seq)
	require.EqualValues(t, 1, b.Publish("other", "a", nil).Seq, "seq is per job")
}

func TestSubscribeSnapshotThenLaterEvents(t *testing.T) {
	b := New(10)
	b.Publish("j", "a", nil)
	b.Publish("j", "b", nil)
	sub := b.Subscribe("j", func() any { return "state" })
	b.Publish("j", "c", nil)
	b.Close("j")
	var got []Event
	for ev := range sub.Events() {
		got = append(got, ev)
	}
	require.Len(t, got, 2)
	require.Equal(t, Event{Type: "job.snapshot", JobID: "j", Seq: 2, EmittedAt: got[0].EmittedAt, Data: "state"}, got[0])
	require.EqualValues(t, 3, got[1].Seq)
	require.Nil(t, sub.Err())

	late := b.Subscribe("j", func() any { return "final" })
	ev, ok := <-late.Events()
	require.True(t, ok)
	require.EqualValues(t, 3, ev.Seq)
	_, ok = <-late.Events()
	require.False(t, ok, "closed stream delivers only the snapshot")
}

func TestSlowConsumerClosed(t *testing.T) {
	b := New(3)
	sub := b.Subscribe("j", func() any { return nil })
	for i := 0; i < 10; i++ {
		b.Publish("j", "e", nil)
	}
	n := 0
	for range sub.Events() {
		n++
	}
	require.ErrorIs(t, sub.Err(), ErrSlowConsumer)
	require.Equal(t, 4, n, "snapshot plus buffer")
	// other subscribers are unaffected
	ok := b.Subscribe("j", func() any { return nil })
	b.Publish("j", "e", nil)
	require.Len(t, ok.Events(), 2)
	ok.Close()
	ok.Close()
}

func TestConcurrentSubscribeHasNoGapOrDuplicate(t *testing.T) {
	b := New(1000)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			b.Publish("j", "e", nil)
		}
		b.Close("j")
	}()
	for k := 0; k < 20; k++ {
		sub := b.Subscribe("j", func() any { return nil })
		last := int64(-1)
		for ev := range sub.Events() {
			if last >= 0 {
				require.Equal(t, last+1, ev.Seq)
			}
			last = ev.Seq
		}
		require.EqualValues(t, 500, last)
	}
	wg.Wait()
}

func TestClosedDeliversOnlyTheSnapshotAndKeepsNoState(t *testing.T) {
	b := New(4)
	sub := b.Closed("j", 42, "snap")
	ev, ok := <-sub.Events()
	require.True(t, ok)
	require.Equal(t, Event{Type: "job.snapshot", JobID: "j", Seq: 42, EmittedAt: ev.EmittedAt, Data: "snap"}, ev)
	_, ok = <-sub.Events()
	require.False(t, ok)
	require.NoError(t, sub.Err())
	sub.Close()
	require.Empty(t, b.st)
}
