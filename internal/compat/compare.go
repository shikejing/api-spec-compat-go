package compat

import (
	"sort"
	"strings"

	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

// Comparator diffs two normalized documents.
type Comparator struct{}

// NewComparator creates a Comparator.
func NewComparator() *Comparator { return &Comparator{} }

// CompareFiles loads two files and diffs them.
func (c *Comparator) CompareFiles(l *spec.Loader, oldPath, newPath string) *Report {
	oldDoc, err := l.LoadFile(oldPath)
	if err != nil {
		return &Report{OldFile: oldPath, NewFile: newPath, Status: "load-error", Error: err.Error()}
	}
	newDoc, err := l.LoadFile(newPath)
	if err != nil {
		return &Report{OldFile: oldPath, NewFile: newPath, Status: "load-error", Error: err.Error()}
	}
	r := c.Compare(oldDoc, newDoc)
	r.OldFile = oldDoc.Path
	r.NewFile = newDoc.Path
	return r
}

// Compare diffs two loaded documents.
func (c *Comparator) Compare(old, neu *spec.Document) *Report {
	r := &Report{OldFile: old.Path, NewFile: neu.Path, Status: "compared", Changes: []Change{}}

	oldOps := map[string]*spec.Operation{}
	newOps := map[string]*spec.Operation{}
	for _, op := range old.Operations {
		oldOps[opKey(op)] = op
	}
	for _, op := range neu.Operations {
		newOps[opKey(op)] = op
	}
	var keys []string
	for k := range oldOps {
		keys = append(keys, k)
	}
	for k := range newOps {
		if _, ok := oldOps[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, k := range keys {
		o, inOld := oldOps[k]
		n, inNew := newOps[k]
		var target *spec.Operation
		if inOld {
			target = o
		} else {
			target = n
		}
		path := pathOp(opKey(target))
		switch {
		case inOld && !inNew:
			r.Changes = append(r.Changes, Change{
				Level: LevelBreaking, Direction: DirectionRequest, Kind: KindRemoved, Path: path,
				Operation: k, Description: "operation removed: " + k,
				Reason:      "clients relying on this operation lose it; this is a backward-incompatible removal",
				OldLocation: o.Location,
			})
		case !inOld && inNew:
			r.Changes = append(r.Changes, Change{
				Level: LevelCompat, Direction: DirectionRequest, Kind: KindAdded, Path: path,
				Operation: k, Description: "operation added: " + k,
				Reason:      "adding a new operation does not affect existing clients",
				NewLocation: n.Location,
			})
		default:
			r.Changes = append(r.Changes, c.diffOperation(o, n)...)
		}
	}
	sortChanges(r.Changes)
	return r
}

func (c *Comparator) diffOperation(o, n *spec.Operation) []Change {
	var ch []Change
	op := opKey(o)

	// Deprecation flag.
	if !o.Deprecated && n.Deprecated {
		ch = append(ch, Change{Level: LevelWarning, Direction: DirectionRequest, Kind: KindDeprecated,
			Path: pathOp(op), Operation: op,
			Description: "operation marked deprecated",
			Reason:      "deprecation is not itself a wire break, but signals upcoming removal",
			OldLocation: o.Location, NewLocation: n.Location})
	}

	ch = append(ch, c.diffParams(o, n, op)...)
	ch = append(ch, c.diffBodies(o, n, op)...)
	ch = append(ch, c.diffResponses(o, n, op)...)
	return ch
}

func (c *Comparator) diffParams(o, n *spec.Operation, op string) []Change {
	var ch []Change
	oldP := map[string]*spec.Parameter{}
	newP := map[string]*spec.Parameter{}
	for _, p := range o.Parameters {
		if p.In == spec.InBody || p.In == spec.InForm {
			continue
		}
		oldP[string(p.In)+":"+p.Name] = p
	}
	for _, p := range n.Parameters {
		if p.In == spec.InBody || p.In == spec.InForm {
			continue
		}
		newP[string(p.In)+":"+p.Name] = p
	}
	var keys []string
	for k := range oldP {
		keys = append(keys, k)
	}
	for k := range newP {
		if _, ok := oldP[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		po := oldP[k]
		pn := newP[k]
		ppath := pathOp(op) + "/parameters/" + k
		switch {
		case po == nil:
			// Added parameter: only non-breaking when optional.
			level := LevelCompat
			reason := "optional parameter can be omitted by existing clients"
			if pn.Required {
				level = LevelBreaking
				reason = "new parameter is required; existing requests that omit it will be rejected"
			}
			ch = append(ch, Change{Level: level, Direction: DirectionRequest, Kind: KindAdded,
				Path: ppath, Operation: op,
				Description: "request parameter added: " + k, Reason: reason, NewLocation: pn.Location})
		case pn == nil:
			ch = append(ch, Change{Level: LevelBreaking, Direction: DirectionRequest, Kind: KindRemoved,
				Path: ppath, Operation: op,
				Description: "request parameter removed: " + k,
				Reason:      "clients may still send this parameter; even optional-parameter removal is treated conservatively",
				OldLocation: po.Location})
		default:
			// Requiredness flip.
			if !po.Required && pn.Required {
				ch = append(ch, Change{Level: LevelBreaking, Direction: DirectionRequest, Kind: KindRequiredAdded,
					Path: ppath + "/required", Operation: op,
					Description: "request parameter became required: " + k,
					Reason:      "existing requests omitting the parameter will now fail",
					OldLocation: po.Location, NewLocation: pn.Location})
			} else if po.Required && !pn.Required {
				ch = append(ch, Change{Level: LevelCompat, Direction: DirectionRequest, Kind: KindRequiredRemoved,
					Path: ppath + "/required", Operation: op,
					Description: "request parameter became optional: " + k,
					Reason:      "relaxing a requirement is backward compatible",
					OldLocation: po.Location, NewLocation: pn.Location})
			}
			ch = append(ch, diffSchema(po.Schema, pn.Schema, DirectionRequest, ppath, map[schemaPair]bool{})...)
		}
	}
	return ch
}

func (c *Comparator) diffBodies(o, n *spec.Operation, op string) []Change {
	osb := bodySchema(o.RequestBody)
	nsb := bodySchema(n.RequestBody)
	oreq := o.RequestBody != nil && o.RequestBody.Required
	nreq := n.RequestBody != nil && n.RequestBody.Required
	bpath := pathOp(op) + "/requestBody"
	switch {
	case osb == nil && nsb != nil:
		level := LevelCompat
		reason := "new optional request body is ignored by existing requests"
		if nreq {
			level = LevelBreaking
			reason = "new required request body rejects existing requests that send no body"
		}
		return []Change{{Level: level, Direction: DirectionRequest, Kind: KindAdded,
			Path: bpath, Operation: op,
			Description: "request body added", Reason: reason,
			NewLocation: n.RequestBody.Location}}
	case osb != nil && nsb == nil:
		return []Change{{Level: LevelBreaking, Direction: DirectionRequest, Kind: KindRemoved,
			Path: bpath, Operation: op,
			Description: "request body removed",
			Reason:      "clients sending the previously documented body may now be rejected; treated conservatively",
			OldLocation: o.RequestBody.Location}}
	case osb != nil && nsb != nil:
		var ch []Change
		if !oreq && nreq {
			ch = append(ch, Change{Level: LevelBreaking, Direction: DirectionRequest, Kind: KindRequiredAdded,
				Path: bpath + "/required", Operation: op,
				Description: "request body became required",
				Reason:      "existing requests without a body will now fail",
				OldLocation: o.RequestBody.Location, NewLocation: n.RequestBody.Location})
		} else if oreq && !nreq {
			ch = append(ch, Change{Level: LevelCompat, Direction: DirectionRequest, Kind: KindRequiredRemoved,
				Path: bpath + "/required", Operation: op,
				Description: "request body became optional",
				Reason:      "relaxing body presence is backward compatible",
				OldLocation: o.RequestBody.Location, NewLocation: n.RequestBody.Location})
		}
		ch = append(ch, diffSchema(osb, nsb, DirectionRequest, bpath+"/content", map[schemaPair]bool{})...)
		return ch
	}
	return nil
}

func (c *Comparator) diffResponses(o, n *spec.Operation, op string) []Change {
	var ch []Change
	oldR := map[string]*spec.Response{}
	newR := map[string]*spec.Response{}
	for _, r := range o.Responses {
		oldR[responseKey(r.Status)] = r
	}
	for _, r := range n.Responses {
		newR[responseKey(r.Status)] = r
	}
	var keys []string
	for k := range oldR {
		keys = append(keys, k)
	}
	for k := range newR {
		if _, ok := oldR[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		ro := oldR[k]
		rn := newR[k]
		rpath := pathOp(op) + "/responses/" + k
		switch {
		case ro == nil:
			ch = append(ch, Change{Level: LevelCompat, Direction: DirectionResponse, Kind: KindAdded,
				Path: rpath, Operation: op,
				Description: "response added: " + k,
				Reason:      "clients can ignore response variants they do not handle",
				NewLocation: rn.Location})
		case rn == nil:
			ch = append(ch, Change{Level: LevelBreaking, Direction: DirectionResponse, Kind: KindRemoved,
				Path: rpath, Operation: op,
				Description: "response removed: " + k,
				Reason:      "clients handling this status code lose the documented contract",
				OldLocation: ro.Location})
		default:
			if ro.Body != nil && rn.Body != nil {
				ch = append(ch, diffSchema(ro.Body, rn.Body, DirectionResponse, rpath+"/content", map[schemaPair]bool{})...)
			}
		}
	}
	return ch
}

// helpers

func opKey(op *spec.Operation) string { return op.Method + " " + op.Path }

func pathOp(op string) string {
	parts := strings.SplitN(op, " ", 2)
	return "paths" + parts[1] + "/" + strings.ToLower(parts[0])
}

func bodySchema(rb *spec.RequestBody) *spec.Schema {
	if rb == nil {
		return nil
	}
	preferred := []string{"application/json", "application/hal+json"}
	for _, mt := range preferred {
		if s, ok := rb.Content[mt]; ok {
			return s
		}
	}
	var keys []string
	for mt := range rb.Content {
		keys = append(keys, mt)
	}
	sort.Strings(keys)
	for _, mt := range keys {
		if strings.Contains(mt, "json") {
			return rb.Content[mt]
		}
	}
	if len(keys) > 0 {
		return rb.Content[keys[0]]
	}
	return nil
}

func responseKey(status string) string {
	if status == "default" {
		return "default"
	}
	return status
}

// HasBreaking reports whether any change is breaking or potentially-breaking.
func (r Report) HasBreaking() bool {
	for _, c := range r.Changes {
		if c.Level != LevelCompat {
			return true
		}
	}
	return false
}

func sortChanges(cs []Change) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Direction != b.Direction {
			return a.Direction < b.Direction
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Description < b.Description
	})
}
