package main

import (
	"reflect"
	"testing"

	"github.com/spf13/pflag"
)

// TestApplyDocToCfg_EveryFieldHasEffect guards against TD-19: adding a field
// to hostsFileYAML without wiring it into applyDocToCfg. For every pointer/
// slice field on hostsFileYAML (excluding Hosts/Groups/Thresholds, which are
// structurally different and returned/handled separately), it builds a doc
// with only that field set to a distinguishing sentinel value, applies it to
// a zero-value config with an empty (nothing-changed) FlagSet, and asserts
// the resulting config differs from the zero value. A field that
// applyDocToCfg forgot to read would leave cfg unchanged and fail this test.
func TestApplyDocToCfg_EveryFieldHasEffect(t *testing.T) {
	docType := reflect.TypeOf(hostsFileYAML{})
	skip := map[string]bool{
		"Hosts":      true, // returned directly, not merged into cfg
		"Groups":     true, // returned directly, not merged into cfg
		"Thresholds": true, // covered by TestApplyThresholdsDoc_EveryFieldHasEffect
	}

	for i := 0; i < docType.NumField(); i++ {
		field := docType.Field(i)
		if skip[field.Name] {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			doc := hostsFileYAML{}
			docVal := reflect.ValueOf(&doc).Elem()
			if !setSentinel(field.Name, docVal.FieldByName(field.Name)) {
				t.Fatalf("no sentinel setter for field %s (kind %s) — extend setSentinel", field.Name, field.Type.Kind())
			}

			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			zero := config{}
			_, _, got, err := applyDocToCfg(zero, fs, doc)
			if err != nil {
				t.Fatalf("applyDocToCfg: %v", err)
			}
			if reflect.DeepEqual(got, zero) {
				t.Errorf("setting hostsFileYAML.%s had no observable effect on config — is it wired into applyDocToCfg?", field.Name)
			}
		})
	}
}

// TestOverlayThresholdsDoc_EveryFieldHasEffect is TestApplyDocToCfg_EveryFieldHasEffect's
// counterpart for thresholdsYAML, which overlayThresholdsDoc merges separately.
func TestOverlayThresholdsDoc_EveryFieldHasEffect(t *testing.T) {
	thType := reflect.TypeOf(thresholdsYAML{})

	for i := 0; i < thType.NumField(); i++ {
		field := thType.Field(i)
		t.Run(field.Name, func(t *testing.T) {
			th := thresholdsYAML{}
			thVal := reflect.ValueOf(&th).Elem()
			if !setSentinel(field.Name, thVal.FieldByName(field.Name)) {
				t.Fatalf("no sentinel setter for field %s (kind %s) — extend setSentinel", field.Name, field.Type.Kind())
			}

			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			zero := config{}
			got := zero
			got.thresholds = overlayThresholdsDoc(got.thresholds, fs, &th)
			if reflect.DeepEqual(got, zero) {
				t.Errorf("setting thresholdsYAML.%s had no observable effect on config — is it wired into overlayThresholdsDoc?", field.Name)
			}
		})
	}
}

// setSentinel sets v (addressable, currently zero) to a distinguishing
// non-zero value based on its type and field name. Returns false if the type
// isn't handled, so tests fail loudly instead of silently passing on an
// unrecognised field.
func setSentinel(fieldName string, v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Ptr:
		elem := reflect.New(v.Type().Elem())
		if !setSentinel(fieldName, elem.Elem()) {
			return false
		}
		v.Set(elem)
		return true
	case reflect.String:
		if fieldName == "Duration" {
			// Duration is parsed with time.ParseDuration, unlike every other
			// *string field here — the plain "sentinel" text below would
			// make applyDocToCfg return an error instead of applying it.
			v.SetString("999h")
		} else {
			v.SetString("sentinel")
		}
		return true
	case reflect.Int, reflect.Int64:
		v.SetInt(999)
		return true
	case reflect.Float64:
		v.SetFloat(999)
		return true
	case reflect.Bool:
		v.SetBool(true)
		return true
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.String {
			v.Set(reflect.ValueOf([]string{"sentinel"}))
			return true
		}
		return false
	default:
		return false
	}
}
