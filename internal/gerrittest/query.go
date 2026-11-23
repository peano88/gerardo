package gerrittest

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// predicate tells whether a change matches a query.
type predicate func(s *Server, c *Change) bool

// parseQuery parses the subset of the Gerrit query language the server
// supports: operators, negation with - or NOT, AND (implicit) and OR, and
// parentheses. A bare word matches the subject.
func parseQuery(q string) (predicate, error) {
	p := &queryParser{tokens: tokenize(q)}
	pred, err := p.or()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.tokens) {
		return nil, fmt.Errorf("unexpected %q", p.tokens[p.pos])
	}
	return pred, nil
}

func tokenize(q string) []string {
	var tokens []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range q {
		switch {
		case r == '"':
			inQuote = !inQuote
		case inQuote:
			cur.WriteRune(r)
		case unicode.IsSpace(r):
			flush()
		case r == '(' || r == ')':
			flush()
			tokens = append(tokens, string(r))
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}

type queryParser struct {
	tokens []string
	pos    int
}

func (p *queryParser) peek() string {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return ""
}

func (p *queryParser) or() (predicate, error) {
	left, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.peek() == "OR" {
		p.pos++
		right, err := p.and()
		if err != nil {
			return nil, err
		}
		l := left
		left = func(s *Server, c *Change) bool { return l(s, c) || right(s, c) }
	}
	return left, nil
}

func (p *queryParser) and() (predicate, error) {
	var preds []predicate
	for tok := p.peek(); tok != "" && tok != ")" && tok != "OR"; tok = p.peek() {
		if tok == "AND" {
			p.pos++
			continue
		}
		pred, err := p.term()
		if err != nil {
			return nil, err
		}
		preds = append(preds, pred)
	}
	if len(preds) == 0 {
		return nil, fmt.Errorf("empty query")
	}
	return func(s *Server, c *Change) bool {
		for _, pred := range preds {
			if !pred(s, c) {
				return false
			}
		}
		return true
	}, nil
}

func (p *queryParser) term() (predicate, error) {
	tok := p.tokens[p.pos]
	p.pos++
	switch {
	case tok == "(":
		pred, err := p.or()
		if err != nil {
			return nil, err
		}
		if p.peek() != ")" {
			return nil, fmt.Errorf("missing )")
		}
		p.pos++
		return pred, nil
	case tok == "NOT" || (strings.HasPrefix(tok, "-") && len(tok) > 1):
		if tok == "NOT" {
			if p.pos >= len(p.tokens) {
				return nil, fmt.Errorf("NOT without a term")
			}
		} else {
			p.tokens[p.pos-1] = tok[1:]
			p.pos--
		}
		pred, err := p.term()
		if err != nil {
			return nil, err
		}
		return func(s *Server, c *Change) bool { return !pred(s, c) }, nil
	}
	op, value, ok := strings.Cut(tok, ":")
	if !ok {
		if n, err := strconv.Atoi(tok); err == nil {
			return func(_ *Server, c *Change) bool { return c.Number == n }, nil
		}
		word := strings.ToLower(tok)
		return func(_ *Server, c *Change) bool { return strings.Contains(strings.ToLower(c.Subject), word) }, nil
	}
	return operator(op, value)
}

func operator(op, value string) (predicate, error) {
	switch op {
	case "is", "status":
		return statusPredicate(value)
	case "owner":
		return func(s *Server, c *Change) bool { return s.isAccount(c.Owner, value) }, nil
	case "reviewer":
		return func(s *Server, c *Change) bool {
			for _, id := range c.Reviewers {
				if s.isAccount(id, value) {
					return true
				}
			}
			return false
		}, nil
	case "attention":
		return func(s *Server, c *Change) bool {
			for _, id := range c.Attention {
				if s.isAccount(id, value) {
					return true
				}
			}
			return false
		}, nil
	case "project":
		return func(_ *Server, c *Change) bool { return c.Project == value }, nil
	case "branch":
		return func(_ *Server, c *Change) bool { return c.Branch == value }, nil
	case "topic":
		return func(_ *Server, c *Change) bool { return c.Topic == value }, nil
	case "change":
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("invalid change %q", value)
		}
		return func(_ *Server, c *Change) bool { return c.Number == n }, nil
	}
	return nil, fmt.Errorf("Unsupported operator %s:%s", op, value)
}

func statusPredicate(value string) (predicate, error) {
	switch value {
	case "open", "new":
		return func(_ *Server, c *Change) bool { return c.Status == "NEW" }, nil
	case "merged":
		return func(_ *Server, c *Change) bool { return c.Status == "MERGED" }, nil
	case "abandoned":
		return func(_ *Server, c *Change) bool { return c.Status == "ABANDONED" }, nil
	case "closed":
		return func(_ *Server, c *Change) bool { return c.Status != "NEW" }, nil
	case "wip":
		return func(_ *Server, c *Change) bool { return c.WorkInProgress }, nil
	}
	return nil, fmt.Errorf("Unrecognized value: %s", value)
}
