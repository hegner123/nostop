package plan

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// DetectedLevel represents a structural level found in a markdown file.
type DetectedLevel struct {
	Marker   MarkerType
	Depth    int
	Count    int
	Prefix   string   // common prefix if detected (e.g., "Phase")
	Examples []string // first few examples for display
}

// DetectionResult holds the results of scanning a markdown file for structure.
type DetectionResult struct {
	File        string
	Levels      []DetectedLevel
	Ambiguities []string    // descriptions of unclear structure
	Suggested   *PlanSchema // nil if too ambiguous
}

// HasAmbiguities returns true if the detection found unclear structure.
func (d *DetectionResult) HasAmbiguities() bool {
	return len(d.Ambiguities) > 0
}

// DetectStructure scans a markdown file and detects its hierarchical structure.
// It identifies heading levels, checklists, numbered lists, and bullets,
// then builds a suggested PlanSchema.
func DetectStructure(path string) (*DetectionResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	result := &DetectionResult{File: path}

	// Track what we find
	headings := make(map[int]*levelAccumulator) // depth -> accumulator
	var checklists []*levelAccumulator           // by indent depth
	var numbered []*levelAccumulator
	checklistByDepth := make(map[int]*levelAccumulator)
	numberedByDepth := make(map[int]*levelAccumulator)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Detect headings
		if depth, name, ok := detectHeading(line); ok {
			if headings[depth] == nil {
				headings[depth] = &levelAccumulator{marker: MarkerHeading, depth: depth}
			}
			headings[depth].add(name)
			continue
		}

		// Detect checklists (- [ ] or - [x])
		if indent, name, ok := detectChecklist(line); ok {
			if checklistByDepth[indent] == nil {
				acc := &levelAccumulator{marker: MarkerChecklist, depth: indent}
				checklistByDepth[indent] = acc
				checklists = append(checklists, acc)
			}
			checklistByDepth[indent].add(name)
			continue
		}

		// Detect numbered lists
		if indent, name, ok := detectNumbered(line); ok {
			if numberedByDepth[indent] == nil {
				acc := &levelAccumulator{marker: MarkerNumbered, depth: indent}
				numberedByDepth[indent] = acc
				numbered = append(numbered, acc)
			}
			numberedByDepth[indent].add(name)
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan file: %w", err)
	}

	// Build detected levels and schema
	result.buildLevels(headings, checklists, numbered)
	result.buildSchema(path)

	return result, nil
}

// levelAccumulator collects items at a specific marker+depth combination.
type levelAccumulator struct {
	marker   MarkerType
	depth    int
	count    int
	names    []string
	prefixes map[string]int // first word -> count
}

func (a *levelAccumulator) add(name string) {
	a.count++
	if len(a.names) < 3 {
		a.names = append(a.names, name)
	}
	if a.prefixes == nil {
		a.prefixes = make(map[string]int)
	}
	// Track first word as potential prefix
	firstWord, _, _ := strings.Cut(name, " ")
	// Strip trailing colon or number
	firstWord = strings.TrimRight(firstWord, ":0123456789")
	if firstWord != "" {
		a.prefixes[firstWord]++
	}
}

// dominantPrefix returns the most common first word if it appears in >50% of items.
func (a *levelAccumulator) dominantPrefix() string {
	if a.count < 2 {
		return ""
	}
	threshold := a.count / 2
	for prefix, count := range a.prefixes {
		if count > threshold && len(prefix) > 1 {
			return prefix
		}
	}
	return ""
}

func (a *levelAccumulator) toDetected() DetectedLevel {
	return DetectedLevel{
		Marker:   a.marker,
		Depth:    a.depth,
		Count:    a.count,
		Prefix:   a.dominantPrefix(),
		Examples: a.names,
	}
}

// detectHeading checks if a line is a markdown heading.
// Returns (depth, name, matched).
func detectHeading(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}

	depth := 0
	for depth < len(line) && line[depth] == '#' {
		depth++
	}
	if depth > 6 || depth >= len(line) || line[depth] != ' ' {
		return 0, "", false
	}

	name := strings.TrimSpace(line[depth+1:])
	if name == "" {
		return 0, "", false
	}
	return depth, name, true
}

// detectChecklist checks if a line is a checklist item.
// Returns (indent depth in spaces/2, name, matched).
func detectChecklist(line string) (int, string, bool) {
	// Count leading spaces
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}

	rest := line[indent:]

	// Must start with "- ["
	after, ok := strings.CutPrefix(rest, "- [")
	if !ok {
		return 0, "", false
	}

	// Find closing bracket
	closeBracket := strings.Index(after, "]")
	if closeBracket < 0 {
		return 0, "", false
	}

	// Check bracket contents
	inside := strings.TrimSpace(after[:closeBracket])
	switch inside {
	case "", "x", "X", "✓", "✔":
		// valid
	default:
		return 0, "", false
	}

	name := strings.TrimSpace(after[closeBracket+1:])
	if name == "" {
		return 0, "", false
	}

	return indent / 2, name, true
}

// detectNumbered checks if a line is a numbered list item.
// Returns (indent depth in spaces/2, name, matched).
func detectNumbered(line string) (int, string, bool) {
	// Count leading spaces
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}

	rest := line[indent:]

	// Must start with digit(s)
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(rest) {
		return 0, "", false
	}

	// Must be followed by . or )
	if rest[i] != '.' && rest[i] != ')' {
		return 0, "", false
	}

	name := strings.TrimSpace(rest[i+1:])
	if name == "" {
		return 0, "", false
	}

	return indent / 2, name, true
}

// buildLevels converts accumulated data into ordered DetectedLevels.
func (r *DetectionResult) buildLevels(
	headings map[int]*levelAccumulator,
	checklists []*levelAccumulator,
	numbered []*levelAccumulator,
) {
	// Add headings in order of depth (H1, H2, H3...)
	for depth := 1; depth <= 6; depth++ {
		if acc, ok := headings[depth]; ok {
			r.Levels = append(r.Levels, acc.toDetected())
		}
	}

	// Add checklists
	for _, acc := range checklists {
		r.Levels = append(r.Levels, acc.toDetected())
	}

	// Add numbered lists
	for _, acc := range numbered {
		r.Levels = append(r.Levels, acc.toDetected())
	}
}

// buildSchema generates a PlanSchema from detected levels.
func (r *DetectionResult) buildSchema(filePath string) {
	if len(r.Levels) == 0 {
		r.Ambiguities = append(r.Ambiguities, "No structured content found (no headings, checklists, or numbered lists).")
		return
	}

	// Separate headings from list-type markers
	var headingLevels []DetectedLevel
	var listLevels []DetectedLevel

	for _, level := range r.Levels {
		switch level.Marker {
		case MarkerHeading:
			headingLevels = append(headingLevels, level)
		default:
			listLevels = append(listLevels, level)
		}
	}

	// Check for ambiguities
	r.checkAmbiguities(headingLevels, listLevels)

	// Build schema levels
	var schemaLevels []LevelConfig

	// Filter headings: skip H1 if there's only one (likely a title)
	filteredHeadings := filterHeadings(headingLevels)

	// Assign names based on position in hierarchy
	headingNames := assignHeadingNames(filteredHeadings)

	for i, h := range filteredHeadings {
		cfg := LevelConfig{
			Name:   headingNames[i],
			Marker: MarkerHeading,
			Depth:  h.Depth,
			Prefix: h.Prefix,
		}
		schemaLevels = append(schemaLevels, cfg)
	}

	// Add the best list-type level (prefer checklists, then numbered)
	if len(listLevels) > 0 {
		best := pickBestListLevel(listLevels)
		name := "item"
		// Avoid name collision
		for _, cfg := range schemaLevels {
			if cfg.Name == name {
				name = "task"
				break
			}
		}
		schemaLevels = append(schemaLevels, LevelConfig{
			Name:   name,
			Marker: best.Marker,
			Depth:  best.Depth,
		})
	}

	if len(schemaLevels) == 0 {
		r.Ambiguities = append(r.Ambiguities, "Could not determine a meaningful hierarchy from the detected structure.")
		return
	}

	r.Suggested = &PlanSchema{
		Schema: SchemaVersion,
		File:   filePath,
		Levels: schemaLevels,
	}
}

// filterHeadings removes a single H1 (title line) from the heading list.
func filterHeadings(headings []DetectedLevel) []DetectedLevel {
	if len(headings) == 0 {
		return nil
	}

	// If there's an H1 with count 1, it's the document title — skip it
	var filtered []DetectedLevel
	for _, h := range headings {
		if h.Depth == 1 && h.Count == 1 {
			continue
		}
		filtered = append(filtered, h)
	}
	return filtered
}

// assignHeadingNames picks names like "phase", "step", "section" based on
// heading count and position in the hierarchy.
func assignHeadingNames(headings []DetectedLevel) []string {
	names := make([]string, len(headings))
	available := []string{"phase", "section", "step", "substep"}

	for i := range headings {
		if i < len(available) {
			names[i] = available[i]
		} else {
			names[i] = fmt.Sprintf("level%d", i+1)
		}
	}

	// If there's only one heading level, call it "section" not "phase"
	if len(headings) == 1 {
		names[0] = "section"
	}

	return names
}

// pickBestListLevel chooses the most useful list level.
// Prefers checklists (they carry status), then numbered (ordered), then bullet.
func pickBestListLevel(levels []DetectedLevel) DetectedLevel {
	// Prefer checklists at depth 0
	for _, l := range levels {
		if l.Marker == MarkerChecklist && l.Depth == 0 {
			return l
		}
	}
	// Then any checklist
	for _, l := range levels {
		if l.Marker == MarkerChecklist {
			return l
		}
	}
	// Then numbered at depth 0
	for _, l := range levels {
		if l.Marker == MarkerNumbered && l.Depth == 0 {
			return l
		}
	}
	// Whatever's first
	return levels[0]
}

// checkAmbiguities identifies cases where the structure isn't clear.
func (r *DetectionResult) checkAmbiguities(headings, lists []DetectedLevel) {
	// Many heading levels — hard to pick the right hierarchy
	if len(headings) > 3 {
		depths := make([]string, len(headings))
		for i, h := range headings {
			depths[i] = fmt.Sprintf("H%d (%d found)", h.Depth, h.Count)
		}
		r.Ambiguities = append(r.Ambiguities,
			fmt.Sprintf("Found %d heading levels: %s. Which levels represent your plan hierarchy?",
				len(headings), strings.Join(depths, ", ")))
	}

	// Multiple list types at same indent
	if len(lists) > 1 {
		markers := make([]string, len(lists))
		for i, l := range lists {
			markers[i] = fmt.Sprintf("%s at depth %d (%d found)", l.Marker, l.Depth, l.Count)
		}
		r.Ambiguities = append(r.Ambiguities,
			fmt.Sprintf("Found multiple list types: %s. Which should be tracked as work items?",
				strings.Join(markers, ", ")))
	}

	// No list items — headings only
	if len(lists) == 0 && len(headings) > 0 {
		r.Ambiguities = append(r.Ambiguities,
			"No checklist or numbered list items found. The plan uses headings only — work units will be heading-level only.")
	}
}

// FormatDetection returns a human-readable summary of what was detected.
func FormatDetection(d *DetectionResult) string {
	var sb strings.Builder

	sb.WriteString("Detected structure in ")
	sb.WriteString(d.File)
	sb.WriteString(":\n")

	for _, level := range d.Levels {
		sb.WriteString(fmt.Sprintf("  %s (depth %d): %d items",
			level.Marker, level.Depth, level.Count))
		if level.Prefix != "" {
			sb.WriteString(fmt.Sprintf(", prefix %q", level.Prefix))
		}
		if len(level.Examples) > 0 {
			sb.WriteString(" — e.g. ")
			for i, ex := range level.Examples {
				if i > 0 {
					sb.WriteString(", ")
				}
				if len(ex) > 50 {
					ex = ex[:47] + "..."
				}
				sb.WriteString(fmt.Sprintf("%q", ex))
			}
		}
		sb.WriteString("\n")
	}

	if d.Suggested != nil {
		sb.WriteString(fmt.Sprintf("\nSuggested schema: %d levels", len(d.Suggested.Levels)))
		for _, l := range d.Suggested.Levels {
			sb.WriteString(fmt.Sprintf("\n  %s: %s depth=%d", l.Name, l.Marker, l.Depth))
			if l.Prefix != "" {
				sb.WriteString(fmt.Sprintf(" prefix=%q", l.Prefix))
			}
		}
	}

	return sb.String()
}
