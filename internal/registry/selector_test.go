package registry

import (
	"strings"
	"testing"
)

func TestParseLabelSelectorFiltersReturnsSyntaxGuide(t *testing.T) {
	_, err := ParseLabelSelectorFilters([]string{"version in v2"}, nil)
	if err == nil {
		t.Fatalf("expected parse selector error")
	}

	message := err.Error()
	if !strings.Contains(message, "expected values like (v1,v2)") {
		t.Fatalf("expected selector parse detail in error, got %s", message)
	}
	if !strings.Contains(message, LabelSelectorSyntaxSummary) {
		t.Fatalf("expected selector syntax summary in error, got %s", message)
	}
	if !strings.Contains(message, LabelSelectorValidExample) {
		t.Fatalf("expected valid selector example in error, got %s", message)
	}
	if !strings.Contains(message, LabelSelectorInvalidExample) {
		t.Fatalf("expected invalid selector example in error, got %s", message)
	}
}
