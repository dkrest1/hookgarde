package httpapi_test

import (
	"context"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func contract(t *testing.T) *openapi3.T {
	t.Helper()
	doc, e := openapi3.NewLoader().LoadFromFile("../../../../docs/api/openapi.json")
	if e != nil {
		t.Fatal(e)
	}
	return doc
}
func TestOpenAPIContract(t *testing.T) {
	if e := contract(t).Validate(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func schema(t *testing.T, name string, data any) {
	t.Helper()
	s := contract(t).Components.Schemas[name]
	if s == nil {
		t.Fatalf("missing schema %s", name)
	}
	if e := s.Value.VisitJSON(data); e != nil {
		t.Fatalf("%s response violates contract: %v", name, e)
	}
}
