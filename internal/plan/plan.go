package plan

import (
    "fmt"
    "strings"
)

// UnitStatus represents the state of a work unit.
type UnitStatus int

const (
    UnitPending  UnitStatus = iota // Not started
    UnitActive                     // Currently being worked on
    UnitComplete                   // Finished but not archived
    UnitArchived                   // Finished and archived
)

// String returns the string representation of the status.
func (s UnitStatus) String() string {
    switch s {
    case UnitPending:
        return "pending"
    case UnitActive:
        return "active"
    case UnitComplete:
        return "complete"
    case UnitArchived:
        return "archived"
    default:
        return "unknown"
    }
}

// ParseUnitStatus parses a status string.
func ParseUnitStatus(s string) (UnitStatus, error) {
    switch strings.ToLower(s) {
    case "pending":
        return UnitPending, nil
    case "active":
        return UnitActive, nil
    case "complete", "completed", "done":
        return UnitComplete, nil
    case "archived":
        return UnitArchived, nil
    default:
        return UnitPending, fmt.Errorf("unknown status: %s", s)
    }
}

// WorkUnit represents a single unit of work in a plan.
type WorkUnit struct {
    ID       string     // Derived hierarchical ID: "phase-1/step-1.1"
    Name     string     // Human-readable name: "Complete Charm v2 Migration"
    Level    string     // Level name from schema: "phase", "step", "item"
    Status   UnitStatus // Current status
    Parent   string     // Parent work unit ID, empty for top-level
    Children []string   // Child work unit IDs in order
    Line     int        // Source line number in plan file (1-based)
    RawText  string     // Original line text from the plan
}

// IsLeaf returns true if this unit has no children.
func (u *WorkUnit) IsLeaf() bool {
    return len(u.Children) == 0
}

// Depth returns the nesting depth of this unit (0 for top-level).
func (u *WorkUnit) Depth() int {
    return strings.Count(u.ID, "/")
}

// Plan represents a parsed plan with all its work units.
type Plan struct {
    File       string              // Path to the plan file
    SchemaPath string              // Path to the schema file
    Units      map[string]*WorkUnit // All units keyed by ID
    Root       []string            // Top-level unit IDs in document order
    Schema     *PlanSchema         // The parsed schema config
}

// NewPlan creates an empty plan.
func NewPlan(file, schemaPath string, schema *PlanSchema) *Plan {
    return &Plan{
        File:       file,
        SchemaPath: schemaPath,
        Units:      make(map[string]*WorkUnit),
        Root:       nil,
        Schema:     schema,
    }
}

// AddUnit adds a work unit to the plan.
func (p *Plan) AddUnit(unit *WorkUnit) {
    p.Units[unit.ID] = unit

    if unit.Parent == "" {
        p.Root = append(p.Root, unit.ID)
    } else if parent, ok := p.Units[unit.Parent]; ok {
        parent.Children = append(parent.Children, unit.ID)
    }
}

// GetUnit returns a work unit by ID.
func (p *Plan) GetUnit(id string) *WorkUnit {
    return p.Units[id]
}

// GetChildren returns all immediate children of a unit.
func (p *Plan) GetChildren(id string) []*WorkUnit {
    unit := p.Units[id]
    if unit == nil {
        return nil
    }

    children := make([]*WorkUnit, 0, len(unit.Children))
    for _, childID := range unit.Children {
        if child := p.Units[childID]; child != nil {
            children = append(children, child)
        }
    }
    return children
}

// GetDescendants returns all descendants of a unit (depth-first).
func (p *Plan) GetDescendants(id string) []*WorkUnit {
    var result []*WorkUnit
    p.walkDescendants(id, &result)
    return result
}

func (p *Plan) walkDescendants(id string, result *[]*WorkUnit) {
    unit := p.Units[id]
    if unit == nil {
        return
    }

    for _, childID := range unit.Children {
        if child := p.Units[childID]; child != nil {
            *result = append(*result, child)
            p.walkDescendants(childID, result)
        }
    }
}

// AllUnits returns all units in document order (depth-first).
func (p *Plan) AllUnits() []*WorkUnit {
    var result []*WorkUnit
    for _, rootID := range p.Root {
        if root := p.Units[rootID]; root != nil {
            result = append(result, root)
            result = append(result, p.GetDescendants(rootID)...)
        }
    }
    return result
}

// UnitsAtLevel returns all units at a specific level.
func (p *Plan) UnitsAtLevel(level string) []*WorkUnit {
    var result []*WorkUnit
    for _, unit := range p.AllUnits() {
        if unit.Level == level {
            result = append(result, unit)
        }
    }
    return result
}

// UnitsWithStatus returns all units with a specific status.
func (p *Plan) UnitsWithStatus(status UnitStatus) []*WorkUnit {
    var result []*WorkUnit
    for _, unit := range p.AllUnits() {
        if unit.Status == status {
            result = append(result, unit)
        }
    }
    return result
}

// SetStatus updates a unit's status.
func (p *Plan) SetStatus(id string, status UnitStatus) error {
    unit := p.Units[id]
    if unit == nil {
        return fmt.Errorf("work unit not found: %s", id)
    }
    unit.Status = status
    return nil
}

// Stats returns summary statistics about the plan.
type PlanStats struct {
    TotalUnits    int
    PendingUnits  int
    ActiveUnits   int
    CompleteUnits int
    ArchivedUnits int
    ByLevel       map[string]int
}

// Stats calculates plan statistics.
func (p *Plan) Stats() PlanStats {
    stats := PlanStats{
        ByLevel: make(map[string]int),
    }

    for _, unit := range p.Units {
        stats.TotalUnits++
        stats.ByLevel[unit.Level]++

        switch unit.Status {
        case UnitPending:
            stats.PendingUnits++
        case UnitActive:
            stats.ActiveUnits++
        case UnitComplete:
            stats.CompleteUnits++
        case UnitArchived:
            stats.ArchivedUnits++
        }
    }

    return stats
}
