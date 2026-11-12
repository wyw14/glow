package styleplan

import (
	"sync"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestPlannerCachesByVersionAndRequest(t *testing.T) {
	planner := NewPlanner()
	caps := fullCapabilities()
	req := Request{Theme: "dark", ThemeExplicit: true}

	first := planner.Plan(caps, req)
	second := planner.Plan(caps, req)

	if first != second {
		t.Fatal("expected the same strategy instance to be served from cache")
	}
	if planner.Builds() != 1 || planner.Len() != 1 {
		t.Fatalf("expected one build and one entry, got %d builds and %d entries", planner.Builds(), planner.Len())
	}

	other := planner.Plan(caps, Request{Theme: "light", ThemeExplicit: true})
	if other == first {
		t.Fatal("different requests must produce different strategies")
	}
	if planner.Builds() != 2 || planner.Len() != 2 {
		t.Fatalf("expected two builds and two entries, got %d builds and %d entries", planner.Builds(), planner.Len())
	}
}

func TestPlannerInvalidatesOnVersionChange(t *testing.T) {
	planner := NewPlanner()
	req := Request{Theme: AutoTheme}

	capsA := fullCapabilities()
	capsB := fullCapabilities()
	capsB.Color = colorprofile.ANSI256

	strategyA := planner.Plan(capsA, req)
	if planner.Len() != 1 {
		t.Fatalf("expected one cached entry, got %d", planner.Len())
	}

	strategyB := planner.Plan(capsB, req)
	if planner.Len() != 1 {
		t.Fatalf("version change must evict old entries, got %d entries", planner.Len())
	}
	if planner.Builds() != 2 {
		t.Fatalf("expected two builds after a version change, got %d", planner.Builds())
	}
	if strategyB.Version() != capsB.Version() {
		t.Error("the new strategy must target the new capability version")
	}

	rebuiltA := planner.Plan(capsA, req)
	if rebuiltA == strategyA {
		t.Fatal("evicted strategies must be rebuilt rather than resurrected")
	}
	if planner.Builds() != 3 {
		t.Fatalf("expected three builds after rebuilding the old version, got %d", planner.Builds())
	}
}

func TestPlannerConcurrentReadsBuildOnce(t *testing.T) {
	planner := NewPlanner()
	caps := fullCapabilities()
	req := Request{Theme: "dark", ThemeExplicit: true}

	const goroutines = 200
	var wg sync.WaitGroup
	results := make(chan *Strategy, goroutines)
	start := make(chan struct{})

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			<-start
			results <- planner.Plan(caps, req)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	first := planner.Plan(caps, req)
	for strategy := range results {
		if strategy != first {
			t.Fatal("concurrent reads must share a single cached strategy")
		}
	}
	if planner.Builds() != 1 {
		t.Fatalf("expected exactly one build under concurrent reads, got %d", planner.Builds())
	}
}

func TestPlannerInvalidateClearsCache(t *testing.T) {
	planner := NewPlanner()
	caps := fullCapabilities()
	req := Request{Theme: AutoTheme}

	before := planner.Plan(caps, req)
	planner.Invalidate()

	if planner.Len() != 0 || planner.Builds() != 0 {
		t.Fatal("invalidation must clear entries and counters")
	}
	after := planner.Plan(caps, req)
	if after == before {
		t.Fatal("strategies built after invalidation must be new instances")
	}
}
