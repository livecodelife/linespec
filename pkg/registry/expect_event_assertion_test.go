package registry

import (
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

// prov-2026-919f8621: EXPECT EVENT was never asserted. VerifyAll skipped unhit
// Event mocks, mockHitKey keyed EVENT by Table (always empty) instead of Topic,
// and Negative was in no key arm. These tests drive the registry the way the
// runner does: a container-side registry records hits, the host-side registry
// built from the same spec receives them (GetHits -> SetHits) and VerifyAll
// decides.

func eventSpec() *types.TestSpec {
	return &types.TestSpec{
		Name: "event_assertion",
		Expects: []types.ExpectStatement{
			{Channel: types.Event, Topic: "orders.created"},
			{Channel: types.Event, Topic: "orders.shipped"},
		},
	}
}

func TestExpectEventAsserted_UnproducedTopicFailsAsUnmet(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Name:    "never_produced",
		Expects: []types.ExpectStatement{{Channel: types.Event, Topic: "orders.created"}},
	})
	err := reg.VerifyAll()
	if err == nil {
		t.Fatal("bug: an EXPECT EVENT whose topic was never produced to passed VerifyAll")
	}
	if !strings.Contains(err.Error(), "orders.created") {
		t.Errorf("failure should name the topic, got: %v", err)
	}
}

func TestExpectEventAsserted_ProducedTopicPasses(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Name:    "produced",
		Expects: []types.ExpectStatement{{Channel: types.Event, Topic: "orders.created"}},
	})
	if _, ok := reg.FindKafkaMockWithBody("orders.created", func(string, string) bool { return true }); !ok {
		t.Fatal("expected the event mock to match a produce to its topic")
	}
	if err := reg.VerifyAll(); err != nil {
		t.Fatalf("an EXPECT EVENT that fired must pass: %v", err)
	}
}

func TestExpectEventAsserted_DifferentTopicsHitIndependently(t *testing.T) {
	containerSide := NewMockRegistry()
	containerSide.Register(eventSpec())
	if _, ok := containerSide.FindKafkaMockWithBody("orders.created", func(string, string) bool { return true }); !ok {
		t.Fatal("expected a match on orders.created")
	}
	hits := containerSide.GetHits()
	if len(hits) != 1 {
		t.Fatalf("one produce must yield one hit key, got %d: %v", len(hits), hits)
	}

	// Host side: only orders.created fired, so orders.shipped is unmet.
	err := mergeAndVerify(eventSpec(), hits)
	if err == nil {
		t.Fatal("bug: orders.shipped was never produced to but the hit on orders.created satisfied it (EVENT hit keys collide)")
	}
	if !strings.Contains(err.Error(), "orders.shipped") {
		t.Errorf("failure should name the unmet topic orders.shipped, got: %v", err)
	}
}

func TestExpectEventAsserted_NegativeAndPositiveHaveDistinctKeys(t *testing.T) {
	pos := &types.ExpectStatement{Channel: types.Event, Topic: "orders.created"}
	neg := &types.ExpectStatement{Channel: types.Event, Topic: "orders.created", Negative: true}
	if mockHitKey(pos) == mockHitKey(neg) {
		t.Fatalf("bug: EXPECT EVENT and EXPECT_NOT EVENT on one topic share hit key %q", mockHitKey(pos))
	}
	other := &types.ExpectStatement{Channel: types.Event, Topic: "orders.shipped"}
	if mockHitKey(pos) == mockHitKey(other) {
		t.Fatalf("bug: EVENT mocks on different topics share hit key %q", mockHitKey(pos))
	}
}

func TestExpectEventAsserted_ProducedNegativeTopicFails(t *testing.T) {
	spec := func() *types.TestSpec {
		return &types.TestSpec{
			Name: "forbidden_event",
			Expects: []types.ExpectStatement{
				{Channel: types.Event, Topic: "orders.created"},
			},
			ExpectsNot: []types.ExpectStatement{
				{Channel: types.Event, Topic: "orders.created"},
			},
		}
	}
	// Only the positive expectation fires; the host receives that hit key.
	containerSide := NewMockRegistry()
	containerSide.Register(spec())
	if _, ok := containerSide.FindKafkaMockWithBody("orders.created", func(string, string) bool { return true }); !ok {
		t.Fatal("expected the positive event mock to match")
	}
	// Positive satisfied, negative must not be reported as hit by that key.
	if err := mergeAndVerify(spec(), containerSide.GetHits()); err != nil {
		t.Fatalf("a produce matched by the positive EXPECT must not trip the EXPECT_NOT beside it, or vice versa: %v", err)
	}

	// Now the negative alone is hit (CheckNegativeMocks is what a proxy calls).
	neg := NewMockRegistry()
	neg.Register(&types.TestSpec{
		Name:       "forbidden_event_produced",
		ExpectsNot: []types.ExpectStatement{{Channel: types.Event, Topic: "orders.created"}},
	})
	neg.CheckNegativeMocks("orders.created", "")
	if err := neg.VerifyAll(); err == nil {
		t.Fatal("bug: an EXPECT_NOT EVENT whose topic was produced to passed VerifyAll")
	}
}
