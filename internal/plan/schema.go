// Package plan provides plan parsing and work unit management for nostop.
package plan

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
)

// MarkerType defines how a level is identified in the plan document.
type MarkerType string

const (
    MarkerHeading   MarkerType = "heading"   // ## Heading
    MarkerChecklist MarkerType = "checklist" // - [ ] or - [x]
    MarkerNumbered  MarkerType = "numbered"  // 1. Item
    MarkerBullet    MarkerType = "bullet"    // - Item
)

// LevelConfig defines how to identify work units at a specific level.
type LevelConfig struct {
    Name   string     `json:"name"`   // User-chosen label: "phase", "step", "item"
    Marker MarkerType `json:"marker"` // How to identify this level
    Depth  int        `json:"depth"`  // For headings: # count. For lists: indent depth
    Prefix string     `json:"prefix"` // Optional prefix filter (e.g., "Phase")
}

// PlanSchema defines the structure for parsing a plan document.
type PlanSchema struct {
    Schema string        `json:"$schema"` // Version identifier
    File   string        `json:"file"`    // Path to the plan markdown file
    Levels []LevelConfig `json:"levels"`  // Hierarchy of work unit levels
}

// SchemaVersion is the current schema version.
const SchemaVersion = "nostop/plan/v1"

// DefaultSchemaFile is the default location for plan schema.
const DefaultSchemaFile = "nostop-plan.json"

// LoadSchema reads a plan schema from a JSON file.
func LoadSchema(path string) (*PlanSchema, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("read schema file: %w", err)
    }

    var schema PlanSchema
    if err := json.Unmarshal(data, &schema); err != nil {
        return nil, fmt.Errorf("parse schema JSON: %w", err)
    }

    if err := schema.Validate(); err != nil {
        return nil, fmt.Errorf("invalid schema: %w", err)
    }

    return &schema, nil
}

// Validate checks that the schema is well-formed.
func (s *PlanSchema) Validate() error {
    if s.File == "" {
        return fmt.Errorf("file path is required")
    }

    if len(s.Levels) == 0 {
        return fmt.Errorf("at least one level is required")
    }

    seen := make(map[string]bool)
    for i, level := range s.Levels {
        if level.Name == "" {
            return fmt.Errorf("level %d: name is required", i)
        }
        if seen[level.Name] {
            return fmt.Errorf("level %d: duplicate name %q", i, level.Name)
        }
        seen[level.Name] = true

        switch level.Marker {
        case MarkerHeading:
            if level.Depth < 1 || level.Depth > 6 {
                return fmt.Errorf("level %d (%s): heading depth must be 1-6, got %d", i, level.Name, level.Depth)
            }
        case MarkerChecklist, MarkerNumbered, MarkerBullet:
            // Depth 0 is valid (no indent), negative is not
            if level.Depth < 0 {
                return fmt.Errorf("level %d (%s): depth cannot be negative", i, level.Name)
            }
        default:
            return fmt.Errorf("level %d (%s): invalid marker type %q", i, level.Name, level.Marker)
        }
    }

    return nil
}

// ResolvePlanPath resolves the plan file path relative to the schema file.
func (s *PlanSchema) ResolvePlanPath(schemaPath string) string {
    if filepath.IsAbs(s.File) {
        return s.File
    }
    return filepath.Join(filepath.Dir(schemaPath), s.File)
}

// SaveSchema writes the schema to a JSON file.
func (s *PlanSchema) SaveSchema(path string) error {
    data, err := json.MarshalIndent(s, "", "  ")
    if err != nil {
        return fmt.Errorf("marshal schema: %w", err)
    }

    if err := os.WriteFile(path, data, 0644); err != nil {
        return fmt.Errorf("write schema file: %w", err)
    }

    return nil
}

// NewSchema creates a new schema with default version.
func NewSchema(planFile string, levels []LevelConfig) *PlanSchema {
    return &PlanSchema{
        Schema: SchemaVersion,
        File:   planFile,
        Levels: levels,
    }
}
