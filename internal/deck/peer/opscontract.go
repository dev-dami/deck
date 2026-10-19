package peer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/hub"
)

// Ops contracts (docs/peers-design.md §3): one Deck asks a teammate's Deck
// for a report, for example "report podman containers: name, state". The
// contract names presets (Scope) and maybe Targets, and a result schema.
// The contractor runs it at once, without its person, when every preset is
// inside the max_scope it gave the requester; an agent with no tools but
// sh.run (in a sandbox) and contract.result does the work. Only the result
// goes back, and both Decks check it against the schema.

// KindOps is the Kind of an ops contract; "" is a code contract.
const KindOps = "ops"

// The field types of a result schema.
const (
	FieldString = "string"
	FieldNumber = "number"
	FieldBool   = "bool"
)

// The limits of an ops contract.
const (
	maxOpsRows    = 200
	maxOpsResult  = 64 << 10
	maxOpsFields  = 20
	maxOpsTargets = 20
	// defaultFieldLength is a string field's maxLength when it gives none.
	defaultFieldLength = 200
	maxFieldLength     = 2000
	// maxOpsRunning caps the ops contracts one teammate may have running
	// here.
	maxOpsRunning = 2
)

// Field is one field of an ops contract's result: each row of the result
// has these fields and no others.
type Field struct {
	Name string `json:"name"`
	// Type is FieldString, FieldNumber or FieldBool.
	Type string `json:"type"`
	// MaxLength caps a string, in characters; 0 is defaultFieldLength.
	MaxLength int `json:"max_length,omitempty"`
}

// checkOps reports what is wrong with the ops contract c: its scope, its
// targets or its schema.
func checkOps(c Contract) error {
	if len(c.Scope) == 0 {
		return errors.New("an ops contract needs a scope")
	}
	for i, s := range c.Scope {
		if !slices.Contains(config.OpsPresets, s) {
			return fmt.Errorf("%q is not a preset (use %s)", truncate(s, 40), strings.Join(config.OpsPresets, ", "))
		}
		if slices.Contains(c.Scope[:i], s) {
			return fmt.Errorf("preset %s is in the scope twice", s)
		}
	}
	if len(c.Targets) > maxOpsTargets {
		return fmt.Errorf("an ops contract can name at most %d targets", maxOpsTargets)
	}
	for _, t := range c.Targets {
		if !validTarget(t) {
			return fmt.Errorf("%q cannot be a target: use letters, digits and . _ - @ :", truncate(t, 40))
		}
	}
	if len(c.Schema) == 0 || len(c.Schema) > maxOpsFields {
		return fmt.Errorf("an ops contract needs 1 to %d fields", maxOpsFields)
	}
	for i, f := range c.Schema {
		switch {
		case !validField(f.Name):
			return fmt.Errorf("%q cannot be a field name: use a-z, 0-9 and _, starting with a letter", truncate(f.Name, 40))
		case slices.ContainsFunc(c.Schema[:i], func(o Field) bool { return o.Name == f.Name }):
			return fmt.Errorf("field %s is in the schema twice", f.Name)
		case f.Type != FieldString && f.Type != FieldNumber && f.Type != FieldBool:
			return fmt.Errorf("field %s: the type must be string, number or bool", f.Name)
		case f.MaxLength < 0 || f.MaxLength > maxFieldLength:
			return fmt.Errorf("field %s: max_length must be 0 to %d", f.Name, maxFieldLength)
		}
	}
	return nil
}

// outside are the presets of scope that max does not allow.
func outside(scope, max []string) []string {
	var out []string
	for _, s := range scope {
		if !slices.Contains(max, s) {
			out = append(out, s)
		}
	}
	return out
}

// validField reports whether s can name a field: 1 to 32 of a-z, 0-9 and
// '_', starting with a letter.
func validField(s string) bool {
	if s == "" || len(s) > 32 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// validTarget reports whether s can name a target (a container, a
// service): 1 to 64 of letters, digits and . _ - @ :, starting with a
// letter or digit.
func validTarget(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && strings.ContainsRune("._-@:", r):
		default:
			return false
		}
	}
	return true
}

// CheckResult checks an ops contract's result against its schema and
// returns it cleaned: {"rows": [...]} with at most maxOpsRows rows, each
// with only the schema's fields, each value of its field's type (or null)
// and no string longer than its field allows; at most maxOpsResult bytes.
// Strings are redacted and put on one line.
func CheckResult(schema []Field, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > maxOpsResult {
		return nil, fmt.Errorf("the result is larger than %d KB", maxOpsResult>>10)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return nil, errors.New(`the result must be a JSON object: {"rows": [...]}`)
	}
	for k := range top {
		if k != "rows" {
			return nil, fmt.Errorf("the result has the property %q: it can only have rows", truncate(k, 40))
		}
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(top["rows"], &rows); err != nil || rows == nil {
		return nil, errors.New("rows must be a list of objects")
	}
	if len(rows) > maxOpsRows {
		return nil, fmt.Errorf("the result has %d rows: at most %d are allowed", len(rows), maxOpsRows)
	}
	names := make([]string, len(schema))
	for i, f := range schema {
		names[i] = f.Name
	}
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		if row == nil {
			return nil, fmt.Errorf("row %d is not an object", i+1)
		}
		out[i] = map[string]any{}
		for k, v := range row {
			j := slices.Index(names, k)
			if j < 0 {
				return nil, fmt.Errorf("row %d has the property %q, which is not a field (the fields are %s)", i+1, truncate(k, 40), strings.Join(names, ", "))
			}
			val, err := fieldValue(schema[j], v)
			if err != nil {
				return nil, fmt.Errorf("row %d: %s %v", i+1, k, err)
			}
			out[i][k] = val
		}
	}
	clean, err := json.Marshal(map[string]any{"rows": out})
	if err != nil {
		return nil, err
	}
	if len(clean) > maxOpsResult {
		return nil, fmt.Errorf("the result is larger than %d KB", maxOpsResult>>10)
	}
	return clean, nil
}

// fieldValue is v as a value of field f, or why it is not one.
func fieldValue(f Field, v json.RawMessage) (any, error) {
	if string(bytes.TrimSpace(v)) == "null" {
		return nil, nil
	}
	switch f.Type {
	case FieldNumber:
		var n float64
		if json.Unmarshal(v, &n) != nil {
			return nil, errors.New("must be a number")
		}
		return n, nil
	case FieldBool:
		var b bool
		if json.Unmarshal(v, &b) != nil {
			return nil, errors.New("must be true or false")
		}
		return b, nil
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return nil, errors.New("must be a string")
	}
	limit := f.MaxLength
	if limit == 0 {
		limit = defaultFieldLength
	}
	if utf8.RuneCountInString(s) > limit {
		return nil, fmt.Errorf("is longer than %d characters", limit)
	}
	return cleanLine(hub.Redact(s), 4*limit), nil
}

// OpsBrief is the task the agent of ops contract c gets. It is made from
// the contract's parts on this Deck, not from the requester's words.
func OpsBrief(c Contract) string {
	targets := "all of them"
	if len(c.Targets) > 0 {
		targets = strings.Join(c.Targets, ", ")
	}
	fields := make([]string, len(c.Schema))
	for i, f := range c.Schema {
		fields[i] = f.Name + " (" + f.Type
		if f.Type == FieldString {
			n := f.MaxLength
			if n == 0 {
				n = defaultFieldLength
			}
			fields[i] += fmt.Sprintf(", at most %d characters", n)
		}
		fields[i] += ")"
	}
	return fmt.Sprintf("%s wants a report: %s.\nScope: %s. Targets: %s.\n"+
		"Find the facts with sh.run, then deliver them with contract.result {\"result\": {\"rows\": [...]}}: one row per item, at most %d rows. "+
		"Each row has these fields and no others: %s. Use null for a value you cannot find.",
		c.Peer, c.Title, strings.Join(c.Scope, ", "), targets, maxOpsRows, strings.Join(fields, "; "))
}
