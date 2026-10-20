// Package sections holds the storefront's section types: the JSON schema
// of each type's settings (embedded, served to the dashboard editor and
// enforced on every save) and the Go types the renderer decodes them into
// (ADR-0016).
package sections

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

//go:embed schemas/*.json
var schemaFiles embed.FS

const baseURL = "https://schemas.iftahonline.com/storefront/"

// Registry validates section settings. Build it once with New; it is safe
// for concurrent use.
type Registry struct {
	schemas map[domain.SectionType]*jsonschema.Schema
	raw     map[domain.SectionType]json.RawMessage
	defs    json.RawMessage
	printer *message.Printer
}

var _ domain.SettingsValidator = (*Registry)(nil)

// New compiles every section type's schema. It fails if a type has no
// schema or a schema does not compile.
func New() (*Registry, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	entries, err := schemaFiles.ReadDir("schemas")
	if err != nil {
		return nil, fmt.Errorf("sections: %w", err)
	}
	raw := make(map[domain.SectionType]json.RawMessage, len(entries))
	for _, e := range entries {
		b, readErr := schemaFiles.ReadFile("schemas/" + e.Name())
		if readErr != nil {
			return nil, fmt.Errorf("sections: %w", readErr)
		}
		doc, parseErr := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if parseErr != nil {
			return nil, fmt.Errorf("sections: %s: %w", e.Name(), parseErr)
		}
		if err = c.AddResource(baseURL+e.Name(), doc); err != nil {
			return nil, fmt.Errorf("sections: %s: %w", e.Name(), err)
		}
		raw[domain.SectionType(strings.TrimSuffix(e.Name(), ".json"))] = b
	}
	r := &Registry{
		schemas: make(map[domain.SectionType]*jsonschema.Schema, len(domain.SectionTypes())),
		raw:     make(map[domain.SectionType]json.RawMessage, len(domain.SectionTypes())),
		defs:    raw["defs"],
		printer: message.NewPrinter(language.English),
	}
	if r.defs == nil {
		return nil, errors.New("sections: no defs.json")
	}
	for _, t := range domain.SectionTypes() {
		if _, ok := raw[t]; !ok {
			return nil, fmt.Errorf("sections: no schema for %s", t)
		}
		sch, compileErr := c.Compile(baseURL + string(t) + ".json")
		if compileErr != nil {
			return nil, fmt.Errorf("sections: compile %s: %w", t, compileErr)
		}
		r.schemas[t], r.raw[t] = sch, raw[t]
	}
	return r, nil
}

// TypeSchema is one section type with its settings schema.
type TypeSchema struct {
	Type   domain.SectionType
	Schema json.RawMessage
}

// Schemas returns every section type's schema, in domain order.
func (r *Registry) Schemas() []TypeSchema {
	out := make([]TypeSchema, 0, len(r.raw))
	for _, t := range domain.SectionTypes() {
		out = append(out, TypeSchema{Type: t, Schema: r.raw[t]})
	}
	return out
}

// Definitions returns the shared definitions schema (defs.json) that the
// type schemas reference.
func (r *Registry) Definitions() json.RawMessage { return r.defs }

// ValidateSettings implements domain.SettingsValidator.
func (r *Registry) ValidateSettings(t domain.SectionType, raw json.RawMessage) []errs.FieldError {
	sch, ok := r.schemas[t]
	if !ok {
		return []errs.FieldError{{Code: "invalid", Message: "unknown section type"}}
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return []errs.FieldError{{Code: "invalid", Message: "settings must be a JSON object"}}
	}
	if fes := wholeNumbers(doc, nil, nil); len(fes) > 0 {
		return fes
	}
	err = sch.Validate(doc)
	if err == nil {
		return decodable(t, raw)
	}
	ve, ok := err.(*jsonschema.ValidationError) //nolint:errorlint // Validate returns this type unwrapped
	if !ok {
		return []errs.FieldError{{Code: "invalid", Message: "settings are invalid"}}
	}
	return r.fieldErrors(ve, nil)
}

// maxFieldErrors bounds the report for a hostile or badly broken input.
const maxFieldErrors = 50

// fieldErrors flattens a validation error tree into one FieldError per
// leaf, named by the instance location ("items[2].name").
func (r *Registry) fieldErrors(ve *jsonschema.ValidationError, out []errs.FieldError) []errs.FieldError {
	if len(out) >= maxFieldErrors {
		return out
	}
	if len(ve.Causes) > 0 {
		for _, c := range ve.Causes {
			out = r.fieldErrors(c, out)
		}
		return out
	}
	field := fieldPath(ve.InstanceLocation)
	switch k := ve.ErrorKind.(type) {
	case *kind.Required:
		for _, p := range k.Missing {
			out = append(out, errs.FieldError{Field: joinPath(field, p), Code: "required", Message: p + " is required"})
		}
		return out
	case *kind.DependentRequired:
		for _, p := range k.Missing {
			out = append(out, errs.FieldError{Field: joinPath(field, p), Code: "dependentRequired", Message: p + " is required when " + k.Prop + " is set"})
		}
		return out
	case *kind.AdditionalProperties:
		for _, p := range k.Properties {
			out = append(out, errs.FieldError{Field: joinPath(field, p), Code: "unknown", Message: p + " is not a setting of this section"})
		}
		return out
	}
	code := "invalid"
	if kp := ve.ErrorKind.KeywordPath(); len(kp) > 0 {
		code = kp[len(kp)-1]
	}
	return append(out, errs.FieldError{Field: field, Code: code, Message: ve.ErrorKind.LocalizedString(r.printer)})
}

// decodable is the last gate: settings that pass the schema must also
// decode into the renderer's Go types (JSON Schema's date-time, for one,
// accepts forms Go's time parser does not). A failure is reported on the
// top-level setting that causes it.
func decodable(t domain.SectionType, raw json.RawMessage) []errs.FieldError {
	if _, err := Decode(t, raw); err == nil {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return []errs.FieldError{{Code: "invalid", Message: "settings must be a JSON object"}}
	}
	var out []errs.FieldError
	for _, k := range slices.Sorted(maps.Keys(top)) {
		one, err := json.Marshal(map[string]json.RawMessage{k: top[k]})
		if err != nil {
			continue
		}
		if _, err = Decode(t, one); err != nil {
			out = append(out, errs.FieldError{Field: k, Code: "invalid", Message: k + " has a value this section cannot use"})
		}
	}
	if len(out) == 0 {
		out = []errs.FieldError{{Code: "invalid", Message: "settings could not be read"}}
	}
	return out
}

// wholeNumbers rejects number literals written with a fraction or an
// exponent. Every numeric setting is an integer, and JSON Schema accepts
// 8.0 as one while the renderer's Go types do not; refusing it on save
// keeps stored settings decodable.
func wholeNumbers(v any, loc []string, out []errs.FieldError) []errs.FieldError {
	switch x := v.(type) {
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			out = append(out, errs.FieldError{Field: fieldPath(loc), Code: "type", Message: "must be a whole number written without a fraction or exponent"})
		}
	case map[string]any:
		for k, e := range x {
			out = wholeNumbers(e, append(slices.Clip(loc), k), out)
		}
	case []any:
		for i, e := range x {
			out = wholeNumbers(e, append(slices.Clip(loc), strconv.Itoa(i)), out)
		}
	}
	if len(out) > maxFieldErrors {
		return out[:maxFieldErrors]
	}
	return out
}

func fieldPath(loc []string) string {
	var b strings.Builder
	for _, p := range loc {
		b.WriteString(pathPart(b.Len() == 0, p))
	}
	return b.String()
}

func joinPath(field, prop string) string { return field + pathPart(field == "", prop) }

func pathPart(first bool, p string) string {
	if _, err := strconv.Atoi(p); err == nil {
		return "[" + p + "]"
	}
	if first {
		return p
	}
	return "." + p
}
