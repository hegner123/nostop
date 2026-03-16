// Package nostop provides the PlanTracker for work unit message tracking.
package nostop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hegner123/nostop/internal/plan"
	"github.com/hegner123/nostop/internal/storage"
)

// PlanTracker manages active plan state and work unit tracking.
// It tracks which work unit is currently active and tags new messages
// with the active work unit ID.
type PlanTracker struct {
	storage        *storage.SQLite
	conversationID string

	// Active plan state
	plan       *plan.Plan
	schemaPath string
	planHash   string // Hash of plan file content for change detection

	// Active work unit - messages created while this is set get tagged
	activeWorkUnitID string

	mu sync.RWMutex
}

// NewPlanTracker creates a new PlanTracker for a conversation.
func NewPlanTracker(store *storage.SQLite, conversationID string) *PlanTracker {
	return &PlanTracker{
		storage:        store,
		conversationID: conversationID,
	}
}

// LoadPlan loads a plan from a schema file and parses the associated plan document.
// If a plan was previously loaded, this syncs any status changes from the file.
func (t *PlanTracker) LoadPlan(ctx context.Context, schemaPath string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Load and validate schema
	schema, err := plan.LoadSchema(schemaPath)
	if err != nil {
		return fmt.Errorf("failed to load schema: %w", err)
	}

	// Resolve plan file path relative to schema
	planPath := schema.File
	if !filepath.IsAbs(planPath) {
		planPath = filepath.Join(filepath.Dir(schemaPath), planPath)
	}

	// Read plan file content for hash
	content, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("failed to read plan file: %w", err)
	}

	// Calculate hash for change detection
	hash := sha256.Sum256(content)
	newHash := hex.EncodeToString(hash[:])

	// Parse the plan
	parsedPlan, err := plan.ParseWithSchema(planPath, schema)
	if err != nil {
		return fmt.Errorf("failed to parse plan: %w", err)
	}

	// Save work units to database
	if err := t.savePlanUnits(ctx, parsedPlan); err != nil {
		return fmt.Errorf("failed to save plan units: %w", err)
	}

	t.plan = parsedPlan
	t.schemaPath = schemaPath
	t.planHash = newHash

	return nil
}

// savePlanUnits persists plan work units to the database.
func (t *PlanTracker) savePlanUnits(ctx context.Context, p *plan.Plan) error {
	// Convert plan.WorkUnit to storage.WorkUnit
	var units []storage.WorkUnit
	for _, wu := range p.AllUnits() {
		var parentID *string
		if wu.Parent != "" {
			parentID = &wu.Parent
		}

		units = append(units, storage.WorkUnit{
			ID:             wu.ID,
			ConversationID: t.conversationID,
			PlanFile:       p.File,
			Name:           wu.Name,
			Level:          wu.Level,
			Status:         convertPlanStatus(wu.Status),
			ParentID:       parentID,
			LineNumber:     wu.Line,
		})
	}

	return t.storage.SavePlanWorkUnits(ctx, t.conversationID, p.File, units)
}

// convertPlanStatus converts plan.UnitStatus to storage.WorkUnitStatus.
func convertPlanStatus(status plan.UnitStatus) storage.WorkUnitStatus {
	switch status {
	case plan.UnitPending:
		return storage.WorkUnitPending
	case plan.UnitActive:
		return storage.WorkUnitActive
	case plan.UnitComplete:
		return storage.WorkUnitComplete
	case plan.UnitArchived:
		return storage.WorkUnitArchived
	default:
		return storage.WorkUnitPending
	}
}

// RefreshPlan re-parses the plan file and detects status changes.
// Returns the IDs of work units whose status changed.
func (t *PlanTracker) RefreshPlan(ctx context.Context) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.plan == nil || t.schemaPath == "" {
		return nil, nil
	}

	// Reload schema
	schema, err := plan.LoadSchema(t.schemaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to reload schema: %w", err)
	}

	// Resolve plan file path
	planPath := schema.File
	if !filepath.IsAbs(planPath) {
		planPath = filepath.Join(filepath.Dir(t.schemaPath), planPath)
	}

	// Read plan file content
	content, err := os.ReadFile(planPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read plan file: %w", err)
	}

	// Check hash for changes
	hash := sha256.Sum256(content)
	newHash := hex.EncodeToString(hash[:])
	if newHash == t.planHash {
		return nil, nil // No changes
	}

	// Re-parse the plan
	newPlan, err := plan.ParseWithSchema(planPath, schema)
	if err != nil {
		return nil, fmt.Errorf("failed to parse plan: %w", err)
	}

	// Detect status changes
	var changedIDs []string
	for id, newUnit := range newPlan.Units {
		if oldUnit := t.plan.GetUnit(id); oldUnit != nil {
			if oldUnit.Status != newUnit.Status {
				changedIDs = append(changedIDs, id)

				// Update status in database
				if err := t.storage.UpdateWorkUnitStatus(ctx, id, convertPlanStatus(newUnit.Status)); err != nil {
					// Log but continue
					continue
				}
			}
		}
	}

	t.plan = newPlan
	t.planHash = newHash

	return changedIDs, nil
}

// SetActiveWorkUnit sets the currently active work unit.
// Subsequent messages will be tagged with this work unit ID.
func (t *PlanTracker) SetActiveWorkUnit(ctx context.Context, workUnitID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Validate work unit exists
	if t.plan != nil {
		if wu := t.plan.GetUnit(workUnitID); wu == nil {
			return fmt.Errorf("work unit not found: %s", workUnitID)
		}
	}

	// Update status to Active if pending
	if err := t.storage.UpdateWorkUnitStatus(ctx, workUnitID, storage.WorkUnitActive); err != nil {
		return fmt.Errorf("failed to update work unit status: %w", err)
	}

	// Update in-memory plan status
	if t.plan != nil {
		if wu := t.plan.GetUnit(workUnitID); wu != nil && wu.Status == plan.UnitPending {
			wu.Status = plan.UnitActive
		}
	}

	t.activeWorkUnitID = workUnitID
	return nil
}

// ClearActiveWorkUnit clears the active work unit.
func (t *PlanTracker) ClearActiveWorkUnit() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.activeWorkUnitID = ""
}

// GetActiveWorkUnitID returns the currently active work unit ID.
// Returns empty string if no work unit is active.
func (t *PlanTracker) GetActiveWorkUnitID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.activeWorkUnitID
}

// GetActiveWorkUnit returns the currently active work unit.
// Returns nil if no work unit is active.
func (t *PlanTracker) GetActiveWorkUnit() *plan.WorkUnit {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.activeWorkUnitID == "" || t.plan == nil {
		return nil
	}
	return t.plan.GetUnit(t.activeWorkUnitID)
}

// CompleteWorkUnit marks a work unit as complete.
func (t *PlanTracker) CompleteWorkUnit(ctx context.Context, workUnitID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.storage.UpdateWorkUnitStatus(ctx, workUnitID, storage.WorkUnitComplete); err != nil {
		return err
	}

	// Update in-memory plan status
	if t.plan != nil {
		if wu := t.plan.GetUnit(workUnitID); wu != nil {
			wu.Status = plan.UnitComplete
		}
	}

	// Clear active if this was the active unit
	if t.activeWorkUnitID == workUnitID {
		t.activeWorkUnitID = ""
	}

	return nil
}

// GetPlan returns the loaded plan, or nil if none loaded.
func (t *PlanTracker) GetPlan() *plan.Plan {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.plan
}

// HasPlan returns true if a plan is loaded.
func (t *PlanTracker) HasPlan() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.plan != nil
}

// GetWorkUnits returns all work units from the loaded plan.
func (t *PlanTracker) GetWorkUnits() []*plan.WorkUnit {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.plan == nil {
		return nil
	}
	return t.plan.AllUnits()
}

// GetWorkUnitsAtLevel returns work units at a specific level.
func (t *PlanTracker) GetWorkUnitsAtLevel(level string) []*plan.WorkUnit {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.plan == nil {
		return nil
	}
	return t.plan.UnitsAtLevel(level)
}

// GetRootUnits returns top-level work units.
func (t *PlanTracker) GetRootUnits() []*plan.WorkUnit {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.plan == nil {
		return nil
	}

	var roots []*plan.WorkUnit
	for _, id := range t.plan.Root {
		if wu := t.plan.GetUnit(id); wu != nil {
			roots = append(roots, wu)
		}
	}
	return roots
}

// GetChildren returns child work units of the given parent.
func (t *PlanTracker) GetChildren(parentID string) []*plan.WorkUnit {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.plan == nil {
		return nil
	}
	return t.plan.GetChildren(parentID)
}

// GetSchemaPath returns the path to the loaded schema file.
func (t *PlanTracker) GetSchemaPath() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.schemaPath
}

// GetPlanFile returns the path to the plan markdown file.
func (t *PlanTracker) GetPlanFile() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.plan == nil {
		return ""
	}
	return t.plan.File
}

// GetStats returns statistics about the loaded plan.
func (t *PlanTracker) GetStats() *plan.PlanStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.plan == nil {
		return nil
	}
	stats := t.plan.Stats()
	return &stats
}

// WatchForChanges starts a goroutine that periodically checks for plan file changes.
// The callback is called with changed work unit IDs when changes are detected.
// Returns a cancel function to stop watching.
func (t *PlanTracker) WatchForChanges(ctx context.Context, interval time.Duration, callback func([]string)) func() {
	watchCtx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				changedIDs, err := t.RefreshPlan(ctx)
				if err != nil {
					// Log error but continue watching
					continue
				}
				if len(changedIDs) > 0 && callback != nil {
					callback(changedIDs)
				}
			}
		}
	}()

	return cancel
}
