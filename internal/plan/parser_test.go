package plan

import (
    "os"
    "path/filepath"
    "testing"
)

func TestSchemaValidation(t *testing.T) {
    tests := []struct {
        name    string
        schema  PlanSchema
        wantErr bool
    }{
        {
            name: "valid schema",
            schema: PlanSchema{
                Schema: SchemaVersion,
                File:   "test.md",
                Levels: []LevelConfig{
                    {Name: "phase", Marker: MarkerHeading, Depth: 2},
                    {Name: "step", Marker: MarkerChecklist, Depth: 0},
                },
            },
            wantErr: false,
        },
        {
            name: "empty file path",
            schema: PlanSchema{
                Schema: SchemaVersion,
                File:   "",
                Levels: []LevelConfig{
                    {Name: "phase", Marker: MarkerHeading, Depth: 2},
                },
            },
            wantErr: true,
        },
        {
            name: "no levels",
            schema: PlanSchema{
                Schema: SchemaVersion,
                File:   "test.md",
                Levels: []LevelConfig{},
            },
            wantErr: true,
        },
        {
            name: "duplicate level names",
            schema: PlanSchema{
                Schema: SchemaVersion,
                File:   "test.md",
                Levels: []LevelConfig{
                    {Name: "step", Marker: MarkerHeading, Depth: 2},
                    {Name: "step", Marker: MarkerChecklist, Depth: 0},
                },
            },
            wantErr: true,
        },
        {
            name: "invalid heading depth",
            schema: PlanSchema{
                Schema: SchemaVersion,
                File:   "test.md",
                Levels: []LevelConfig{
                    {Name: "phase", Marker: MarkerHeading, Depth: 7},
                },
            },
            wantErr: true,
        },
        {
            name: "invalid marker type",
            schema: PlanSchema{
                Schema: SchemaVersion,
                File:   "test.md",
                Levels: []LevelConfig{
                    {Name: "phase", Marker: "invalid", Depth: 2},
                },
            },
            wantErr: true,
        },
        {
            name: "negative depth",
            schema: PlanSchema{
                Schema: SchemaVersion,
                File:   "test.md",
                Levels: []LevelConfig{
                    {Name: "item", Marker: MarkerChecklist, Depth: -1},
                },
            },
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := tt.schema.Validate()
            if (err != nil) != tt.wantErr {
                t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestSchemaLoadSave(t *testing.T) {
    tmpDir := t.TempDir()
    schemaPath := filepath.Join(tmpDir, "test-schema.json")

    // Create a schema
    schema := NewSchema("PLAN.md", []LevelConfig{
        {Name: "phase", Marker: MarkerHeading, Depth: 2, Prefix: "Phase"},
        {Name: "step", Marker: MarkerHeading, Depth: 3},
        {Name: "item", Marker: MarkerChecklist, Depth: 0},
    })

    // Save it
    if err := schema.SaveSchema(schemaPath); err != nil {
        t.Fatalf("SaveSchema() error = %v", err)
    }

    // Load it back
    loaded, err := LoadSchema(schemaPath)
    if err != nil {
        t.Fatalf("LoadSchema() error = %v", err)
    }

    // Verify
    if loaded.File != schema.File {
        t.Errorf("File = %q, want %q", loaded.File, schema.File)
    }
    if len(loaded.Levels) != len(schema.Levels) {
        t.Errorf("Levels count = %d, want %d", len(loaded.Levels), len(schema.Levels))
    }
    for i, level := range loaded.Levels {
        if level.Name != schema.Levels[i].Name {
            t.Errorf("Level[%d].Name = %q, want %q", i, level.Name, schema.Levels[i].Name)
        }
    }
}

func TestParserBasic(t *testing.T) {
    tmpDir := t.TempDir()
    planPath := filepath.Join(tmpDir, "PLAN.md")

    // Create a simple plan
    planContent := `# My Plan

## Phase 1: Setup

### Step 1.1: Install Dependencies

- [ ] Install Go
- [x] Install Node.js

### Step 1.2: Configure Environment

- [ ] Set up database

## Phase 2: Implementation ✅ COMPLETE

### Step 2.1: Build Core

- [x] Create main.go
- [x] Add tests
`
    if err := os.WriteFile(planPath, []byte(planContent), 0644); err != nil {
        t.Fatalf("Failed to write plan file: %v", err)
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

    // Check phases
    phases := plan.UnitsAtLevel("phase")
    if len(phases) != 2 {
        t.Errorf("Phase count = %d, want 2", len(phases))
    }

    // Check steps
    steps := plan.UnitsAtLevel("step")
    if len(steps) != 3 {
        t.Errorf("Step count = %d, want 3", len(steps))
    }

    // Check items
    items := plan.UnitsAtLevel("item")
    if len(items) != 5 {
        t.Errorf("Item count = %d, want 5", len(items))
    }

    // Check hierarchy
    if len(plan.Root) != 2 {
        t.Errorf("Root count = %d, want 2", len(plan.Root))
    }

    // Check status detection
    phase2 := plan.Units["phase-2"]
    if phase2 == nil {
        t.Fatal("phase-2 not found")
    }
    if phase2.Status != UnitComplete {
        t.Errorf("phase-2 status = %v, want Complete", phase2.Status)
    }

    // Check checklist status
    var completedItems, pendingItems int
    for _, item := range items {
        if item.Status == UnitComplete {
            completedItems++
        } else if item.Status == UnitPending {
            pendingItems++
        }
    }
    if completedItems != 3 {
        t.Errorf("Completed items = %d, want 3", completedItems)
    }
    if pendingItems != 2 {
        t.Errorf("Pending items = %d, want 2", pendingItems)
    }
}

func TestParserNumberedList(t *testing.T) {
    tmpDir := t.TempDir()
    planPath := filepath.Join(tmpDir, "PLAN.md")

    planContent := `# Plan

## Tasks

1. First task
2. Second task (DONE)
3. Third task
`
    if err := os.WriteFile(planPath, []byte(planContent), 0644); err != nil {
        t.Fatalf("Failed to write plan file: %v", err)
    }

    schema := &PlanSchema{
        Schema: SchemaVersion,
        File:   planPath,
        Levels: []LevelConfig{
            {Name: "section", Marker: MarkerHeading, Depth: 2},
            {Name: "task", Marker: MarkerNumbered, Depth: 0},
        },
    }

    plan, err := ParseWithSchema(planPath, schema)
    if err != nil {
        t.Fatalf("Parse() error = %v", err)
    }

    tasks := plan.UnitsAtLevel("task")
    if len(tasks) != 3 {
        t.Errorf("Task count = %d, want 3", len(tasks))
    }

    // Check status detection
    for _, task := range tasks {
        if task.Name == "Second task (DONE)" && task.Status != UnitComplete {
            t.Errorf("Second task status = %v, want Complete", task.Status)
        }
    }
}

func TestParserBulletList(t *testing.T) {
    tmpDir := t.TempDir()
    planPath := filepath.Join(tmpDir, "PLAN.md")

    planContent := `# Plan

## Features

- Feature A
- Feature B ✅
- Feature C
`
    if err := os.WriteFile(planPath, []byte(planContent), 0644); err != nil {
        t.Fatalf("Failed to write plan file: %v", err)
    }

    schema := &PlanSchema{
        Schema: SchemaVersion,
        File:   planPath,
        Levels: []LevelConfig{
            {Name: "section", Marker: MarkerHeading, Depth: 2},
            {Name: "feature", Marker: MarkerBullet, Depth: 0},
        },
    }

    plan, err := ParseWithSchema(planPath, schema)
    if err != nil {
        t.Fatalf("Parse() error = %v", err)
    }

    features := plan.UnitsAtLevel("feature")
    if len(features) != 3 {
        t.Errorf("Feature count = %d, want 3", len(features))
    }

    // Check status detection for emoji
    for _, f := range features {
        if f.Name == "Feature B ✅" && f.Status != UnitComplete {
            t.Errorf("Feature B status = %v, want Complete", f.Status)
        }
    }
}

func TestPlanStats(t *testing.T) {
    plan := NewPlan("test.md", "", nil)

    plan.AddUnit(&WorkUnit{ID: "phase-1", Name: "Phase 1", Level: "phase", Status: UnitComplete})
    plan.AddUnit(&WorkUnit{ID: "phase-2", Name: "Phase 2", Level: "phase", Status: UnitActive})
    plan.AddUnit(&WorkUnit{ID: "phase-1/step-1", Name: "Step 1", Level: "step", Status: UnitArchived, Parent: "phase-1"})
    plan.AddUnit(&WorkUnit{ID: "phase-1/step-2", Name: "Step 2", Level: "step", Status: UnitPending, Parent: "phase-1"})

    stats := plan.Stats()

    if stats.TotalUnits != 4 {
        t.Errorf("TotalUnits = %d, want 4", stats.TotalUnits)
    }
    if stats.CompleteUnits != 1 {
        t.Errorf("CompleteUnits = %d, want 1", stats.CompleteUnits)
    }
    if stats.ActiveUnits != 1 {
        t.Errorf("ActiveUnits = %d, want 1", stats.ActiveUnits)
    }
    if stats.ArchivedUnits != 1 {
        t.Errorf("ArchivedUnits = %d, want 1", stats.ArchivedUnits)
    }
    if stats.PendingUnits != 1 {
        t.Errorf("PendingUnits = %d, want 1", stats.PendingUnits)
    }
    if stats.ByLevel["phase"] != 2 {
        t.Errorf("ByLevel[phase] = %d, want 2", stats.ByLevel["phase"])
    }
    if stats.ByLevel["step"] != 2 {
        t.Errorf("ByLevel[step] = %d, want 2", stats.ByLevel["step"])
    }
}

func TestWorkUnitNavigation(t *testing.T) {
    plan := NewPlan("test.md", "", nil)

    // Build a tree:
    // phase-1
    //   step-1
    //     item-1
    //     item-2
    //   step-2
    // phase-2

    plan.AddUnit(&WorkUnit{ID: "phase-1", Name: "Phase 1", Level: "phase"})
    plan.AddUnit(&WorkUnit{ID: "phase-1/step-1", Name: "Step 1", Level: "step", Parent: "phase-1"})
    plan.AddUnit(&WorkUnit{ID: "phase-1/step-1/item-1", Name: "Item 1", Level: "item", Parent: "phase-1/step-1"})
    plan.AddUnit(&WorkUnit{ID: "phase-1/step-1/item-2", Name: "Item 2", Level: "item", Parent: "phase-1/step-1"})
    plan.AddUnit(&WorkUnit{ID: "phase-1/step-2", Name: "Step 2", Level: "step", Parent: "phase-1"})
    plan.AddUnit(&WorkUnit{ID: "phase-2", Name: "Phase 2", Level: "phase"})

    // Test GetChildren
    phase1Children := plan.GetChildren("phase-1")
    if len(phase1Children) != 2 {
        t.Errorf("phase-1 children = %d, want 2", len(phase1Children))
    }

    step1Children := plan.GetChildren("phase-1/step-1")
    if len(step1Children) != 2 {
        t.Errorf("step-1 children = %d, want 2", len(step1Children))
    }

    // Test GetDescendants
    phase1Descendants := plan.GetDescendants("phase-1")
    if len(phase1Descendants) != 4 {
        t.Errorf("phase-1 descendants = %d, want 4", len(phase1Descendants))
    }

    // Test AllUnits (document order)
    all := plan.AllUnits()
    if len(all) != 6 {
        t.Errorf("AllUnits = %d, want 6", len(all))
    }

    // Verify order
    expectedOrder := []string{"phase-1", "phase-1/step-1", "phase-1/step-1/item-1", "phase-1/step-1/item-2", "phase-1/step-2", "phase-2"}
    for i, unit := range all {
        if unit.ID != expectedOrder[i] {
            t.Errorf("AllUnits[%d].ID = %q, want %q", i, unit.ID, expectedOrder[i])
        }
    }
}

func TestCleanName(t *testing.T) {
    tests := []struct {
        input    string
        expected string
    }{
        {"Simple name", "Simple name"},
        {"Done task ✅", "Done task"},
        {"Complete task (DONE)", "Complete task"},
        {"Finished [COMPLETE]", "Finished"},
        {"✅ Already done", "Already done"},
        {"Task ✅ COMPLETE", "Task"},
    }

    for _, tt := range tests {
        t.Run(tt.input, func(t *testing.T) {
            result := CleanName(tt.input)
            if result != tt.expected {
                t.Errorf("CleanName(%q) = %q, want %q", tt.input, result, tt.expected)
            }
        })
    }
}
