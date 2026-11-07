package controller

import (
	"strings"
	"testing"
	"time"
)

func TestTurnStreamBeginPublishesMeta(t *testing.T) {
	hub := &turnHub{turns: map[string]*TurnStream{}}
	turn := hub.Begin("conv-1")
	defer turn.Finish()

	replay, _, _, state := turn.Subscribe(0, "")
	if state != subOK {
		t.Fatalf("state = %v, want subOK", state)
	}
	if len(replay) != 1 || replay[0].Event != "meta" {
		t.Fatalf("replay = %+v, want one meta frame", replay)
	}
	if !strings.Contains(replay[0].Data, `"turn_id":"`) || !strings.Contains(replay[0].Data, `"conversation_id":"conv-1"`) {
		t.Fatalf("meta payload = %q", replay[0].Data)
	}
	if replay[0].Seq != 1 {
		t.Fatalf("meta seq = %d, want 1", replay[0].Seq)
	}
	meta, ok := turn.Meta()
	if !ok || meta.Seq != 1 || meta.Event != "meta" {
		t.Fatalf("Meta() = %+v, ok=%v", meta, ok)
	}
}

func TestTurnStreamReplayFromSeq(t *testing.T) {
	turn := (&turnHub{turns: map[string]*TurnStream{}}).Begin("conv-1")
	defer turn.Finish()
	turn.Publish("message", "a")
	turn.Publish("message", "b")
	turn.Publish("message", "c") // seq 4

	cases := []struct {
		from int64
		want []string
	}{
		{0, []string{"meta", "a", "b", "c"}},
		{1, []string{"a", "b", "c"}},
		{3, []string{"c"}},
		{4, nil},
	}
	for _, tc := range cases {
		replay, _, _, state := turn.Subscribe(tc.from, "")
		if state != subOK {
			t.Fatalf("from=%d state = %v, want subOK", tc.from, state)
		}
		var got []string
		for _, f := range replay {
			if f.Event == "meta" {
				got = append(got, "meta")
				continue
			}
			got = append(got, f.Data)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("from=%d replay = %v, want %v", tc.from, got, tc.want)
		}
	}
}

func TestTurnStreamResetWhenTrimmed(t *testing.T) {
	turn := (&turnHub{turns: map[string]*TurnStream{}}).Begin("conv-1")
	defer turn.Finish()
	big := strings.Repeat("x", 1<<20)
	for i := 0; i < 5; i++ { // 5 MiB > maxTurnBufferBytes
		turn.Publish("message", big)
	}
	if _, _, _, state := turn.Subscribe(0, ""); state != subReset {
		t.Fatalf("state = %v, want subReset after trim", state)
	}
}

func TestTurnStreamTurnMismatchResets(t *testing.T) {
	turn := (&turnHub{turns: map[string]*TurnStream{}}).Begin("conv-1")
	defer turn.Finish()
	if _, _, _, state := turn.Subscribe(0, "other-turn"); state != subReset {
		t.Fatalf("state = %v, want subReset for foreign turn", state)
	}
}

func TestTurnStreamAheadOfTurnResets(t *testing.T) {
	turn := (&turnHub{turns: map[string]*TurnStream{}}).Begin("conv-1")
	defer turn.Finish()
	if _, _, _, state := turn.Subscribe(99, ""); state != subReset {
		t.Fatalf("state = %v, want subReset when from > seq", state)
	}
}

func TestTurnStreamSubscribeReceivesNewFrames(t *testing.T) {
	turn := (&turnHub{turns: map[string]*TurnStream{}}).Begin("conv-1")
	defer turn.Finish()
	_, ch, cancel, state := turn.Subscribe(1, "")
	if state != subOK || ch == nil {
		t.Fatalf("state=%v ch=%v", state, ch)
	}
	defer cancel()
	turn.Publish("message", "live")
	select {
	case f := <-ch:
		if f.Data != "live" {
			t.Fatalf("frame = %+v", f)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber got no frame")
	}
}

func TestTurnStreamFinishClosesSubscribers(t *testing.T) {
	turn := (&turnHub{turns: map[string]*TurnStream{}}).Begin("conv-1")
	_, ch, cancel, _ := turn.Subscribe(1, "")
	defer cancel()
	turn.Finish()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("subscriber channel should be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber channel not closed on finish")
	}
	// 结束后仍能补发（buffer 留给迟到的重连）
	replay, ch2, _, state := turn.Subscribe(0, "")
	if state != subOK || ch2 != nil || len(replay) == 0 {
		t.Fatalf("state=%v ch=%v replay=%d", state, ch2, len(replay))
	}
}

func TestTurnStreamOldHandleDoesNotPolluteNewTurn(t *testing.T) {
	hub := &turnHub{turns: map[string]*TurnStream{}}
	old := hub.Begin("conv-1")
	cur := hub.Begin("conv-1")
	defer cur.Finish()

	old.Publish("error", "请求已取消")
	old.Publish("done", "{}")
	old.Finish()

	replay, _, _, _ := cur.Subscribe(0, "")
	for _, f := range replay {
		if f.Event == "done" || f.Event == "error" {
			t.Fatalf("new turn buffer polluted by old turn: %+v", f)
		}
	}
}

func TestTurnHubSweepKeepsLiveTurns(t *testing.T) {
	hub := &turnHub{turns: map[string]*TurnStream{}}
	live := hub.Begin("conv-live")
	stale := hub.Begin("conv-stale")

	live.mu.Lock()
	live.touchedAt = time.Now().Add(-time.Hour)
	live.mu.Unlock()
	stale.Finish() // 先结束，再把它拨旧（Finish 会刷新 touchedAt）
	stale.mu.Lock()
	stale.touchedAt = time.Now().Add(-time.Hour)
	stale.mu.Unlock()

	hub.Sweep()
	if hub.Get("conv-live") == nil {
		t.Fatal("live turn must never be swept")
	}
	if hub.Get("conv-stale") != nil {
		t.Fatal("finished turn should be swept after TTL")
	}
}
