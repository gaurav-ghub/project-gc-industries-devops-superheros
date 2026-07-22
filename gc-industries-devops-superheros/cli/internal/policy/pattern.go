package policy

import (
	"fmt"
	"strconv"
	"strings"
)

// This file implements the subset of Kyverno's `validate.pattern` language that
// the policies in infra/kyverno_policy/ actually use:
//
//	"?*"          any non-empty value            (require-resources)
//	"!*:latest"   negated wildcard               (disallow-latest-tag)
//	"docker.io/*" prefix wildcard                (restrict-image-registries)
//	true          literal scalar                 (enforce-security-context)
//	">=1 & <=1"   numeric range, & = AND, | = OR (enforce-replica-range)
//
// Anything outside that subset is reported as unsupported rather than quietly
// treated as a pass — see errUnsupported. A gate that silently approves a rule
// it does not understand is worse than no gate, because it produces evidence of
// compliance that was never checked.

// errUnsupported marks a pattern construct this matcher cannot evaluate. The
// engine surfaces it as a skipped rule instead of a pass or a failure.
type errUnsupported struct{ what string }

func (e errUnsupported) Error() string { return "unsupported pattern construct: " + e.what }

// mismatch describes why a resource failed a pattern, in terms of the field
// that failed — "spec.containers[0].image" is actionable, "pattern mismatch" is
// not.
type mismatch struct {
	path string
	msg  string
}

func (m mismatch) String() string { return m.path + ": " + m.msg }

// match walks pattern against value, returning the first mismatch found (nil
// when the resource satisfies the pattern).
func match(path string, pattern, value any) (*mismatch, error) {
	switch p := pattern.(type) {
	case map[string]any:
		return matchMap(path, p, value)
	case []any:
		return matchList(path, p, value)
	default:
		return matchScalar(path, pattern, value)
	}
}

func matchMap(path string, pattern map[string]any, value any) (*mismatch, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return &mismatch{path, "expected an object here, found none"}, nil
	}
	for k, pv := range pattern {
		if strings.HasPrefix(k, "(") || strings.HasPrefix(k, "^(") ||
			strings.HasPrefix(k, "+(") || strings.HasPrefix(k, "=(") ||
			strings.HasPrefix(k, "X(") || strings.HasPrefix(k, "<(") {
			return nil, errUnsupported{"anchor key " + k}
		}
		child := k
		if path != "" {
			child = path + "." + k
		}
		v, present := m[k]
		if !present {
			return &mismatch{child, "is not set"}, nil
		}
		mm, err := match(child, pv, v)
		if err != nil || mm != nil {
			return mm, err
		}
	}
	return nil, nil
}

// matchList requires every pattern element to match *every* element of the
// resource list.
//
// Kyverno's documented default is weaker — a pattern element need only match at
// least one resource element — which means a policy like "containers must
// declare resource limits" passes as soon as any one container does. LaunchPad
// deliberately takes the stricter reading: the gate exists to make a promise
// about the whole workload. For LaunchPad-generated manifests the two readings
// coincide anyway, because charts/app renders exactly one container per pod.
func matchList(path string, pattern []any, value any) (*mismatch, error) {
	items, ok := value.([]any)
	if !ok {
		return &mismatch{path, "expected a list here, found none"}, nil
	}
	if len(items) == 0 {
		return &mismatch{path, "is empty"}, nil
	}
	for _, p := range pattern {
		for i, item := range items {
			mm, err := match(fmt.Sprintf("%s[%d]", path, i), p, item)
			if err != nil || mm != nil {
				return mm, err
			}
		}
	}
	return nil, nil
}

func matchScalar(path string, pattern, value any) (*mismatch, error) {
	ps, ok := pattern.(string)
	if !ok {
		// A non-string pattern (bool, int) is a literal equality check.
		if fmt.Sprint(pattern) != fmt.Sprint(value) {
			return &mismatch{path, fmt.Sprintf("is %v, must be %v", value, pattern)}, nil
		}
		return nil, nil
	}
	return matchStringPattern(path, ps, value)
}

// matchStringPattern evaluates one Kyverno scalar pattern, which may be a
// conjunction ("&") or disjunction ("|") of conditions.
func matchStringPattern(path, pattern string, value any) (*mismatch, error) {
	if strings.Contains(pattern, "|") {
		var last *mismatch
		for _, part := range strings.Split(pattern, "|") {
			mm, err := matchStringPattern(path, strings.TrimSpace(part), value)
			if err != nil {
				return nil, err
			}
			if mm == nil {
				return nil, nil
			}
			last = mm
		}
		return last, nil
	}
	if strings.Contains(pattern, "&") {
		for _, part := range strings.Split(pattern, "&") {
			mm, err := matchStringPattern(path, strings.TrimSpace(part), value)
			if err != nil || mm != nil {
				return mm, err
			}
		}
		return nil, nil
	}
	return matchCondition(path, pattern, value)
}

// operators are checked longest-first so ">=" is not read as ">".
var operators = []string{">=", "<=", "!=", ">", "<", "="}

func matchCondition(path, pattern string, value any) (*mismatch, error) {
	got := fmt.Sprint(value)

	// Negation: "!*:latest" passes when the wildcard does NOT match.
	if strings.HasPrefix(pattern, "!") && !strings.HasPrefix(pattern, "!=") {
		if wildcard(pattern[1:], got) {
			return &mismatch{path, fmt.Sprintf("is %q, which is not allowed (must not match %q)", got, pattern[1:])}, nil
		}
		return nil, nil
	}

	for _, op := range operators {
		if !strings.HasPrefix(pattern, op) {
			continue
		}
		operand := strings.TrimSpace(pattern[len(op):])
		want, err1 := quantity(operand)
		have, err2 := quantity(got)
		if err1 != nil || err2 != nil {
			// Kyverno also allows string equality via "=" / "!=".
			switch op {
			case "=":
				if got != operand {
					return &mismatch{path, fmt.Sprintf("is %q, must be %q", got, operand)}, nil
				}
				return nil, nil
			case "!=":
				if got == operand {
					return &mismatch{path, fmt.Sprintf("is %q, which is not allowed", got)}, nil
				}
				return nil, nil
			}
			return nil, errUnsupported{fmt.Sprintf("%q applied to non-numeric value %q", pattern, got)}
		}
		if compare(op, have, want) {
			return nil, nil
		}
		return &mismatch{path, fmt.Sprintf("is %s, must be %s %s", got, op, operand)}, nil
	}

	if wildcard(pattern, got) {
		return nil, nil
	}
	return &mismatch{path, fmt.Sprintf("is %q, must match %q", got, pattern)}, nil
}

func compare(op string, have, want float64) bool {
	switch op {
	case ">=":
		return have >= want
	case "<=":
		return have <= want
	case ">":
		return have > want
	case "<":
		return have < want
	case "=":
		return have == want
	case "!=":
		return have != want
	}
	return false
}

// quantity parses a number, optionally carrying a Kubernetes resource suffix, so
// a policy can express thresholds against values like "500m" or "512Mi".
func quantity(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty quantity")
	}
	suffixes := []struct {
		suffix string
		mult   float64
	}{
		{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40},
		{"m", 0.001}, {"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12},
	}
	for _, sfx := range suffixes {
		if strings.HasSuffix(s, sfx.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(s, sfx.suffix), 64)
			if err != nil {
				return 0, err
			}
			return n * sfx.mult, nil
		}
	}
	return strconv.ParseFloat(s, 64)
}

// wildcard reports whether s matches a Kyverno glob, where '*' is any run of
// characters (including none) and '?' is exactly one.
func wildcard(pattern, s string) bool {
	// Classic two-pointer glob match: linear, no backtracking blowup.
	var (
		p, i       int
		star, mark = -1, 0
		pl, sl     = len(pattern), len(s)
	)
	for i < sl {
		switch {
		case p < pl && (pattern[p] == '?' || pattern[p] == s[i]):
			p++
			i++
		case p < pl && pattern[p] == '*':
			star = p
			mark = i
			p++
		case star >= 0:
			p = star + 1
			mark++
			i = mark
		default:
			return false
		}
	}
	for p < pl && pattern[p] == '*' {
		p++
	}
	return p == pl
}
