package spec

import (
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// buildDocument constructs the normalized Document from a parsed root node.
func (l *Loader) buildDocument(path string, root *yaml.Node) (*Document, error) {
	format, version := detectFormat(root)
	if format == FormatUnknown {
		return nil, &ResolveError{
			Location: *nodeLoc(root, path),
			Message:  "not a recognized interface description (missing swagger: '2.0' or openapi: 3.x)",
		}
	}
	doc := &Document{
		Path:    path,
		Format:  format,
		Version: version,
		Root:    root,
		Loader:  l,
	}
	if info := mapField(root, "info"); info != nil {
		if t := mapField(info, "title"); t != nil {
			doc.Title = scalarString(t)
		}
	}
	pathsNode := mapField(root, "paths")
	if pathsNode == nil {
		return doc, nil
	}
	pathNames := mapKeys(pathsNode)
	sort.Strings(pathNames)
	for _, p := range pathNames {
		item := mapField(pathsNode, p)
		if item == nil {
			continue
		}
		pathLevelParams := mapField(item, "parameters")
		for _, m := range httpMethods {
			opNode := mapField(item, m)
			if opNode == nil {
				continue
			}
			op, err := l.buildOperation(m, p, opNode, pathLevelParams, root, format, path)
			if err != nil {
				return nil, err
			}
			doc.Operations = append(doc.Operations, op)
		}
	}
	return doc, nil
}

func (l *Loader) buildOperation(method, path string, op, pathParams, root *yaml.Node, format Format, file string) (*Operation, error) {
	o := &Operation{
		Method:   strings.ToUpper(method),
		Path:     path,
		Location: nodeLoc(op, file),
	}
	if id := mapField(op, "operationId"); id != nil {
		o.OperationID = scalarString(id)
	}
	if d := mapField(op, "deprecated"); d != nil {
		if b, ok := scalarValue(d).(bool); ok {
			o.Deprecated = b
		}
	}

	// Merge path-level + operation-level parameters; op-level overrides by (in,name).
	merged := map[string]*yaml.Node{}
	var order []string
	addParams := func(node *yaml.Node) {
		for _, p := range seqNodes(node) {
			p = deref(p)
			p, _, err := l.derefNode(p, file, map[string]bool{})
			if err != nil || p == nil {
				continue
			}
			in := scalarString(mapField(p, "in"))
			name := scalarString(mapField(p, "name"))
			key := in + ":" + name
			if _, exists := merged[key]; !exists {
				order = append(order, key)
			}
			merged[key] = p
		}
	}
	addParams(pathParams)
	addParams(mapField(op, "parameters"))

	for _, key := range order {
		p := merged[key]
		param := &Parameter{
			Name:     scalarString(mapField(p, "name")),
			In:       In(scalarString(mapField(p, "in"))),
			Location: nodeLoc(p, file),
		}
		if r := mapField(p, "required"); r != nil {
			if b, ok := scalarValue(r).(bool); ok {
				param.Required = b
			}
		}
		if param.In == InPath {
			param.Required = true
		}
		if d := mapField(p, "deprecated"); d != nil {
			if b, ok := scalarValue(d).(bool); ok {
				param.Deprecated = b
			}
		}
		if format == FormatSwagger2 {
			if param.In == InBody {
				if sch := mapField(p, "schema"); sch != nil {
					s, err := l.buildSchema(sch, file, map[string]bool{})
					if err != nil {
						return nil, err
					}
					param.Schema = s
				}
			} else {
				if s, err := l.buildSwaggerParamSchema(p, file); err != nil {
					return nil, err
				} else {
					param.Schema = s
				}
			}
		} else if sch := mapField(p, "schema"); sch != nil {
			s, err := l.buildSchema(sch, file, map[string]bool{})
			if err != nil {
				return nil, err
			}
			param.Schema = s
		} else {
			param.Schema = &Schema{Kind: KindAny, Location: param.Location}
		}
		o.Parameters = append(o.Parameters, param)
	}
	sort.Slice(o.Parameters, func(i, j int) bool {
		if o.Parameters[i].In != o.Parameters[j].In {
			return o.Parameters[i].In < o.Parameters[j].In
		}
		return o.Parameters[i].Name < o.Parameters[j].Name
	})

	// Request body.
	if format == FormatSwagger2 {
		if rb := l.swaggerBody(o.Parameters, file); rb != nil {
			o.RequestBody = rb
		}
	} else if rb := mapField(op, "requestBody"); rb != nil {
		rb = deref(rb)
		body := &RequestBody{Location: nodeLoc(rb, file)}
		if r := mapField(rb, "required"); r != nil {
			if b, ok := scalarValue(r).(bool); ok {
				body.Required = b
			}
		}
		body.Content = map[string]*Schema{}
		if content := mapField(rb, "content"); content != nil {
			types := mapKeys(content)
			sort.Strings(types)
			for _, mt := range types {
				mtNode := mapField(content, mt)
				sch := mapField(mtNode, "schema")
				if sch == nil {
					body.Content[mt] = &Schema{Kind: KindAny, Location: nodeLoc(mtNode, file)}
					continue
				}
				s, err := l.buildSchema(sch, file, map[string]bool{})
				if err != nil {
					return nil, err
				}
				body.Content[mt] = s
			}
		}
		o.RequestBody = body
	}

	// Responses.
	if resp := mapField(op, "responses"); resp != nil {
		codes := mapKeys(resp)
		sort.Strings(codes)
		for _, code := range codes {
			rn := mapField(resp, code)
			rn = deref(rn)
			r := &Response{Status: code, Location: nodeLoc(rn, file)}
			if n, err := strconv.Atoi(code); err == nil {
				r.StatusCode = n
			}
			if format == FormatSwagger2 {
				if sch := mapField(rn, "schema"); sch != nil {
					s, err := l.buildSchema(sch, file, map[string]bool{})
					if err != nil {
						return nil, err
					}
					r.Body = s
				}
			} else if content := mapField(rn, "content"); content != nil {
				types := mapKeys(content)
				sort.Strings(types)
				body, err := pickBody(content, types, l, file)
				if err != nil {
					return nil, err
				}
				r.Body = body
			}
			o.Responses = append(o.Responses, r)
		}
	}
	return o, nil
}

// pickBody selects the primary JSON-ish media type schema for response comparison.
func pickBody(content *yaml.Node, types []string, l *Loader, file string) (*Schema, error) {
	prefer := func(t string) *yaml.Node { return mapField(mapField(content, t), "schema") }
	candidates := []string{"application/json", "application/hal+json", "application/vnd.api+json"}
	var chosen *yaml.Node
	var chosenType string
	for _, c := range candidates {
		if _, ok := indexOf(types, c); ok {
			chosen = prefer(c)
			chosenType = c
			break
		}
	}
	if chosen == nil {
		for _, t := range types {
			if strings.HasSuffix(t, "+json") || strings.Contains(t, "json") {
				chosen = prefer(t)
				chosenType = t
				break
			}
		}
	}
	if chosen == nil && len(types) > 0 {
		chosenType = types[0]
		chosen = prefer(types[0])
	}
	if chosen == nil {
		return &Schema{Kind: KindAny, Location: nodeLoc(content, file)}, nil
	}
	s, err := l.buildSchema(chosen, file, map[string]bool{})
	if err != nil {
		return nil, err
	}
	_ = chosenType
	return s, nil
}

func indexOf(ss []string, target string) (int, bool) {
	for i, s := range ss {
		if s == target {
			return i, true
		}
	}
	return -1, false
}

// swaggerBody converts a Swagger 2.0 body parameter into a RequestBody.
func (l *Loader) swaggerBody(params []*Parameter, file string) *RequestBody {
	for _, p := range params {
		if p.In == InBody {
			return &RequestBody{
				Required: p.Required,
				Content:  map[string]*Schema{"application/json": p.Schema},
				Location: p.Location,
			}
		}
	}
	return nil
}

// buildSwaggerParamSchema normalizes non-body Swagger parameters (type/items).
func (l *Loader) buildSwaggerParamSchema(p *yaml.Node, file string) (*Schema, error) {
	t := scalarString(mapField(p, "type"))
	s := &Schema{Location: nodeLoc(p, file)}
	switch t {
	case "integer":
		s.Kind = KindInteger
	case "number":
		s.Kind = KindNumber
	case "boolean":
		s.Kind = KindBoolean
	case "array":
		s.Kind = KindArray
		if it := mapField(p, "items"); it != nil {
			is, err := l.buildSwaggerParamSchema(it, file)
			if err != nil {
				return nil, err
			}
			s.Items = is
		}
	case "file":
		s.Kind = KindString
		s.Format = "binary"
	default:
		s.Kind = KindString
	}
	if f := mapField(p, "format"); f != nil {
		s.Format = scalarString(f)
	}
	fillConstraints(s, p)
	if en := mapField(p, "enum"); en != nil {
		s.Enum = enumValues(en)
	}
	return s, nil
}
