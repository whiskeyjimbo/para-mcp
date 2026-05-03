package domain

import "testing"

func TestApplySummariesMode_Never(t *testing.T) {
	derived := &DerivedMetadata{Summary: "hello"}
	summaries := []NoteSummary{{Derived: derived}, {Derived: derived}}
	ApplySummariesMode(summaries, SummariesNever)
	for i, s := range summaries {
		if s.Derived != nil {
			t.Errorf("entry %d: expected Derived=nil, got %v", i, s.Derived)
		}
	}
}

func TestApplySummariesMode_AutoIsNoop(t *testing.T) {
	derived := &DerivedMetadata{Summary: "hello"}
	summaries := []NoteSummary{{Derived: derived}}
	ApplySummariesMode(summaries, SummariesAuto)
	if summaries[0].Derived == nil {
		t.Error("auto should not strip Derived")
	}
}

func TestApplySummariesMode_AlwaysIsNoop(t *testing.T) {
	derived := &DerivedMetadata{Summary: "hello"}
	summaries := []NoteSummary{{Derived: derived}}
	ApplySummariesMode(summaries, SummariesAlways)
	if summaries[0].Derived == nil {
		t.Error("always should not strip Derived")
	}
}
