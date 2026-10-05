package diff

import (
	"encoding/json"
	"fmt"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// checkSchema compares an old and new schema at one position. dir
// selects the direction of judgment: for requests the new schema must
// accept everything the old one accepted; for responses everything the
// new schema may produce must be consumable by old clients.
func (c *comparer) checkSchema(oldS, newS *spec.Schema, dir Direction, path string) {
	oldS, newS = spec.Deref(oldS), spec.Deref(newS)
	if oldS == nil || newS == nil {
		if oldS != newS {
			c.add(LevelWarning, KindUnknownConstruct, dir, path,
				"schema present on only one side; cannot compare", locOf(oldS, newS))
		}
		return
	}
	if oldS == newS {
		return // same memoized schema instance
	}
	key := seenKey{oldS, newS, dir}
	if c.seen[key] {
		return // recursive schema already compared at this pair/direction
	}
	c.seen[key] = true

	// Unions and leftover compositions cannot be decided statically;
	// judge conservatively unless structurally identical.
	if len(oldS.OneOf)+len(oldS.AnyOf)+len(oldS.AllOf)+len(newS.OneOf)+len(newS.AnyOf)+len(newS.AllOf) > 0 {
		if fingerprint(oldS) != fingerprint(newS) {
			c.add(LevelWarning, KindUnionChanged, dir, path,
				"union/composed schema changed; static judgment impossible, treated conservatively",
				newS.Loc)
		}
		return
	}

	c.checkType(oldS, newS, dir, path)
	c.checkNullable(oldS, newS, dir, path)
	c.checkEnum(oldS, newS, dir, path)
	c.checkFormat(oldS, newS, dir, path)

	if oldS.Type == "object" || len(oldS.Properties) > 0 || len(newS.Properties) > 0 {
		c.checkProperties(oldS, newS, dir, path)
	}
	if oldS.Type == "array" || newS.Type == "array" {
		c.checkSchema(oldS.Items, newS.Items, dir, path+"[]")
	}
	c.checkAdditionalProperties(oldS, newS, dir, path)
}

func locOf(schemas ...*spec.Schema) spec.Location {
	for _, s := range schemas {
		if s != nil {
			return s.Loc
		}
	}
	return spec.Location{}
}

func (c *comparer) checkType(oldS, newS *spec.Schema, dir Direction, path string) {
	if oldS.Type == newS.Type {
		return
	}
	// integer <-> number is the only partially ordered pair.
	if isIntNumber(oldS.Type, newS.Type) {
		if dir == DirectionRequest {
			c.add(LevelInfo, KindTypeChanged, dir, path,
				fmt.Sprintf("type widened from %s to %s; all old requests remain valid", oldS.Type, newS.Type), newS.Loc)
		} else {
			c.add(LevelBreaking, KindTypeChanged, dir, path,
				fmt.Sprintf("response type widened from %s to %s; clients expecting %s may break", oldS.Type, newS.Type, oldS.Type), newS.Loc)
		}
		return
	}
	if isIntNumber(newS.Type, oldS.Type) {
		if dir == DirectionRequest {
			c.add(LevelBreaking, KindTypeNarrowed, dir, path,
				fmt.Sprintf("request type narrowed from %s to %s", oldS.Type, newS.Type), newS.Loc)
		} else {
			c.add(LevelInfo, KindTypeChanged, dir, path,
				fmt.Sprintf("response type narrowed from %s to %s; old clients still accept it", oldS.Type, newS.Type), newS.Loc)
		}
		return
	}
	if oldS.Type == "" || newS.Type == "" {
		c.add(LevelWarning, KindUnknownConstruct, dir, path,
			fmt.Sprintf("type changed from %q to %q with an unspecified side; cannot decide statically", oldS.Type, newS.Type),
			newS.Loc)
		return
	}
	c.add(LevelBreaking, KindTypeChanged, dir, path,
		fmt.Sprintf("type changed from %s to %s", oldS.Type, newS.Type), newS.Loc)
}

func isIntNumber(intSide, numSide string) bool {
	return intSide == "integer" && numSide == "number"
}

func (c *comparer) checkNullable(oldS, newS *spec.Schema, dir Direction, path string) {
	if oldS.Nullable == newS.Nullable {
		return
	}
	if newS.Nullable {
		if dir == DirectionRequest {
			c.add(LevelInfo, KindTypeChanged, dir, path, "schema became nullable; old requests remain valid", newS.Loc)
		} else {
			c.add(LevelBreaking, KindTypeChanged, dir, path,
				"response schema became nullable; clients may not handle null", newS.Loc)
		}
		return
	}
	if dir == DirectionRequest {
		c.add(LevelBreaking, KindTypeNarrowed, dir, path,
			"request schema is no longer nullable; null values are now rejected", newS.Loc)
	} else {
		c.add(LevelInfo, KindTypeChanged, dir, path, "response schema is no longer nullable", newS.Loc)
	}
}

func (c *comparer) checkEnum(oldS, newS *spec.Schema, dir Direction, path string) {
	oldEnum, newEnum := oldS.Enum, newS.Enum
	switch {
	case oldEnum == nil && newEnum == nil:
		return
	case oldEnum == nil:
		// New restriction on allowed values.
		if dir == DirectionRequest {
			c.add(LevelBreaking, KindEnumChanged, dir, path,
				"enum constraint added to a request schema; previously valid values are now rejected", newS.Loc)
		} else {
			c.add(LevelInfo, KindEnumChanged, dir, path,
				"enum constraint added to a response schema; clients see a subset of previous values", newS.Loc)
		}
	case newEnum == nil:
		if dir == DirectionRequest {
			c.add(LevelInfo, KindEnumChanged, dir, path,
				"enum constraint removed from a request schema; all old values remain valid", newS.Loc)
		} else {
			c.add(LevelBreaking, KindEnumChanged, dir, path,
				"enum constraint removed from a response schema; clients may see unknown values", newS.Loc)
		}
	default:
		c.checkEnumSets(oldEnum, newEnum, dir, path, newS.Loc)
	}
}

func (c *comparer) checkEnumSets(oldEnum, newEnum []any, dir Direction, path string, loc spec.Location) {
	if enumEqual(oldEnum, newEnum) {
		return
	}
	oldSet := enumSet(oldEnum)
	newSet := enumSet(newEnum)
	sub, super := true, true
	for k := range oldSet {
		if !newSet[k] {
			super = false
		}
	}
	for k := range newSet {
		if !oldSet[k] {
			sub = false
		}
	}
	switch {
	case sub && !super: // new ⊂ old
		if dir == DirectionRequest {
			c.add(LevelBreaking, KindEnumChanged, dir, path,
				"request enum narrowed; previously accepted values are now rejected", loc)
		} else {
			c.add(LevelInfo, KindEnumChanged, dir, path,
				"response enum narrowed; clients see a subset of previous values", loc)
		}
	case super && !sub: // new ⊃ old
		if dir == DirectionRequest {
			c.add(LevelInfo, KindEnumChanged, dir, path,
				"request enum widened; all old values remain valid", loc)
		} else {
			c.add(LevelBreaking, KindEnumChanged, dir, path,
				"response enum widened; clients may see unknown values", loc)
		}
	default:
		c.add(LevelWarning, KindEnumChanged, dir, path,
			"enum values changed without a subset relation; cannot decide statically, treated conservatively", loc)
	}
}

func enumSet(enum []any) map[string]bool {
	m := make(map[string]bool, len(enum))
	for _, v := range enum {
		m[fmt.Sprintf("%#v", v)] = true
	}
	return m
}

func enumEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := enumSet(a), enumSet(b)
	for k := range sa {
		if !sb[k] {
			return false
		}
	}
	return true
}

func (c *comparer) checkFormat(oldS, newS *spec.Schema, dir Direction, path string) {
	if oldS.Format == newS.Format {
		return
	}
	// int32 -> int64 widens the value range.
	if oldS.Format == "int32" && newS.Format == "int64" {
		if dir == DirectionRequest {
			c.add(LevelInfo, KindTypeChanged, dir, path, "format widened from int32 to int64", newS.Loc)
		} else {
			c.add(LevelBreaking, KindTypeChanged, dir, path,
				"response format widened from int32 to int64; 32-bit clients may overflow", newS.Loc)
		}
		return
	}
	if oldS.Format == "int64" && newS.Format == "int32" {
		if dir == DirectionRequest {
			c.add(LevelBreaking, KindTypeNarrowed, dir, path,
				"request format narrowed from int64 to int32", newS.Loc)
		} else {
			c.add(LevelInfo, KindTypeChanged, dir, path, "response format narrowed from int64 to int32", newS.Loc)
		}
		return
	}
	c.add(LevelWarning, KindUnknownConstruct, dir, path,
		fmt.Sprintf("format changed from %q to %q; compatibility cannot be decided statically", oldS.Format, newS.Format),
		newS.Loc)
}

func (c *comparer) checkProperties(oldS, newS *spec.Schema, dir Direction, path string) {
	oldReq := oldS.RequiredSet()
	newReq := newS.RequiredSet()

	for _, name := range spec.SortedKeys(oldS.Properties) {
		pp := path + "." + name
		newP, ok := newS.Properties[name]
		if !ok {
			c.propertyRemoved(pp, name, dir, oldS, newS)
			continue
		}
		c.checkSchema(oldS.Properties[name], newP, dir, pp)
	}
	for _, name := range spec.SortedKeys(newS.Properties) {
		if _, ok := oldS.Properties[name]; ok {
			continue
		}
		pp := path + "." + name
		if dir == DirectionRequest {
			if newReq[name] {
				c.add(LevelBreaking, KindAdded, dir, pp,
					"required property added to request schema", newS.Properties[name].Loc)
			} else {
				c.add(LevelInfo, KindAdded, dir, pp,
					"optional property added to request schema", newS.Properties[name].Loc)
			}
		} else {
			c.add(LevelInfo, KindAdded, dir, pp,
				"property added to response schema; old clients ignore unknown fields", newS.Properties[name].Loc)
		}
	}
	// Requiredness changes on properties present on both sides.
	for _, name := range spec.SortedKeys(oldS.Properties) {
		if _, ok := newS.Properties[name]; !ok {
			continue
		}
		pp := path + "." + name
		was, is := oldReq[name], newReq[name]
		switch {
		case !was && is:
			if dir == DirectionRequest {
				c.add(LevelBreaking, KindRequiredChanged, dir, pp,
					"request property became required", newS.Loc)
			} else {
				c.add(LevelInfo, KindRequiredChanged, dir, pp,
					"response property is now guaranteed present", newS.Loc)
			}
		case was && !is:
			if dir == DirectionRequest {
				c.add(LevelInfo, KindRequiredChanged, dir, pp,
					"request property is no longer required", newS.Loc)
			} else {
				c.add(LevelWarning, KindRequiredChanged, dir, pp,
					"response property is no longer guaranteed present; clients may rely on it", newS.Loc)
			}
		}
	}
}

func (c *comparer) propertyRemoved(pp, name string, dir Direction, oldS, newS *spec.Schema) {
	oldP := oldS.Properties[name]
	if dir == DirectionRequest {
		// If the new schema forbids additional properties, old clients
		// sending this property will now be rejected.
		if newS.AdditionalPropertiesSet && newS.AdditionalProperties == nil {
			c.add(LevelBreaking, KindRemoved, dir, pp,
				"request property removed and additionalProperties is false; old clients sending it are rejected", oldP.Loc)
		} else {
			c.add(LevelWarning, KindRemoved, dir, pp,
				"request property removed; clients may still send it", oldP.Loc)
		}
		return
	}
	c.add(LevelBreaking, KindRemoved, dir, pp,
		"response property removed; clients reading it will break", oldP.Loc)
}

func (c *comparer) checkAdditionalProperties(oldS, newS *spec.Schema, dir Direction, path string) {
	if oldS.AdditionalPropertiesSet == newS.AdditionalPropertiesSet &&
		fingerprint(oldS.AdditionalProperties) == fingerprint(newS.AdditionalProperties) {
		return
	}
	// Tightening additionalProperties in a request rejects old clients.
	if dir == DirectionRequest &&
		(!oldS.AdditionalPropertiesSet || oldS.AdditionalProperties != nil) &&
		newS.AdditionalPropertiesSet && newS.AdditionalProperties == nil {
		c.add(LevelBreaking, KindTypeNarrowed, dir, path,
			"additionalProperties became false in a request schema; old clients sending extra fields are rejected",
			newS.Loc)
		return
	}
	c.add(LevelWarning, KindUnknownConstruct, dir, path,
		"additionalProperties changed; compatibility cannot be decided statically", newS.Loc)
}

// fingerprint renders a schema in a canonical structural form for
// equality checks. Locations and aliases are excluded.
func fingerprint(s *spec.Schema) string {
	if s == nil {
		return "<nil>"
	}
	data, err := json.Marshal(s)
	if err != nil {
		return "<error>"
	}
	return string(data)
}
