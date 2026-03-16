package plan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectStructureBasic(t *testing.T) {
	tmpDir := t.TempDir()
	planPath := filepath.Join(tmpDir, "PLAN.md")

	content := `# My Project Plan

## Phase 1: Setup

### Step 1.1: Install Dependencies

- [ ] Install Go
- [x] Install Node.js

### Step 1.2: Configure Environment

- [ ] Set up database

## Phase 2: Implementation

### Step 2.1: Build Core

- [x] Create main.go
- [x] Add tests
`
	if err := os.WriteFile(planPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write plan: %v", err)
	}

	result, err := DetectStructure(planPath)
	if err != nil {
		t.Fatalf("DetectStructure() error = %v", err)
	}

	// Should detect H1, H2, H3, and checklists
	if len(result.Levels) < 3 {
		t.Errorf("Expected at least 3 levels, got %d", len(result.Levels))
		for _, l := range result.Levels {
			t.Logf("  %s depth=%d count=%d", l.Marker, l.Depth, l.Count)
		}
	}

	// Should generate a suggested schema
	if result.Suggested == nil {
		t.Fatal("Expected a suggested schema, got nil")
	}

	// H1 with count 1 should be filtered out (title)
	for _, l := range result.Suggested.Levels {
		if l.Marker == MarkerHeading && l.Depth == 1 {
			t.Error("H1 title should have been filtered from schema")
		}
	}

	t.Logf("Suggested schema: %d levels", len(result.Suggested.Levels))
	for _, l := range result.Suggested.Levels {
		t.Logf("  %s: %s depth=%d prefix=%q", l.Name, l.Marker, l.Depth, l.Prefix)
	}
}

func TestDetectStructurePrefixDetection(t *testing.T) {
	tmpDir := t.TempDir()
	planPath := filepath.Join(tmpDir, "PLAN.md")

	content := `# Plan

## Phase 1: First Phase
## Phase 2: Second Phase
## Phase 3: Third Phase

- [ ] Item A
- [ ] Item B
`
	if err := os.WriteFile(planPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write plan: %v", err)
	}

	result, err := DetectStructure(planPath)
	if err != nil {
		t.Fatalf("DetectStructure() error = %v", err)
	}

	if result.Suggested == nil {
		t.Fatal("Expected suggested schema")
	}

	// H2 should have "Phase" prefix detected
	foundPhasePrefix := false
	for _, l := range result.Suggested.Levels {
		if l.Marker == MarkerHeading && l.Depth == 2 && l.Prefix == "Phase" {
			foundPhasePrefix = true
		}
	}
	if !foundPhasePrefix {
		t.Error("Expected Phase prefix on H2 level")
		for _, l := range result.Suggested.Levels {
			t.Logf("  %s: %s depth=%d prefix=%q", l.Name, l.Marker, l.Depth, l.Prefix)
		}
	}
}

func TestDetectStructureNumberedLists(t *testing.T) {
	tmpDir := t.TempDir()
	planPath := filepath.Join(tmpDir, "PLAN.md")

	content := `## Tasks

1. First task
2. Second task
3. Third task
`
	if err := os.WriteFile(planPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write plan: %v", err)
	}

	result, err := DetectStructure(planPath)
	if err != nil {
		t.Fatalf("DetectStructure() error = %v", err)
	}

	if result.Suggested == nil {
		t.Fatal("Expected suggested schema")
	}

	// Should have heading + numbered levels
	hasNumbered := false
	for _, l := range result.Suggested.Levels {
		if l.Marker == MarkerNumbered {
			hasNumbered = true
		}
	}
	if !hasNumbered {
		t.Error("Expected numbered list level in schema")
	}
}

func TestDetectStructureEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	planPath := filepath.Join(tmpDir, "PLAN.md")

	content := `Just some text with no structure.

Another paragraph.
`
	if err := os.WriteFile(planPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write plan: %v", err)
	}

	result, err := DetectStructure(planPath)
	if err != nil {
		t.Fatalf("DetectStructure() error = %v", err)
	}

	if result.Suggested != nil {
		t.Error("Expected no suggested schema for unstructured file")
	}

	if !result.HasAmbiguities() {
		t.Error("Expected ambiguities for unstructured file")
	}
}

func TestDetectStructureManyHeadingLevels(t *testing.T) {
	tmpDir := t.TempDir()
	planPath := filepath.Join(tmpDir, "PLAN.md")

	content := `# Title
## Section
### Subsection
#### Sub-subsection
##### Detail
`
	if err := os.WriteFile(planPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write plan: %v", err)
	}

	result, err := DetectStructure(planPath)
	if err != nil {
		t.Fatalf("DetectStructure() error = %v", err)
	}

	// 4+ heading levels should trigger ambiguity
	if !result.HasAmbiguities() {
		t.Error("Expected ambiguity with 4+ heading levels")
	}

	t.Logf("Ambiguities:")
	for _, a := range result.Ambiguities {
		t.Logf("  %s", a)
	}
}

func TestDetectStructureSingleHeadingLevel(t *testing.T) {
	tmpDir := t.TempDir()
	planPath := filepath.Join(tmpDir, "PLAN.md")

	content := `## Feature A

- [ ] Task 1
- [x] Task 2

## Feature B

- [ ] Task 3
`
	if err := os.WriteFile(planPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write plan: %v", err)
	}

	result, err := DetectStructure(planPath)
	if err != nil {
		t.Fatalf("DetectStructure() error = %v", err)
	}

	if result.Suggested == nil {
		t.Fatal("Expected suggested schema")
	}

	// Single heading level should be named "section" not "phase"
	for _, l := range result.Suggested.Levels {
		if l.Marker == MarkerHeading && l.Name != "section" {
			t.Errorf("Single heading level should be named 'section', got %q", l.Name)
		}
	}
}

func TestFormatDetection(t *testing.T) {
	result := &DetectionResult{
		File: "PLAN.md",
		Levels: []DetectedLevel{
			{Marker: MarkerHeading, Depth: 2, Count: 3, Prefix: "Phase", Examples: []string{"Phase 1: Setup"}},
			{Marker: MarkerChecklist, Depth: 0, Count: 5, Examples: []string{"Install Go", "Add tests"}},
		},
		Suggested: &PlanSchema{
			Schema: SchemaVersion,
			File:   "PLAN.md",
			Levels: []LevelConfig{
				{Name: "section", Marker: MarkerHeading, Depth: 2, Prefix: "Phase"},
				{Name: "item", Marker: MarkerChecklist, Depth: 0},
			},
		},
	}

	output := FormatDetection(result)
	if output == "" {
		t.Error("FormatDetection returned empty string")
	}
	t.Logf("Output:\n%s", output)
}
