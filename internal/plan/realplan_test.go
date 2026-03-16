package plan

import (
    "os"
    "testing"
)

// TestParseRealPlan tests parsing a realistic plan structure similar to CRUSH_PATTERNS_PLAN.md
func TestParseRealPlan(t *testing.T) {
    tmpDir := t.TempDir()
    planPath := tmpDir + "/CRUSH_PATTERNS_PLAN.md"

    // Create a plan that mimics the structure of CRUSH_PATTERNS_PLAN.md
    planContent := `# Crush Patterns & Charm Packages Adoption Plan

## Status: Draft v3 (post-review, 2026-03-13)

**Phase 1: COMPLETE** (2026-03-13) — All v2 migration work finished.

## Phase 1: Complete Charm v2 Migration — COMPLETE

**Goal:** Finish the v2 upgrade that is already in progress.

### 1.1 — Already completed

- [x] go.mod updated
- [x] Old v1 packages removed
- [x] App.View() returns tea.View

### 1.2 — Remaining work

**7 View() string methods** that need conversion:

| File | Line | Method |
|---|---|---|
| chat.go | 464 | ChatModel.View() |

### 1.3 — Migration checklist

- [x] Update tea.KeyMsg to tea.KeyPressMsg
- [x] Run go build
- [ ] Run full test suite

## Phase 2: Typed Event Channel

**Goal:** Replace per-model streamCh with a typed channel.

### 2.1 — Define StreamEvent interface

- [ ] Create pkg/events/stream.go
- [ ] Define StreamEvent interface

### 2.2 — Implement EventBroker

- [ ] Single subscriber design
- [ ] Lifecycle management

## Phase 3: Content Parts Model — IN PROGRESS

**Goal:** Replace string content with interface-based parts.

### 3.1 — Define ContentPart interface

- [x] Create interface definition
- [ ] Add text part
- [ ] Add tool part
`
    if err := os.WriteFile(planPath, []byte(planContent), 0644); err != nil {
        t.Fatalf("Failed to write plan: %v", err)
    }

    schema := &PlanSchema{
        Schema: SchemaVersion,
        File:   planPath,
        Levels: []LevelConfig{
            {Name: "phase", Marker: MarkerHeading, Depth: 2, Prefix: "Phase"},
            {Name: "step", Marker: MarkerHeading, Depth: 3},
            {Name: "item", Marker: MarkerChecklist, Depth: 0},
        },
    }

    plan, err := ParseWithSchema(planPath, schema)
    if err != nil {
        t.Fatalf("Parse() error = %v", err)
    }

    // Verify phases
    phases := plan.UnitsAtLevel("phase")
    if len(phases) != 3 {
        t.Errorf("Phase count = %d, want 3", len(phases))
        for _, p := range phases {
            t.Logf("  Phase: %s - %s (status=%v)", p.ID, p.Name, p.Status)
        }
    }

    // Check Phase 1 is marked complete
    var phase1 *WorkUnit
    for _, p := range phases {
        if p.Name == "Phase 1: Complete Charm v2 Migration — COMPLETE" {
            phase1 = p
            break
        }
    }
    if phase1 == nil {
        t.Fatal("Phase 1 not found")
    }
    if phase1.Status != UnitComplete {
        t.Errorf("Phase 1 status = %v, want Complete", phase1.Status)
    }

    // Check Phase 3 is marked active
    var phase3 *WorkUnit
    for _, p := range phases {
        if p.Name == "Phase 3: Content Parts Model — IN PROGRESS" {
            phase3 = p
            break
        }
    }
    if phase3 == nil {
        t.Fatal("Phase 3 not found")
    }
    if phase3.Status != UnitActive {
        t.Errorf("Phase 3 status = %v, want Active", phase3.Status)
    }

    // Check steps
    steps := plan.UnitsAtLevel("step")
    if len(steps) != 6 {
        t.Errorf("Step count = %d, want 6", len(steps))
        for _, s := range steps {
            t.Logf("  Step: %s - %s", s.ID, s.Name)
        }
    }

    // Check items
    items := plan.UnitsAtLevel("item")
    t.Logf("Found %d items:", len(items))
    for _, item := range items {
        t.Logf("  Item: %s - %s (status=%v)", item.ID, item.Name, item.Status)
    }

    // Count completed vs pending items
    var completed, pending int
    for _, item := range items {
        if item.Status == UnitComplete {
            completed++
        } else {
            pending++
        }
    }
    t.Logf("Items: %d completed, %d pending", completed, pending)

    // Verify expected item count (13 items total)
    if len(items) != 13 {
        t.Errorf("Item count = %d, want 13", len(items))
    }

    // Verify stats
    stats := plan.Stats()
    t.Logf("Stats: total=%d, pending=%d, active=%d, complete=%d, archived=%d",
        stats.TotalUnits, stats.PendingUnits, stats.ActiveUnits, stats.CompleteUnits, stats.ArchivedUnits)
}
