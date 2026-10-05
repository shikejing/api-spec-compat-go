package compat

import (
	"fmt"
	"sort"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// schemaPair guards recursion through shared/cyclic references at one path.
type schemaPair struct {
	oldPtr string
	newPtr string
	path   string
}

// schemaIdentity identifies a schema node stably across two documents.
func schemaIdentity(s *spec.Schema) string {
	if s == nil {
		return "<nil>"
	}
	if s.Kind == spec.KindRef {
		return "ref:" + s.Ref
	}
	if s.Location != nil {
		return fmt.Sprintf("node:%s:%d:%d", s.Location.File, s.Location.Line, s.Location.Column)
	}
	return fmt.Sprintf("anon:%p", s)
}

// effective unwraps $ref wrappers and returns the structural schema plus the
// union of nullable flags declared along the ref chain.
func effective(s *spec.Schema) (*spec.Schema, bool) {
	nullable := false
	chain := 0
	for s != nil && s.Kind == spec.KindRef {
		nullable = nullable || s.Nullable
		if s.Base == nil {
			return s, nullable // cyclic back-edge
		}
		s = s.Base
		chain++
		if chain > 64 {
			return s, nullable
		}
	}
	if s != nil {
		nullable = nullable || s.Nullable
	}
	return s, nullable
}

// diffSchema produces changes between two normalized schemas.
func diffSchema(old, neu *spec.Schema, dir Direction, path string, seen map[schemaPair]bool) []Change {
	old, oldNull := effective(old)
	neu, newNull := effective(neu)
	if old == nil || neu == nil {
		return nil
	}
	pair := schemaPair{schemaIdentity(old), schemaIdentity(neu), path}
	if seen[pair] {
		return nil
	}
	seen[pair] = true

	var ch []Change

	// Nullable toggle: non-null -> null widens the wire; conservatively judged.
	if !oldNull && newNull {
		ch = append(ch, mkChange(dir, KindTypeWidened, path,
			"schema became nullable",
			"nullable introduces a null wire value; consumers that do not expect null may misbehave, so this is judged conservatively",
			LevelWarning, old, neu))
	} else if oldNull && !newNull {
		ch = append(ch, mkChange(dir, KindTypeNarrowed, path,
			"schema is no longer nullable",
			nullRemovalReason(dir),
			breakLevel(dir), old, neu))
	}

	// Enum handling (works for primitives and enum-only schemas).
	if len(old.Enum) > 0 || len(neu.Enum) > 0 {
		ch = append(ch, diffEnums(old.Enum, neu.Enum, dir, path, old, neu)...)
	}

	ok, nk := old.Kind, neu.Kind
	if ok == spec.KindRef && old.Base == nil {
		ok = spec.KindAny // unresolved cycle target: stay conservative below
	}
	if nk == spec.KindRef && neu.Base == nil {
		nk = spec.KindAny
	}

	if ok == nk {
		switch ok {
		case spec.KindObject:
			ch = append(ch, diffObject(old, neu, dir, path, seen)...)
		case spec.KindArray:
			ch = append(ch, diffArray(old, neu, dir, path, seen)...)
		default:
			ch = append(ch, diffPrimitive(old, neu, dir, path)...)
		}
	} else {
		ch = append(ch, diffKindChange(old, neu, ok, nk, dir, path, seen)...)
	}

	// Constraints on arrays/primitives.
	ch = append(ch, diffConstraints(old, neu, dir, path)...)

	sortChanges(ch)
	return ch
}

func mkChange(dir Direction, kind Kind, path, desc, reason string, level Level, old, neu *spec.Schema) Change {
	c := Change{Level: level, Direction: dir, Kind: kind, Path: path, Description: desc, Reason: reason}
	if old != nil {
		c.OldLocation = old.Loc()
	}
	if neu != nil {
		c.NewLocation = neu.Loc()
	}
	return c
}

func breakLevel(dir Direction) Level { return LevelBreaking }

func nullRemovalReason(dir Direction) string {
	if dir == DirectionRequest {
		return "removing null forbids null values that clients currently send"
	}
	return "removing a previously documented null can break clients that already handle or receive null"
}

// diffEnums compares enum value sets conservatively.
func diffEnums(oldVals, newVals []any, dir Direction, path string, old, neu *spec.Schema) []Change {
	oldSet := map[string]any{}
	newSet := map[string]any{}
	for _, v := range oldVals {
		oldSet[enumKey(v)] = v
	}
	for _, v := range newVals {
		newSet[enumKey(v)] = v
	}
	var removed, added []string
	for k := range oldSet {
		if _, ok := newSet[k]; !ok {
			removed = append(removed, k)
		}
	}
	for k := range newSet {
		if _, ok := oldSet[k]; !ok {
			added = append(added, k)
		}
	}
	sort.Strings(removed)
	sort.Strings(added)
	var ch []Change
	if len(removed) > 0 {
		level, reason := enumNarrowLevel(dir)
		ch = append(ch, mkChange(dir, KindEnumNarrowed, path+"/enum",
			fmt.Sprintf("enum values removed: %v", removed), reason, level, old, neu))
	}
	if len(added) > 0 {
		level, reason := enumWidenLevel(dir)
		ch = append(ch, mkChange(dir, KindEnumWidened, path+"/enum",
			fmt.Sprintf("enum values added: %v", added), reason, level, old, neu))
	}
	return ch
}

func enumNarrowLevel(dir Direction) (Level, string) {
	if dir == DirectionRequest {
		return LevelBreaking, "the server no longer accepts previously valid request values; existing clients are rejected"
	}
	return LevelWarning, "the server promises a smaller set of responses; consumers with exhaustive handling or hard-coded values may behave unexpectedly"
}

func enumWidenLevel(dir Direction) (Level, string) {
	if dir == DirectionRequest {
		return LevelCompat, "clients may keep sending the old values; accepting more is backward compatible"
	}
	return LevelWarning, "the server may emit response values consumers have never seen and may not handle; judged conservatively"
}

func enumKey(v any) string {
	switch t := v.(type) {
	case string:
		return "s:" + t
	case float64:
		return "n:" + fmt.Sprintf("%v", t)
	case bool:
		if t {
			return "b:true"
		}
		return "b:false"
	case nil:
		return "z:null"
	default:
		return fmt.Sprintf("?%v", t)
	}
}

func diffObject(old, neu *spec.Schema, dir Direction, path string, seen map[schemaPair]bool) []Change {
	var ch []Change
	oldP := map[string]*spec.Property{}
	newP := map[string]*spec.Property{}
	for _, p := range old.Properties {
		oldP[p.Name] = p
	}
	for _, p := range neu.Properties {
		newP[p.Name] = p
	}
	names := map[string]bool{}
	for n := range oldP {
		names[n] = true
	}
	for n := range newP {
		names[n] = true
	}
	var ordered []string
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)

	for _, name := range ordered {
		po := oldP[name]
		pn := newP[name]
		ppath := path + "/properties/" + name
		switch {
		case po == nil:
			if pn.Required {
				ch = append(ch, propertyAddRequired(dir, ppath, name, pn))
			} else {
				ch = append(ch, propertyAddOptional(dir, ppath, name, pn))
			}
		case pn == nil:
			ch = append(ch, propertyRemove(dir, ppath, name, po))
		default:
			if !po.Required && pn.Required {
				ch = append(ch, requiredFlipAdded(dir, ppath, name, po, pn))
			} else if po.Required && !pn.Required {
				ch = append(ch, requiredFlipRemoved(dir, ppath, name, po, pn))
			}
			ch = append(ch, diffSchema(po.Schema, pn.Schema, dir, ppath, seen)...)
		}
	}

	// additionalProperties tightening: open -> closed is a wire narrowing.
	if !old.AdditionalSet && neu.AdditionalSet && !neu.AdditionalAllowed {
		ch = append(ch, mkChange(dir, KindConstraintTightened, path+"/additionalProperties",
			"object closed: additional properties are now forbidden",
			additionalCloseReason(dir), LevelWarning, old, neu))
	}
	return ch
}

func propertyAddRequired(dir Direction, path, name string, p *spec.Property) Change {
	if dir == DirectionRequest {
		return mkChange(dir, KindRequiredAdded, path,
			"required request property added: "+name,
			"existing requests without this property will be rejected", LevelBreaking, nil, p.Schema)
	}
	return mkChange(dir, KindRequiredAdded, path,
		"required response property promised: "+name,
		"this does not break consumers that ignore unknown fields, but the promise cannot be verified statically for older servers",
		LevelWarning, nil, p.Schema)
}

func propertyAddOptional(dir Direction, path, name string, p *spec.Property) Change {
	if dir == DirectionRequest {
		return mkChange(dir, KindAdded, path,
			"optional request property added: "+name,
			"existing clients simply omit it", LevelCompat, nil, p.Schema)
	}
	return mkChange(dir, KindAdded, path,
		"optional response property added: "+name,
		"extra response fields are normally ignored by consumers", LevelCompat, nil, p.Schema)
}

func propertyRemove(dir Direction, path, name string, p *spec.Property) Change {
	if dir == DirectionRequest {
		return mkChange(dir, KindRemoved, path,
			"request property removed: "+name,
			"clients may still send this field; removal is judged conservatively",
			LevelWarning, p.Schema, nil)
	}
	return mkChange(dir, KindRemoved, path,
		"response property removed: "+name,
		"clients reading this field will stop receiving it", LevelBreaking, p.Schema, nil)
}

func requiredFlipAdded(dir Direction, path, name string, po, pn *spec.Property) Change {
	if dir == DirectionRequest {
		return mkChange(dir, KindRequiredAdded, path+"/required",
			"request property became required: "+name,
			"existing requests omitting it will be rejected", LevelBreaking, po.Schema, pn.Schema)
	}
	return mkChange(dir, KindRequiredAdded, path+"/required",
		"response property became required/guaranteed: "+name,
		"a stronger guarantee is safe for tolerant readers, but cannot be verified statically",
		LevelWarning, po.Schema, pn.Schema)
}

func requiredFlipRemoved(dir Direction, path, name string, po, pn *spec.Property) Change {
	if dir == DirectionRequest {
		return mkChange(dir, KindRequiredRemoved, path+"/required",
			"request property became optional: "+name,
			"relaxing a requirement is backward compatible", LevelCompat, po.Schema, pn.Schema)
	}
	return mkChange(dir, KindRequiredRemoved, path+"/required",
		"response property is no longer guaranteed: "+name,
		"clients that relied on its presence may now observe its absence", LevelBreaking, po.Schema, pn.Schema)
}

func additionalCloseReason(dir Direction) string {
	if dir == DirectionRequest {
		return "requests carrying extra fields may now be rejected"
	}
	return "the response schema no longer permits extension fields; consumers of tightly-bound models may be affected"
}

func diffArray(old, neu *spec.Schema, dir Direction, path string, seen map[schemaPair]bool) []Change {
	var ch []Change
	if old.Items != nil && neu.Items != nil {
		ch = append(ch, diffSchema(old.Items, neu.Items, dir, path+"/items", seen)...)
	} else if old.Items == nil && neu.Items != nil {
		ch = append(ch, mkChange(dir, KindTypeNarrowed, path+"/items",
			"array item schema added",
			"elements are now constrained; previously valid untyped elements may fail",
			LevelWarning, old, neu))
	}
	return ch
}

func diffPrimitive(old, neu *spec.Schema, dir Direction, path string) []Change {
	if old.Format != neu.Format && (old.Format != "" || neu.Format != "") {
		return []Change{mkChange(dir, KindTypeChanged, path+"/format",
			fmt.Sprintf("format changed: %q -> %q", old.Format, neu.Format),
			"format is an annotation with no guaranteed validation, but often implies wire semantics; judged conservatively",
			LevelWarning, old, neu)}
	}
	return nil
}

// diffKindChange classifies transitions between different schema constructors.
// Any construct that cannot be proven safe is treated as potentially-breaking.
func diffKindChange(old, neu *spec.Schema, ok, nk spec.SchemaKind, dir Direction, path string, seen map[schemaPair]bool) []Change {
	// allOf flattening: inspect structural sub-schemas when comparable.
	if ok == spec.KindAllOf {
		return diffComposedChange(old, neu, dir, path, seen, "allOf")
	}
	if nk == spec.KindAllOf {
		return diffComposedChange(neu, old, dir, path, seen, "allOf-new")
	}
	// Union replacements (oneOf/anyOf) are inherently non-statically-decidable.
	if ok == spec.KindOneOf || ok == spec.KindAnyOf || nk == spec.KindOneOf || nk == spec.KindAnyOf {
		return []Change{mkChange(dir, KindTypeChanged, path,
			fmt.Sprintf("union type replacement: %s -> %s", ok, nk),
			"acceptance/value-set overlap of union branches cannot be proven statically; treated as potentially breaking",
			LevelWarning, old, neu)}
	}
	if ok == spec.KindNot || nk == spec.KindNot {
		return []Change{mkChange(dir, KindTypeChanged, path,
			fmt.Sprintf("negated schema change: %s -> %s", ok, nk),
			"negation cannot be evaluated for compatibility without full data knowledge; treated as potentially breaking",
			LevelWarning, old, neu)}
	}
	// Numeric widening integer -> number is safe for readers, unsafe reversed.
	if (ok == spec.KindInteger && nk == spec.KindNumber) ||
		(ok == spec.KindNumber && nk == spec.KindInteger) {
		level := LevelWarning
		reason := "numeric precision/range changes may truncate or reject values; judged conservatively"
		if nk == spec.KindNumber && ok == spec.KindInteger {
			if dir == DirectionResponse {
				level = LevelWarning
			}
		}
		return []Change{mkChange(dir, KindTypeChanged, path,
			fmt.Sprintf("numeric type changed: %s -> %s", ok, nk), reason, level, old, neu)}
	}
	if ok == spec.KindAny || nk == spec.KindAny {
		return []Change{mkChange(dir, KindTypeChanged, path,
			fmt.Sprintf("type changed via untyped schema: %s -> %s", ok, nk),
			"an untyped (any) schema imposes no statically provable guarantee; treated as potentially breaking",
			LevelWarning, old, neu)}
	}
	return []Change{mkChange(dir, KindTypeChanged, path,
		fmt.Sprintf("type changed: %s -> %s", ok, nk),
		"fundamental type change makes previously valid values potentially invalid",
		LevelBreaking, old, neu)}
}

func diffComposedChange(a, b *spec.Schema, dir Direction, path string, seen map[schemaPair]bool, tag string) []Change {
	var ch []Change
	for i, sub := range a.Sub {
		if sub != nil {
			ch = append(ch, diffSchema(sub, b, dir, fmt.Sprintf("%s/%s/%d", path, tag, i), seen)...)
		}
	}
	if len(ch) == 0 {
		ch = append(ch, mkChange(dir, KindTypeChanged, path,
			"composition ("+tag+") cannot be proven equivalent",
			"allOf merge semantics cannot be fully evaluated statically; judged conservatively",
			LevelWarning, a, b))
	}
	return ch
}

// diffConstraints checks numeric/string/array bounds.
func diffConstraints(old, neu *spec.Schema, dir Direction, path string) []Change {
	var ch []Change
	cmpInt := func(name string, a, b *int) {
		if a == nil || b == nil {
			if a == nil && b != nil {
				ch = append(ch, constraintAdd(dir, path+"/"+name, name, old, neu))
			} else if a != nil && b == nil {
				ch = append(ch, constraintDrop(dir, path+"/"+name, name, old, neu))
			}
			return
		}
		if *a != *b {
			if tighterInt(name, *a, *b) {
				ch = append(ch, constraintTight(dir, path+"/"+name, fmt.Sprintf("%s: %d -> %d", name, *a, *b), old, neu))
			} else {
				ch = append(ch, constraintLoose(dir, path+"/"+name, fmt.Sprintf("%s: %d -> %d", name, *a, *b), old, neu))
			}
		}
	}
	cmpFloat := func(name string, a, b *float64) {
		if a == nil || b == nil {
			if a == nil && b != nil {
				ch = append(ch, constraintAdd(dir, path+"/"+name, name, old, neu))
			} else if a != nil && b == nil {
				ch = append(ch, constraintDrop(dir, path+"/"+name, name, old, neu))
			}
			return
		}
		if *a != *b {
			if tighterFloat(name, *a, *b) {
				ch = append(ch, constraintTight(dir, path+"/"+name, fmt.Sprintf("%s: %v -> %v", name, *a, *b), old, neu))
			} else {
				ch = append(ch, constraintLoose(dir, path+"/"+name, fmt.Sprintf("%s: %v -> %v", name, *a, *b), old, neu))
			}
		}
	}
	cmpInt("minLength", old.MinLength, neu.MinLength)
	cmpInt("maxLength", old.MaxLength, neu.MaxLength)
	cmpInt("minItems", old.MinItems, neu.MinItems)
	cmpInt("maxItems", old.MaxItems, neu.MaxItems)
	cmpInt("minProperties", old.MinProps, neu.MinProps)
	cmpInt("maxProperties", old.MaxProps, neu.MaxProps)
	cmpFloat("minimum", old.Minimum, neu.Minimum)
	cmpFloat("maximum", old.Maximum, neu.Maximum)
	if old.Pattern != neu.Pattern && (old.Pattern != "" || neu.Pattern != "") {
		if neu.Pattern != "" {
			ch = append(ch, constraintTight(dir, path+"/pattern", "regex pattern changed/added", old, neu))
		} else {
			ch = append(ch, constraintLoose(dir, path+"/pattern", "regex pattern removed", old, neu))
		}
	}
	return ch
}

func tighterInt(name string, a, b int) bool {
	switch name {
	case "minLength", "minItems", "minProperties":
		return b > a
	default: // max*: tightening means smaller upper bound
		return b < a
	}
}

func tighterFloat(name string, a, b float64) bool {
	if name == "minimum" {
		return b > a
	}
	return b < a // maximum
}

func constraintAdd(dir Direction, path, name string, old, neu *spec.Schema) Change {
	return mkChange(dir, KindConstraintTightened, path,
		"constraint added: "+name, constraintReason(dir, true), levelForConstraint(dir, true), old, neu)
}

func constraintDrop(dir Direction, path, name string, old, neu *spec.Schema) Change {
	return mkChange(dir, KindConstraintRelaxed, path,
		"constraint removed: "+name, constraintReason(dir, false), levelForConstraint(dir, false), old, neu)
}

func constraintTight(dir Direction, path, detail string, old, neu *spec.Schema) Change {
	return mkChange(dir, KindConstraintTightened, path, "constraint tightened: "+detail,
		constraintReason(dir, true), levelForConstraint(dir, true), old, neu)
}

func constraintLoose(dir Direction, path, detail string, old, neu *spec.Schema) Change {
	return mkChange(dir, KindConstraintRelaxed, path, "constraint relaxed: "+detail,
		constraintReason(dir, false), levelForConstraint(dir, false), old, neu)
}

func levelForConstraint(dir Direction, tightened bool) Level {
	if !tightened {
		// Relaxed: response-side still warning (server may send out-of-old-range values).
		if dir == DirectionResponse {
			return LevelWarning
		}
		return LevelCompat
	}
	// Tightened: request rejects old values (breaking); response usually safe
	// (subset) but judged conservatively as potentially-breaking.
	if dir == DirectionRequest {
		return LevelBreaking
	}
	return LevelWarning
}

func constraintReason(dir Direction, tightened bool) string {
	if tightened {
		if dir == DirectionRequest {
			return "the new bound rejects some previously valid request values"
		}
		return "a tighter response bound cannot be guaranteed for already-deployed servers; judged conservatively"
	}
	if dir == DirectionRequest {
		return "a wider accepted range keeps existing values valid"
	}
	return "responses may now carry values outside the old range that consumers may reject"
}
