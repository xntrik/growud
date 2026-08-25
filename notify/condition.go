package notify

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Condition is a boolean expression over the metrics in a Snapshot. It is
// either a leaf comparison — {"metric": "battery_soc", "op": ">=", "value": 95}
// — or one of the combinators all / any / not wrapping further conditions.
type Condition struct {
	All []Condition `json:"all,omitempty"`
	Any []Condition `json:"any,omitempty"`
	Not *Condition  `json:"not,omitempty"`

	Metric string   `json:"metric,omitempty"`
	Op     string   `json:"op,omitempty"`
	Value  *float64 `json:"value,omitempty"`
}

// Comparisons are on float64s, so equality gets a tolerance rather than an
// exact bit match.
const compareEpsilon = 1e-9

var validOps = map[string]bool{
	">": true, ">=": true, "<": true, "<=": true, "==": true, "!=": true,
}

// Eval reports whether the condition holds for the given snapshot. A leaf
// naming a metric that is missing from the snapshot evaluates to false.
func (c *Condition) Eval(s Snapshot) bool {
	switch {
	case len(c.All) > 0:
		for i := range c.All {
			if !c.All[i].Eval(s) {
				return false
			}
		}
		return true

	case len(c.Any) > 0:
		for i := range c.Any {
			if c.Any[i].Eval(s) {
				return true
			}
		}
		return false

	case c.Not != nil:
		return !c.Not.Eval(s)
	}

	v, ok := s.Values[c.Metric]
	if !ok || c.Value == nil {
		return false
	}
	return compare(v, c.Op, *c.Value)
}

func compare(a float64, op string, b float64) bool {
	switch op {
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case "==":
		return math.Abs(a-b) < compareEpsilon
	case "!=":
		return math.Abs(a-b) >= compareEpsilon
	}
	return false
}

// validate checks the condition tree is well formed and only references
// metrics the engine actually produces, so typos surface at load time rather
// than as a rule that silently never fires.
func (c *Condition) validate() error {
	branches := 0
	if len(c.All) > 0 {
		branches++
	}
	if len(c.Any) > 0 {
		branches++
	}
	if c.Not != nil {
		branches++
	}
	isLeaf := c.Metric != "" || c.Op != "" || c.Value != nil
	if isLeaf {
		branches++
	}

	switch branches {
	case 1:
	case 0:
		return fmt.Errorf("empty condition: expected all, any, not, or a metric comparison")
	default:
		return fmt.Errorf("condition must use exactly one of all, any, not, or a metric comparison")
	}

	switch {
	case len(c.All) > 0:
		for i := range c.All {
			if err := c.All[i].validate(); err != nil {
				return fmt.Errorf("all[%d]: %w", i, err)
			}
		}
		return nil

	case len(c.Any) > 0:
		for i := range c.Any {
			if err := c.Any[i].validate(); err != nil {
				return fmt.Errorf("any[%d]: %w", i, err)
			}
		}
		return nil

	case c.Not != nil:
		if err := c.Not.validate(); err != nil {
			return fmt.Errorf("not: %w", err)
		}
		return nil
	}

	if c.Metric == "" {
		return fmt.Errorf("metric is required")
	}
	if !MetricExists(c.Metric) {
		return fmt.Errorf("unknown metric %q (known metrics: %s)", c.Metric, strings.Join(MetricNames(), ", "))
	}
	if c.Op == "" {
		return fmt.Errorf("op is required")
	}
	if !validOps[c.Op] {
		return fmt.Errorf("unknown op %q (want %s)", c.Op, strings.Join(sortedOps(), ", "))
	}
	if c.Value == nil {
		return fmt.Errorf("value is required")
	}
	return nil
}

func sortedOps() []string {
	ops := make([]string, 0, len(validOps))
	for op := range validOps {
		ops = append(ops, op)
	}
	sort.Strings(ops)
	return ops
}

// Describe renders the condition as a compact human-readable string, used by
// the `growud notify` dry run output.
func (c *Condition) Describe() string {
	switch {
	case len(c.All) > 0:
		return "(" + joinDescribed(c.All, " AND ") + ")"
	case len(c.Any) > 0:
		return "(" + joinDescribed(c.Any, " OR ") + ")"
	case c.Not != nil:
		return "NOT " + c.Not.Describe()
	}
	if c.Value == nil {
		return c.Metric + " " + c.Op + " ?"
	}
	return fmt.Sprintf("%s %s %s", c.Metric, c.Op, formatNumber(*c.Value))
}

func joinDescribed(cs []Condition, sep string) string {
	parts := make([]string, 0, len(cs))
	for i := range cs {
		parts = append(parts, cs[i].Describe())
	}
	return strings.Join(parts, sep)
}
