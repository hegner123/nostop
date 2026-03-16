package plan

import (
    "bufio"
    "fmt"
    "os"
    "strings"
    "unicode"
)

// Parser parses plan markdown files into a Plan structure.
type Parser struct {
    schema *PlanSchema
}

// NewParser creates a new parser for the given schema.
func NewParser(schema *PlanSchema) *Parser {
    return &Parser{schema: schema}
}

// Parse reads and parses a plan file.
func (p *Parser) Parse(schemaPath string) (*Plan, error) {
    planPath := p.schema.ResolvePlanPath(schemaPath)
    
    file, err := os.Open(planPath)
    if err != nil {
        return nil, fmt.Errorf("open plan file: %w", err)
    }
    defer file.Close()

    plan := NewPlan(planPath, schemaPath, p.schema)
    
    // Track current parent at each level
    levelParents := make(map[int]string) // level index -> current parent ID at that level
    levelCounters := make(map[string]int) // level name -> counter for ID generation

    scanner := bufio.NewScanner(file)
    lineNum := 0

    for scanner.Scan() {
        lineNum++
        line := scanner.Text()

        // Try to match each level in order
        for levelIdx, levelCfg := range p.schema.Levels {
            match, name, status := p.matchLevel(line, levelCfg)
            if !match {
                continue
            }

            // Generate ID
            levelCounters[levelCfg.Name]++
            id := p.generateID(levelCfg.Name, levelCounters[levelCfg.Name], levelIdx, levelParents)

            // Determine parent — walk up to find nearest ancestor
            parent := ""
            for i := levelIdx - 1; i >= 0; i-- {
                if pid, ok := levelParents[i]; ok {
                    parent = pid
                    break
                }
            }

            unit := &WorkUnit{
                ID:      id,
                Name:    name,
                Level:   levelCfg.Name,
                Status:  status,
                Parent:  parent,
                Line:    lineNum,
                RawText: line,
            }

            plan.AddUnit(unit)

            // Update parent tracking for this level and clear deeper levels
            levelParents[levelIdx] = id
            for i := levelIdx + 1; i < len(p.schema.Levels); i++ {
                delete(levelParents, i)
            }

            // Reset counters for child levels when we enter a new parent
            for i := levelIdx + 1; i < len(p.schema.Levels); i++ {
                levelCounters[p.schema.Levels[i].Name] = 0
            }

            break // Only match first matching level
        }
    }

    if err := scanner.Err(); err != nil {
        return nil, fmt.Errorf("scan plan file: %w", err)
    }

    return plan, nil
}

// matchLevel checks if a line matches a level configuration.
// Returns (matched, name, status).
func (p *Parser) matchLevel(line string, cfg LevelConfig) (bool, string, UnitStatus) {
    switch cfg.Marker {
    case MarkerHeading:
        return p.matchHeading(line, cfg)
    case MarkerChecklist:
        return p.matchChecklist(line, cfg)
    case MarkerNumbered:
        return p.matchNumbered(line, cfg)
    case MarkerBullet:
        return p.matchBullet(line, cfg)
    default:
        return false, "", UnitPending
    }
}

// matchHeading matches heading markers (##, ###, etc).
func (p *Parser) matchHeading(line string, cfg LevelConfig) (bool, string, UnitStatus) {
    // Build expected prefix: "## " for depth 2, "### " for depth 3, etc.
    prefix := strings.Repeat("#", cfg.Depth) + " "
    
    if !strings.HasPrefix(line, prefix) {
        return false, "", UnitPending
    }

    // Make sure it's exactly this depth (not more #s)
    if len(line) > len(prefix) && line[len(prefix)-1] == '#' {
        return false, "", UnitPending
    }

    name := strings.TrimPrefix(line, prefix)
    name = strings.TrimSpace(name)

    // Check prefix filter if set
    if cfg.Prefix != "" && !strings.HasPrefix(name, cfg.Prefix) {
        return false, "", UnitPending
    }

    // Extract status from heading text
    status := p.extractHeadingStatus(name)

    return true, name, status
}

// matchChecklist matches checklist items (- [ ] or - [x]).
func (p *Parser) matchChecklist(line string, cfg LevelConfig) (bool, string, UnitStatus) {
    // Calculate expected indent
    indent := strings.Repeat("  ", cfg.Depth) // 2 spaces per depth level

    trimmed := line
    if cfg.Depth > 0 {
        if !strings.HasPrefix(line, indent) {
            return false, "", UnitPending
        }
        trimmed = strings.TrimPrefix(line, indent)
    }

    // Must start with "- ["
    after, ok := strings.CutPrefix(trimmed, "- [")
    if !ok {
        return false, "", UnitPending
    }

    // Find closing bracket
    closeBracket := strings.Index(after, "]")
    if closeBracket < 0 {
        return false, "", UnitPending
    }

    inside := after[:closeBracket]
    rest := after[closeBracket+1:]

    // Rest must have at least one space then content
    rest = strings.TrimLeft(rest, " \t")
    if rest == "" {
        return false, "", UnitPending
    }
    name := strings.TrimSpace(rest)

    // Determine status from bracket contents
    insideTrimmed := strings.TrimSpace(inside)
    switch insideTrimmed {
    case "":
        // - [ ] unchecked
        return true, name, UnitPending
    case "x", "X", "✓", "✔":
        // - [x] checked
        return true, name, UnitComplete
    default:
        return false, "", UnitPending
    }
}

// matchNumbered matches numbered list items (1. Item).
func (p *Parser) matchNumbered(line string, cfg LevelConfig) (bool, string, UnitStatus) {
    indent := strings.Repeat("  ", cfg.Depth)

    trimmed := line
    if cfg.Depth > 0 {
        if !strings.HasPrefix(line, indent) {
            return false, "", UnitPending
        }
        trimmed = strings.TrimPrefix(line, indent)
    }

    // Must start with digits
    i := 0
    for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
        i++
    }
    if i == 0 {
        return false, "", UnitPending
    }

    // Must be followed by . or )
    if i >= len(trimmed) {
        return false, "", UnitPending
    }
    if trimmed[i] != '.' && trimmed[i] != ')' {
        return false, "", UnitPending
    }
    i++

    // Must have at least one space then content
    rest := trimmed[i:]
    rest = strings.TrimLeft(rest, " \t")
    if rest == "" {
        return false, "", UnitPending
    }

    name := strings.TrimSpace(rest)
    status := p.extractInlineStatus(name)
    return true, name, status
}

// matchBullet matches bullet list items (- Item).
func (p *Parser) matchBullet(line string, cfg LevelConfig) (bool, string, UnitStatus) {
    indent := strings.Repeat("  ", cfg.Depth)
    
    trimmed := line
    if cfg.Depth > 0 {
        if !strings.HasPrefix(line, indent) {
            return false, "", UnitPending
        }
        trimmed = strings.TrimPrefix(line, indent)
    }

    // Match - but not - [ ] (that's checklist)
    if !strings.HasPrefix(trimmed, "- ") {
        return false, "", UnitPending
    }
    
    // Exclude checklists
    if strings.HasPrefix(trimmed, "- [") {
        return false, "", UnitPending
    }

    name := strings.TrimPrefix(trimmed, "- ")
    name = strings.TrimSpace(name)
    status := p.extractInlineStatus(name)

    return true, name, status
}

// extractHeadingStatus extracts status from heading text.
// Looks for patterns like "✅ COMPLETE", "(DONE)", "[COMPLETE]"
func (p *Parser) extractHeadingStatus(text string) UnitStatus {
    upper := strings.ToUpper(text)
    
    // Check for completion markers
    completionMarkers := []string{
        "✅", "COMPLETE", "COMPLETED", "DONE", "FINISHED",
    }
    
    for _, marker := range completionMarkers {
        if strings.Contains(upper, marker) {
            return UnitComplete
        }
    }

    // Check for in-progress markers
    progressMarkers := []string{
        "IN PROGRESS", "WIP", "ACTIVE", "CURRENT",
    }
    
    for _, marker := range progressMarkers {
        if strings.Contains(upper, marker) {
            return UnitActive
        }
    }

    return UnitPending
}

// extractInlineStatus extracts status from inline text.
func (p *Parser) extractInlineStatus(text string) UnitStatus {
    upper := strings.ToUpper(text)
    
    // Check for ✅ or (DONE) or [COMPLETE] at end
    if strings.HasSuffix(text, "✅") {
        return UnitComplete
    }
    if strings.HasSuffix(upper, "(DONE)") || strings.HasSuffix(upper, "[DONE]") {
        return UnitComplete
    }
    if strings.HasSuffix(upper, "(COMPLETE)") || strings.HasSuffix(upper, "[COMPLETE]") {
        return UnitComplete
    }

    return UnitPending
}

// generateID creates a hierarchical ID for a work unit.
func (p *Parser) generateID(level string, counter int, levelIdx int, parents map[int]string) string {
    // Simple ID: level-counter
    baseID := fmt.Sprintf("%s-%d", level, counter)

    // Walk up the parent chain to find the nearest ancestor.
    // This handles cases where intermediate levels are skipped
    // (e.g., a numbered item under a phase with no intervening task heading).
    for i := levelIdx - 1; i >= 0; i-- {
        if parentID, ok := parents[i]; ok {
            return fmt.Sprintf("%s/%s", parentID, baseID)
        }
    }

    return baseID
}

// ParseFile is a convenience function to parse a plan from schema and plan files.
func ParseFile(schemaPath string) (*Plan, error) {
    schema, err := LoadSchema(schemaPath)
    if err != nil {
        return nil, err
    }

    parser := NewParser(schema)
    return parser.Parse(schemaPath)
}

// ParseWithSchema parses a plan using an in-memory schema.
func ParseWithSchema(planPath string, schema *PlanSchema) (*Plan, error) {
    // Temporarily set the file path if not set
    if schema.File == "" {
        schema.File = planPath
    }

    parser := NewParser(schema)
    
    // For in-memory schema, we need to handle path resolution differently
    file, err := os.Open(planPath)
    if err != nil {
        return nil, fmt.Errorf("open plan file: %w", err)
    }
    defer file.Close()

    plan := NewPlan(planPath, "", schema)
    
    levelParents := make(map[int]string)
    levelCounters := make(map[string]int)

    scanner := bufio.NewScanner(file)
    lineNum := 0

    for scanner.Scan() {
        lineNum++
        line := scanner.Text()

        for levelIdx, levelCfg := range schema.Levels {
            match, name, status := parser.matchLevel(line, levelCfg)
            if !match {
                continue
            }

            levelCounters[levelCfg.Name]++
            id := parser.generateID(levelCfg.Name, levelCounters[levelCfg.Name], levelIdx, levelParents)

            // Determine parent — walk up to find nearest ancestor
            parent := ""
            for i := levelIdx - 1; i >= 0; i-- {
                if pid, ok := levelParents[i]; ok {
                    parent = pid
                    break
                }
            }

            unit := &WorkUnit{
                ID:      id,
                Name:    name,
                Level:   levelCfg.Name,
                Status:  status,
                Parent:  parent,
                Line:    lineNum,
                RawText: line,
            }

            plan.AddUnit(unit)
            levelParents[levelIdx] = id

            for i := levelIdx + 1; i < len(schema.Levels); i++ {
                delete(levelParents, i)
            }
            for i := levelIdx + 1; i < len(schema.Levels); i++ {
                levelCounters[schema.Levels[i].Name] = 0
            }

            break
        }
    }

    if err := scanner.Err(); err != nil {
        return nil, fmt.Errorf("scan plan file: %w", err)
    }

    return plan, nil
}

// CleanName removes status markers and whitespace from a name.
func CleanName(name string) string {
    // Remove common status suffixes
    suffixes := []string{
        " ✅", " (DONE)", " [DONE]", " (COMPLETE)", " [COMPLETE]",
        " ✅ COMPLETE", " — COMPLETE",
    }
    
    result := name
    for _, suffix := range suffixes {
        result = strings.TrimSuffix(result, suffix)
        result = strings.TrimSuffix(result, strings.ToLower(suffix))
    }
    
    // Remove leading status markers
    result = strings.TrimLeftFunc(result, func(r rune) bool {
        return r == '✅' || r == '✓' || r == '✔' || unicode.IsSpace(r)
    })

    return strings.TrimSpace(result)
}
