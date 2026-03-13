package tools

import (
	"fmt"
	"os"
	"strings"
)

type CSExtractor struct{}

func (e *CSExtractor) Extensions() []string {
	return []string{".cs"}
}

func (e *CSExtractor) Extract(filePath string, exportedOnly bool) (*FileShape, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	s := &csScanner{src: string(data), line: 1, exportedOnly: exportedOnly}
	shape := &FileShape{File: filePath}
	s.parseBody(shape)
	return shape, nil
}

// Scanner

type csScanner struct {
	src          string
	pos          int
	line         int
	exportedOnly bool
	namespace    string
}

func (s *csScanner) eof() bool { return s.pos >= len(s.src) }

func (s *csScanner) peek() byte {
	if s.eof() {
		return 0
	}
	return s.src[s.pos]
}

func (s *csScanner) advance() {
	if s.pos < len(s.src) {
		if s.src[s.pos] == '\n' {
			s.line++
		}
		s.pos++
	}
}

func (s *csScanner) skipWhitespace() {
	for s.pos < len(s.src) {
		ch := s.src[s.pos]
		if ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' {
			return
		}
		if ch == '\n' {
			s.line++
		}
		s.pos++
	}
}

func (s *csScanner) skipLineComment() {
	s.pos += 2
	for s.pos < len(s.src) && s.src[s.pos] != '\n' {
		s.pos++
	}
}

func (s *csScanner) skipBlockComment() {
	s.pos += 2
	for s.pos+1 < len(s.src) {
		if s.src[s.pos] == '\n' {
			s.line++
		}
		if s.src[s.pos] == '*' && s.src[s.pos+1] == '/' {
			s.pos += 2
			return
		}
		s.pos++
	}
	s.pos = len(s.src)
}

func (s *csScanner) atCommentStart() bool {
	return s.pos+1 < len(s.src) && s.src[s.pos] == '/' &&
		(s.src[s.pos+1] == '/' || s.src[s.pos+1] == '*')
}

func (s *csScanner) skipComment() {
	if s.src[s.pos+1] == '/' {
		s.skipLineComment()
	} else {
		s.skipBlockComment()
	}
}

func (s *csScanner) skipWhitespaceAndComments() {
	for !s.eof() {
		s.skipWhitespace()
		if s.eof() {
			return
		}
		if s.atCommentStart() {
			s.skipComment()
			continue
		}
		return
	}
}

func (s *csScanner) readWord() string {
	start := s.pos
	for s.pos < len(s.src) && csIsIdentChar(s.src[s.pos]) {
		s.pos++
	}
	return s.src[start:s.pos]
}

func (s *csScanner) peekWord() string {
	saved := s.pos
	word := s.readWord()
	s.pos = saved
	return word
}

func csIsIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func csIsIdentChar(ch byte) bool {
	return csIsIdentStart(ch) || (ch >= '0' && ch <= '9')
}

// String handling

func (s *csScanner) skipStringLiteral() {
	ch := s.peek()

	if ch == '@' || ch == '$' {
		s.skipPrefixedString()
		return
	}

	if ch == '\'' {
		s.advance()
		for !s.eof() {
			if s.peek() == '\\' {
				s.advance()
				if !s.eof() {
					s.advance()
				}
				continue
			}
			if s.peek() == '\'' {
				s.advance()
				return
			}
			s.advance()
		}
		return
	}

	if ch == '"' {
		if s.pos+2 < len(s.src) && s.src[s.pos+1] == '"' && s.src[s.pos+2] == '"' {
			s.skipRawString()
			return
		}
		s.advance()
		for !s.eof() {
			if s.peek() == '\\' {
				s.advance()
				if !s.eof() {
					s.advance()
				}
				continue
			}
			if s.peek() == '"' {
				s.advance()
				return
			}
			if s.peek() == '\n' {
				return
			}
			s.advance()
		}
	}
}

func (s *csScanner) skipPrefixedString() {
	isVerbatim := false
	isInterpolated := false

	for !s.eof() && (s.peek() == '@' || s.peek() == '$') {
		if s.peek() == '@' {
			isVerbatim = true
		}
		if s.peek() == '$' {
			isInterpolated = true
		}
		s.advance()
	}

	if s.eof() || s.peek() != '"' {
		return
	}

	if s.pos+2 < len(s.src) && s.src[s.pos+1] == '"' && s.src[s.pos+2] == '"' {
		s.skipRawString()
		return
	}

	s.advance() // opening "
	depth := 0

	for !s.eof() {
		ch := s.peek()

		if isVerbatim {
			if ch == '"' {
				if s.pos+1 < len(s.src) && s.src[s.pos+1] == '"' {
					s.advance()
					s.advance()
					continue
				}
				s.advance()
				return
			}
		} else {
			if ch == '\\' {
				s.advance()
				if !s.eof() {
					s.advance()
				}
				continue
			}
			if ch == '"' && depth == 0 {
				s.advance()
				return
			}
			if ch == '\n' && depth == 0 {
				return
			}
		}

		if isInterpolated {
			if ch == '{' {
				if s.pos+1 < len(s.src) && s.src[s.pos+1] == '{' {
					s.advance()
					s.advance()
					continue
				}
				depth++
			}
			if ch == '}' {
				if depth > 0 {
					depth--
				} else if s.pos+1 < len(s.src) && s.src[s.pos+1] == '}' {
					s.advance()
					s.advance()
					continue
				}
			}
		}
		s.advance()
	}
}

func (s *csScanner) skipRawString() {
	quoteCount := 0
	for s.pos < len(s.src) && s.src[s.pos] == '"' {
		quoteCount++
		s.advance()
	}
	for !s.eof() {
		if s.peek() == '"' {
			count := 0
			for s.pos+count < len(s.src) && s.src[s.pos+count] == '"' {
				count++
			}
			if count >= quoteCount {
				for range quoteCount {
					s.advance()
				}
				return
			}
		}
		s.advance()
	}
}

func (s *csScanner) skipStringOrComment() bool {
	ch := s.peek()
	if ch == '"' || ch == '\'' {
		s.skipStringLiteral()
		return true
	}
	if ch == '@' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '"' {
		s.skipStringLiteral()
		return true
	}
	if ch == '$' && s.pos+1 < len(s.src) && (s.src[s.pos+1] == '"' || s.src[s.pos+1] == '@') {
		s.skipStringLiteral()
		return true
	}
	if s.atCommentStart() {
		s.skipComment()
		return true
	}
	return false
}

// Block and balanced reading

func (s *csScanner) skipBlockContent() {
	depth := 1
	for !s.eof() && depth > 0 {
		if s.skipStringOrComment() {
			continue
		}
		ch := s.src[s.pos]
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
		}
		s.advance()
	}
}

func (s *csScanner) readBalanced(open, close byte) string {
	if s.eof() || s.src[s.pos] != open {
		return ""
	}
	start := s.pos
	s.advance()
	depth := 1
	for !s.eof() && depth > 0 {
		if s.skipStringOrComment() {
			continue
		}
		ch := s.src[s.pos]
		if ch == open {
			depth++
		} else if ch == close {
			depth--
		}
		s.advance()
	}
	return s.src[start:s.pos]
}

func (s *csScanner) readAngleBrackets() string {
	if s.eof() || s.src[s.pos] != '<' {
		return ""
	}
	start := s.pos
	s.advance()
	depth := 1
	for !s.eof() && depth > 0 {
		if s.skipStringOrComment() {
			continue
		}
		ch := s.src[s.pos]
		switch {
		case ch == '(':
			s.readBalanced('(', ')')
		case ch == '[':
			s.readBalanced('[', ']')
		case ch == '{':
			s.readBalanced('{', '}')
		case ch == '<':
			depth++
			s.advance()
		case ch == '>':
			if s.pos > 0 && s.src[s.pos-1] == '=' {
				s.advance()
			} else {
				depth--
				s.advance()
			}
		default:
			s.advance()
		}
	}
	return s.src[start:s.pos]
}

func (s *csScanner) skipStatement() {
	depth := 0
	for !s.eof() {
		if s.skipStringOrComment() {
			continue
		}
		ch := s.src[s.pos]
		switch ch {
		case '{':
			depth++
			s.advance()
		case '}':
			if depth > 0 {
				depth--
				s.advance()
				if depth == 0 {
					return
				}
			} else {
				return
			}
		case ';':
			if depth == 0 {
				s.advance()
				return
			}
			s.advance()
		default:
			s.advance()
		}
	}
}

func (s *csScanner) skipMember() {
	s.skipStatement()
}

func (s *csScanner) skipAttribute() {
	s.readBalanced('[', ']')
	s.skipWhitespaceAndComments()
}

func (s *csScanner) skipPreprocessor() {
	for !s.eof() && s.peek() != '\n' {
		s.advance()
	}
}

func (s *csScanner) skipBody() {
	s.skipWhitespaceAndComments()
	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.skipBlockContent()
	} else if s.pos+1 < len(s.src) && s.peek() == '=' && s.src[s.pos+1] == '>' {
		s.advance()
		s.advance()
		s.skipStatement()
	} else if !s.eof() && s.peek() == ';' {
		s.advance()
	}
}

// Type reading

func (s *csScanner) readCSharpType() string {
	start := s.pos

	word := s.peekWord()
	if word == "ref" || word == "in" || word == "out" || word == "params" || word == "scoped" {
		s.readWord()
		s.skipWhitespaceAndComments()
		if s.peekWord() == "readonly" {
			s.readWord()
			s.skipWhitespaceAndComments()
		}
	}

	// Tuple
	if s.peek() == '(' {
		s.readBalanced('(', ')')
		s.csTypeSuffix()
		return strings.TrimSpace(s.src[start:s.pos])
	}

	ident := s.readWord()
	if ident == "" {
		return ""
	}

	s.skipWhitespaceAndComments()

	if !s.eof() && s.peek() == '<' {
		s.readAngleBrackets()
		s.skipWhitespaceAndComments()
	}

	for !s.eof() && s.peek() == '.' {
		s.advance()
		s.skipWhitespaceAndComments()
		s.readWord()
		s.skipWhitespaceAndComments()
		if !s.eof() && s.peek() == '<' {
			s.readAngleBrackets()
			s.skipWhitespaceAndComments()
		}
	}

	s.csTypeSuffix()
	return strings.TrimSpace(s.src[start:s.pos])
}

func (s *csScanner) csTypeSuffix() {
	for !s.eof() {
		s.skipWhitespaceAndComments()
		if s.peek() == '?' {
			s.advance()
			continue
		}
		if s.peek() == '*' {
			s.advance()
			continue
		}
		if s.peek() == '[' {
			saved := s.pos
			s.advance()
			isArray := true
			for !s.eof() {
				ch := s.peek()
				if ch == ']' {
					s.advance()
					break
				}
				if ch != ',' {
					isArray = false
					break
				}
				s.advance()
			}
			if !isArray {
				s.pos = saved
			}
			continue
		}
		break
	}
}

// Modifier tracking

type csModifiers struct {
	hasPublic, hasProtected, hasPrivate, hasInternal bool
	hasStatic, hasAbstract, hasConst, hasReadonly    bool
	hasAsync, hasNew, hasVirtual, hasOverride        bool
	hasSealed, hasPartial, hasExtern, hasUnsafe      bool
	hasVolatile, hasFile, hasRequired                bool
}

func (m csModifiers) isTopLevelVisible() bool {
	return m.hasPublic
}

func (m csModifiers) isMemberVisible() bool {
	if m.hasPublic {
		return true
	}
	if m.hasProtected && !m.hasPrivate {
		return true
	}
	return false
}

func (s *csScanner) readModifiers() csModifiers {
	var m csModifiers
	for {
		s.skipWhitespaceAndComments()
		word := s.peekWord()
		switch word {
		case "public":
			m.hasPublic = true
		case "protected":
			m.hasProtected = true
		case "private":
			m.hasPrivate = true
		case "internal":
			m.hasInternal = true
		case "static":
			m.hasStatic = true
		case "abstract":
			m.hasAbstract = true
		case "sealed":
			m.hasSealed = true
		case "virtual":
			m.hasVirtual = true
		case "override":
			m.hasOverride = true
		case "readonly":
			m.hasReadonly = true
		case "const":
			m.hasConst = true
		case "async":
			m.hasAsync = true
		case "new":
			m.hasNew = true
		case "partial":
			m.hasPartial = true
		case "extern":
			m.hasExtern = true
		case "unsafe":
			m.hasUnsafe = true
		case "volatile":
			m.hasVolatile = true
		case "file":
			m.hasFile = true
		case "required":
			m.hasRequired = true
		default:
			return m
		}
		s.readWord()
	}
}

// Where clauses

func (s *csScanner) readWhereClauses() string {
	if s.peekWord() != "where" {
		return ""
	}
	start := s.pos
	for s.peekWord() == "where" {
		s.readWord()
		s.skipWhitespaceAndComments()
		for !s.eof() {
			if s.skipStringOrComment() {
				continue
			}
			ch := s.peek()
			if ch == '{' || ch == ';' {
				break
			}
			if ch == '=' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '>' {
				break
			}
			if csIsIdentStart(ch) {
				w := s.peekWord()
				if w == "where" {
					break
				}
			}
			s.advance()
		}
	}
	return strings.TrimSpace(s.src[start:s.pos])
}

// Heritage clause (base class / interfaces after :)

func (s *csScanner) readHeritageName() string {
	start := s.pos
	depth := 0
	for !s.eof() {
		ch := s.peek()
		if depth == 0 {
			if ch == ',' || ch == '{' || ch == ';' {
				break
			}
			if csIsIdentStart(ch) {
				w := s.peekWord()
				if w == "where" {
					break
				}
			}
		}
		switch ch {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		}
		s.advance()
	}
	return strings.TrimSpace(s.src[start:s.pos])
}

func (s *csScanner) parseHeritageList(td *TypeDef) {
	if s.peek() != ':' {
		return
	}
	s.advance()
	s.skipWhitespaceAndComments()

	for {
		name := s.readHeritageName()
		if name != "" {
			td.Embeds = append(td.Embeds, name)
		}
		s.skipWhitespaceAndComments()
		if s.peek() == ',' {
			s.advance()
			s.skipWhitespaceAndComments()
			continue
		}
		break
	}
}

// Property summary

func (s *csScanner) readPropertySummary() string {
	s.advance() // skip opening {
	depth := 1
	var accessors []string

	for !s.eof() && depth > 0 {
		if s.skipStringOrComment() {
			continue
		}
		ch := s.peek()
		if ch == '{' {
			depth++
			s.advance()
		} else if ch == '}' {
			depth--
			s.advance()
		} else if depth == 1 && csIsIdentStart(ch) {
			word := s.readWord()
			switch word {
			case "private", "protected", "internal":
				s.skipWhitespaceAndComments()
				next := s.peekWord()
				if next == "get" || next == "set" || next == "init" {
					s.readWord()
					accessors = append(accessors, word+" "+next)
				}
			case "get", "set", "init", "add", "remove":
				accessors = append(accessors, word)
			}
		} else {
			s.advance()
		}
	}

	if len(accessors) == 0 {
		return "{ }"
	}
	return "{ " + strings.Join(accessors, "; ") + "; }"
}

// Top-level parsing

func (s *csScanner) parseBody(shape *FileShape) {
	for !s.eof() {
		savedPos := s.pos
		s.skipWhitespaceAndComments()
		if s.eof() {
			break
		}

		if s.peek() == '#' {
			s.skipPreprocessor()
			continue
		}

		if s.peek() == '[' {
			s.skipAttribute()
			continue
		}

		if s.peek() == '}' {
			s.advance()
			return
		}

		if s.peek() == ';' {
			s.advance()
			continue
		}

		line := s.line
		word := s.peekWord()

		switch word {
		case "using":
			s.readWord()
			path := s.parseUsing()
			if path != "" {
				shape.Imports = append(shape.Imports, path)
			}
		case "namespace":
			s.readWord()
			s.parseNamespace(shape)
		case "":
			s.advance()
		default:
			s.parseTopLevelDecl(shape, line)
		}

		if s.pos == savedPos {
			s.advance()
		}
	}
}

func (s *csScanner) parseUsing() string {
	s.skipWhitespaceAndComments()
	start := s.pos
	for !s.eof() && s.peek() != ';' {
		if s.skipStringOrComment() {
			continue
		}
		s.advance()
	}
	result := strings.TrimSpace(s.src[start:s.pos])
	if !s.eof() && s.peek() == ';' {
		s.advance()
	}
	return result
}

func (s *csScanner) parseNamespace(shape *FileShape) {
	s.skipWhitespaceAndComments()

	start := s.pos
	for !s.eof() && s.peek() != '{' && s.peek() != ';' {
		s.advance()
	}
	nsName := strings.TrimSpace(s.src[start:s.pos])

	if s.namespace != "" {
		s.namespace = s.namespace + "." + nsName
	} else {
		s.namespace = nsName
	}
	if shape.Package == "" {
		shape.Package = s.namespace
	}

	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.parseBody(shape)
	} else if !s.eof() && s.peek() == ';' {
		s.advance() // file-scoped namespace
	}
}

func (s *csScanner) parseTopLevelDecl(shape *FileShape, line int) {
	mods := s.readModifiers()

	word := s.peekWord()

	// ref struct
	if word == "ref" {
		saved := s.pos
		savedLine := s.line
		s.readWord()
		s.skipWhitespaceAndComments()
		if s.peekWord() == "struct" {
			if s.exportedOnly && !mods.isTopLevelVisible() {
				s.skipStatement()
				return
			}
			s.parseClassOrStruct(shape, line, "struct")
			return
		}
		s.pos = saved
		s.line = savedLine
	}

	if s.exportedOnly && !mods.isTopLevelVisible() && word != "" {
		s.skipStatement()
		return
	}

	switch word {
	case "class", "struct":
		s.parseClassOrStruct(shape, line, word)
	case "record":
		s.parseRecord(shape, line)
	case "interface":
		s.parseInterface(shape, line)
	case "enum":
		s.parseEnum(shape, line)
	case "delegate":
		s.parseDelegate(shape, line)
	default:
		s.skipStatement()
	}
}

// Type declaration parsers

func (s *csScanner) parseClassOrStruct(shape *FileShape, line int, kind string) {
	s.readWord() // class or struct
	s.skipWhitespaceAndComments()

	name := s.readWord()
	if name == "" {
		s.skipStatement()
		return
	}

	td := TypeDef{
		Name: name,
		Kind: kind,
		Line: line,
	}

	s.skipWhitespaceAndComments()

	if !s.eof() && s.peek() == '<' {
		td.Name += s.readAngleBrackets()
		s.skipWhitespaceAndComments()
	}

	s.parseHeritageList(&td)
	s.skipWhitespaceAndComments()

	where := s.readWhereClauses()
	if where != "" {
		td.Name += " " + where
	}
	s.skipWhitespaceAndComments()

	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.parseClassMembers(shape, &td, name)
	} else if !s.eof() && s.peek() == ';' {
		s.advance()
	}

	shape.Types = append(shape.Types, td)
}

func (s *csScanner) parseRecord(shape *FileShape, line int) {
	s.readWord() // record
	s.skipWhitespaceAndComments()

	kind := "record"
	next := s.peekWord()
	if next == "class" || next == "struct" {
		kind = "record " + next
		s.readWord()
		s.skipWhitespaceAndComments()
	}

	name := s.readWord()
	if name == "" {
		s.skipStatement()
		return
	}

	td := TypeDef{
		Name: name,
		Kind: kind,
		Line: line,
	}

	s.skipWhitespaceAndComments()

	if !s.eof() && s.peek() == '<' {
		td.Name += s.readAngleBrackets()
		s.skipWhitespaceAndComments()
	}

	// Positional parameters
	if !s.eof() && s.peek() == '(' {
		params := s.readBalanced('(', ')')
		inner := params[1 : len(params)-1]
		s.parseRecordParams(inner, &td)
		s.skipWhitespaceAndComments()
	}

	s.parseHeritageList(&td)
	s.skipWhitespaceAndComments()

	where := s.readWhereClauses()
	if where != "" {
		td.Name += " " + where
	}
	s.skipWhitespaceAndComments()

	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.parseClassMembers(shape, &td, name)
	} else if !s.eof() && s.peek() == ';' {
		s.advance()
	}

	shape.Types = append(shape.Types, td)
}

func (s *csScanner) parseRecordParams(paramStr string, td *TypeDef) {
	ps := &csScanner{src: paramStr, line: 1}
	for !ps.eof() {
		ps.skipWhitespaceAndComments()
		if ps.eof() {
			break
		}

		if ps.peek() == '[' {
			ps.skipAttribute()
			continue
		}

		typeName := ps.readCSharpType()
		if typeName == "" {
			break
		}
		ps.skipWhitespaceAndComments()
		paramName := ps.readWord()

		if paramName != "" {
			td.Fields = append(td.Fields, FieldDef{
				Name: paramName,
				Type: typeName,
				Tag:  "{ get; init; }",
			})
		}

		ps.skipWhitespaceAndComments()
		// skip default value
		if !ps.eof() && ps.peek() == '=' {
			for !ps.eof() && ps.peek() != ',' {
				ps.advance()
			}
		}
		if !ps.eof() && ps.peek() == ',' {
			ps.advance()
		}
	}
}

func (s *csScanner) parseInterface(shape *FileShape, line int) {
	s.readWord() // interface
	s.skipWhitespaceAndComments()

	name := s.readWord()
	if name == "" {
		s.skipStatement()
		return
	}

	td := TypeDef{
		Name: name,
		Kind: "interface",
		Line: line,
	}

	s.skipWhitespaceAndComments()

	if !s.eof() && s.peek() == '<' {
		td.Name += s.readAngleBrackets()
		s.skipWhitespaceAndComments()
	}

	s.parseHeritageList(&td)
	s.skipWhitespaceAndComments()

	where := s.readWhereClauses()
	if where != "" {
		td.Name += " " + where
	}
	s.skipWhitespaceAndComments()

	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.parseInterfaceMembers(&td)
	}

	shape.Types = append(shape.Types, td)
}

func (s *csScanner) parseEnum(shape *FileShape, line int) {
	s.readWord() // enum
	s.skipWhitespaceAndComments()

	name := s.readWord()
	if name == "" {
		s.skipStatement()
		return
	}

	td := TypeDef{
		Name: name,
		Kind: "enum",
		Line: line,
	}

	s.skipWhitespaceAndComments()

	// Optional base type: enum Foo : byte
	if !s.eof() && s.peek() == ':' {
		s.advance()
		s.skipWhitespaceAndComments()
		baseType := s.readWord()
		if baseType != "" {
			td.Underlying = baseType
		}
		s.skipWhitespaceAndComments()
	}

	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.parseEnumMembers(&td)
	}

	shape.Types = append(shape.Types, td)
}

func (s *csScanner) parseDelegate(shape *FileShape, line int) {
	s.readWord() // delegate
	s.skipWhitespaceAndComments()

	returnType := s.readCSharpType()
	s.skipWhitespaceAndComments()

	name := s.readWord()
	if name == "" {
		s.skipStatement()
		return
	}

	s.skipWhitespaceAndComments()

	typeParams := ""
	if !s.eof() && s.peek() == '<' {
		typeParams = s.readAngleBrackets()
		s.skipWhitespaceAndComments()
	}

	params := ""
	if !s.eof() && s.peek() == '(' {
		params = s.readBalanced('(', ')')
		s.skipWhitespaceAndComments()
	}

	displayName := name
	if typeParams != "" {
		displayName += typeParams
	}

	where := s.readWhereClauses()
	underlying := params + ": " + returnType
	if where != "" {
		underlying += " " + where
	}

	shape.Types = append(shape.Types, TypeDef{
		Name:       displayName,
		Kind:       "delegate",
		Line:       line,
		Underlying: underlying,
	})

	if !s.eof() && s.peek() == ';' {
		s.advance()
	}
}

// Member parsers

func (s *csScanner) parseClassMembers(shape *FileShape, td *TypeDef, className string) {
	for !s.eof() {
		s.skipWhitespaceAndComments()
		if s.eof() || s.peek() == '}' {
			if !s.eof() {
				s.advance()
			}
			return
		}

		if s.peek() == '#' {
			s.skipPreprocessor()
			continue
		}
		if s.peek() == '[' {
			s.skipAttribute()
			continue
		}
		if s.peek() == ';' {
			s.advance()
			continue
		}

		line := s.line

		// Destructor
		if s.peek() == '~' {
			s.skipStatement()
			continue
		}

		mods := s.readModifiers()

		if s.exportedOnly && !mods.isMemberVisible() {
			// Check for nested types which use top-level visibility
			word := s.peekWord()
			if word == "class" || word == "struct" || word == "record" || word == "interface" || word == "enum" || word == "delegate" {
				if mods.isTopLevelVisible() {
					goto parseDecl
				}
			}
			s.skipMember()
			continue
		}

	parseDecl:
		word := s.peekWord()

		// Nested types
		switch word {
		case "class", "struct":
			s.parseClassOrStruct(shape, line, word)
			continue
		case "record":
			s.parseRecord(shape, line)
			continue
		case "interface":
			s.parseInterface(shape, line)
			continue
		case "enum":
			s.parseEnum(shape, line)
			continue
		case "delegate":
			s.parseDelegate(shape, line)
			continue
		case "event":
			s.readWord()
			s.parseEvent(td, line, mods)
			continue
		case "const":
			s.readWord()
			s.parseConstMember(td, line, mods)
			continue
		case "implicit", "explicit":
			s.skipStatement()
			continue
		case "ref":
			saved := s.pos
			savedLine := s.line
			s.readWord()
			s.skipWhitespaceAndComments()
			if s.peekWord() == "struct" {
				s.parseClassOrStruct(shape, line, "struct")
				continue
			}
			s.pos = saved
			s.line = savedLine
		}

		// Constructor check
		savedPos := s.pos
		savedLine := s.line
		firstWord := s.readWord()
		s.skipWhitespaceAndComments()

		if firstWord == className && s.peek() == '(' {
			s.parseConstructor(td, line, mods)
			continue
		}

		s.pos = savedPos
		s.line = savedLine
		s.parseMemberDecl(td, line, mods)
	}
}

func (s *csScanner) parseConstructor(td *TypeDef, line int, mods csModifiers) {
	var sig strings.Builder
	if !s.eof() && s.peek() == '(' {
		sig.WriteString(s.readBalanced('(', ')'))
	}

	td.Methods = append(td.Methods, FuncDef{
		Name:      "constructor",
		Signature: sig.String(),
		Line:      line,
	})

	s.skipWhitespaceAndComments()
	// : base(...) or : this(...)
	if !s.eof() && s.peek() == ':' {
		s.advance()
		s.skipWhitespaceAndComments()
		s.readWord() // base or this
		s.skipWhitespaceAndComments()
		if !s.eof() && s.peek() == '(' {
			s.readBalanced('(', ')')
		}
	}
	s.skipBody()
}

func (s *csScanner) parseEvent(td *TypeDef, line int, mods csModifiers) {
	s.skipWhitespaceAndComments()
	eventType := s.readCSharpType()
	s.skipWhitespaceAndComments()
	name := s.readWord()

	displayName := name
	if mods.hasStatic {
		displayName = "static " + name
	}

	td.Fields = append(td.Fields, FieldDef{
		Name: displayName,
		Type: "event " + eventType,
	})

	s.skipWhitespaceAndComments()
	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.skipBlockContent()
	} else if !s.eof() && s.peek() == ';' {
		s.advance()
	}
}

func (s *csScanner) parseConstMember(td *TypeDef, line int, mods csModifiers) {
	s.skipWhitespaceAndComments()
	constType := s.readCSharpType()
	s.skipWhitespaceAndComments()
	name := s.readWord()

	fd := FieldDef{Name: "const " + name, Type: constType}

	s.skipWhitespaceAndComments()
	if !s.eof() && s.peek() == '=' {
		s.advance()
		s.skipWhitespaceAndComments()
		start := s.pos
		for !s.eof() && s.peek() != ';' && s.peek() != ',' {
			if s.skipStringOrComment() {
				continue
			}
			s.advance()
		}
		fd.Tag = strings.TrimSpace(s.src[start:s.pos])
	}

	td.Fields = append(td.Fields, fd)

	if !s.eof() && s.peek() == ';' {
		s.advance()
	}
}

func (s *csScanner) parseMemberDecl(td *TypeDef, line int, mods csModifiers) {
	returnType := s.readCSharpType()
	if returnType == "" {
		s.skipMember()
		return
	}

	s.skipWhitespaceAndComments()

	// Indexer: this[
	if s.peekWord() == "this" && !s.eof() {
		saved := s.pos
		s.readWord()
		s.skipWhitespaceAndComments()
		if !s.eof() && s.peek() == '[' {
			params := s.readBalanced('[', ']')
			s.skipWhitespaceAndComments()
			tag := ""
			if !s.eof() && s.peek() == '{' {
				tag = s.readPropertySummary()
			} else {
				s.skipBody()
			}
			td.Fields = append(td.Fields, FieldDef{
				Name: "this" + params,
				Type: returnType,
				Tag:  tag,
			})
			return
		}
		s.pos = saved
	}

	// Operator
	if s.peekWord() == "operator" {
		s.skipStatement()
		return
	}

	name := s.readWord()
	if name == "" {
		s.skipMember()
		return
	}

	s.skipWhitespaceAndComments()

	displayName := name
	if mods.hasStatic {
		displayName = "static " + name
	}

	// Generic method
	typeParams := ""
	if !s.eof() && s.peek() == '<' {
		typeParams = s.readAngleBrackets()
		s.skipWhitespaceAndComments()
	}

	// Method
	if !s.eof() && s.peek() == '(' {
		params := s.readBalanced('(', ')')
		s.skipWhitespaceAndComments()

		sig := typeParams + params

		where := s.readWhereClauses()
		if where != "" {
			sig += " " + where
		}

		td.Methods = append(td.Methods, FuncDef{
			Name:      displayName,
			Signature: sig + ": " + returnType,
			Line:      line,
		})
		s.skipBody()
		return
	}

	// Property: { get; set; }
	if !s.eof() && s.peek() == '{' {
		tag := s.readPropertySummary()
		td.Fields = append(td.Fields, FieldDef{
			Name: displayName,
			Type: returnType,
			Tag:  tag,
		})
		// Handle default value after property: { get; set; } = value;
		s.skipWhitespaceAndComments()
		if !s.eof() && s.peek() == '=' {
			s.skipStatement()
		} else if !s.eof() && s.peek() == ';' {
			s.advance()
		}
		return
	}

	// Expression-bodied property or method: =>
	if s.pos+1 < len(s.src) && s.peek() == '=' && s.src[s.pos+1] == '>' {
		s.advance()
		s.advance()
		s.skipStatement()
		td.Fields = append(td.Fields, FieldDef{
			Name: displayName,
			Type: returnType,
			Tag:  "{ get; }",
		})
		return
	}

	// Field
	fd := FieldDef{Name: displayName, Type: returnType}
	if !s.eof() && s.peek() == '=' {
		s.advance()
		s.skipStatement()
	} else if !s.eof() && s.peek() == ';' {
		s.advance()
	} else if !s.eof() && s.peek() == ',' {
		// Multiple field declarations: int x, y, z;
		for !s.eof() && s.peek() != ';' {
			s.advance()
		}
		if !s.eof() {
			s.advance()
		}
	} else {
		s.skipMember()
	}
	td.Fields = append(td.Fields, fd)
}

func (s *csScanner) parseInterfaceMembers(td *TypeDef) {
	for !s.eof() {
		s.skipWhitespaceAndComments()
		if s.eof() || s.peek() == '}' {
			if !s.eof() {
				s.advance()
			}
			return
		}

		if s.peek() == '#' {
			s.skipPreprocessor()
			continue
		}
		if s.peek() == '[' {
			s.skipAttribute()
			continue
		}
		if s.peek() == ';' {
			s.advance()
			continue
		}

		line := s.line

		// Skip modifiers (interface members can have static, new, etc. in C# 8+)
		mods := s.readModifiers()

		// Nested types in interfaces (C# 8+)
		word := s.peekWord()
		if word == "event" {
			s.readWord()
			s.parseEvent(td, line, mods)
			continue
		}

		returnType := s.readCSharpType()
		if returnType == "" {
			s.advance()
			continue
		}

		s.skipWhitespaceAndComments()

		// Indexer
		if s.peekWord() == "this" {
			saved := s.pos
			s.readWord()
			s.skipWhitespaceAndComments()
			if !s.eof() && s.peek() == '[' {
				params := s.readBalanced('[', ']')
				s.skipWhitespaceAndComments()
				tag := ""
				if !s.eof() && s.peek() == '{' {
					tag = s.readPropertySummary()
				} else {
					s.skipBody()
				}
				td.Fields = append(td.Fields, FieldDef{
					Name: "this" + params,
					Type: returnType,
					Tag:  tag,
				})
				continue
			}
			s.pos = saved
		}

		name := s.readWord()
		if name == "" {
			s.advance()
			continue
		}

		s.skipWhitespaceAndComments()

		displayName := name
		if mods.hasStatic {
			displayName = "static " + name
		}

		typeParams := ""
		if !s.eof() && s.peek() == '<' {
			typeParams = s.readAngleBrackets()
			s.skipWhitespaceAndComments()
		}

		// Method
		if !s.eof() && s.peek() == '(' {
			params := s.readBalanced('(', ')')
			s.skipWhitespaceAndComments()

			sig := typeParams + params
			where := s.readWhereClauses()
			if where != "" {
				sig += " " + where
			}

			td.Methods = append(td.Methods, FuncDef{
				Name:      displayName,
				Signature: sig + ": " + returnType,
				Line:      line,
			})
			s.skipBody()
			continue
		}

		// Property
		if !s.eof() && s.peek() == '{' {
			tag := s.readPropertySummary()
			td.Fields = append(td.Fields, FieldDef{
				Name: displayName,
				Type: returnType,
				Tag:  tag,
			})
			s.skipWhitespaceAndComments()
			if !s.eof() && s.peek() == ';' {
				s.advance()
			}
			continue
		}

		s.skipBody()
	}
}

func (s *csScanner) parseEnumMembers(td *TypeDef) {
	for !s.eof() {
		s.skipWhitespaceAndComments()
		if s.eof() || s.peek() == '}' {
			if !s.eof() {
				s.advance()
			}
			return
		}

		if s.peek() == '#' {
			s.skipPreprocessor()
			continue
		}
		if s.peek() == '[' {
			s.skipAttribute()
			continue
		}
		if s.peek() == ',' {
			s.advance()
			continue
		}

		name := s.readWord()
		if name == "" {
			s.advance()
			continue
		}

		fd := FieldDef{Name: name}
		s.skipWhitespaceAndComments()

		if !s.eof() && s.peek() == '=' {
			s.advance()
			s.skipWhitespaceAndComments()
			start := s.pos
			depth := 0
			for !s.eof() {
				if s.skipStringOrComment() {
					continue
				}
				ch := s.peek()
				if depth == 0 && (ch == ',' || ch == '}') {
					break
				}
				if ch == '(' {
					depth++
				}
				if ch == ')' && depth > 0 {
					depth--
				}
				s.advance()
			}
			fd.Type = strings.TrimSpace(s.src[start:s.pos])
		}

		td.Fields = append(td.Fields, fd)

		s.skipWhitespaceAndComments()
		if !s.eof() && s.peek() == ',' {
			s.advance()
		}
	}
}
