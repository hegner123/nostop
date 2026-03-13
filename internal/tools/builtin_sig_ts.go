package tools

import (
	"fmt"
	"os"
	"strings"
)

type TSExtractor struct{}

func (e *TSExtractor) Extensions() []string {
	return []string{".ts", ".tsx", ".mts", ".cts"}
}

func (e *TSExtractor) Extract(filePath string, exportedOnly bool) (*FileShape, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	s := &tsScanner{src: string(data), line: 1, exportedOnly: exportedOnly}
	shape := &FileShape{File: filePath}
	s.parse(shape)
	return shape, nil
}

// Scanner

type tsScanner struct {
	src          string
	pos          int
	line         int
	exportedOnly bool
}

func (s *tsScanner) eof() bool { return s.pos >= len(s.src) }

func (s *tsScanner) peek() byte {
	if s.eof() {
		return 0
	}
	return s.src[s.pos]
}

func (s *tsScanner) advance() {
	if s.pos < len(s.src) {
		if s.src[s.pos] == '\n' {
			s.line++
		}
		s.pos++
	}
}

func (s *tsScanner) skipWhitespace() {
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

func (s *tsScanner) skipLineComment() {
	s.pos += 2 // skip //
	for s.pos < len(s.src) && s.src[s.pos] != '\n' {
		s.pos++
	}
}

func (s *tsScanner) skipBlockComment() {
	s.pos += 2 // skip /*
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

func (s *tsScanner) atCommentStart() bool {
	return s.pos+1 < len(s.src) && s.src[s.pos] == '/' &&
		(s.src[s.pos+1] == '/' || s.src[s.pos+1] == '*')
}

func (s *tsScanner) skipComment() {
	if s.src[s.pos+1] == '/' {
		s.skipLineComment()
	} else {
		s.skipBlockComment()
	}
}

func (s *tsScanner) skipWhitespaceAndComments() {
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

func tsIsIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || ch == '$'
}

func tsIsIdentChar(ch byte) bool {
	return tsIsIdentStart(ch) || (ch >= '0' && ch <= '9')
}

func (s *tsScanner) readWord() string {
	start := s.pos
	for s.pos < len(s.src) && tsIsIdentChar(s.src[s.pos]) {
		s.pos++
	}
	return s.src[start:s.pos]
}

func (s *tsScanner) peekWord() string {
	saved := s.pos
	word := s.readWord()
	s.pos = saved
	return word
}

func (s *tsScanner) skipStringLiteral() {
	quote := s.src[s.pos]
	s.advance()

	if quote == '`' {
		depth := 0
		for !s.eof() {
			ch := s.src[s.pos]
			if ch == '\\' {
				s.advance()
				if !s.eof() {
					s.advance()
				}
				continue
			}
			if ch == '$' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '{' {
				depth++
				s.advance()
				s.advance()
				continue
			}
			if ch == '{' && depth > 0 {
				depth++
			}
			if ch == '}' && depth > 0 {
				depth--
			}
			if ch == '`' && depth == 0 {
				s.advance()
				return
			}
			s.advance()
		}
		return
	}

	for !s.eof() {
		ch := s.src[s.pos]
		if ch == '\\' {
			s.advance()
			if !s.eof() {
				s.advance()
			}
			continue
		}
		if ch == quote {
			s.advance()
			return
		}
		if ch == '\n' {
			return
		}
		s.advance()
	}
}

func (s *tsScanner) skipStringOrComment() bool {
	ch := s.peek()
	if ch == '"' || ch == '\'' || ch == '`' {
		s.skipStringLiteral()
		return true
	}
	if s.atCommentStart() {
		s.skipComment()
		return true
	}
	return false
}

// skipBlockContent skips balanced {} content; opening { already consumed.
func (s *tsScanner) skipBlockContent() {
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

// readBalanced reads from the current bracket (inclusive) to its match (inclusive).
func (s *tsScanner) readBalanced(open, close byte) string {
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

// readAngleBrackets reads <...> handling => arrows and nested () [] {}.
func (s *tsScanner) readAngleBrackets() string {
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
				s.advance() // part of =>, not a closing bracket
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

// readTypeExpression reads a type up to ; { , = ) at depth 0.
// Handles => arrows so they don't prematurely terminate on =.
func (s *tsScanner) readTypeExpression() string {
	start := s.pos
	depth := 0

	for !s.eof() {
		if s.skipStringOrComment() {
			continue
		}
		ch := s.peek()

		// Handle => at any depth so > doesn't break angle bracket tracking
		if ch == '=' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '>' {
			s.advance()
			s.advance()
			continue
		}

		if depth == 0 && (ch == ';' || ch == '{' || ch == ',' || ch == '=' || ch == ')') {
			break
		}

		switch ch {
		case '(', '[', '<':
			depth++
		case ')', ']', '>':
			if depth > 0 {
				depth--
			}
		}
		s.advance()
	}
	return strings.TrimSpace(s.src[start:s.pos])
}

// readTypeBody reads a type expression for type alias bodies.
// Tracks {} depth so object types are fully consumed. Stops at ; at depth 0.
func (s *tsScanner) readTypeBody() string {
	start := s.pos
	depth := 0

	for !s.eof() {
		if s.skipStringOrComment() {
			continue
		}
		ch := s.peek()

		if ch == '=' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '>' {
			s.advance()
			s.advance()
			continue
		}

		if depth == 0 && ch == ';' {
			break
		}

		switch ch {
		case '(', '[', '<', '{':
			depth++
		case ')', ']', '>', '}':
			if depth > 0 {
				depth--
			}
		}
		s.advance()
	}
	return strings.TrimSpace(s.src[start:s.pos])
}

// readValueExpression reads a value expression tracking () [] {} depth.
func (s *tsScanner) readValueExpression() string {
	start := s.pos
	depth := 0

	for !s.eof() {
		if s.skipStringOrComment() {
			continue
		}
		ch := s.peek()

		if depth == 0 && ch == ';' {
			break
		}

		switch ch {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		}
		s.advance()
	}
	return strings.TrimSpace(s.src[start:s.pos])
}

func (s *tsScanner) skipStatement() {
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

func (s *tsScanner) skipClassMember() {
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
				return // outer }, don't consume
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

func (s *tsScanner) skipDecorator() {
	s.advance() // @
	s.skipWhitespaceAndComments()
	s.readWord()
	s.skipWhitespaceAndComments()
	for !s.eof() && s.peek() == '.' {
		s.advance()
		s.readWord()
		s.skipWhitespaceAndComments()
	}
	if !s.eof() && s.peek() == '(' {
		s.readBalanced('(', ')')
	}
}

// readHeritageName reads a type name in extends/implements context,
// stopping at , { ; or keywords extends/implements.
func (s *tsScanner) readHeritageName() string {
	start := s.pos
	depth := 0

	for !s.eof() {
		ch := s.peek()

		if depth == 0 {
			if ch == ',' || ch == '{' || ch == ';' {
				break
			}
			if tsIsIdentStart(ch) {
				word := s.peekWord()
				if word == "implements" || word == "extends" {
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

// Top-level parsing

func (s *tsScanner) parse(shape *FileShape) {
	for !s.eof() {
		savedPos := s.pos
		s.skipWhitespaceAndComments()
		if s.eof() {
			break
		}

		if s.peek() == '@' {
			s.skipDecorator()
			continue
		}

		line := s.line
		word := s.peekWord()

		switch word {
		case "import":
			s.readWord()
			path := s.parseImportPath()
			if path != "" {
				shape.Imports = append(shape.Imports, path)
			}

		case "export":
			s.readWord()
			s.skipWhitespaceAndComments()
			s.parseAfterExport(shape, line)

		case "":
			s.advance()

		default:
			if s.exportedOnly {
				s.skipStatement()
			} else {
				s.parseDeclaration(shape, line, word)
			}
		}

		if s.pos == savedPos {
			s.advance()
		}
	}
}

func (s *tsScanner) parseAfterExport(shape *FileShape, line int) {
	word := s.peekWord()

	switch word {
	case "default":
		s.readWord()
		s.skipWhitespaceAndComments()
		next := s.peekWord()
		if next == "" {
			s.skipStatement()
			return
		}
		s.parseDeclaration(shape, line, next)

	case "":
		// export { ... }, export *, export =
		s.skipStatement()

	default:
		s.parseDeclaration(shape, line, word)
	}
}

func (s *tsScanner) parseDeclaration(shape *FileShape, line int, word string) {
	switch word {
	case "declare":
		s.readWord()
		s.skipWhitespaceAndComments()
		next := s.peekWord()
		if next != "" {
			s.parseDeclaration(shape, line, next)
		} else {
			s.skipStatement()
		}

	case "abstract":
		s.readWord()
		s.skipWhitespaceAndComments()
		if s.peekWord() == "class" {
			s.parseClass(shape, line)
		} else {
			s.skipStatement()
		}

	case "async":
		s.readWord()
		s.skipWhitespaceAndComments()
		if s.peekWord() == "function" {
			s.parseFunction(shape, line)
		} else {
			s.skipStatement()
		}

	case "function":
		s.parseFunction(shape, line)

	case "class":
		s.parseClass(shape, line)

	case "interface":
		s.parseInterface(shape, line)

	case "type":
		s.parseTypeAlias(shape, line)

	case "const", "let", "var":
		s.parseVariable(shape, line, word)

	case "enum":
		s.parseEnum(shape, line)

	default:
		s.skipStatement()
	}
}

// Declaration parsers

func (s *tsScanner) parseImportPath() string {
	s.skipWhitespaceAndComments()

	for !s.eof() {
		ch := s.peek()
		if ch == '\'' || ch == '"' {
			start := s.pos + 1
			s.skipStringLiteral()
			end := s.pos - 1
			if end > start {
				s.skipWhitespaceAndComments()
				if !s.eof() && s.peek() == ';' {
					s.advance()
				}
				return s.src[start:end]
			}
			return ""
		}
		if ch == ';' {
			s.advance()
			return ""
		}
		if s.atCommentStart() {
			s.skipComment()
			continue
		}
		s.advance()
	}
	return ""
}

func (s *tsScanner) parseFunction(shape *FileShape, line int) {
	s.readWord() // function
	s.skipWhitespaceAndComments()

	if !s.eof() && s.peek() == '*' {
		s.advance()
		s.skipWhitespaceAndComments()
	}

	name := s.readWord()
	if name == "" {
		s.skipStatement()
		return
	}

	s.skipWhitespaceAndComments()

	var sig strings.Builder

	if !s.eof() && s.peek() == '<' {
		sig.WriteString(s.readAngleBrackets())
		s.skipWhitespaceAndComments()
	}

	if !s.eof() && s.peek() == '(' {
		sig.WriteString(s.readBalanced('(', ')'))
	}

	s.skipWhitespaceAndComments()

	if !s.eof() && s.peek() == ':' {
		s.advance()
		s.skipWhitespaceAndComments()
		sig.WriteString(": ")
		sig.WriteString(s.readTypeExpression())
	}

	shape.Functions = append(shape.Functions, FuncDef{
		Name:      name,
		Signature: sig.String(),
		Line:      line,
	})

	s.skipWhitespaceAndComments()
	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.skipBlockContent()
	} else if !s.eof() && s.peek() == ';' {
		s.advance()
	}
}

func (s *tsScanner) parseClass(shape *FileShape, line int) {
	s.readWord() // class
	s.skipWhitespaceAndComments()

	name := s.readWord()
	if name == "" {
		s.skipStatement()
		return
	}

	td := TypeDef{
		Name: name,
		Kind: "class",
		Line: line,
	}

	s.skipWhitespaceAndComments()

	if !s.eof() && s.peek() == '<' {
		td.Name += s.readAngleBrackets()
		s.skipWhitespaceAndComments()
	}

	s.parseHeritageClause(&td)

	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.parseClassMembers(&td)
	}

	shape.Types = append(shape.Types, td)
}

func (s *tsScanner) parseHeritageClause(td *TypeDef) {
	for !s.eof() {
		s.skipWhitespaceAndComments()
		word := s.peekWord()

		if word == "extends" {
			s.readWord()
			s.skipWhitespaceAndComments()
			for {
				typeName := s.readHeritageName()
				if typeName != "" {
					td.Embeds = append(td.Embeds, "extends "+typeName)
				}
				s.skipWhitespaceAndComments()
				if s.peek() == ',' {
					s.advance()
					s.skipWhitespaceAndComments()
					continue
				}
				break
			}
			continue
		}

		if word == "implements" {
			s.readWord()
			s.skipWhitespaceAndComments()
			for {
				typeName := s.readHeritageName()
				if typeName != "" {
					td.Embeds = append(td.Embeds, "implements "+typeName)
				}
				s.skipWhitespaceAndComments()
				if s.peek() == ',' {
					s.advance()
					s.skipWhitespaceAndComments()
					continue
				}
				break
			}
			continue
		}

		break
	}
}

func (s *tsScanner) parseInterface(shape *FileShape, line int) {
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

	// interfaces only extend, never implement
	if s.peekWord() == "extends" {
		s.readWord()
		s.skipWhitespaceAndComments()
		for {
			typeName := s.readHeritageName()
			if typeName != "" {
				td.Embeds = append(td.Embeds, typeName)
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

	s.skipWhitespaceAndComments()
	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.parseInterfaceMembers(&td)
	}

	shape.Types = append(shape.Types, td)
}

func (s *tsScanner) parseTypeAlias(shape *FileShape, line int) {
	s.readWord() // type
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

	if !s.eof() && s.peek() == '=' {
		s.advance()
		s.skipWhitespaceAndComments()
	}

	underlying := s.readTypeBody()

	displayName := name
	if typeParams != "" {
		displayName += typeParams
	}

	shape.Types = append(shape.Types, TypeDef{
		Name:       displayName,
		Kind:       "alias",
		Line:       line,
		Underlying: underlying,
	})

	if !s.eof() && s.peek() == ';' {
		s.advance()
	}
}

func (s *tsScanner) parseVariable(shape *FileShape, line int, keyword string) {
	s.readWord() // const/let/var
	s.skipWhitespaceAndComments()

	// const enum
	if keyword == "const" && s.peekWord() == "enum" {
		s.parseEnum(shape, line)
		return
	}

	// skip destructuring
	if !s.eof() && (s.peek() == '{' || s.peek() == '[') {
		s.skipStatement()
		return
	}

	name := s.readWord()
	if name == "" {
		s.skipStatement()
		return
	}

	vd := ValueDef{
		Name: name,
		Line: line,
	}

	s.skipWhitespaceAndComments()

	// definite assignment !
	if !s.eof() && s.peek() == '!' {
		s.advance()
		s.skipWhitespaceAndComments()
	}

	if !s.eof() && s.peek() == ':' {
		s.advance()
		s.skipWhitespaceAndComments()
		vd.Type = s.readTypeExpression()
		s.skipWhitespaceAndComments()
	}

	if !s.eof() && s.peek() == '=' {
		s.advance()
		s.skipWhitespaceAndComments()
		vd.Value = s.readValueExpression()
	}

	if keyword == "const" {
		shape.Constants = append(shape.Constants, vd)
	} else {
		shape.Variables = append(shape.Variables, vd)
	}

	if !s.eof() && s.peek() == ';' {
		s.advance()
	}
}

func (s *tsScanner) parseEnum(shape *FileShape, line int) {
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
	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.parseEnumMembers(&td)
	}

	shape.Types = append(shape.Types, td)
}

// Member parsers

func (s *tsScanner) parseClassMembers(td *TypeDef) {
	for !s.eof() {
		s.skipWhitespaceAndComments()
		if s.eof() || s.peek() == '}' {
			if !s.eof() {
				s.advance()
			}
			return
		}

		if s.peek() == '@' {
			s.skipDecorator()
			s.skipWhitespaceAndComments()
			continue
		}

		if s.peek() == ';' {
			s.advance()
			continue
		}

		line := s.line
		isPrivate := false
		isStatic := false

		// Collect modifiers
		for !s.eof() {
			word := s.peekWord()
			switch word {
			case "public":
				s.readWord()
				s.skipWhitespaceAndComments()
			case "private", "protected":
				isPrivate = true
				s.readWord()
				s.skipWhitespaceAndComments()
			case "static":
				isStatic = true
				s.readWord()
				s.skipWhitespaceAndComments()
			case "readonly", "abstract", "override", "accessor":
				s.readWord()
				s.skipWhitespaceAndComments()
			case "async":
				s.readWord()
				s.skipWhitespaceAndComments()
			default:
				goto doneModifiers
			}
		}
	doneModifiers:

		// # private fields
		if !s.eof() && s.peek() == '#' {
			isPrivate = true
			s.advance()
		}

		if s.exportedOnly && isPrivate {
			s.skipClassMember()
			continue
		}

		word := s.peekWord()

		// constructor
		if word == "constructor" {
			s.readWord()
			s.skipWhitespaceAndComments()
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
			if !s.eof() && s.peek() == '{' {
				s.advance()
				s.skipBlockContent()
			}
			continue
		}

		// get/set accessors
		if word == "get" || word == "set" {
			prefix := word
			savedPos := s.pos
			savedLine := s.line
			s.readWord()
			s.skipWhitespaceAndComments()

			// If next is ( or <, then "get"/"set" is the method name itself
			if !s.eof() && (s.peek() == '(' || s.peek() == '<') {
				s.pos = savedPos
				s.line = savedLine
				// fall through to regular member parsing
			} else {
				accessorName := s.readWord()
				if accessorName != "" {
					s.parseMemberMethod(td, prefix+" "+accessorName, line, isStatic)
					continue
				}
				s.pos = savedPos
				s.line = savedLine
			}
		}

		// Regular member name
		name := s.readWord()
		if name == "" {
			if !s.eof() && s.peek() == '[' {
				s.readBalanced('[', ']')
				s.skipClassMember()
			} else {
				s.advance()
			}
			continue
		}

		s.skipWhitespaceAndComments()

		// ? optional
		if !s.eof() && s.peek() == '?' {
			s.advance()
			s.skipWhitespaceAndComments()
		}
		// ! definite
		if !s.eof() && s.peek() == '!' {
			s.advance()
			s.skipWhitespaceAndComments()
		}

		// Method: ( or <
		if !s.eof() && (s.peek() == '(' || s.peek() == '<') {
			s.parseMemberMethod(td, name, line, isStatic)
			continue
		}

		// Property
		displayName := name
		if isStatic {
			displayName = "static " + name
		}
		fd := FieldDef{Name: displayName}

		if !s.eof() && s.peek() == ':' {
			s.advance()
			s.skipWhitespaceAndComments()
			fd.Type = s.readTypeExpression()
			s.skipWhitespaceAndComments()
		}

		if !s.eof() && s.peek() == '=' {
			s.advance()
			s.skipWhitespaceAndComments()
			s.readValueExpression()
		}

		if !s.eof() && s.peek() == ';' {
			s.advance()
		}

		td.Fields = append(td.Fields, fd)
	}
}

func (s *tsScanner) parseMemberMethod(td *TypeDef, name string, line int, isStatic bool) {
	var sig strings.Builder

	if !s.eof() && s.peek() == '<' {
		sig.WriteString(s.readAngleBrackets())
		s.skipWhitespaceAndComments()
	}

	if !s.eof() && s.peek() == '(' {
		sig.WriteString(s.readBalanced('(', ')'))
	}

	s.skipWhitespaceAndComments()

	if !s.eof() && s.peek() == ':' {
		s.advance()
		s.skipWhitespaceAndComments()
		sig.WriteString(": ")
		sig.WriteString(s.readTypeExpression())
	}

	displayName := name
	if isStatic {
		displayName = "static " + name
	}

	td.Methods = append(td.Methods, FuncDef{
		Name:      displayName,
		Signature: sig.String(),
		Line:      line,
	})

	s.skipWhitespaceAndComments()
	if !s.eof() && s.peek() == '{' {
		s.advance()
		s.skipBlockContent()
	} else if !s.eof() && s.peek() == ';' {
		s.advance()
	}
}

func (s *tsScanner) parseInterfaceMembers(td *TypeDef) {
	for !s.eof() {
		s.skipWhitespaceAndComments()
		if s.eof() || s.peek() == '}' {
			if !s.eof() {
				s.advance()
			}
			return
		}

		if s.peek() == ';' || s.peek() == ',' {
			s.advance()
			continue
		}

		line := s.line

		// readonly modifier
		if s.peekWord() == "readonly" {
			s.readWord()
			s.skipWhitespaceAndComments()
		}

		// Index signature: [key: Type]: Type
		if s.peek() == '[' {
			indexSig := s.readBalanced('[', ']')
			s.skipWhitespaceAndComments()
			if !s.eof() && s.peek() == ':' {
				s.advance()
				s.skipWhitespaceAndComments()
				valType := s.readTypeExpression()
				td.Fields = append(td.Fields, FieldDef{
					Name: indexSig,
					Type: valType,
				})
			}
			s.skipWhitespaceAndComments()
			if !s.eof() && (s.peek() == ';' || s.peek() == ',') {
				s.advance()
			}
			continue
		}

		// Call signature: (params): Return
		if s.peek() == '(' || (s.peek() == '<' && !tsIsIdentStart(s.peek())) {
			var sig strings.Builder
			if s.peek() == '<' {
				sig.WriteString(s.readAngleBrackets())
				s.skipWhitespaceAndComments()
			}
			if !s.eof() && s.peek() == '(' {
				sig.WriteString(s.readBalanced('(', ')'))
			}
			s.skipWhitespaceAndComments()
			if !s.eof() && s.peek() == ':' {
				s.advance()
				s.skipWhitespaceAndComments()
				sig.WriteString(": ")
				sig.WriteString(s.readTypeExpression())
			}
			td.Methods = append(td.Methods, FuncDef{
				Name:      "(call)",
				Signature: sig.String(),
				Line:      line,
			})
			s.skipWhitespaceAndComments()
			if !s.eof() && (s.peek() == ';' || s.peek() == ',') {
				s.advance()
			}
			continue
		}

		name := s.readWord()
		if name == "" {
			s.advance()
			continue
		}

		s.skipWhitespaceAndComments()

		if !s.eof() && s.peek() == '?' {
			s.advance()
			s.skipWhitespaceAndComments()
		}

		// Method signature
		if !s.eof() && (s.peek() == '(' || s.peek() == '<') {
			var sig strings.Builder
			if s.peek() == '<' {
				sig.WriteString(s.readAngleBrackets())
				s.skipWhitespaceAndComments()
			}
			if !s.eof() && s.peek() == '(' {
				sig.WriteString(s.readBalanced('(', ')'))
			}
			s.skipWhitespaceAndComments()
			if !s.eof() && s.peek() == ':' {
				s.advance()
				s.skipWhitespaceAndComments()
				sig.WriteString(": ")
				sig.WriteString(s.readTypeExpression())
			}
			td.Methods = append(td.Methods, FuncDef{
				Name:      name,
				Signature: sig.String(),
				Line:      line,
			})
		} else if !s.eof() && s.peek() == ':' {
			s.advance()
			s.skipWhitespaceAndComments()
			propType := s.readTypeExpression()
			td.Fields = append(td.Fields, FieldDef{
				Name: name,
				Type: propType,
			})
		}

		s.skipWhitespaceAndComments()
		if !s.eof() && (s.peek() == ';' || s.peek() == ',') {
			s.advance()
		}
	}
}

func (s *tsScanner) parseEnumMembers(td *TypeDef) {
	for !s.eof() {
		s.skipWhitespaceAndComments()
		if s.eof() || s.peek() == '}' {
			if !s.eof() {
				s.advance()
			}
			return
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
			fd.Type = s.readEnumValue()
		}

		td.Fields = append(td.Fields, fd)

		s.skipWhitespaceAndComments()
		if !s.eof() && s.peek() == ',' {
			s.advance()
		}
	}
}

func (s *tsScanner) readEnumValue() string {
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
		switch ch {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		s.advance()
	}
	return strings.TrimSpace(s.src[start:s.pos])
}
