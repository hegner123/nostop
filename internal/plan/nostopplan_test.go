package plan

import (
	"testing"
)

func TestParseNostopPlan(t *testing.T) {
	schemaPath := "../../nostop-plan.json"
	schema, err := LoadSchema(schemaPath)
	if err != nil {
		t.Fatalf("LoadSchema error: %v", err)
	}

	planPath := schema.ResolvePlanPath(schemaPath)
	p, err := ParseWithSchema(planPath, schema)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	stats := p.Stats()
	t.Logf("Stats: total=%d pending=%d active=%d complete=%d",
		stats.TotalUnits, stats.PendingUnits, stats.ActiveUnits, stats.CompleteUnits)
	t.Logf("By level: %v", stats.ByLevel)

	if stats.TotalUnits == 0 {
		t.Error("Expected at least some work units")
	}

	t.Logf("\nRoot units (%d):", len(p.Root))
	for _, id := range p.Root {
		u := p.GetUnit(id)
		t.Logf("  %s: %s (status=%s, children=%d)", u.ID, u.Name, u.Status, len(u.Children))
	}

	// Show first few items at each level
	for _, levelName := range []string{"phase", "task", "subtask", "item"} {
		units := p.UnitsAtLevel(levelName)
		t.Logf("\n%s level: %d units", levelName, len(units))
		for i, u := range units {
			if i >= 5 {
				t.Logf("  ... and %d more", len(units)-5)
				break
			}
			t.Logf("  %s: %s (status=%s)", u.ID, u.Name, u.Status)
		}
	}
}
